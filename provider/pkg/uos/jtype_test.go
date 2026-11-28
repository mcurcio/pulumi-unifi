package uos

import "testing"

func TestParseTypes(t *testing.T) {
	for _, c := range []struct{ desc, sig, want string }{
		{"I", "", "int"},
		{"[[J", "", "long[][]"},
		{"Ljava/util/List;", "Ljava/util/List<Ljava/lang/String;>;", "java.util.List<java.lang.String>"},
		{"Ljava/util/Map;", "Ljava/util/Map<Ljava/lang/String;Ljava/util/List<+Lcom/x/Y;>;>;", "java.util.Map<java.lang.String, java.util.List<? extends com.x.Y>>"},
		{"Ljava/util/List;", "Ljava/util/List<*>;", "java.util.List<?>"},
		{"Lcom/x/Outer$Inner;", "Lcom/x/Outer<TT;>.Inner<Ljava/lang/String;>;", "com.x.Outer$Inner<java.lang.String>"},
		{"Ljava/lang/Object;", "TT;", "T"},
		{"Ljava/util/List;", "Ljava/util/List<", "java.util.List"}, // bad signature: the descriptor wins
	} {
		if got := ParseFieldType(c.desc, c.sig).String(); got != c.want {
			t.Errorf("ParseFieldType(%q, %q) = %q, want %q", c.desc, c.sig, got, c.want)
		}
	}
	ps, ret := ParseMethodType("(Ljava/lang/String;Lcom/x/B;)Lorg/springframework/http/ResponseEntity;",
		"<T:Ljava/lang/Object;U::Ljava/lang/Comparable<TU;>;>(Ljava/lang/String;Lcom/x/B;)Lorg/springframework/http/ResponseEntity<Lcom/x/V;>;")
	if len(ps) != 2 || ps[1].Name != "com.x.B" || ret.String() != "org.springframework.http.ResponseEntity<com.x.V>" {
		t.Errorf("ParseMethodType = %v %v", ps, ret)
	}
	// A generic signature with a different arity (synthetic parameters) is ignored.
	ps, _ = ParseMethodType("(Lcom/x/Outer;I)V", "(I)V")
	if len(ps) != 2 {
		t.Errorf("arity mismatch not caught: %v", ps)
	}
}
