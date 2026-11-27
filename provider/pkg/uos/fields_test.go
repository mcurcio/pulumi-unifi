package uos

import (
	"strings"
	"testing"
)

func TestInferValidation(t *testing.T) {
	for _, c := range []struct {
		rule, typ, enum, pattern string
	}{
		{"", "string", "", ""},
		{"true|false", "bool", "", ""},
		{"false|true", "bool", "", ""},
		{"auto|manual|off", "enum", "auto,manual,off", ""},
		{"^$|disabled|enabled", "enum", ",disabled,enabled", ""},
		{"1|2|5", "int", "1,2,5", ""},
		{"[0-9]|1[0-9]|2[0-3]|^$", "int", "", "[0-9]|1[0-9]|2[0-3]|^$"},
		{"^([0-9]|[1-9][0-9]|100)$", "int", "", "^([0-9]|[1-9][0-9]|100)$"},
		{`^[0-9]+\.[0-9]+$`, "number", "", `^[0-9]+\.[0-9]+$`},
		{`^(([0-9]|[1-9][0-9])\.){3}([0-9]|[1-9][0-9])$`, "string", "", `^(([0-9]|[1-9][0-9])\.){3}([0-9]|[1-9][0-9])$`},
		{".{1,64}", "string", "", ".{1,64}"},
		{"[\\d\\w]+", "string", "", "[\\d\\w]+"},
	} {
		f := inferValidation(c.rule)
		if f.Type != c.typ || strings.Join(f.Enum, ",") != c.enum || f.Pattern != c.pattern || f.Source != "fields" {
			t.Errorf("inferValidation(%q) = type %s enum %q pattern %q; want %s %q %q", c.rule, f.Type, strings.Join(f.Enum, ","), f.Pattern, c.typ, c.enum, c.pattern)
		}
	}
}

func TestClassicKinds(t *testing.T) {
	ks, err := ClassicKinds(map[string][]byte{
		"PortForward": []byte(`{"name": ".{1,32}", "fwd_port": "[0-9]{1,5}", "src": [""], "opts": {"a": "true|false"}, "pair": ["", ""]}`),
		"Setting":     []byte(`{"b_sec": {"x": ""}, "a_sec": {"y": "true|false"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range ks {
		names = append(names, k.Name+"="+k.Collection)
	}
	if got := strings.Join(names, " "); got != "PortForward=rest/portforward Setting.a_sec=rest/setting/a_sec Setting.b_sec=rest/setting/b_sec" {
		t.Errorf("kinds = %s", got)
	}
	pf := ks[0]
	if pf.Fields["src"].Type != "array" || pf.Fields["src"].Items.Type != "string" || pf.Fields["opts"].Fields["a"].Type != "bool" ||
		pf.Fields["pair"].Items.Type != "any" || pf.Fields["fwd_port"].Type != "int" || pf.Fields["name"].WireName != "name" {
		t.Errorf("PortForward fields = %+v", pf.Fields)
	}
	if pf.Verbs != "" || pf.IDField != "_id" {
		t.Errorf("classic facts the files do not state must stay unknown: %+v", pf)
	}
	for _, bad := range []string{`[]`, `{"Setting": 1}`, `nope`} {
		if _, err := ClassicKinds(map[string][]byte{"Setting": []byte(bad)}); err == nil {
			t.Errorf("ClassicKinds(%s): no error", bad)
		}
	}
}

func TestSecrets(t *testing.T) {
	s, err := ParseSensitive([]byte(`{"min_field_size": 5, "sensitive_db_fields_by_collection": {"wlanconf": ["x_passphrase", "name"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"x_anything": true, "wpa_psk": true, "radius_secret": true, "PassPhrase": true, "api_token": true,
		"private_key": true, "privatekey": true, "user_password": true, "name": false, "enabled": false, "tokenize": true,
	} {
		if got := SecretName(name); got != want {
			t.Errorf("SecretName(%q) = %v, want %v", name, got, want)
		}
	}
	ks, _ := ClassicKinds(map[string][]byte{"WlanConf": []byte(`{"name": "", "ssid": "", "keys": [""]}`), "Other": []byte(`{"name": ""}`)})
	for _, k := range ks {
		MarkSecrets(k, s)
	}
	if ks[1].Name != "WlanConf" || !ks[1].Fields["name"].Secret || ks[1].Fields["ssid"].Secret {
		t.Errorf("redaction list applies per collection: %+v", ks[1].Fields)
	}
	if ks[0].Fields["name"].Secret {
		t.Error("redaction list leaked to another collection")
	}
	if _, err := ParseSensitive([]byte(`[`)); err == nil {
		t.Error("bad JSON accepted")
	}
}
