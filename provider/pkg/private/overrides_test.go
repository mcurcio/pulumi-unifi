package private

import (
	"os"
	"strings"
	"testing"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/uos"
)

// The committed kinds.yaml parses strictly and is internally consistent.
func TestKindsYAMLValid(t *testing.T) {
	o, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var exposed []string
	for _, e := range o.Expose {
		exposed = append(exposed, e.Kind+"="+e.Token)
	}
	// Slice 1 (design §10): exactly these four kinds.
	if got := strings.Join(exposed, " "); got != "qos-rules=QosRule apgroups=ApGroup firewall-app-blocks=FirewallAppBlock network-members-group=NetworkMembersGroup" {
		t.Errorf("expose = %s", got)
	}
	// The live findings of the research report are recorded.
	itemGet := map[string]bool{}
	for _, g := range o.ItemGet {
		if g.ItemGet {
			t.Errorf("itemGet %s: only measured 405s (false) are recorded", g.Ref)
		}
		itemGet[g.Kind] = true
	}
	for _, k := range []string{"apgroups", "firewall-app-blocks", "acl-rules", "static-dns"} {
		if !itemGet[k] {
			t.Errorf("itemGet: the measured 405 on %s is not recorded", k)
		}
	}
	ir := map[string]string{}
	for _, l := range o.ImplicitRequired {
		ir[l.Kind] = strings.Join(l.Fields, ",")
	}
	for k, f := range map[string]string{"firewall/zone": "network_ids", "static-dns": "record_type", "nat": "source_filter"} {
		if ir[k] != f {
			t.Errorf("implicitRequired %s = %q, want %q", k, ir[k], f)
		}
	}
}

// Every official twin names tokens that exist in the committed golden.
func TestOfficialTwinsNameGoldenTokens(t *testing.T) {
	p, err := uos.FindRepoFile("provider/pkg/gen/testdata/tokens.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		golden[strings.TrimSpace(l)] = true
	}
	o, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tw := range o.OfficialTwins {
		for _, tok := range tw.Official {
			if !golden[tok] {
				t.Errorf("officialTwins %s: %s is not in the golden token set", tw.Ref, tok)
			}
		}
	}
}

func TestValidateRejects(t *testing.T) {
	base := "version: 1\n"
	for name, c := range map[string]struct{ doc, want string }{
		"unknown key":    {base + "bogus: 1\n", "field bogus not found"},
		"version":        {"version: 2\n", "version: want 1"},
		"no source":      {base + "expose:\n  - {kind: a, token: A, naturalKey: [name]}\n", "source is empty"},
		"bad token":      {base + "expose:\n  - {kind: a, token: notPascal, naturalKey: [name], source: s}\n", "not PascalCase"},
		"dup token":      {base + "expose:\n  - {kind: a, token: A, naturalKey: [n], source: s}\n  - {kind: b, token: A, naturalKey: [n], source: s}\n", "already used"},
		"no key":         {base + "expose:\n  - {kind: a, token: A, source: s}\n", "no fields"},
		"bad path":       {base + "implicitRequired:\n  - {kind: a, fields: [\"a..b\"], source: s}\n", "bad field path"},
		"bad api":        {base + "itemGet:\n  - {kind: a, api: v3, itemGet: false, source: s}\n", "api must be"},
		"exposed twin":   {base + "expose:\n  - {kind: a, token: A, naturalKey: [n], source: s}\nofficialTwins:\n  - {kind: a, official: [x], source: s}\n", "already serves it"},
		"twin no token":  {base + "officialTwins:\n  - {kind: a, source: s}\n", "names no official token"},
		"default no val": {base + "defaults:\n  - {kind: a, field: f, source: s}\n", "value is missing"},
		"rename no prop": {base + "rename:\n  - {kind: a, field: f, source: s}\n", "property is empty"},
		"rule no msg":    {base + "rules:\n  - {kind: a, fields: [f], source: s}\n", "message is empty"},
	} {
		_, err := Parse([]byte(c.doc))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, c.want)
		}
	}
}

func synthModel() *kind.Model {
	str := func(n string) *kind.Field { return &kind.Field{WireName: n, Type: kind.TypeString} }
	return &kind.Model{Source: kind.Source{NetworkVersion: "9.9.9"}, Kinds: []*kind.Kind{
		{Name: "widgets", API: kind.APIV2, Fields: map[string]*kind.Field{
			"name":     str("name"),
			"schedule": {WireName: "schedule", Type: kind.TypeObject, Fields: map[string]*kind.Field{"mode": str("mode")}},
			"monitors": {WireName: "monitors", Type: kind.TypeArray, Items: &kind.Field{Type: kind.TypeObject,
				Fields: map[string]*kind.Field{"host": str("host")}}},
			"target": {WireName: "target", Type: kind.TypeObject, Fields: map[string]*kind.Field{"kind": str("kind")},
				Polymorph: &kind.Polymorph{Prop: "kind", Variants: map[string]map[string]*kind.Field{"NET": {"ids": str("ids")}}}},
		}},
		{Name: "poly", API: kind.APIV2, Fields: map[string]*kind.Field{"name": str("name")},
			Polymorph: &kind.Polymorph{Prop: "t", Variants: map[string]map[string]*kind.Field{"A": {"a": str("a")}}}},
		{Name: "Thing", API: kind.APIClassic, Fields: map[string]*kind.Field{"name": str("name")}},
	}}
}

func TestFieldAt(t *testing.T) {
	k := synthModel().Find(kind.APIV2, "widgets")
	for p, ok := range map[string]bool{
		"name": true, "schedule.mode": true, "monitors[].host": true, "target.ids": true, "target.kind": true,
		"nope": false, "schedule.nope": false, "monitors.host": false, "name[]": false, "monitors[][].host": false,
	} {
		if got := FieldAt(k, p) != nil; got != ok {
			t.Errorf("FieldAt(%s) = %v, want %v", p, got, ok)
		}
	}
	if FieldAt(synthModel().Find(kind.APIV2, "poly"), "a") == nil {
		t.Error("FieldAt does not look into body-level variants")
	}
}

func TestResolve(t *testing.T) {
	ok := `version: 1
expose:
  - {kind: widgets, token: Widget, naturalKey: [name], source: s}
implicitRequired:
  - {kind: widgets, fields: [schedule.mode, "monitors[].host", target.ids], source: s}
notSecret:
  - {kind: Thing, api: classic, fields: [name], source: s}
`
	o, err := Parse([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Resolve(synthModel()); err != nil {
		t.Errorf("Resolve: %v", err)
	}
	for doc, want := range map[string]string{
		"version: 1\nimplicitRequired:\n  - {kind: gone, fields: [x], source: s}\n":           "kind v2:gone is not in the model",
		"version: 1\nimplicitRequired:\n  - {kind: widgets, fields: [renamed], source: s}\n":  "field v2:widgets.renamed is not in the model",
		"version: 1\nexpose:\n  - {kind: poly, token: Poly, naturalKey: [name], source: s}\n": "polymorphic body",
		"version: 1\nitemGet:\n  - {kind: Thing, itemGet: false, source: s}\n":                "kind v2:Thing is not in the model",
	} {
		o, err := Parse([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if err := o.Resolve(synthModel()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Resolve(%q) = %v, want %q", doc, err, want)
		}
	}
}
