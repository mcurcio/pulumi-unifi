package uos

// JSON field models of Spring request/response bodies, recovered from
// (obfuscated) bytecode: a port of tools/uos-extract/dto.py onto the
// class-file parser. It anchors on annotations and on the real field names
// that survive obfuscation, never on class names.
//
// Per field: type, required (@NotNull/@NotBlank/@NotEmpty, Swagger REQUIRED),
// optional (Optional<>), default (@Value.Default: the constant when the body
// returns a plain constant, else "computed"), constraints (Jakarta/Hibernate
// validation, custom validators' messages), enum wire values (@JsonValue, or
// @JsonProperty on constants, else the constant's name), polymorphism
// (@JsonTypeInfo + @JsonSubTypes).

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// tmodel is the extractor's intermediate body model (dto.py's dicts).
type tmodel struct {
	Type      string // bool int number string uuid datetime date any enum object array map ref
	Values    []string
	Items     *tmodel
	MapValues *tmodel
	Fields    []tfield
	Disc      string
	HasDisc   bool
	Variants  []tvariant

	Required, ReadOnly, WriteOnly, Optional bool

	HasDefault      bool
	Default         any // string, int64, float64, bool, []any{}, map[string]any{}, nil
	DefaultComputed bool

	Pattern  string
	Len      *rangeC
	Size     *rangeC
	Min, Max *float64
	NonEmpty bool
	Custom   []string
}

type rangeC struct{ Min, Max *int64 }

type tfield struct {
	Name string
	M    *tmodel
}

type tvariant struct {
	Name string
	M    *tmodel
}

func (m *tmodel) field(name string) *tmodel {
	for _, f := range m.Fields {
		if f.Name == name {
			return f.M
		}
	}
	return nil
}

var primTypes = map[string]string{
	"boolean": "bool", "java.lang.Boolean": "bool",
	"int": "int", "long": "int", "short": "int",
	"java.lang.Integer": "int", "java.lang.Long": "int", "java.lang.Short": "int",
	"double": "number", "float": "number", "java.lang.Double": "number", "java.lang.Float": "number",
	"java.math.BigDecimal": "number",
	"java.lang.String":     "string", "java.util.UUID": "uuid",
	"java.time.Instant": "datetime", "java.time.OffsetDateTime": "datetime", "java.time.LocalDate": "date",
	"java.lang.Object": "any", "com.fasterxml.jackson.databind.JsonNode": "any",
}

const (
	jacksonAnn = "com.fasterxml.jackson.annotation."
	maxDepth   = 8
)

// dto models body types over a class index.
type dto struct {
	x      *Index
	enums  map[string][]enumConst
	custom map[string]string
}

type enumConst struct{ Field, Wire string }

func newDTO(x *Index) *dto {
	return &dto{x: x, enums: map[string][]enumConst{}, custom: map[string]string{}}
}

func isEnum(c *ClassFile) bool { return c != nil && c.Super == "java.lang.Enum" }

// model returns the JSON model of a Java type.
func (d *dto) model(t JType, depth int, seen map[string]bool) *tmodel {
	if t.Wildcard == '*' || t.Wildcard == '-' || t.TypeVar {
		return &tmodel{Type: "ref"}
	}
	if t.Dims > 0 {
		e := t
		e.Dims--
		return &tmodel{Type: "array", Items: d.model(e, depth, seen)}
	}
	arg := func(i int) JType {
		if i < len(t.Args) {
			return t.Args[i]
		}
		return JType{Name: "java.lang.Object"}
	}
	switch t.Name {
	case "java.util.Optional":
		r := d.model(arg(0), depth, seen)
		r.Optional = true
		return r
	case "java.util.List", "java.util.Set", "java.util.Collection", "java.util.SortedSet":
		return &tmodel{Type: "array", Items: d.model(arg(0), depth, seen)}
	case "java.util.Map", "java.util.SortedMap":
		return &tmodel{Type: "map", MapValues: d.model(arg(1), depth, seen)}
	}
	if p, ok := primTypes[t.Name]; ok {
		return &tmodel{Type: p}
	}
	if !strings.HasPrefix(t.Name, "com.ubnt") || depth > maxDepth || seen[t.Name] {
		return &tmodel{Type: "ref"}
	}
	seen2 := make(map[string]bool, len(seen)+1)
	for k := range seen {
		seen2[k] = true
	}
	seen2[t.Name] = true
	c := d.x.Class(t.Name)
	if isEnum(c) {
		var vals []string
		have := map[string]bool{}
		for _, e := range d.enumInfo(c) {
			if !have[e.Wire] {
				have[e.Wire] = true
				vals = append(vals, e.Wire)
			}
		}
		return &tmodel{Type: "enum", Values: vals}
	}
	out := &tmodel{Type: "object"}
	if c == nil {
		return out
	}
	if a := findAnn(c.Annotations, jacksonAnn+"JsonTypeInfo"); a != nil {
		out.HasDisc = true
		if v := a.Elem("property"); v != nil && v.Tag == 's' {
			out.Disc = v.Str
		}
	}
	type sub struct{ class, name string }
	var subs []sub
	if a := findAnn(c.Annotations, jacksonAnn+"JsonSubTypes"); a != nil {
		if v := a.Elem("value"); v != nil {
			for _, e := range v.Array {
				if e.Tag != '@' || e.Ann == nil {
					continue
				}
				cv, nv := e.Ann.Elem("value"), e.Ann.Elem("name")
				if cv != nil && cv.Tag == 'c' && nv != nil && nv.Tag == 's' {
					subs = append(subs, sub{cv.Class, nv.Str})
				}
			}
		}
	}
	// Getter-style properties (Immutables interfaces): no-arg, non-private
	// methods carrying @JsonProperty(value=...).
	for i := range c.Methods {
		m := &c.Methods[i]
		if m.Access&accPrivate != 0 || !strings.HasPrefix(m.Descriptor, "()") || strings.HasPrefix(m.Name, "<") {
			continue
		}
		name, ok := jsonPropertyName(m.Annotations)
		if !ok || name == "" || jsonIgnored(m.Annotations) {
			continue
		}
		_, ret := ParseMethodType(m.Descriptor, m.Signature)
		f := d.annotate(d.model(ret, depth+1, seen2), m.AllAnnotations())
		if hasAnnContaining(m.AllAnnotations(), "Value$Default") {
			d.defaultOf(c, m, f)
		}
		if out.field(name) == nil {
			out.Fields = append(out.Fields, tfield{name, f})
		}
	}
	// Field-level annotations (Lombok/POJO style), even when getters also
	// carry @JsonProperty; superclass fields are inherited.
	var pojo []tfield
	d.pojoFields(c, &pojo, depth, seen2, map[string]bool{})
	for _, pf := range pojo {
		g := out.field(pf.Name)
		if g == nil {
			out.Fields = append(out.Fields, pf)
			continue
		}
		g.Required = g.Required || pf.M.Required
		g.ReadOnly = g.ReadOnly || pf.M.ReadOnly
		g.WriteOnly = g.WriteOnly || pf.M.WriteOnly
		mergeConstraints(g, pf.M)
	}
	for _, s := range subs {
		out.Variants = append(out.Variants, tvariant{s.name, d.model(JType{Name: s.class}, depth+1, seen2)})
	}
	if len(subs) > 0 && out.Disc != "" && out.field(out.Disc) == nil {
		// The type id rides in a property no field declares.
		names := make([]string, 0, len(subs))
		for _, s := range subs {
			names = append(names, s.name)
		}
		out.Fields = append(out.Fields, tfield{out.Disc, &tmodel{Type: "enum", Values: names, Required: true}})
	}
	return out
}

// mergeConstraints fills g's constraints from p where g states none (the
// getter's own annotations win, as in dto.py).
func mergeConstraints(g, p *tmodel) {
	if g.Len == nil {
		g.Len = p.Len
	}
	if g.Size == nil {
		g.Size = p.Size
	}
	if g.Min == nil {
		g.Min = p.Min
	}
	if g.Max == nil {
		g.Max = p.Max
	}
	if g.Pattern == "" {
		g.Pattern = p.Pattern
	}
	g.NonEmpty = g.NonEmpty || p.NonEmpty
	if len(g.Custom) == 0 {
		g.Custom = p.Custom
	}
}

func (d *dto) pojoFields(c *ClassFile, out *[]tfield, depth int, seen map[string]bool, visited map[string]bool) {
	if visited[c.Name] {
		return
	}
	visited[c.Name] = true
	if strings.HasPrefix(c.Super, "com.ubnt") {
		if sc := d.x.Class(c.Super); sc != nil {
			d.pojoFields(sc, out, depth, seen, visited)
		}
	}
	getters := map[string]bool{}
	for _, m := range c.Methods {
		if m.Access&accPublic != 0 && strings.HasPrefix(m.Descriptor, "()") &&
			(strings.HasPrefix(m.Name, "get") || strings.HasPrefix(m.Name, "is")) {
			getters[m.Name] = true
		}
	}
	for i := range c.Fields {
		f := &c.Fields[i]
		if f.Access&accStatic != 0 || f.Access&(accPrivate|accProtected|accPublic) == 0 {
			continue
		}
		up := f.Name
		if up != "" {
			up = strings.ToUpper(up[:1]) + up[1:]
		}
		hasGetter := getters["get"+up] || getters["is"+up]
		jp, hasJP := jsonPropertyName(f.Annotations)
		if !hasGetter && findAnn(f.Annotations, jacksonAnn+"JsonProperty") == nil {
			continue // obfuscated getters: trust @JsonProperty
		}
		if jsonIgnored(f.Annotations) {
			continue
		}
		name := f.Name
		if hasJP && jp != "" {
			name = jp
		}
		m := d.annotate(d.model(ParseFieldType(f.Descriptor, f.Signature), depth+1, seen), f.AllAnnotations())
		replaced := false
		for j := range *out {
			if (*out)[j].Name == name {
				(*out)[j].M = m // a subclass field shadows its superclass's
				replaced = true
			}
		}
		if !replaced {
			*out = append(*out, tfield{name, m})
		}
	}
}

// jsonPropertyName returns @JsonProperty's value; ok is false when there is
// no @JsonProperty with a value element.
func jsonPropertyName(anns []Annotation) (string, bool) {
	a := findAnn(anns, jacksonAnn+"JsonProperty")
	if a == nil {
		return "", false
	}
	v := a.Elem("value")
	if v == nil || v.Tag != 's' {
		return "", false
	}
	return v.Str, true
}

// jsonIgnored: @JsonIgnore with no value, or value=true.
func jsonIgnored(anns []Annotation) bool {
	a := findAnn(anns, jacksonAnn+"JsonIgnore")
	if a == nil {
		return false
	}
	v := a.Elem("value")
	return v == nil || v.Int != 0
}

func hasAnnContaining(anns []Annotation, s string) bool {
	for _, a := range anns {
		if strings.Contains(a.Type, s) {
			return true
		}
	}
	return false
}

var (
	reRequiredAnn   = regexp.MustCompile(`constraints\.(NotNull|NotBlank|NotEmpty)\b`)
	reConstraintAnn = regexp.MustCompile(`(?:validation\.constraints|hibernate\.validator\.constraints)\.(\w+)$`)
)

// walkValues visits every element value of an annotation, recursively.
func walkValues(a *Annotation, fn func(*ElementValue, string)) {
	for i := range a.Elems {
		walkValue(&a.Elems[i].Value, a.Elems[i].Name, fn)
	}
}

func walkValue(v *ElementValue, name string, fn func(*ElementValue, string)) {
	fn(v, name)
	switch v.Tag {
	case '@':
		if v.Ann != nil {
			walkValues(v.Ann, fn)
		}
	case '[':
		for i := range v.Array {
			walkValue(&v.Array[i], name, fn)
		}
	}
}

// annotate applies a member's annotations to its model.
func (d *dto) annotate(f *tmodel, anns []Annotation) *tmodel {
	customs := map[string]bool{}
	for i := range anns {
		a := &anns[i]
		if reRequiredAnn.MatchString(a.Type) {
			f.Required = true
		}
		if strings.HasSuffix(a.Type, "media.Schema") {
			if v := a.Elem("required"); v != nil && v.Tag == 'Z' && v.Int != 0 {
				f.Required = true
			}
		}
		walkValues(a, func(v *ElementValue, _ string) {
			if v.Tag != 'e' {
				return
			}
			switch {
			case strings.HasSuffix(v.EnumType, "RequiredMode") && v.EnumConst == "REQUIRED":
				f.Required = true
			case (strings.HasSuffix(v.EnumType, "$Access") || strings.HasSuffix(v.EnumType, "$AccessMode")) && v.EnumConst == "READ_ONLY":
				f.ReadOnly = true
			case (strings.HasSuffix(v.EnumType, "$Access") || strings.HasSuffix(v.EnumType, "$AccessMode")) && v.EnumConst == "WRITE_ONLY":
				f.WriteOnly = true
			}
		})
		if m := reConstraintAnn.FindStringSubmatch(a.Type); m != nil {
			applyConstraint(f, m[1], a)
		}
		if strings.HasPrefix(a.Type, "com.ubnt") {
			if msg := d.customConstraint(a.Type); msg != "" {
				customs[msg] = true
			}
		}
	}
	for m := range customs {
		f.Custom = append(f.Custom, m)
	}
	sort.Strings(f.Custom)
	return f
}

func intElem(a *Annotation, name string) *int64 {
	if v := a.Elem(name); v != nil && (v.Tag == 'I' || v.Tag == 'J' || v.Tag == 'S' || v.Tag == 'B') {
		x := v.Int
		return &x
	}
	return nil
}

func numElem(a *Annotation, name string) *float64 {
	v := a.Elem(name)
	if v == nil {
		return nil
	}
	switch v.Tag {
	case 'I', 'J', 'S', 'B':
		x := float64(v.Int)
		return &x
	case 'D', 'F':
		x := v.Float
		return &x
	case 's':
		if x, err := strconv.ParseFloat(v.Str, 64); err == nil {
			return &x
		}
	}
	return nil
}

func applyConstraint(f *tmodel, cn string, a *Annotation) {
	switch cn {
	case "NotNull":
	case "NotEmpty", "NotBlank":
		f.NonEmpty = true
	case "Size":
		if r := (rangeC{intElem(a, "min"), intElem(a, "max")}); r.Min != nil || r.Max != nil {
			f.Size = &r
		}
	case "Length":
		if r := (rangeC{intElem(a, "min"), intElem(a, "max")}); r.Min != nil || r.Max != nil {
			f.Len = &r
		}
	case "Min", "DecimalMin", "PositiveOrZero":
		if x := numElem(a, "value"); x != nil {
			f.Min = x
		}
	case "Max", "DecimalMax":
		if x := numElem(a, "value"); x != nil {
			f.Max = x
		}
	case "Range":
		if x := numElem(a, "min"); x != nil {
			f.Min = x
		}
		if x := numElem(a, "max"); x != nil {
			f.Max = x
		}
	case "Pattern":
		if v := a.Elem("regexp"); v != nil && v.Tag == 's' {
			f.Pattern = v.Str
		}
	}
}

// customConstraint returns a project annotation's message when it is a
// Bean Validation constraint (meta-annotated with @Constraint), else "".
func (d *dto) customConstraint(typ string) string {
	if m, ok := d.custom[typ]; ok {
		return m
	}
	msg := ""
	if c := d.x.Class(typ); c != nil &&
		(findAnn(c.Annotations, "jakarta.validation.Constraint") != nil || findAnn(c.Annotations, "javax.validation.Constraint") != nil) {
		msg = "custom constraint " + typ[strings.LastIndexAny(typ, ".$")+1:]
		if m := c.Method("message", ""); m != nil && m.AnnotationDefault != nil && m.AnnotationDefault.Tag == 's' {
			msg = m.AnnotationDefault.Str
		}
	}
	d.custom[typ] = msg
	return msg
}

// ---------- enums ----------

// enumInfo returns an enum's constants in declaration order with their wire
// values: @JsonProperty on the constant, else the constructor string that a
// @JsonValue accessor returns, else the constant's name.
func (d *dto) enumInfo(c *ClassFile) []enumConst {
	if e, ok := d.enums[c.Name]; ok {
		return e
	}
	d.enums[c.Name] = nil
	// Does @JsonValue serialize a field (getfield) or name()?
	jsonValueField := false
	for i := range c.Methods {
		m := &c.Methods[i]
		if m.Access&accPublic == 0 || !strings.HasPrefix(m.Descriptor, "()") || m.Access&accStatic != 0 {
			continue
		}
		if findAnn(m.Annotations, jacksonAnn+"JsonValue") == nil {
			continue
		}
		getfield, callsName := false, false
		for _, in := range c.Code(m.Name, m.Descriptor) {
			switch in.Op {
			case opGetfield:
				getfield = true
			case opInvokevirtual, opInvokespecial, opInvokeiface:
				if r := c.ref(in.Arg); r.Name == "name" && r.Descriptor == "()Ljava/lang/String;" {
					callsName = true
				}
			}
		}
		jsonValueField = getfield && !callsName
	}
	props := map[string]string{}
	isConst := map[string]bool{}
	var order []string
	for i := range c.Fields {
		f := &c.Fields[i]
		if f.Access&accEnum == 0 {
			continue
		}
		isConst[f.Name] = true
		order = append(order, f.Name)
		if p, ok := jsonPropertyName(f.Annotations); ok {
			props[f.Name] = p
		}
	}
	// Walk <clinit>: each constant is `new C; dup; <ctor args>; invokespecial
	// C.<init>; putstatic C.CONST`. Collect the String literals of the
	// constructor arguments.
	wire := map[string]string{}
	var clinitOrder []string
	ins := c.Code("<clinit>", "()V")
	for i, in := range ins {
		if in.Op != opPutstatic {
			continue
		}
		r := c.ref(in.Arg)
		if r.Owner != c.Name || !isConst[r.Name] {
			continue
		}
		if _, done := wire[r.Name]; done {
			continue
		}
		var lits []string
		// find the matching `new` (C or an anonymous C$N) before this putstatic
		start := -1
		for j := i - 1; j >= 0; j-- {
			if ins[j].Op == opNew {
				if n := c.classRef(ins[j].Arg); n == c.Name || strings.HasPrefix(n, c.Name+"$") {
					start = j
					break
				}
			}
			if ins[j].Op == opPutstatic {
				break
			}
		}
		if start >= 0 {
			newCls := c.classRef(ins[start].Arg)
			for j := start + 1; j < i; j++ {
				if ins[j].Op == opInvokespecial {
					if r2 := c.ref(ins[j].Arg); r2.Owner == newCls && r2.Name == "<init>" {
						break
					}
				}
				if (ins[j].Op == opLdc || ins[j].Op == opLdcW) && c.isStringConst(ins[j].Arg) {
					v, _ := c.constLiteral(ins[j].Arg)
					lits = append(lits, v.(string))
				}
			}
		}
		name := r.Name
		if len(lits) > 0 {
			name = lits[0]
		}
		w := name
		if p, ok := props[r.Name]; ok {
			w = p
		} else if jsonValueField && len(lits) == 2 {
			w = lits[1]
		}
		wire[r.Name] = w
		clinitOrder = append(clinitOrder, r.Name)
	}
	var out []enumConst
	for _, n := range clinitOrder {
		out = append(out, enumConst{n, wire[n]})
	}
	for _, n := range order { // constants not seen in <clinit>
		if _, ok := wire[n]; !ok {
			w := n
			if p, ok := props[n]; ok {
				w = p
			}
			out = append(out, enumConst{n, w})
		}
	}
	d.enums[c.Name] = out
	return out
}

func (d *dto) enumWire(c *ClassFile, constName string) (string, bool) {
	for _, e := range d.enumInfo(c) {
		if e.Field == constName {
			return e.Wire, true
		}
	}
	return "", false
}

// staticValue resolves a static field to a literal: an enum constant's wire
// value, or a constant copied from one in <clinit>.
func (d *dto) staticValue(owner, field string, depth int) (any, bool) {
	c := d.x.Class(owner)
	if c == nil {
		return nil, false
	}
	if isEnum(c) {
		if w, ok := d.enumWire(c, field); ok {
			return w, true
		}
		return nil, false
	}
	if depth > 1 {
		return nil, false
	}
	ins := c.Code("<clinit>", "()V")
	for i := 1; i < len(ins); i++ {
		if ins[i].Op != opPutstatic || ins[i-1].Op != opGetstatic {
			continue
		}
		if r := c.ref(ins[i].Arg); r.Owner == c.Name && r.Name == field {
			g := c.ref(ins[i-1].Arg)
			return d.staticValue(g.Owner, g.Name, depth+1)
		}
	}
	return nil, false
}

var boxing = map[string]bool{
	"java.lang.Integer": true, "java.lang.Long": true, "java.lang.Short": true, "java.lang.Byte": true,
	"java.lang.Boolean": true, "java.lang.Double": true, "java.lang.Float": true,
}

// defaultOf decodes an @Value.Default method body into a constant when it is
// `<const>; [box;] return`, else marks the default computed.
func (d *dto) defaultOf(c *ClassFile, m *Member, f *tmodel) {
	f.HasDefault = true
	ins, err := decodeCode(m.Code)
	if err != nil || len(ins) < 2 || len(ins) > 3 {
		f.DefaultComputed = true
		return
	}
	last := ins[len(ins)-1].Op
	if last < opIreturn || last > opAreturn {
		f.DefaultComputed = true
		return
	}
	boxBool := false
	if len(ins) == 3 {
		mid := ins[1]
		r := c.ref(mid.Arg)
		if mid.Op != opInvokestatic || !boxing[r.Owner] || r.Name != "valueOf" {
			f.DefaultComputed = true
			return
		}
		boxBool = r.Owner == "java.lang.Boolean"
	}
	_, ret := ParseMethodType(m.Descriptor, "")
	retBool := ret.Name == "boolean" && ret.Dims == 0
	a := ins[0]
	switch {
	case a.Op >= opIconstM1 && a.Op <= opIconst5, a.Op == opBipush, a.Op == opSipush:
		n := int64(a.Arg)
		if a.Op <= opIconst5 {
			n = int64(int(a.Op) - opIconst0)
		}
		if retBool || boxBool {
			f.Default = n != 0
		} else {
			f.Default = n
		}
	case a.Op == opLconst0 || a.Op == opLconst1:
		f.Default = int64(a.Op - opLconst0)
	case a.Op >= opFconst0 && a.Op <= opFconst2:
		f.Default = float64(a.Op - opFconst0)
	case a.Op == opDconst0 || a.Op == opDconst1:
		f.Default = float64(a.Op - opDconst0)
	case a.Op == opLdc || a.Op == opLdcW || a.Op == opLdc2W:
		v, ok := c.constLiteral(a.Arg)
		if !ok {
			f.DefaultComputed = true
			return
		}
		f.Default = v
	case a.Op == opGetstatic && len(ins) == 2:
		r := c.ref(a.Arg)
		v, ok := d.staticValue(r.Owner, r.Name, 0)
		if !ok {
			f.DefaultComputed = true
			return
		}
		f.Default = v
	case a.Op == opInvokestatic && len(ins) == 2:
		r := c.ref(a.Arg)
		if !strings.HasPrefix(r.Descriptor, "()") {
			f.DefaultComputed = true
			return
		}
		switch {
		case (r.Owner == "java.util.List" || r.Owner == "java.util.Set") && (r.Name == "of"):
			f.Default = []any{}
		case r.Owner == "java.util.Map" && r.Name == "of":
			f.Default = map[string]any{}
		case r.Owner == "java.util.Optional" && r.Name == "empty":
			f.Default = nil
		default:
			f.DefaultComputed = true
		}
	default:
		f.DefaultComputed = true
	}
}

// ---------- routes → bodies ----------

// handler returns the controller method for a route.
func (d *dto) handler(r *Route) (*ClassFile, *Member) {
	c := d.x.Class(r.Class)
	if c == nil {
		return nil, nil
	}
	return c, c.Method(r.Method, r.Desc)
}

// bodyType is the @RequestBody parameter's type, if any.
func (d *dto) bodyType(r *Route) (JType, bool) {
	_, m := d.handler(r)
	if m == nil {
		return JType{}, false
	}
	params, _ := ParseMethodType(m.Descriptor, m.Signature)
	for i, pa := range m.ParamAnnotations {
		if findAnn(pa, springAnn+"RequestBody") != nil && i < len(params) {
			return params[i], true
		}
	}
	return JType{}, false
}

// returnType is the handler's return type, unwrapping ResponseEntity<>.
func (d *dto) returnType(r *Route) (JType, bool) {
	_, m := d.handler(r)
	if m == nil {
		return JType{}, false
	}
	_, ret := ParseMethodType(m.Descriptor, m.Signature)
	if ret.Name == "void" && ret.Dims == 0 {
		return JType{}, false
	}
	if ret.Name == "org.springframework.http.ResponseEntity" && len(ret.Args) == 1 && ret.Dims == 0 {
		ret = ret.Args[0]
	}
	return ret, true
}
