//go:build e2e_cli

// The Tier-2 CLI driver (docs/design/private-binding.md §S2.11, `make test-e2e`).
//
// It drives the pinned Pulumi CLI over the Python program in ./program and the
// PRIVATE=0 plugin + SDK against the seeded live controller that
// test/e2e/run.sh boots, and checks every result with raw HTTP against the
// official API. It needs:
//
//	E2E_PULUMI    the Pulumi CLI binary
//	E2E_API_HOST  the controller edge, e.g. 127.0.0.1:11443
//	E2E_WORK      a scratch directory (Pulumi home, file backend, program copy)
//	UNIFI_APIKEY  the seed's throwaway key (run.sh reads it from seed.env)
//	PATH          with the plugin directory and the SDK venv's bin first
//
// The key travels in the environment only: never on a command line, in
// Pulumi config or in a log line.
package cli

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- raw official API ----------------------------------------------------------

type api struct {
	t    *testing.T
	host string
	key  string
	site string // UUID
	http *http.Client
}

func (a *api) do(method, path string, body any) (int, []byte) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "https://"+a.host+path, rd)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("X-API-Key", a.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (a *api) official(method, rel string, body any) (int, []byte) {
	return a.do(method, "/proxy/network/integration/v1/sites/"+a.site+rel, body)
}

func (a *api) json(method, rel string, body any, want int) map[string]any {
	a.t.Helper()
	code, b := a.official(method, rel, body)
	if code != want {
		a.t.Fatalf("%s %s: %d %s (want %d)", method, rel, code, b, want)
	}
	var out map[string]any
	if len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			a.t.Fatalf("%s %s: %v: %s", method, rel, err, b)
		}
	}
	return out
}

func (a *api) order() []string {
	a.t.Helper()
	o := a.json("GET", "/acl-rules/ordering", nil, 200)
	var ids []string
	for _, v := range o["orderedAclRuleIds"].([]any) {
		ids = append(ids, v.(string))
	}
	return ids
}

func (a *api) rule(id string) (map[string]any, bool) {
	a.t.Helper()
	code, b := a.official("GET", "/acl-rules/"+id, nil)
	if code == 404 {
		return nil, false
	}
	if code != 200 {
		a.t.Fatalf("GET acl-rules/%s: %d %s", id, code, b)
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, true
}

// rulesWithPrefix lists the site's ACL rules whose name starts with prefix.
func (a *api) rulesWithPrefix(prefix string) []map[string]any {
	a.t.Helper()
	page := a.json("GET", "/acl-rules?limit=200", nil, 200)
	var out []map[string]any
	for _, v := range page["data"].([]any) {
		r := v.(map[string]any)
		if n, _ := r["name"].(string); strings.HasPrefix(n, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func expect(t *testing.T, what string, o map[string]any, want map[string]any) {
	t.Helper()
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		got, ok := o[k]
		if want[k] == nil {
			if ok && got != nil {
				t.Errorf("%s: %s = %v, want absent or null", what, k, got)
			}
			continue
		}
		wj, _ := json.Marshal(want[k])
		gj, _ := json.Marshal(got)
		if !ok || string(wj) != string(gj) {
			t.Errorf("%s: %s = %s, want %s", what, k, gj, wj)
		}
	}
}

// --- Pulumi CLI ----------------------------------------------------------------

type cli struct {
	t    *testing.T
	bin  string
	dir  string
	env  []string
	logs string
}

func (p *cli) run(args ...string) (string, error) {
	p.t.Helper()
	args = append(args, "--non-interactive")
	cmd := exec.Command(p.bin, args...)
	cmd.Dir = p.dir
	cmd.Env = p.env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err := cmd.Run()
	p.t.Logf("$ pulumi %s (%s)", strings.Join(args, " "), time.Since(start).Round(time.Second))
	if f, ferr := os.OpenFile(p.logs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); ferr == nil {
		fmt.Fprintf(f, "\n$ pulumi %s\n%s%s", strings.Join(args, " "), out.String(), errb.String())
		f.Close()
	}
	if err != nil {
		return out.String() + errb.String(), fmt.Errorf("pulumi %s: %v\n%s%s", strings.Join(args, " "), err, tail(out.String(), 60), tail(errb.String(), 30))
	}
	return out.String(), nil
}

func (p *cli) must(args ...string) string {
	p.t.Helper()
	out, err := p.run(args...)
	if err != nil {
		p.t.Fatal(err)
	}
	return out
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n"
}

type step struct {
	Op           string                     `json:"op"`
	URN          string                     `json:"urn"`
	DetailedDiff map[string]json.RawMessage `json:"detailedDiff"`
}

// steps decodes `pulumi preview --json` and returns every step.
func steps(t *testing.T, out string) []step {
	t.Helper()
	var doc struct {
		Steps []step `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("preview --json: %v\n%s", err, tail(out, 40))
	}
	return doc.Steps
}

func changes(ss []step) []step {
	var ch []step
	for _, s := range ss {
		switch s.Op {
		case "same", "read", "refresh":
		default:
			ch = append(ch, s)
		}
	}
	return ch
}

func env(t *testing.T, name string) string {
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s is not set; run this through `make test-e2e` (test/e2e/run.sh)", name)
	}
	return v
}

// childEnv is os.Environ() without the provider variables other than the key,
// so a developer's own apiHost or site never reaches the throwaway controller.
func childEnv() []string {
	drop := map[string]bool{"UNIFI_API_HOST": true, "UNIFI_SITEID": true, "UNIFI_NETWORK_BASE_URL": true, "UNIFI_ALLOW_INSECURE": true}
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !drop[name] {
			out = append(out, kv)
		}
	}
	return out
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// --- the test ------------------------------------------------------------------

func TestCLIE2E(t *testing.T) {
	bin := env(t, "E2E_PULUMI")
	host := env(t, "E2E_API_HOST")
	work := env(t, "E2E_WORK")
	key := env(t, "UNIFI_APIKEY")

	a := &api{t: t, host: host, key: key, http: &http.Client{Timeout: 60 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}} // #nosec G402 -- throwaway local controller
	code, b := a.do("GET", "/proxy/network/integration/v1/sites", nil)
	var sites struct {
		Data []struct {
			ID                string `json:"id"`
			InternalReference string `json:"internalReference"`
		} `json:"data"`
	}
	if code != 200 || json.Unmarshal(b, &sites) != nil || len(sites.Data) == 0 {
		t.Fatalf("GET sites: %d %s", code, b)
	}
	a.site = sites.Data[0].ID
	siteName := sites.Data[0].InternalReference
	nets := a.json("GET", "/networks?limit=200", nil, 200)
	netID := nets["data"].([]any)[0].(map[string]any)["id"].(string)
	prefix := fmt.Sprintf("e2e-%d", time.Now().Unix())
	t.Logf("site %s (%s), network %s, prefix %s", a.site, siteName, netID, prefix)

	prog := filepath.Join(work, "program")
	copyDir(t, "program", prog)
	home, state := filepath.Join(work, "pulumi-home"), filepath.Join(work, "state")
	for _, d := range []string{home, state} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p := &cli{t: t, bin: bin, dir: prog, logs: filepath.Join(work, "pulumi.log"), env: append(childEnv(),
		"PULUMI_HOME="+home, "PULUMI_BACKEND_URL=file://"+state, "PULUMI_SKIP_UPDATE_CHECK=1",
		"PULUMI_CONFIG_PASSPHRASE=e2e-throwaway", "PULUMI_DIY_BACKEND_NO_LEGACY_WARNING=1")}

	// Raw objects this run creates outside Pulumi, swept at the end.
	var unmanaged []string
	destroyed := map[string]bool{}
	t.Cleanup(func() {
		for _, s := range []string{"main", "alias"} {
			if !destroyed[s] {
				_, _ = p.run("destroy", "--yes", "--skip-preview", "-s", s)
			}
		}
		for _, id := range unmanaged {
			code, _ := a.official("DELETE", "/acl-rules/"+id, nil)
			t.Logf("swept unmanaged rule %s: %d", id, code)
		}
		for _, r := range a.rulesWithPrefix(prefix) {
			code, _ := a.official("DELETE", "/acl-rules/"+r["id"].(string), nil)
			t.Errorf("rule %v (%v) was left behind; swept: %d", r["name"], r["id"], code)
		}
	})

	stackInit := func(name string, cfg map[string]string) {
		p.must("stack", "init", name)
		keys := make([]string, 0, len(cfg))
		for k := range cfg {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p.must("config", "set", "-s", name, k, cfg[k])
		}
	}
	outputs := func(stack string) map[string]string {
		var o struct {
			IDs map[string]string `json:"ids"`
		}
		if err := json.Unmarshal([]byte(p.must("stack", "output", "--json", "-s", stack)), &o); err != nil {
			t.Fatal(err)
		}
		return o.IDs
	}
	run := func(name string, fn func(t *testing.T)) {
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}
	leading := func(t *testing.T, want ...string) {
		t.Helper()
		got := a.order()
		if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
			t.Fatalf("live order %v: want it to start with %v", got, want)
		}
	}

	base := map[string]string{
		"unifi:apiHost": host, "unifi:allowInsecure": "true", "unifi:siteId": a.site,
		"unifi-e2e-cli:prefix": prefix, "unifi-e2e-cli:networkId": netID,
	}
	mainCfg := map[string]string{"unifi-e2e-cli:step": "1", "unifi-e2e-cli:mode": "main"}
	for k, v := range base {
		mainCfg[k] = v
	}
	stackInit("main", mainCfg)

	var ids map[string]string
	run("1-create", func(t *testing.T) {
		p.must("up", "--yes", "--skip-preview", "-s", "main")
		ids = outputs("main")
		if ids["order"] != "official:"+a.site {
			t.Errorf("ordering id %q, want official:%s (one per site)", ids["order"], a.site)
		}
		r1, ok1 := a.rule(ids["r1"])
		r2, ok2 := a.rule(ids["r2"])
		if !ok1 || !ok2 {
			t.Fatalf("rules not on the controller: r1 %v r2 %v", ok1, ok2)
		}
		expect(t, "r1", r1, map[string]any{"type": "IPV4", "name": prefix + "-r1", "action": "BLOCK",
			"enabled": true, "protocolFilter": []string{"TCP"}})
		expect(t, "r2", r2, map[string]any{"type": "MAC", "name": prefix + "-r2", "action": "ALLOW",
			"networkIdFilter": netID})
		leading(t, ids["r2"], ids["r1"])
	})

	run("2-read-noop", func(t *testing.T) {
		p.must("refresh", "--yes", "--skip-preview", "-s", "main")
		p.must("preview", "--expect-no-changes", "-s", "main")
		p.must("up", "--yes", "--expect-no-changes", "-s", "main")
	})

	run("3-update", func(t *testing.T) {
		p.must("config", "set", "-s", "main", "unifi-e2e-cli:step", "2")
		p.must("up", "--yes", "--skip-preview", "-s", "main")
		got := outputs("main")
		for _, k := range []string{"r1", "r2", "order"} {
			if got[k] != ids[k] {
				t.Errorf("%s was replaced (%s -> %s); want an in-place update", k, ids[k], got[k])
			}
		}
		r1, _ := a.rule(ids["r1"])
		r2, _ := a.rule(ids["r2"])
		// protocolFilter was dropped from the program: the full PUT clears it.
		expect(t, "r1", r1, map[string]any{"action": "ALLOW", "protocolFilter": nil, "name": prefix + "-r1"})
		expect(t, "r2", r2, map[string]any{"name": prefix + "-r2-renamed", "networkIdFilter": netID})
		leading(t, ids["r1"], ids["r2"])
		// The ordering PUT renumbered `index`; it is output only, so no drift.
		p.must("preview", "--refresh", "--expect-no-changes", "-s", "main")
	})

	var u1, v2 string
	run("4-unmanaged-kept", func(t *testing.T) {
		// A rule created with the official API, outside the program, lands in
		// the unmanaged tail; a rule created with the private v2 API is in the
		// same order and must be kept by every ordering PUT.
		r := a.json("POST", "/acl-rules", map[string]any{"type": "IPV4", "name": prefix + "-u1", "enabled": false, "action": "ALLOW"}, 201)
		u1 = r["id"].(string)
		unmanaged = append(unmanaged, u1)
		// acl_index = the current rule count puts it at the end (a taken index
		// would tie with a managed rule and move ahead of it, probe d2).
		code, b := a.do("POST", "/proxy/network/v2/api/site/"+siteName+"/acl-rules", map[string]any{
			"type": "IPV4", "name": prefix + "-v2", "enabled": false, "action": "ALLOW", "acl_index": len(a.order()),
			"ip_acl_protocol": "ALL", "specific_enforcers": []any{},
			"traffic_source":      map[string]any{"type": "NETWORK", "network_ids": []any{}},
			"traffic_destination": map[string]any{"type": "NETWORK", "network_ids": []any{}},
		})
		if code/100 != 2 {
			t.Fatalf("v2 POST acl-rules: %d %s", code, b)
		}
		var vr map[string]any
		_ = json.Unmarshal(b, &vr)
		v2, _ = vr["external_id"].(string)
		if v2 == "" {
			t.Fatalf("v2 create answered no external_id: %s", b)
		}
		unmanaged = append(unmanaged, v2)
		p.must("preview", "--refresh", "--expect-no-changes", "-s", "main")
		// A reorder must send every live rule, the unmanaged ones included.
		p.must("config", "set", "-s", "main", "unifi-e2e-cli:step", "3")
		p.must("up", "--yes", "--skip-preview", "-s", "main")
		got := a.order()
		leading(t, ids["r2"], ids["r1"])
		for _, id := range []string{u1, v2} {
			found := false
			for _, g := range got {
				found = found || g == id
			}
			if !found {
				t.Errorf("unmanaged rule %s missing from the order %v", id, got)
			}
		}
		p.must("preview", "--refresh", "--expect-no-changes", "-s", "main")
	})

	run("5-drift", func(t *testing.T) {
		// Another tool moves an unmanaged rule ahead of the managed block.
		live := a.order()
		moved := []string{u1}
		for _, id := range live {
			if id != u1 {
				moved = append(moved, id)
			}
		}
		a.json("PUT", "/acl-rules/ordering", map[string]any{"orderedAclRuleIds": moved}, 200)
		ch := changes(steps(t, p.must("preview", "--refresh", "--json", "-s", "main")))
		if len(ch) != 1 || ch[0].Op != "update" || !strings.HasSuffix(ch[0].URN, "::order") {
			t.Fatalf("preview after an out-of-band move: %+v; want one update of the ordering", ch)
		}
		if _, ok := ch[0].DetailedDiff["orderedAclRuleIds"]; !ok || len(ch[0].DetailedDiff) != 1 {
			t.Errorf("detailedDiff %v; want orderedAclRuleIds only", ch[0].DetailedDiff)
		}
		p.must("up", "--yes", "--skip-preview", "--refresh", "-s", "main")
		leading(t, ids["r2"], ids["r1"])
		p.must("preview", "--refresh", "--expect-no-changes", "-s", "main")
	})

	run("6-import-ordering-by-site-name", func(t *testing.T) {
		impCfg := map[string]string{"unifi-e2e-cli:mode": "alias"}
		for k, v := range base {
			impCfg[k] = v
		}
		stackInit("imp", impCfg)
		defer p.run("stack", "rm", "imp", "--force", "--yes") // release only
		p.must("import", "unifi:sites/v1:AclRuleOrdering", "order", siteName,
			"--yes", "--protect=false", "--generate-code=false", "--skip-preview", "-s", "imp")
		var st struct {
			Deployment struct {
				Resources []struct {
					URN, ID string
				} `json:"resources"`
			} `json:"deployment"`
		}
		if err := json.Unmarshal([]byte(p.must("stack", "export", "-s", "imp")), &st); err != nil {
			t.Fatal(err)
		}
		found := ""
		for _, r := range st.Deployment.Resources {
			if strings.HasSuffix(r.URN, "::order") {
				found = r.ID
			}
		}
		if found != "official:"+a.site {
			t.Errorf("imported ordering id %q, want official:%s", found, a.site)
		}
	})

	run("7-destroy", func(t *testing.T) {
		before := a.order()
		p.must("destroy", "--yes", "--skip-preview", "-s", "main")
		destroyed["main"] = true
		for _, k := range []string{"r1", "r2"} {
			if _, ok := a.rule(ids[k]); ok {
				t.Errorf("%s %s still on the controller after destroy", k, ids[k])
			}
		}
		// Destroying the ordering released it: the remaining rules keep their
		// relative order.
		var want []string
		for _, id := range before {
			if id != ids["r1"] && id != ids["r2"] {
				want = append(want, id)
			}
		}
		if got := a.order(); !reflect.DeepEqual(got, want) {
			t.Errorf("order after destroy %v, want %v (released, left as is)", got, want)
		}
		p.must("stack", "rm", "main", "--yes")
	})

	run("8-alias-migration", func(t *testing.T) {
		// A stack whose state holds the old token migrates with no replace:
		// build it with the new program, rewrite the state to the former token
		// (what a stack created before the rename holds), then run the new
		// program over it.
		aliasCfg := map[string]string{"unifi-e2e-cli:mode": "alias"}
		for k, v := range base {
			aliasCfg[k] = v
		}
		stackInit("alias", aliasCfg)
		p.must("up", "--yes", "--skip-preview", "-s", "alias")
		id := outputs("alias")["legacy"]
		raw := p.must("stack", "export", "-s", "alias")
		const newTok, oldTok = "unifi:sites/v1:AclRuleIpv4", "unifi:sites/v1:TrafficMatchIpv4"
		if strings.Count(raw, newTok) == 0 {
			t.Fatalf("state holds no %s", newTok)
		}
		f := filepath.Join(work, "alias-state.json")
		if err := os.WriteFile(f, []byte(strings.ReplaceAll(raw, newTok, oldTok)), 0o600); err != nil {
			t.Fatal(err)
		}
		p.must("stack", "import", "--file", f, "-s", "alias")
		if !strings.Contains(p.must("stack", "export", "-s", "alias"), "::"+oldTok+"::legacy") {
			t.Fatalf("the rewritten state does not hold the old URN")
		}
		ss := steps(t, p.must("preview", "--json", "-s", "alias"))
		for _, s := range ss {
			if strings.Contains(s.URN, "::legacy") && s.Op != "same" {
				t.Errorf("preview over the old-token state: %s %s; want same (an alias rename)", s.Op, s.URN)
			}
		}
		if out, err := p.run("preview", "--expect-no-changes", "-s", "alias"); err != nil {
			t.Errorf("preview --expect-no-changes over the old-token state failed:\n%s", tail(out, 20))
		}
		out := p.must("up", "--yes", "--skip-preview", "-s", "alias")
		if strings.Contains(out, "created") && !strings.Contains(out, "0 created") || strings.Contains(out, "deleted") {
			t.Errorf("up over the old-token state created or deleted something:\n%s", tail(out, 20))
		}
		after := p.must("stack", "export", "-s", "alias")
		if strings.Contains(after, oldTok) || !strings.Contains(after, "::"+newTok+"::legacy") {
			t.Errorf("after up the state still holds %s, or not %s", oldTok, newTok)
		}
		if got := outputs("alias")["legacy"]; got != id {
			t.Errorf("rule id %s -> %s: the alias must keep the same object", id, got)
		}
		if _, ok := a.rule(id); !ok {
			t.Errorf("rule %s is gone after the migration", id)
		}
		p.must("destroy", "--yes", "--skip-preview", "-s", "alias")
		destroyed["alias"] = true
		if _, ok := a.rule(id); ok {
			t.Errorf("rule %s still on the controller after destroy", id)
		}
		p.must("stack", "rm", "alias", "--yes")
	})
}
