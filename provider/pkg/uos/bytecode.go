package uos

// JVM bytecode decoding (JVMS §6.5): just enough to walk a method's
// instructions and read their operands. Nothing is executed.

import (
	"encoding/binary"
	"fmt"
)

// Opcodes the extractor looks at.
const (
	opIconstM1      = 0x02
	opIconst0       = 0x03
	opIconst5       = 0x08
	opLconst0       = 0x09
	opLconst1       = 0x0a
	opFconst0       = 0x0b
	opFconst2       = 0x0d
	opDconst0       = 0x0e
	opDconst1       = 0x0f
	opBipush        = 0x10
	opSipush        = 0x11
	opLdc           = 0x12
	opLdcW          = 0x13
	opLdc2W         = 0x14
	opIreturn       = 0xac
	opLreturn       = 0xad
	opFreturn       = 0xae
	opDreturn       = 0xaf
	opAreturn       = 0xb0
	opGetstatic     = 0xb2
	opPutstatic     = 0xb3
	opGetfield      = 0xb4
	opPutfield      = 0xb5
	opInvokevirtual = 0xb6
	opInvokespecial = 0xb7
	opInvokestatic  = 0xb8
	opInvokeiface   = 0xb9
	opInvokedynamic = 0xba
	opNew           = 0xbb
	opTableswitch   = 0xaa
	opLookupswitch  = 0xab
	opWide          = 0xc4
)

// Insn is one decoded instruction. Arg is the constant-pool index for
// ldc/field/method/new instructions, or the immediate for bipush/sipush.
type Insn struct {
	Off int
	Op  byte
	Arg int
}

// opLen gives fixed instruction lengths (including the opcode); 0 marks the
// variable-length ones handled in decodeCode.
var opLen = func() [256]byte {
	var t [256]byte
	for i := range t {
		t[i] = 1
	}
	set := func(n byte, ops ...int) {
		for _, o := range ops {
			t[o] = n
		}
	}
	set(2, 0x10, 0x12, 0x15, 0x16, 0x17, 0x18, 0x19, 0x36, 0x37, 0x38, 0x39, 0x3a, 0xa9, 0xbc)
	set(3, 0x11, 0x13, 0x14, 0x84, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xbb, 0xbd, 0xc0, 0xc1, 0xc6, 0xc7)
	for o := 0x99; o <= 0xa8; o++ {
		t[o] = 3
	}
	set(4, 0xc5)
	set(5, 0xb9, 0xba, 0xc8, 0xc9)
	set(0, opTableswitch, opLookupswitch, opWide)
	return t
}()

// decodeCode decodes a Code attribute's bytecode into instructions.
func decodeCode(code []byte) ([]Insn, error) {
	var out []Insn
	for pc := 0; pc < len(code); {
		op := code[pc]
		n := int(opLen[op])
		switch op {
		case opTableswitch, opLookupswitch:
			p := (pc + 4) &^ 3 // operands are 4-byte aligned from the method start
			if p+12 > len(code) {
				return nil, fmt.Errorf("switch at %d truncated", pc)
			}
			if op == opTableswitch {
				lo := int32(binary.BigEndian.Uint32(code[p+4:]))
				hi := int32(binary.BigEndian.Uint32(code[p+8:]))
				if hi < lo {
					return nil, fmt.Errorf("tableswitch at %d: bad bounds", pc)
				}
				n = p + 12 + 4*int(hi-lo+1) - pc
			} else {
				np := int32(binary.BigEndian.Uint32(code[p+4:]))
				if np < 0 {
					return nil, fmt.Errorf("lookupswitch at %d: bad count", pc)
				}
				n = p + 8 + 8*int(np) - pc
			}
		case opWide:
			if pc+1 < len(code) && code[pc+1] == 0x84 {
				n = 6
			} else {
				n = 4
			}
		}
		if pc+n > len(code) {
			return nil, fmt.Errorf("instruction 0x%02x at %d truncated", op, pc)
		}
		in := Insn{Off: pc, Op: op}
		switch {
		case op == opBipush:
			in.Arg = int(int8(code[pc+1]))
		case op == opSipush:
			in.Arg = int(int16(binary.BigEndian.Uint16(code[pc+1:])))
		case op == opLdc:
			in.Arg = int(code[pc+1])
		case n >= 3 && (op == opLdcW || op == opLdc2W || (op >= opGetstatic && op <= opInvokedynamic) || op == opNew):
			in.Arg = int(binary.BigEndian.Uint16(code[pc+1:]))
		}
		out = append(out, in)
		pc += n
	}
	return out, nil
}

// Code returns the decoded instructions of a method ("" desc matches any).
func (c *ClassFile) Code(name, desc string) []Insn {
	m := c.Method(name, desc)
	if m == nil || m.Code == nil {
		return nil
	}
	ins, err := decodeCode(m.Code)
	if err != nil {
		return nil
	}
	return ins
}

// Method returns the first method with this name (and descriptor, if given).
func (c *ClassFile) Method(name, desc string) *Member {
	for i := range c.Methods {
		if c.Methods[i].Name == name && (desc == "" || c.Methods[i].Descriptor == desc) {
			return &c.Methods[i]
		}
	}
	return nil
}

// Field returns the field with this name, or nil.
func (c *ClassFile) Field(name string) *Member {
	for i := range c.Fields {
		if c.Fields[i].Name == name {
			return &c.Fields[i]
		}
	}
	return nil
}

// constLiteral resolves an ldc/ldc_w/ldc2_w operand to a Go value: string,
// int64 or float64. ok is false for class, method-type and dynamic constants.
func (c *ClassFile) constLiteral(idx int) (v any, ok bool) {
	e := c.entry(uint16(idx))
	switch e.tag {
	case cpString:
		return c.utf8(e.a), true
	case cpInteger, cpLong:
		return e.i, true
	case cpFloat, cpDouble:
		return e.f, true
	}
	return nil, false
}

// isStringConst reports whether an ldc operand is a CONSTANT_String.
func (c *ClassFile) isStringConst(idx int) bool { return c.entry(uint16(idx)).tag == cpString }

// ref resolves a field/method instruction's operand.
func (c *ClassFile) ref(idx int) MemberRef { return c.memberRef(uint16(idx)) }

// classRef resolves a `new` operand to a dotted class name.
func (c *ClassFile) classRef(idx int) string { return c.className(uint16(idx)) }
