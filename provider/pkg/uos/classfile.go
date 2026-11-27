package uos

// A small, read-only JVM class-file parser (JVMS §4), standard library only.
// It reads what the route and DTO extraction needs: the constant pool, class,
// field and method declarations, generic Signature attributes, annotations
// (declaration, parameter and type annotations, visible and invisible),
// AnnotationDefault, and method Code. It never loads or runs any class.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
)

// Access flags used by the extractor.
const (
	accPublic    = 0x0001
	accPrivate   = 0x0002
	accProtected = 0x0004
	accStatic    = 0x0008
	accFinal     = 0x0010
	accInterface = 0x0200
	accAbstract  = 0x0400
	accEnum      = 0x4000
)

// Constant-pool tags.
const (
	cpUtf8               = 1
	cpInteger            = 3
	cpFloat              = 4
	cpLong               = 5
	cpDouble             = 6
	cpClass              = 7
	cpString             = 8
	cpFieldref           = 9
	cpMethodref          = 10
	cpInterfaceMethodref = 11
	cpNameAndType        = 12
	cpMethodHandle       = 15
	cpMethodType         = 16
	cpDynamic            = 17
	cpInvokeDynamic      = 18
	cpModule             = 19
	cpPackage            = 20
)

type cpEntry struct {
	tag  byte
	a, b uint16 // indexes (class/string/ref/nameAndType)
	s    string // Utf8
	i    int64  // Integer/Long
	f    float64
}

// ClassFile is one parsed class.
type ClassFile struct {
	Name       string // binary name with dots: com.example.Foo$Bar
	Super      string // "" for java.lang.Object's own superclass
	Interfaces []string
	Access     uint16
	Signature  string
	// Annotations holds the class's declaration annotations (visible and
	// invisible); TypeAnnotations its type annotations.
	Annotations     []Annotation
	TypeAnnotations []Annotation
	Fields          []Member
	Methods         []Member

	cp []cpEntry
}

// Member is a field or a method.
type Member struct {
	Access     uint16
	Name       string
	Descriptor string
	Signature  string
	// Annotations: declaration annotations (visible + invisible).
	Annotations []Annotation
	// TypeAnnotations: type annotations on this member (any target, any path).
	TypeAnnotations []Annotation
	// ParamAnnotations: per-parameter declaration annotations (methods).
	ParamAnnotations  [][]Annotation
	AnnotationDefault *ElementValue
	Code              []byte
}

// Annotation is one annotation with its element values.
type Annotation struct {
	Type    string // binary name with dots
	Visible bool
	Elems   []Element
}

// Element is one name=value pair of an annotation.
type Element struct {
	Name  string
	Value ElementValue
}

// ElementValue is an annotation element value (JVMS §4.7.16.1).
type ElementValue struct {
	Tag       byte   // B C D F I J S Z s e c @ [
	Str       string // s
	Int       int64  // B C I J S Z
	Float     float64
	EnumType  string // e: binary name with dots
	EnumConst string // e
	Class     string // c: the descriptor's type, dotted (e.g. com.example.Foo)
	Ann       *Annotation
	Array     []ElementValue
}

// Elem returns the named element's value, or nil.
func (a *Annotation) Elem(name string) *ElementValue {
	for i := range a.Elems {
		if a.Elems[i].Name == name {
			return &a.Elems[i].Value
		}
	}
	return nil
}

// Strings returns an element's string value(s): a single string or an array
// of strings. ok is false when the element is absent.
func (a *Annotation) Strings(name string) (out []string, ok bool) {
	v := a.Elem(name)
	if v == nil {
		return nil, false
	}
	if v.Tag == '[' {
		for _, e := range v.Array {
			if e.Tag == 's' {
				out = append(out, e.Str)
			}
		}
		return out, true
	}
	if v.Tag == 's' {
		return []string{v.Str}, true
	}
	return nil, true
}

// AllAnnotations returns a member's declaration and type annotations.
func (m *Member) AllAnnotations() []Annotation {
	out := make([]Annotation, 0, len(m.Annotations)+len(m.TypeAnnotations))
	out = append(out, m.Annotations...)
	return append(out, m.TypeAnnotations...)
}

// HasAnnotation reports whether the member carries an annotation of this
// exact type among its declaration or type annotations.
func (m *Member) HasAnnotation(typ string) bool {
	return findAnn(m.AllAnnotations(), typ) != nil
}

func findAnn(anns []Annotation, typ string) *Annotation {
	for i := range anns {
		if anns[i].Type == typ {
			return &anns[i]
		}
	}
	return nil
}

type reader struct {
	b   []byte
	off int
	err error
}

var errShort = errors.New("class file truncated")

func (r *reader) need(n int) bool {
	if r.err != nil {
		return false
	}
	if n < 0 || r.off+n > len(r.b) {
		r.err = errShort
		return false
	}
	return true
}

func (r *reader) u1() byte {
	if !r.need(1) {
		return 0
	}
	v := r.b[r.off]
	r.off++
	return v
}

func (r *reader) u2() uint16 {
	if !r.need(2) {
		return 0
	}
	v := binary.BigEndian.Uint16(r.b[r.off:])
	r.off += 2
	return v
}

func (r *reader) u4() uint32 {
	if !r.need(4) {
		return 0
	}
	v := binary.BigEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v
}

func (r *reader) bytes(n int) []byte {
	if !r.need(n) {
		return nil
	}
	v := r.b[r.off : r.off+n]
	r.off += n
	return v
}

// ParseClass parses one class file.
func ParseClass(b []byte) (*ClassFile, error) {
	r := &reader{b: b}
	if r.u4() != 0xCAFEBABE {
		return nil, errors.New("not a class file (bad magic)")
	}
	r.u2() // minor
	r.u2() // major
	n := int(r.u2())
	c := &ClassFile{cp: make([]cpEntry, n)}
	for i := 1; i < n; i++ {
		e := cpEntry{tag: r.u1()}
		switch e.tag {
		case cpUtf8:
			l := int(r.u2())
			e.s = decodeMUTF8(r.bytes(l))
		case cpInteger:
			e.i = int64(int32(r.u4()))
		case cpFloat:
			e.f = float64(math.Float32frombits(r.u4()))
		case cpLong:
			hi, lo := r.u4(), r.u4()
			e.i = int64(uint64(hi)<<32 | uint64(lo))
		case cpDouble:
			hi, lo := r.u4(), r.u4()
			e.f = math.Float64frombits(uint64(hi)<<32 | uint64(lo))
		case cpClass, cpString, cpMethodType, cpModule, cpPackage:
			e.a = r.u2()
		case cpFieldref, cpMethodref, cpInterfaceMethodref, cpNameAndType, cpDynamic, cpInvokeDynamic:
			e.a, e.b = r.u2(), r.u2()
		case cpMethodHandle:
			e.a = uint16(r.u1())
			e.b = r.u2()
		default:
			return nil, fmt.Errorf("unknown constant-pool tag %d at entry %d", e.tag, i)
		}
		c.cp[i] = e
		if e.tag == cpLong || e.tag == cpDouble {
			i++ // takes two slots
		}
		if r.err != nil {
			return nil, r.err
		}
	}
	c.Access = r.u2()
	c.Name = c.className(r.u2())
	if s := r.u2(); s != 0 {
		c.Super = c.className(s)
	}
	for i, k := 0, int(r.u2()); i < k; i++ {
		c.Interfaces = append(c.Interfaces, c.className(r.u2()))
	}
	for _, dst := range []*[]Member{&c.Fields, &c.Methods} {
		for i, k := 0, int(r.u2()); i < k; i++ {
			m := Member{Access: r.u2(), Name: c.utf8(r.u2()), Descriptor: c.utf8(r.u2())}
			c.readAttrs(r, &m)
			*dst = append(*dst, m)
		}
	}
	var cm Member
	c.readAttrs(r, &cm)
	c.Signature, c.Annotations, c.TypeAnnotations = cm.Signature, cm.Annotations, cm.TypeAnnotations
	if r.err != nil {
		return nil, r.err
	}
	return c, nil
}

func (c *ClassFile) entry(i uint16) cpEntry {
	if int(i) <= 0 || int(i) >= len(c.cp) {
		return cpEntry{}
	}
	return c.cp[i]
}

func (c *ClassFile) utf8(i uint16) string { return c.entry(i).s }

// className resolves a CONSTANT_Class to a dotted binary name.
func (c *ClassFile) className(i uint16) string {
	return strings.ReplaceAll(c.utf8(c.entry(i).a), "/", ".")
}

// MemberRef is a resolved field or method reference.
type MemberRef struct {
	Owner, Name, Descriptor string // Owner dotted
}

func (c *ClassFile) memberRef(i uint16) MemberRef {
	e := c.entry(i)
	nt := c.entry(e.b)
	return MemberRef{Owner: c.className(e.a), Name: c.utf8(nt.a), Descriptor: c.utf8(nt.b)}
}

// readAttrs reads an attribute table into m (Signature, annotations, Code, ...).
func (c *ClassFile) readAttrs(r *reader, m *Member) {
	for i, k := 0, int(r.u2()); i < k; i++ {
		name := c.utf8(r.u2())
		l := int(r.u4())
		body := r.bytes(l)
		if r.err != nil {
			return
		}
		ar := &reader{b: body}
		switch name {
		case "Signature":
			m.Signature = c.utf8(ar.u2())
		case "RuntimeVisibleAnnotations", "RuntimeInvisibleAnnotations":
			vis := name == "RuntimeVisibleAnnotations"
			for j, n := 0, int(ar.u2()); j < n; j++ {
				m.Annotations = append(m.Annotations, c.readAnnotation(ar, vis))
			}
		case "RuntimeVisibleParameterAnnotations", "RuntimeInvisibleParameterAnnotations":
			vis := name == "RuntimeVisibleParameterAnnotations"
			np := int(ar.u1())
			for len(m.ParamAnnotations) < np {
				m.ParamAnnotations = append(m.ParamAnnotations, nil)
			}
			for p := 0; p < np; p++ {
				for j, n := 0, int(ar.u2()); j < n; j++ {
					m.ParamAnnotations[p] = append(m.ParamAnnotations[p], c.readAnnotation(ar, vis))
				}
			}
		case "RuntimeVisibleTypeAnnotations", "RuntimeInvisibleTypeAnnotations":
			vis := name == "RuntimeVisibleTypeAnnotations"
			for j, n := 0, int(ar.u2()); j < n; j++ {
				skipTypeAnnotationTarget(ar)
				m.TypeAnnotations = append(m.TypeAnnotations, c.readAnnotation(ar, vis))
			}
		case "AnnotationDefault":
			v := c.readElementValue(ar)
			m.AnnotationDefault = &v
		case "Code":
			ar.u2() // max_stack
			ar.u2() // max_locals
			cl := int(ar.u4())
			m.Code = ar.bytes(cl)
		}
		if ar.err != nil && r.err == nil {
			r.err = fmt.Errorf("attribute %s: %w", name, ar.err)
		}
	}
}

// skipTypeAnnotationTarget skips target_type, target_info and type_path
// (JVMS §4.7.20) so the annotation itself can be read.
func skipTypeAnnotationTarget(r *reader) {
	switch t := r.u1(); {
	case t == 0x00 || t == 0x01: // type_parameter
		r.u1()
	case t == 0x10: // supertype
		r.u2()
	case t == 0x11 || t == 0x12: // type_parameter_bound
		r.u1()
		r.u1()
	case t >= 0x13 && t <= 0x15: // empty
	case t == 0x16: // formal_parameter
		r.u1()
	case t == 0x17: // throws
		r.u2()
	case t == 0x40 || t == 0x41: // localvar
		n := int(r.u2())
		r.bytes(6 * n)
	case t == 0x42: // catch
		r.u2()
	case t >= 0x43 && t <= 0x46: // offset
		r.u2()
	case t >= 0x47 && t <= 0x4B: // type_argument
		r.u2()
		r.u1()
	default:
		if r.err == nil {
			r.err = fmt.Errorf("unknown type-annotation target 0x%02x", t)
		}
	}
	n := int(r.u1()) // type_path length
	r.bytes(2 * n)
}

func (c *ClassFile) readAnnotation(r *reader, visible bool) Annotation {
	a := Annotation{Type: descToName(c.utf8(r.u2())), Visible: visible}
	for i, n := 0, int(r.u2()); i < n; i++ {
		name := c.utf8(r.u2())
		a.Elems = append(a.Elems, Element{Name: name, Value: c.readElementValue(r)})
		if r.err != nil {
			break
		}
	}
	return a
}

func (c *ClassFile) readElementValue(r *reader) ElementValue {
	v := ElementValue{Tag: r.u1()}
	switch v.Tag {
	case 'B', 'C', 'I', 'S', 'Z', 'J':
		v.Int = c.entry(r.u2()).i
	case 'D', 'F':
		v.Float = c.entry(r.u2()).f
	case 's':
		v.Str = c.utf8(r.u2())
	case 'e':
		v.EnumType = descToName(c.utf8(r.u2()))
		v.EnumConst = c.utf8(r.u2())
	case 'c':
		v.Class = descToName(c.utf8(r.u2()))
	case '@':
		a := c.readAnnotation(r, true)
		v.Ann = &a
	case '[':
		for i, n := 0, int(r.u2()); i < n; i++ {
			v.Array = append(v.Array, c.readElementValue(r))
			if r.err != nil {
				break
			}
		}
	default:
		if r.err == nil {
			r.err = fmt.Errorf("unknown element-value tag %q", v.Tag)
		}
	}
	return v
}

// descToName turns a field descriptor for a class ("Lcom/x/Foo;") into a
// dotted name; other descriptors are returned with slashes made dots.
func descToName(d string) string {
	if strings.HasPrefix(d, "L") && strings.HasSuffix(d, ";") {
		d = d[1 : len(d)-1]
	}
	return strings.ReplaceAll(d, "/", ".")
}

// decodeMUTF8 decodes the class file's modified UTF-8 (JVMS §4.4.7): a NUL is
// two bytes and supplementary characters are surrogate pairs.
func decodeMUTF8(b []byte) string {
	ascii := true
	for _, x := range b {
		if x >= 0x80 || x == 0 {
			ascii = false
			break
		}
	}
	if ascii {
		return string(b)
	}
	u := make([]uint16, 0, len(b))
	for i := 0; i < len(b); {
		x := b[i]
		switch {
		case x < 0x80:
			u = append(u, uint16(x))
			i++
		case x&0xE0 == 0xC0 && i+1 < len(b):
			u = append(u, uint16(x&0x1F)<<6|uint16(b[i+1]&0x3F))
			i += 2
		case x&0xF0 == 0xE0 && i+2 < len(b):
			u = append(u, uint16(x&0x0F)<<12|uint16(b[i+1]&0x3F)<<6|uint16(b[i+2]&0x3F))
			i += 3
		default:
			u = append(u, 0xFFFD)
			i++
		}
	}
	return string(utf16.Decode(u))
}
