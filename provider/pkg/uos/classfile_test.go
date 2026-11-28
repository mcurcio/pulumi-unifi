package uos

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "classes", "com", "ubnt", "fixture", name+".class"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseClassAnnotations(t *testing.T) {
	c, err := ParseClass(readFixture(t, "WidgetBody"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "com.ubnt.fixture.WidgetBody" || c.Access&accInterface == 0 {
		t.Fatalf("class = %s access %#x", c.Name, c.Access)
	}
	tags := c.Method("tags", "")
	if tags == nil {
		t.Fatal("no tags()")
	}
	if tags.Signature != "()Ljava/util/List<Ljava/lang/String;>;" {
		t.Errorf("tags signature = %q", tags.Signature)
	}
	// @Value.Default has CLASS retention: it is read from the invisible table.
	var def *Annotation
	for i := range tags.Annotations {
		if tags.Annotations[i].Type == "org.immutables.value.Value$Default" {
			def = &tags.Annotations[i]
		}
	}
	if def == nil || def.Visible {
		t.Errorf("Value$Default: want an invisible annotation, got %+v", def)
	}
	// The type-use @ValidMac on the list element is a type annotation.
	if findAnn(tags.TypeAnnotations, "com.ubnt.fixture.ValidMac") == nil {
		t.Errorf("type annotations = %+v", tags.TypeAnnotations)
	}
	name := c.Method("name", "")
	size := findAnn(name.Annotations, "jakarta.validation.constraints.Size")
	if size == nil || size.Elem("min").Int != 1 || size.Elem("max").Int != 64 {
		t.Errorf("Size = %+v", size)
	}
	pass := c.Method("pass", "")
	jp := findAnn(pass.Annotations, "com.fasterxml.jackson.annotation.JsonProperty")
	if v := jp.Elem("access"); v == nil || v.Tag != 'e' || v.EnumType != "com.fasterxml.jackson.annotation.JsonProperty$Access" || v.EnumConst != "WRITE_ONLY" {
		t.Errorf("access = %+v", v)
	}
}

func TestParseClassSubTypesAndDefaults(t *testing.T) {
	c, err := ParseClass(readFixture(t, "Matcher"))
	if err != nil {
		t.Fatal(err)
	}
	st := findAnn(c.Annotations, "com.fasterxml.jackson.annotation.JsonSubTypes")
	if st == nil {
		t.Fatal("no @JsonSubTypes")
	}
	var got []string
	for _, e := range st.Elem("value").Array {
		got = append(got, e.Ann.Elem("value").Class+"="+e.Ann.Elem("name").Str)
	}
	want := []string{"com.ubnt.fixture.AnyMatcher=ANY", "com.ubnt.fixture.NetMatcher=NETWORK"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("subtypes = %v", got)
	}
	vm, err := ParseClass(readFixture(t, "ValidMac"))
	if err != nil {
		t.Fatal(err)
	}
	if m := vm.Method("message", ""); m == nil || m.AnnotationDefault == nil || m.AnnotationDefault.Str != "must be a valid MAC" {
		t.Errorf("message() default = %+v", m)
	}
}

func TestParseClassParamAnnotations(t *testing.T) {
	c, err := ParseClass(readFixture(t, "WidgetController"))
	if err != nil {
		t.Fatal(err)
	}
	m := c.Method("update", "")
	if m == nil || len(m.ParamAnnotations) != 2 || findAnn(m.ParamAnnotations[1], "org.springframework.web.bind.annotation.RequestBody") == nil {
		t.Fatalf("update params = %+v", m)
	}
}

// Every truncation of a valid class must be an error, never a panic.
func TestParseClassTruncated(t *testing.T) {
	b := readFixture(t, "WidgetBody")
	for n := 0; n < len(b); n += 7 {
		if _, err := ParseClass(b[:n]); err == nil {
			t.Fatalf("truncated at %d: no error", n)
		}
	}
	if _, err := ParseClass([]byte("not a class file")); err == nil {
		t.Fatal("bad magic accepted")
	}
}

func TestDecodeMUTF8(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                    "plain",
		"\xc0\x80":                 "\x00",
		"caf\xc3\xa9":              "café",
		"\xed\xa0\xbd\xed\xb8\x80": "😀", // a surrogate pair, each half 3 bytes
		"\xe2\x82\xac":             "€",
	} {
		if got := decodeMUTF8([]byte(in)); got != want {
			t.Errorf("decodeMUTF8(%q) = %q, want %q", in, got, want)
		}
	}
}
