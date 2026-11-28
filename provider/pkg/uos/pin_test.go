package uos

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The committed pin is complete, well-formed and tied to the official spec pin
// (design §4, Tier 1: committed files only).
func TestPinTie(t *testing.T) {
	pp, err := FindRepoFile("uos/pin.env")
	if err != nil {
		t.Fatal(err)
	}
	pin, err := ReadPin(pp)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := FindRepoFile("openapi/pin.env")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	spec := ParseEnv(b)
	if pin.NetworkVersion != spec["SPEC_VERSION"] {
		t.Errorf("UOS_NETWORK_VERSION=%s but SPEC_VERSION=%s: one controller version per release", pin.NetworkVersion, spec["SPEC_VERSION"])
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(pin.SHA256) {
		t.Errorf("UOS_SHA256 %q is not a lowercase hex SHA-256", pin.SHA256)
	}
	if n, err := strconv.ParseInt(pin.Size, 10, 64); err != nil || n <= 0 {
		t.Errorf("UOS_SIZE %q", pin.Size)
	}
	if !strings.HasPrefix(pin.URL, "https://") || !strings.Contains(pin.URL, pin.Version) || !strings.Contains(pin.URL, pin.Platform) {
		t.Errorf("UOS_URL %q does not name version %s / platform %s over https", pin.URL, pin.Version, pin.Platform)
	}
	// The human-readable manifest must agree with the pin.
	src, err := FindRepoFile("uos/SOURCE")
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := os.ReadFile(src)
	for _, v := range []string{pin.SHA256, pin.URL, pin.Version, pin.NetworkVersion} {
		if !strings.Contains(string(sb), v) {
			t.Errorf("uos/SOURCE does not mention %s", v)
		}
	}
}

func TestParseEnv(t *testing.T) {
	e := ParseEnv([]byte("# c\nA=1\n B = two \nC=\"q v\"\nD='s'\nnoequals\n"))
	if e["A"] != "1" || e["B"] != "two" || e["C"] != "q v" || e["D"] != "s" || len(e) != 4 {
		t.Errorf("ParseEnv = %v", e)
	}
	dir := t.TempDir()
	p := dir + "/pin.env"
	os.WriteFile(p, []byte("UOS_VERSION=1\n"), 0o644)
	if _, err := ReadPin(p); err == nil {
		t.Error("an incomplete pin was accepted")
	}
}
