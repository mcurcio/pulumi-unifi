package private

// Model-backed gates (make private-check). They need the extracted model
// (UNIFI_UOS_MODEL, written by `make uos-model`) and, for the determinism
// check, the pinned installer (UNIFI_UOS_INSTALLER). Without them each test
// logs "NOT RUN (no model)" and skips; with UNIFI_REQUIRE_UOS_MODEL=1 (set by
// private-check) a skip is a failure instead.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/uos"
)

func notRun(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("UNIFI_REQUIRE_UOS_MODEL") == "1" {
		t.Fatalf("NOT RUN (%s) but UNIFI_REQUIRE_UOS_MODEL=1", why)
	}
	t.Skipf("NOT RUN (%s)", why)
}

func modelBytes(t *testing.T) []byte {
	t.Helper()
	p := os.Getenv("UNIFI_UOS_MODEL")
	if p == "" {
		notRun(t, "no model: UNIFI_UOS_MODEL unset; run make private-check")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		notRun(t, "no model: "+err.Error())
	}
	return b
}

func loadModel(t *testing.T) *kind.Model {
	t.Helper()
	m, err := kind.Parse(modelBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// digests reads testdata/digests.txt: "<name> <sha256>" lines.
func digests(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("testdata/digests.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) == 2 && !strings.HasPrefix(l, "#") {
			out[f[0]] = f[1]
		}
	}
	return out
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// The model file is canonical, matches the committed digest, and records the
// pinned inputs (installer, Network version, official spec tie).
func TestPrivateModelPinnedAndCanonical(t *testing.T) {
	b := modelBytes(t)
	m, err := kind.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	c, err := kind.Canonical(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, c) {
		t.Error("model.json is not in canonical form")
	}
	pp, _ := uos.FindRepoFile("uos/pin.env")
	pin, err := uos.ReadPin(pp)
	if err != nil {
		t.Fatal(err)
	}
	sp, _ := uos.FindRepoFile("openapi/pin.env")
	sb, _ := os.ReadFile(sp)
	spec := uos.ParseEnv(sb)
	s := m.Source
	checks := [][3]string{
		{"uosVersion", s.UOSVersion, pin.Version},
		{"installerSha256", s.InstallerSHA256, pin.SHA256},
		{"networkVersion", s.NetworkVersion, pin.NetworkVersion},
		{"networkVersion vs SPEC_VERSION", s.NetworkVersion, spec["SPEC_VERSION"]},
		{"integrationSha256 vs SPEC_SHA256 (the pin tie)", s.IntegrationSHA256, spec["SPEC_SHA256"]},
		{"extractorVersion", s.ExtractorVersion, uos.ExtractorVersion},
	}
	for _, c := range checks {
		if c[1] != c[2] {
			t.Errorf("model %s = %q, want %q (stale model? make uos-model)", c[0], c[1], c[2])
		}
	}
	got := sha(b)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeDigest(t, "model.json", got)
		return
	}
	if want := digests(t)["model.json"]; got != want {
		t.Errorf("model.json sha256 %s, digests.txt says %s: review the model change, then UPDATE_GOLDEN=1 make private-check", got, want)
	}
}

func writeDigest(t *testing.T, name, sum string) {
	t.Helper()
	b, err := os.ReadFile("testdata/digests.txt")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	done := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == name {
			l, done = name+" "+sum, true
		}
		out = append(out, l)
	}
	if !done {
		out = append(out, name+" "+sum)
	}
	if err := os.WriteFile("testdata/digests.txt", []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Extracting twice from the pinned installer gives byte-identical models,
// equal to the model file.
func TestPrivateModelDeterministic(t *testing.T) {
	b := modelBytes(t)
	inst := os.Getenv("UNIFI_UOS_INSTALLER")
	if inst == "" {
		notRun(t, "no installer: UNIFI_UOS_INSTALLER unset")
	}
	pp, _ := uos.FindRepoFile("uos/pin.env")
	pin, err := uos.ReadPin(pp)
	if err != nil {
		t.Fatal(err)
	}
	var outs [][]byte
	for i := 0; i < 2; i++ {
		ex, err := uos.ExtractInstaller(inst, uos.ExtractOptions{WantSHA256: pin.SHA256})
		if err != nil {
			t.Fatal(err)
		}
		m, err := uos.BuildModel(ex, pin.Version)
		if err != nil {
			t.Fatal(err)
		}
		c, err := kind.Canonical(m)
		if err != nil {
			t.Fatal(err)
		}
		outs = append(outs, c)
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("two extractions of the pinned installer differ")
	}
	if !bytes.Equal(outs[0], b) {
		t.Error("a fresh extraction differs from model.json (stale model? make uos-model)")
	}
}

// kinds.yaml resolves against the model: every kind and field it names exists.
func TestPrivateOverridesResolve(t *testing.T) {
	m := loadModel(t)
	o, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Resolve(m); err != nil {
		t.Fatal(err)
	}
}

// The slice-1 kinds carry the facts the binding relies on (design §10).
func TestPrivateSliceKinds(t *testing.T) {
	m := loadModel(t)
	o, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	itemGet := map[string]bool{}
	for _, g := range o.ItemGet {
		itemGet[g.Kind] = g.ItemGet
	}
	notSecret := map[string]bool{}
	for _, l := range o.NotSecret {
		for _, f := range l.Fields {
			notSecret[l.Ref.String()+"."+f] = true
		}
	}
	want := map[string]struct {
		id      string
		itemGet bool
	}{
		"qos-rules": {"_id", true}, "apgroups": {"_id", false},
		"firewall-app-blocks": {"_id", false}, "network-members-group": {"id", true},
		// Slice 2 (private-binding.md §S2.7, §S2.8).
		"acl-rules": {"_id", false}, "UserGroup": {"_id", true}, "Setting.ntp": {"_id", false},
	}
	for _, e := range o.Expose {
		k := m.Find(e.APIName(), e.Kind)
		if k == nil {
			t.Errorf("%s not in the model", e.Ref)
			continue
		}
		w, ok := want[e.Kind]
		if !ok {
			t.Errorf("%s: no expectation recorded in this test", e.Ref)
			continue
		}
		if k.IDField != w.id {
			t.Errorf("%s idField = %s, want %s", e.Kind, k.IDField, w.id)
		}
		if g, ok := itemGet[e.Kind]; ok && g != w.itemGet || !ok && k.ItemGet != w.itemGet {
			t.Errorf("%s itemGet: model %v, overrides %v, want %v", e.Kind, k.ItemGet, g, w.itemGet)
		}
		// The route table agrees with every measured 405.
		if g, ok := itemGet[e.Kind]; ok && !g && k.ItemGet {
			t.Errorf("%s: measured 405 but the route table has an item GET", e.Kind)
		}
		verbs, need := k.Verbs, "CUD"
		if verbs == "" {
			verbs = e.Verbs // classic: measured, stated in kinds.yaml
		}
		if e.ShapeName() == ShapeSingleton {
			need = "RU"
		}
		for _, v := range need {
			if !strings.ContainsRune(verbs, v) {
				t.Errorf("%s verbs %q: a %s needs %s", e.Kind, verbs, e.ShapeName(), need)
			}
		}
		for _, nk := range e.NaturalKey {
			if f := FieldAt(k, nk); f == nil || f.Secret && !notSecret[e.Ref.String()+"."+nk] || f.ReadOnly {
				t.Errorf("%s natural key %s: missing, secret or read-only", e.Kind, nk)
			}
		}
	}
}
