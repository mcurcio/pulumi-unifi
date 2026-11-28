package gen

// Slice 2 generator rules (docs/design/private-binding.md §S2.7, §S2.8): the
// G-Q4 variant exception and servedFrom, classic kinds with stated verbs, the
// settings singleton, createDefaults/outputOnly, and the x_ secret lint. The
// model and names are synthetic.

import (
	"encoding/json"
	"strings"
	"testing"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
)

// slice2Model is synthModel plus a discriminated twin ("acl-rules", whose
// official tokens in the real golden are AclRuleIpv4/AclRuleMac), a classic
// collection and a classic settings section.
func slice2Model() *kind.Model {
	m := synthModel()
	str := func(req bool) *kind.Field {
		return &kind.Field{Type: kind.TypeString, Required: req, Source: kind.SourceBytecode}
	}
	m.Kinds = append(m.Kinds,
		&kind.Kind{Name: "acl-rules", API: kind.APIV2, Collection: "acl-rules", ItemPath: "acl-rules/{id}", CreatePath: "acl-rules",
			Verbs: "CRUD", ListGet: true, IDField: "_id", Envelope: kind.EnvelopeBare,
			Fields: map[string]*kind.Field{
				"_id":    str(false),
				"name":   str(false),
				"type":   {Type: kind.TypeEnum, Enum: []string{"MAC", "IPV4", "IPV6"}, Required: true, Source: kind.SourceBytecode},
				"slot":   {Type: kind.TypeInt, Required: true, Source: kind.SourceBytecode},
				"ext_id": {Type: kind.TypeUUID, Source: kind.SourceBytecode},
			}},
		&kind.Kind{Name: "Crew", API: kind.APIClassic, Collection: "rest/crew", ItemPath: "rest/crew/{id}", IDField: "_id",
			Envelope: kind.EnvelopeMetaData, Fields: map[string]*kind.Field{
				"name":     {Type: kind.TypeString, Secret: true, Source: kind.SourceFields},
				"rate_max": {Type: kind.TypeInt, Source: kind.SourceFields},
			}},
		&kind.Kind{Name: "Setting.tick", API: kind.APIClassic, Collection: "rest/setting/tick", ItemPath: "rest/setting/tick/{id}", IDField: "_id",
			Envelope: kind.EnvelopeMetaData, Fields: map[string]*kind.Field{
				"mode":    {Type: kind.TypeEnum, Enum: []string{"auto", "manual"}, Source: kind.SourceFields},
				"x_token": {Type: kind.TypeString, Secret: true, Source: kind.SourceFields},
			}},
	)
	return m
}

const slice2YAML = `version: 1
expose:
  - {kind: acl-rules, token: AclRuleIpv6, naturalKey: [name], variant: {field: type, value: IPV6}, servedFrom: "0.0.0", source: t}
  - {kind: Crew, api: classic, token: Crew, naturalKey: [name], verbs: CRUD, source: t}
  - {kind: Setting.tick, api: classic, token: SettingTick, shape: singleton, verbs: "-RU-", source: t}
officialTwins:
  - {kind: acl-rules, official: ["resource:unifi:sites/v1:AclRuleIpv4", "resource:unifi:sites/v1:AclRuleMac"], officialVariants: {field: type, values: [IPV4, MAC]}, source: t}
itemGet:
  - {kind: Crew, api: classic, itemGet: true, source: t}
listPath:
  - {kind: Crew, api: classic, path: rest/crew, source: t}
  - {kind: Setting.tick, api: classic, path: rest/setting/tick, source: t}
createDefaults:
  - {kind: acl-rules, fields: {slot: 0}, source: t}
outputOnly:
  - {kind: acl-rules, fields: [slot, ext_id], source: t}
notSecret:
  - {kind: Crew, api: classic, fields: [name], source: t}
rename:
  - {kind: acl-rules, field: ext_id, property: aclRuleId, source: t}
defaults:
  - {kind: Setting.tick, api: classic, field: mode, value: auto, source: t}
`

func parse2(t *testing.T, y string) *private.Overrides {
	t.Helper()
	o, err := private.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestGenerateSlice2Kinds(t *testing.T) {
	g := golden(t)
	out, err := Generate(slice2Model(), parse2(t, slice2YAML), g)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(out.Tokens, ","); got != "resource:unifi:network:AclRuleIpv6,resource:unifi:network:Crew,resource:unifi:network:SettingTick" {
		t.Fatalf("tokens %s", got)
	}
	if err := Lint(g, out.Schema, out.Surface); err != nil {
		t.Fatal(err)
	}
	var spec pschema.PackageSpec
	if err := json.Unmarshal(out.Schema, &spec); err != nil {
		t.Fatal(err)
	}
	s, err := engine.ParseSurface(out.Surface)
	if err != nil {
		t.Fatal(err)
	}
	// The variant kind: discriminator gone from the schema, pinned in the
	// surface; output-only fields are outputs; the create default is recorded.
	r := spec.Resources["unifi:network:AclRuleIpv6"]
	if _, ok := r.InputProperties["type"]; ok {
		t.Error("the discriminator is not an input")
	}
	if _, ok := r.Properties["type"]; ok {
		t.Error("the discriminator is not an output")
	}
	for _, p := range []string{"slot", "aclRuleId"} {
		if _, ok := r.InputProperties[p]; ok {
			t.Errorf("%s is output only", p)
		}
		if _, ok := r.Properties[p]; !ok {
			t.Errorf("%s must be an output", p)
		}
	}
	if strings.Join(r.RequiredInputs, ",") != "name" {
		t.Errorf("required inputs %v (slot is required by the model but output only)", r.RequiredInputs)
	}
	if !strings.Contains(r.Description, "unifi:sites/v1:AclRuleIpv4, unifi:sites/v1:AclRuleMac") {
		t.Errorf("description must name the official tokens: %s", r.Description)
	}
	ak := s.Find("unifi:network:AclRuleIpv6")
	if ak.Variant == nil || ak.Variant.Value != "IPV6" || string(ak.CreateDefaults["slot"]) != "0" || ak.Fields["type"] != nil ||
		!ak.Fields["slot"].OutputOnly || ak.Fields["ext_id"].Property != "aclRuleId" || len(ak.OfficialTokens) != 2 {
		t.Errorf("variant kind %+v", ak)
	}
	// The classic collection.
	ck := s.Find("unifi:network:Crew")
	if ck.API != "classic" || ck.Envelope != "meta-data" || ck.ListPath != "rest/crew" || !ck.ItemGet || ck.Fields["name"].Secret {
		t.Errorf("classic kind %+v", ck)
	}
	// The settings singleton.
	tk := s.Find("unifi:network:SettingTick")
	if !tk.IsSingleton() || tk.SingletonKey != "tick" || len(tk.NaturalKey) != 0 || !tk.Fields["x_token"].Secret {
		t.Errorf("singleton kind %+v", tk)
	}
	if !strings.Contains(spec.Resources["unifi:network:SettingTick"].Description, "one per site") {
		t.Errorf("singleton description: %s", spec.Resources["unifi:network:SettingTick"].Description)
	}
}

func TestServedFromGatesTheVariantKind(t *testing.T) {
	g := golden(t)
	for name, c := range map[string]struct{ servedFrom, want string }{
		"unset": {"", "NOT SERVED (servedFrom unset)"},
		"newer": {"99.0.0", "NOT SERVED (Network 0.0.0-test is older than servedFrom 99.0.0)"},
	} {
		y := strings.Replace(slice2YAML, `servedFrom: "0.0.0", `, "", 1)
		if c.servedFrom != "" {
			y = strings.Replace(slice2YAML, `servedFrom: "0.0.0"`, `servedFrom: "`+c.servedFrom+`"`, 1)
		}
		out, err := Generate(slice2Model(), parse2(t, y), g)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(strings.Join(out.Tokens, ","), "AclRuleIpv6") {
			t.Errorf("%s: served: %v", name, out.Tokens)
		}
		if len(out.NotServed) != 1 || !strings.HasSuffix(out.NotServed[0], c.want) {
			t.Errorf("%s: NotServed %v, want %q", name, out.NotServed, c.want)
		}
	}
	// A not-served kind is still built and checked: a bad entry fails gen.
	y := strings.Replace(slice2YAML, `servedFrom: "0.0.0", `, "", 1)
	y = strings.Replace(y, "naturalKey: [name], variant", "naturalKey: [nope], variant", 1)
	if _, err := Generate(slice2Model(), parse2(t, y), g); err == nil {
		t.Error("a not-served kind with a bad natural key must still fail gen")
	}
	if compareVersions("10.4.57", "10.5.0") >= 0 || compareVersions("10.10.1", "10.9.9") <= 0 || compareVersions("10.4.57", "10.4.57") != 0 {
		t.Error("compareVersions")
	}
}

func TestSlice2GenRejects(t *testing.T) {
	g := golden(t)
	for name, c := range map[string]struct{ from, to, want string }{
		"classic verbs unknown": {"verbs: CRUD, ", "", "does not know its verbs"},
		"singleton verbs":       {`verbs: "-RU-"`, `verbs: "-R--"`, "lack U"},
		"x_ not secret":         {"  - {kind: Crew, api: classic, fields: [name], source: t}\n", "  - {kind: Crew, api: classic, fields: [name], source: t}\n  - {kind: Setting.tick, api: classic, fields: [x_token], source: t}\n", "x_ field but not secret"},
		"default not output":    {"fields: [slot, ext_id]", "fields: [ext_id]", "createDefaults slot must be an outputOnly field"},
		"variant not in enum":   {"value: IPV6}", "value: IPV7}", "not a value of the model's enum"},
	} {
		y := strings.Replace(slice2YAML, c.from, c.to, 1)
		o, err := private.Parse([]byte(y))
		if err == nil {
			_, err = Generate(slice2Model(), o, g)
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

// The committed kinds.yaml's officialVariants match the committed golden:
// AclRuleIpv4/AclRuleMac pin IPV4/MAC. If the official API gains IPV6 (a new
// golden token with that const), this fails, and the variant kind moves to
// the official API (G-Q4 variant exception, rule 3).
func TestTwinVariantsMatchTheGolden(t *testing.T) {
	g := golden(t)
	o, err := private.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckTwinVariants(o, g); err != nil {
		t.Fatal(err)
	}
	// A synthetic golden whose official API also serves IPV6 fails.
	var spec map[string]any
	if err := json.Unmarshal(g, &spec); err != nil {
		t.Fatal(err)
	}
	res := spec["resources"].(map[string]any)
	mac := res["unifi:sites/v1:AclRuleMac"].(map[string]any)
	mac["inputProperties"].(map[string]any)["type"].(map[string]any)["const"] = "IPV6"
	b, _ := json.Marshal(spec)
	if err := CheckTwinVariants(o, b); err == nil || !strings.Contains(err.Error(), "now serves type=IPV6") {
		t.Errorf("an official IPV6 variant must fail gen: %v", err)
	}
}
