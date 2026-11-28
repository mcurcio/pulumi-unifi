package uos

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
)

func fixtureKinds(t *testing.T) map[string]*kind.Kind {
	t.Helper()
	out := map[string]*kind.Kind{}
	for _, k := range V2Kinds(fixtureExtractor(t).Crud(V2Prefix)) {
		MarkSecrets(k, nil)
		out[k.Name] = k
	}
	return out
}

func mustField(t *testing.T, k *kind.Kind, name string) *kind.Field {
	t.Helper()
	f := k.Fields[name]
	if f == nil {
		t.Fatalf("%s: no field %q", k.Name, name)
	}
	return f
}

func TestCrudKindsFromFixtures(t *testing.T) {
	ks := fixtureKinds(t)
	if len(ks) != 4 {
		t.Fatalf("kinds = %v", ks)
	}
	for name, want := range map[string]struct {
		verbs, create, item, id   string
		list, itemGet, createList bool
	}{
		"widgets": {"CRUD", "widgets", "widgets/{id}", "_id", true, true, false},
		"gadgets": {"CRUD", "gadgets", "gadgets/{id}", "_id", true, false, true}, // no item GET -> list + filter
		"members": {"CRU-", "members", "members/{id}", "id", false, true, false}, // id field "id"; no list GET
		"things":  {"CR-D", "things/create", "things/{thingId}", "_id", true, false, false},
	} {
		k := ks[name]
		if k == nil {
			t.Errorf("missing kind %s", name)
			continue
		}
		if k.Verbs != want.verbs || k.CreatePath != want.create || k.ItemPath != want.item || k.IDField != want.id ||
			k.ListGet != want.list || k.ItemGet != want.itemGet || k.CreateList != want.createList {
			t.Errorf("%s = %+v, want %+v", name, k, want)
		}
		if k.API != kind.APIV2 || k.Envelope != kind.EnvelopeBare {
			t.Errorf("%s: api %s envelope %s", name, k.API, k.Envelope)
		}
	}
}

func TestDTOFieldFacts(t *testing.T) {
	w := fixtureKinds(t)["widgets"]
	def := func(name string) string { return string(mustField(t, w, name).Default) }
	for name, want := range map[string]string{
		"enabled": "true",   // iconst_1; ireturn on a boolean
		"mode":    `"fast"`, // getstatic of an enum constant -> its @JsonValue wire value
		"mode2":   `"slow"`, // a static copied from an enum constant in <clinit>
		"count":   "5",      // iconst_5; Integer.valueOf
		"big":     "1000",   // sipush
		"huge":    "100000", // ldc2_w long
		"label":   `"abc"`,  // ldc String
		"ratio":   "0.5",    // ldc2_w double
		"tags":    "[]",     // List.of()
	} {
		if got := def(name); got != want {
			t.Errorf("default %s = %s, want %s", name, got, want)
		}
	}
	if f := mustField(t, w, "computed"); !f.DefaultComputed || f.Default != nil {
		t.Errorf("computed default: %+v", f)
	}
	name := mustField(t, w, "name")
	if !name.Required || name.Constraints == nil || !name.Constraints.NonEmpty || *name.Constraints.Size.Min != 1 || *name.Constraints.Size.Max != 64 {
		t.Errorf("name = %+v %+v", name, name.Constraints)
	}
	if c := mustField(t, w, "count").Constraints; c == nil || *c.Min != 0 || *c.Max != 100 {
		t.Errorf("count constraints = %+v", c)
	}
	if f := mustField(t, w, "tags"); f.Constraints == nil || len(f.Constraints.Custom) != 1 || f.Constraints.Custom[0] != "must be a valid MAC" {
		t.Errorf("tags: custom validator on the type argument not found: %+v", f.Constraints)
	}
	if f := mustField(t, w, "code"); f.Pattern != "^[a-z]+$" || f.Constraints != nil {
		t.Errorf("code = %+v (a non-constraint project annotation must add nothing)", f)
	}
	for name, want := range map[string][]string{
		"mode":  {"fast", "slow"},         // @JsonValue on a field accessor
		"color": {"red", "green", "BLUE"}, // @JsonProperty on constants, else the name
		"plain": {"ALPHA", "BETA"},        // @JsonValue returning name(); anonymous constant body
	} {
		if got := mustField(t, w, name).Enum; !equalStrings(got, want) {
			t.Errorf("enum %s = %v, want %v", name, got, want)
		}
	}
	for name, typ := range map[string]string{"note": "string", "ref": "uuid", "when": "datetime", "limits": "map", "matrix": "array", "schedule": "object", "target": "object"} {
		if got := mustField(t, w, name).Type; got != typ {
			t.Errorf("type %s = %s, want %s", name, got, typ)
		}
	}
	if !mustField(t, w, "note").Optional {
		t.Error("Optional<> not marked optional")
	}
	if mustField(t, w, "limits").Values.Type != "int" || mustField(t, w, "matrix").Items.Items.Type != "int" {
		t.Error("map values / nested arrays")
	}
	for _, n := range []string{"rq", "sw", "schedule"} {
		if !mustField(t, w, n).Required {
			t.Errorf("%s not required", n)
		}
	}
	if _, ok := w.Fields["hidden"]; ok {
		t.Error("@JsonIgnore field modelled")
	}
	// Secrets fail closed: x_ prefix, WRITE_ONLY, a secret-looking name.
	if !mustField(t, w, "x_secret").Secret || !mustField(t, w, "pass").Secret || !mustField(t, w, "pass").WriteOnly {
		t.Error("secret marking")
	}
	if mustField(t, w, "label").Secret {
		t.Error("label marked secret")
	}
	if !mustField(t, fixtureKinds(t)["members"], "pskValue").Secret {
		t.Error("pskValue (name matches psk) not secret")
	}
	// Response-only fields are read-only; request fields marked READ_ONLY stay so.
	if !mustField(t, w, "created").ReadOnly || !mustField(t, w, "_id").ReadOnly || mustField(t, w, "name").ReadOnly {
		t.Error("read-only from the response model")
	}
	sched := mustField(t, w, "schedule")
	if f := sched.Fields["next_run"]; f == nil || !f.ReadOnly || f.Type != "date" {
		t.Errorf("schedule.next_run (response-only, nested) = %+v", f)
	}
	if f := sched.Fields["base_field"]; f == nil {
		t.Error("superclass POJO field not inherited")
	}
	if f := sched.Fields["allDay"]; f == nil || f.Type != "bool" {
		t.Error("POJO field with an is-getter")
	}
	for _, n := range []string{"noGetter", "ignored", "counter"} {
		if _, ok := sched.Fields[n]; ok {
			t.Errorf("schedule.%s should not be modelled", n)
		}
	}
	if !sched.Fields["mode"].Required {
		t.Error("schedule.mode @NotNull on the field")
	}
	tg := mustField(t, w, "target")
	if tg.Polymorph == nil || tg.Polymorph.Prop != "matching_target" || len(tg.Polymorph.Variants) != 2 ||
		tg.Polymorph.Variants["NETWORK"]["network_ids"] == nil {
		t.Errorf("polymorph = %+v", tg.Polymorph)
	}
	if d := tg.Fields["matching_target"]; d == nil || !d.Required || !equalStrings(d.Enum, []string{"ANY", "NETWORK"}) {
		t.Errorf("discriminator field = %+v", d)
	}
	m := fixtureKinds(t)["members"]
	if c := mustField(t, m, "name").Constraints; c == nil || c.Len == nil || *c.Len.Max != 100 || c.Len.Min != nil {
		t.Errorf("@Length = %+v", c)
	}
	if c := mustField(t, m, "members").Constraints; c == nil || c.Size == nil || *c.Size.Min != 1 || c.Size.Max != nil {
		t.Errorf("@Size(min) = %+v", c)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The fixture model is a golden of the extractor's output on our own
// synthetic classes (no Ubiquiti text). UPDATE_GOLDEN=1 rewrites it.
func TestFixtureModelGolden(t *testing.T) {
	dir := t.TempDir()
	inst, sum := writeSynthInstaller(t, dir)
	ex, err := ExtractInstaller(inst, ExtractOptions{WantSHA256: sum})
	if err != nil {
		t.Fatal(err)
	}
	m, err := BuildModel(ex, "7.7.7")
	if err != nil {
		t.Fatal(err)
	}
	got, err := kind.Canonical(m)
	if err != nil {
		t.Fatal(err)
	}
	// Determinism: a second extraction gives the same bytes.
	ex2, err := ExtractInstaller(inst, ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := BuildModel(ex2, "7.7.7")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := kind.Canonical(m2); !bytes.Equal(got, again) {
		t.Fatal("two extractions of the same installer differ")
	}
	// The installer checksum varies with the zip writer's output; blank it so
	// the golden holds only extractor facts.
	var gen map[string]any
	if err := json.Unmarshal(got, &gen); err != nil {
		t.Fatal(err)
	}
	src := gen["source"].(map[string]any)
	if src["installerSha256"] != sum || src["networkVersion"] != "9.9.9" || src["uosVersion"] != "7.7.7" || src["extractorVersion"] != ExtractorVersion {
		t.Errorf("source = %v", src)
	}
	src["installerSha256"] = "<synthetic>"
	raw, _ := json.Marshal(gen)
	got, _ = kind.CanonicalJSON(raw)
	golden := filepath.Join("testdata", "fixture-model.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("fixture model differs from %s (review, then UPDATE_GOLDEN=1)", golden)
	}
	// Classic kinds from the synthetic field files, with redaction-list secrets.
	thing := m.Find(kind.APIClassic, "Thing")
	if thing == nil || thing.Collection != "rest/thing" || thing.Envelope != kind.EnvelopeMetaData {
		t.Fatalf("classic Thing = %+v", thing)
	}
	if !thing.Fields["name"].Secret || !thing.Fields["x_passphrase"].Secret || thing.Fields["enabled"].Secret {
		t.Error("classic secrets: redaction list and x_ prefix")
	}
	mg := m.Find(kind.APIClassic, "Setting.mgmt")
	if mg == nil || !mg.Fields["x_ssh_password"].Secret || !mg.Fields["hostname"].Secret || mg.Fields["led_enabled"].Type != "bool" {
		t.Errorf("Setting.mgmt = %+v", mg)
	}
}
