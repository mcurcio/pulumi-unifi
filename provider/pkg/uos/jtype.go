package uos

// Java type descriptors and generic signatures (JVMS §4.3, §4.7.9.1).

import "strings"

// JType is a (possibly generic) Java type.
type JType struct {
	Name     string  // dotted class name, primitive ("int"), "void", or a type variable's name
	Args     []JType // type arguments; nil when raw
	Dims     int     // array dimensions
	Wildcard byte    // 0, '*' (?), '+' (? extends Name), '-' (? super Name)
	TypeVar  bool
}

var primDesc = map[byte]string{'B': "byte", 'C': "char", 'D': "double", 'F': "float", 'I': "int", 'J': "long", 'S': "short", 'Z': "boolean", 'V': "void"}

// String renders the type in Java source form (for messages and tests).
func (t JType) String() string {
	var b strings.Builder
	switch t.Wildcard {
	case '*':
		return "?"
	case '+':
		b.WriteString("? extends ")
	case '-':
		b.WriteString("? super ")
	}
	b.WriteString(t.Name)
	if len(t.Args) > 0 {
		b.WriteByte('<')
		for i, a := range t.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(a.String())
		}
		b.WriteByte('>')
	}
	for i := 0; i < t.Dims; i++ {
		b.WriteString("[]")
	}
	return b.String()
}

type sigParser struct {
	s   string
	pos int
	bad bool
}

func (p *sigParser) peek() byte {
	if p.pos >= len(p.s) {
		p.bad = true
		return 0
	}
	return p.s[p.pos]
}

func (p *sigParser) next() byte {
	c := p.peek()
	if !p.bad {
		p.pos++
	}
	return c
}

// typeSig parses one JavaTypeSignature (or a plain field descriptor).
func (p *sigParser) typeSig() JType {
	dims := 0
	for p.peek() == '[' {
		p.pos++
		dims++
	}
	var t JType
	switch c := p.next(); c {
	case 'L':
		t = p.classSig()
	case 'T':
		end := strings.IndexByte(p.s[p.pos:], ';')
		if end < 0 {
			p.bad = true
			return JType{Name: "java.lang.Object"}
		}
		t = JType{Name: p.s[p.pos : p.pos+end], TypeVar: true}
		p.pos += end + 1
	default:
		name, ok := primDesc[c]
		if !ok {
			p.bad = true
			return JType{Name: "java.lang.Object"}
		}
		t = JType{Name: name}
	}
	t.Dims = dims
	return t
}

// classSig parses after 'L' up to and including ';'. Inner classes
// ("Outer<T>.Inner<U>") become Outer$Inner with the innermost arguments.
func (p *sigParser) classSig() JType {
	var name strings.Builder
	var args []JType
	for !p.bad {
		switch c := p.next(); c {
		case ';':
			return JType{Name: strings.ReplaceAll(name.String(), "/", "."), Args: args}
		case '<':
			args = nil
			for p.peek() != '>' && !p.bad {
				switch p.peek() {
				case '*':
					p.pos++
					args = append(args, JType{Name: "?", Wildcard: '*'})
				case '+', '-':
					w := p.next()
					a := p.typeSig()
					a.Wildcard = w
					args = append(args, a)
				default:
					args = append(args, p.typeSig())
				}
			}
			p.next() // '>'
		case '.':
			name.WriteByte('$')
		default:
			name.WriteByte(c)
		}
	}
	return JType{Name: "java.lang.Object"}
}

// skipTypeParams skips a leading "<T:...;U:...>" section.
func (p *sigParser) skipTypeParams() {
	if p.pos < len(p.s) && p.s[p.pos] == '<' {
		p.pos++
		for p.peek() != '>' && !p.bad {
			// identifier
			end := strings.IndexByte(p.s[p.pos:], ':')
			if end < 0 {
				p.bad = true
				return
			}
			p.pos += end
			for p.pos < len(p.s) && p.s[p.pos] == ':' {
				p.pos++
				if p.peek() == ':' || p.peek() == '>' {
					continue
				}
				// a class bound may be empty ("T::Ljava/lang/Comparable;")
				if c := p.peek(); c == 'L' || c == 'T' || c == '[' {
					p.typeSig()
				}
			}
		}
		p.next()
	}
}

// ParseFieldType parses a field's type from its Signature (preferred) or
// descriptor.
func ParseFieldType(desc, sig string) JType {
	if sig != "" {
		p := &sigParser{s: sig}
		if t := p.typeSig(); !p.bad {
			return t
		}
	}
	p := &sigParser{s: desc}
	return p.typeSig()
}

// ParseMethodType parses a method's parameter and return types from its
// Signature (preferred) or descriptor.
func ParseMethodType(desc, sig string) (params []JType, ret JType) {
	if sig != "" {
		if ps, r, ok := parseMethodSig(sig); ok {
			// A generic signature may omit synthetic parameters; trust it
			// only when the arity agrees with the descriptor.
			if dps, _, ok2 := parseMethodSig(desc); ok2 && len(dps) == len(ps) {
				return ps, r
			}
		}
	}
	ps, r, _ := parseMethodSig(desc)
	return ps, r
}

func parseMethodSig(s string) ([]JType, JType, bool) {
	p := &sigParser{s: s}
	p.skipTypeParams()
	if p.next() != '(' {
		return nil, JType{}, false
	}
	var params []JType
	for p.peek() != ')' && !p.bad {
		params = append(params, p.typeSig())
	}
	p.next()
	ret := p.typeSig()
	return params, ret, !p.bad
}
