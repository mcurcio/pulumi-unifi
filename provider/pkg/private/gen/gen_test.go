package gen

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/uos"
)

func golden(t *testing.T) []byte {
	t.Helper()
	p, err := uos.FindRepoFile("provider/cmd/pulumi-resource-unifi/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// synthModel is a small model with self-written names (no Ubiquiti text).
func synthModel() *kind.Model {
	str := func(req bool) *kind.Field {
		return &kind.Field{Type: kind.TypeString, Required: req, Source: kind.SourceBytecode}
	}
	return &kind.Model{
		Source: kind.Source{UOSVersion: "0.0.1", NetworkVersion: "0.0.0-test"},
		Kinds: []*kind.Kind{
			{
				Name: "widgets", API: kind.APIV2, Collection: "widgets", ItemPath: "widgets/{id}", CreatePath: "widgets",
				Verbs: "CRUD", ListGet: true, ItemGet: true, IDField: "_id", Envelope: kind.EnvelopeBare,
				Fields: map[string]*kind.Field{
					"_id":        {Type: kind.TypeString, Source: kind.SourceBytecode},
					"label":      {Type: kind.TypeString, Source: kind.SourceBytecode},
					"rate_kbps":  {Type: kind.TypeInt, Source: kind.SourceBytecode, Constraints: &kind.Constraints{Min: ptr(0.0)}},
					"mode":       {Type: kind.TypeEnum, Enum: []string{"ON", "OFF"}, Required: true, Default: raw(`"ON"`), Source: kind.SourceBytecode},
					"wifi_psk":   {Type: kind.TypeString, Secret: true, Source: kind.SourceBytecode},
					"created_at": {Type: kind.TypeDateTime, ReadOnly: true, Source: kind.SourceBytecode},
					"origin":     {Type: kind.TypeString, Optional: true, Source: kind.SourceBytecode},
					"tags":       {Type: kind.TypeArray, Items: &kind.Field{Type: kind.TypeString}, Constraints: &kind.Constraints{Size: &kind.Range{Max: ptr(int64(4))}}, Source: kind.SourceBytecode},
					"target": {Type: kind.TypeObject, Required: true, Source: kind.SourceBytecode,
						Fields: map[string]*kind.Field{"by": {Type: kind.TypeEnum, Enum: []string{"ALL", "IP"}, Required: true, Default: raw(`"ALL"`), Source: kind.SourceBytecode}},
						Polymorph: &kind.Polymorph{Prop: "by", Variants: map[string]map[string]*kind.Field{
							"ALL": {},
							"IP":  {"ips": {Type: kind.TypeArray, Items: &kind.Field{Type: kind.TypeString}, Required: true, Source: kind.SourceBytecode}},
						}}},
				},
			},
			{
				Name: "gadgets", API: kind.APIV2, Collection: "gadget", ItemPath: "gadget/{id}", CreatePath: "gadget",
				Verbs: "CRUD", ItemGet: true, IDField: "id", Envelope: kind.EnvelopeBare,
				Fields: map[string]*kind.Field{"id": str(false), "name": str(true), "members": {Type: kind.TypeArray, Items: str(false), Source: kind.SourceBytecode}},
			},
			{Name: "twins", API: kind.APIV2, Collection: "twins", ItemPath: "twins/{id}", Verbs: "CRUD", ListGet: true, ItemGet: true, IDField: "_id",
				Envelope: kind.EnvelopeBare, Fields: map[string]*kind.Field{"name": str(true)}},
			{Name: "readonly", API: kind.APIV2, Collection: "readonly", ItemPath: "readonly/{id}", Verbs: "-R--", ListGet: true, ItemGet: true, IDField: "_id",
				Envelope: kind.EnvelopeBare, Fields: map[string]*kind.Field{"name": str(true)}},
			{Name: "urnish", API: kind.APIV2, Collection: "urnish", ItemPath: "urnish/{id}", Verbs: "CRUD", ListGet: true, ItemGet: true, IDField: "_id",
				Envelope: kind.EnvelopeBare, Fields: map[string]*kind.Field{"name": str(true), "urn": str(false)}},
		},
	}
}

func ptr[T any](v T) *T { return &v }

const synthYAML = `version: 1
expose:
  - {kind: widgets, token: Widget, naturalKey: [label], source: "test"}
  - {kind: gadgets, token: Gadget, naturalKey: [name], source: "test"}
officialTwins:
  - {kind: twins, official: ["resource:unifi:sites/v1:FirewallZone"], source: "test"}
listPath:
  - {kind: gadgets, path: gadgets-all, source: "test"}
defaults:
  - {kind: widgets, field: rate_kbps, value: 0, source: "test"}
createOnly:
  - {kind: widgets, fields: [label], source: "test"}
rename:
  - {kind: urnish, field: urn, property: urnName, source: "test"}
`

func synthOverrides(t *testing.T, extra string) *private.Overrides {
	t.Helper()
	o, err := private.Parse([]byte(synthYAML + extra))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// PRIVATE=0: no model, the merged schema is the golden byte for byte.
func TestNoModelIsGolden(t *testing.T) {
	g := golden(t)
	o, err := private.Load()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(nil, o, g)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Schema, g) {
		t.Fatal("without a model, schema.merged.json must equal schema.json byte for byte")
	}
	s, err := engine.ParseSurface(out.Surface)
	if err != nil || len(s.Kinds) != 0 || len(out.Tokens) != 0 {
		t.Fatalf("empty surface: %v %v", s, err)
	}
	// The engine-served official singletons are served at PRIVATE=0 too; the
	// dispatcher test pins this exact entry (dispatch_singleton_test.go).
	want := `{"api":"official","merge":"leadingBlock","path":"/v1/sites/{siteId}/acl-rules/ordering","property":"orderedAclRuleIds","token":"unifi:sites/v1:AclRuleOrdering"}`
	if !strings.Contains(string(bytes.Join(bytes.Fields(out.Surface), nil)), want) {
		t.Errorf("the PRIVATE=0 surface must serve AclRuleOrdering: %s", out.Surface)
	}
	if err := Lint(g, out.Schema, out.Surface); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateSynthetic(t *testing.T) {
	g := golden(t)
	out, err := Generate(synthModel(), synthOverrides(t, ""), g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Tokens, ",") != "resource:unifi:network:Gadget,resource:unifi:network:Widget" {
		t.Errorf("tokens %v", out.Tokens)
	}
	if err := Lint(g, out.Schema, out.Surface); err != nil {
		t.Fatal(err)
	}
	var spec pschema.PackageSpec
	if err := json.Unmarshal(out.Schema, &spec); err != nil {
		t.Fatal(err)
	}
	w := spec.Resources["unifi:network:Widget"]
	if _, ok := w.InputProperties["id"]; ok {
		t.Error("the id field is the resource id, not a property")
	}
	if _, ok := w.InputProperties["createdAt"]; ok {
		t.Error("read-only fields are outputs only")
	}
	if _, ok := w.Properties["createdAt"]; !ok {
		t.Error("read-only fields are outputs")
	}
	if !w.InputProperties["wifiPsk"].Secret {
		t.Error("secret not flagged")
	}
	if !w.InputProperties["label"].ReplaceOnChanges {
		t.Error("createOnly must be replaceOnChanges")
	}
	// mode has a default (not required); target is required; label is the
	// natural key (required).
	if got := strings.Join(w.RequiredInputs, ","); got != "label,target" {
		t.Errorf("required inputs %s", got)
	}
	tt, ok := spec.Types["unifi:network:WidgetTarget"]
	if !ok {
		t.Fatal("nested object type missing")
	}
	if _, ok := tt.Properties["ips"]; !ok || len(tt.Required) != 0 {
		t.Errorf("variant fields are flattened and optional: %+v", tt)
	}
	if spec.Config.Variables[ConfigBaseURL].Type != "string" || spec.Provider.InputProperties[ConfigBaseURL].DefaultInfo.Environment[0] != EnvBaseURL {
		t.Error("networkBaseUrl config missing")
	}
	s, err := engine.ParseSurface(out.Surface)
	if err != nil {
		t.Fatal(err)
	}
	gk := s.Find("unifi:network:Gadget")
	if gk.ListPath != "gadgets-all" || !gk.ListGet || gk.IDField != "id" {
		t.Errorf("gadget kind %+v", gk)
	}
	wk := s.Find("unifi:network:Widget")
	if string(wk.Fields["rate_kbps"].Default) != "0" || !wk.Fields["origin"].Nullable || wk.Fields["tags"].Constraints.MaxItems == nil {
		t.Errorf("widget facts %+v %+v", wk.Fields["rate_kbps"], wk.Fields["origin"])
	}
	// Deterministic.
	again, _ := Generate(synthModel(), synthOverrides(t, ""), g)
	if !bytes.Equal(again.Schema, out.Schema) || !bytes.Equal(again.Surface, out.Surface) {
		t.Error("two runs differ")
	}
}

func TestGenerateRejects(t *testing.T) {
	g := golden(t)
	cases := map[string]string{
		"twin":     "  - {kind: twins, token: Twin, naturalKey: [name], source: t}\n",
		"verbs":    "  - {kind: readonly, token: Ro, naturalKey: [name], source: t}\n",
		"official": "  - {kind: readonly, token: Ro, naturalKey: [nope], source: t}\n",
	}
	wants := map[string]string{"twin": "official", "verbs": "verbs", "official": "nope"}
	for name, entry := range cases {
		y := strings.Replace(synthYAML, "officialTwins:", entry+"officialTwins:", 1)
		o, err := private.Parse([]byte(y))
		if err == nil {
			_, err = Generate(synthModel(), o, g)
		}
		if err == nil || !strings.Contains(err.Error(), wants[name]) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A field named like a reserved property needs a rename.
	m := synthModel()
	m.Find("v2", "gadgets").Fields["urn"] = &kind.Field{Type: kind.TypeString, Source: kind.SourceBytecode}
	if _, err := Generate(m, synthOverrides(t, ""), g); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("reserved: %v", err)
	}
	// A body-level polymorph needs an opt-in.
	m = synthModel()
	m.Find("v2", "gadgets").Polymorph = &kind.Polymorph{Prop: "name", Variants: map[string]map[string]*kind.Field{}}
	if _, err := Generate(m, synthOverrides(t, ""), g); err == nil || !strings.Contains(err.Error(), "polymorphic") {
		t.Errorf("polymorph: %v", err)
	}
}

func TestLowerCamel(t *testing.T) {
	for in, want := range map[string]string{"download_limit_kbps": "downloadLimitKbps", "name": "name", "for_wlanconf": "forWlanconf", "a-b": "aB"} {
		if got := lowerCamel(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
