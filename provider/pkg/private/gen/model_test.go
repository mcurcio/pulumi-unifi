package gen

// Model-backed gates of the generated private surface (make private-check).
// They need the extracted model (UNIFI_UOS_MODEL, from `make uos-model`).
// Without it each test logs "NOT RUN (no model)" and skips; with
// UNIFI_REQUIRE_UOS_MODEL=1 (set by private-check) a skip is a failure. The
// committed goldens hold hashes and token names only (design §9).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
	"github.com/mcurcio/pulumi-unifi/provider/pkg/private"
)

func notRun(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("UNIFI_REQUIRE_UOS_MODEL") == "1" {
		t.Fatalf("NOT RUN (%s) but UNIFI_REQUIRE_UOS_MODEL=1", why)
	}
	t.Skipf("NOT RUN (%s)", why)
}

func realModel(t *testing.T) *kind.Model {
	t.Helper()
	p := os.Getenv("UNIFI_UOS_MODEL")
	if p == "" {
		notRun(t, "no model: UNIFI_UOS_MODEL unset; run make private-check")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		notRun(t, "no model: "+err.Error())
	}
	m, err := kind.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func generateReal(t *testing.T) *Output {
	t.Helper()
	m := realModel(t)
	o, err := private.Load()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(m, o, golden(t))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const testdata = "../testdata"

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func readDigests(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, "digests.txt"))
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

func writeDigest(t *testing.T, name, s string) {
	t.Helper()
	p := filepath.Join(testdata, "digests.txt")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	done := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == name {
			l, done = name+" "+s, true
		}
		out = append(out, l)
	}
	if !done {
		out = append(out, name+" "+s)
	}
	if err := os.WriteFile(p, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The generated private surface matches the committed digests and token
// golden, passes the private surface lint, and is deterministic.
func TestPrivateSurfaceGolden(t *testing.T) {
	out := generateReal(t)
	again := generateReal(t)
	if !bytes.Equal(out.Schema, again.Schema) || !bytes.Equal(out.Surface, again.Surface) {
		t.Fatal("two generations from the same model differ")
	}
	if err := Lint(golden(t), out.Schema, out.Surface); err != nil {
		t.Fatal(err)
	}
	tokens := strings.Join(out.Tokens, "\n") + "\n"
	tp := filepath.Join(testdata, "tokens.txt")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(tp, []byte(tokens), 0o644); err != nil {
			t.Fatal(err)
		}
		writeDigest(t, "schema.merged.json", sum(out.Schema))
		writeDigest(t, "private.json", sum(out.Surface))
		return
	}
	want, err := os.ReadFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != tokens {
		t.Errorf("private tokens:\n%s\ntokens.txt:\n%s\nreview, then UPDATE_GOLDEN=1 make private-check", tokens, want)
	}
	d := readDigests(t)
	for name, b := range map[string][]byte{"schema.merged.json": out.Schema, "private.json": out.Surface} {
		if got := sum(b); got != d[name] {
			t.Errorf("%s sha256 %s, digests.txt says %s: review the change, then UPDATE_GOLDEN=1 make private-check", name, got, d[name])
		}
	}
}
