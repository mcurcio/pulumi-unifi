//go:build e2e_private

// The private-API e2e driver (docs/design/private-binding.md §11, step 7).
//
// It drives the pinned Pulumi CLI over the Python program in ../program and
// the PRIVATE=1 provider plugin against a real UniFi OS Server, booted fresh
// from the pinned installer's own image by run.sh, and checks every result
// with raw HTTP against the Network application. Run it through
// `make test-e2e-private` (or test/e2e-private/run.sh); it needs:
//
//	E2E_PULUMI    the Pulumi CLI binary
//	E2E_WORK      a scratch directory (Pulumi home, file backend, program copy)
//	PATH          with the plugin directory and the SDK venv's bin first
//
// and, by mode (E2E_MODE):
//
//	direct (default, `make test-e2e-private`): E2E_BASE_URL, the Network
//	  application reached directly before the wizard, e.g.
//	  http://127.0.0.1:18081. No apiKey; the stacks set unifi:networkBaseUrl.
//	edge (`make test-e2e-edge`, private-binding.md §S2.10): E2E_API_HOST, the
//	  UniFi OS edge of the seeded, wizard-completed controller, e.g.
//	  127.0.0.1:11443, and UNIFI_APIKEY, the seed's throwaway key, which
//	  run.sh reads from test/e2e/seed.env into the environment. The stacks set
//	  unifi:apiHost and unifi:allowInsecure only; the provider reaches the
//	  private API through https://<apiHost>/proxy/network with the key, and so
//	  do the raw checks. The key is never logged, put on a command line or in
//	  stack config.
//
// The Pulumi CLI and the provider get the caller's environment without its
// UNIFI_* provider variables (childEnv; in edge mode UNIFI_APIKEY alone is
// passed on), and the stacks pin siteId, so a developer's real key or site
// never reaches the throwaway controller.
package e2e

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

// kind is one private kind as the raw checks see it.
type kind struct {
	key        string // program output key
	token      string // Pulumi type token
	collection string // v2 collection (create, item paths)
	list       string // collection GET path
	idField    string
}

var kinds = []kind{
	{"qos", "unifi:network:QosRule", "qos-rules", "qos-rules", "_id"},
	{"apg", "unifi:network:ApGroup", "apgroups", "apgroups", "_id"},
	{"nmg", "unifi:network:NetworkMembersGroup", "network-members-group", "network-members-groups", "id"},
	{"fab", "unifi:network:FirewallAppBlock", "firewall-app-blocks", "firewall-app-blocks", "_id"},
}

func kindOf(key string) kind {
	for _, k := range kinds {
		if k.key == key {
			return k
		}
	}
	panic("unknown kind " + key)
}

const site = "default"

// --- raw controller access ---------------------------------------------------

type ctl struct {
	t    *testing.T
	base string // the Network application root (direct, or <edge>/proxy/network)
	http *http.Client
	key  string // X-API-Key for the edge; empty in direct mode. Never logged.
}

func (c *ctl) do(method, path string, body any) (int, []byte) {
	c.t.Helper()
	return c.doURL(method, c.base+"/v2/api/site/"+site+"/"+path, body, true)
}

// doURL sends one raw request; withKey=false omits the key (the edge must
// refuse it).
func (c *ctl) doURL(method, url string, body any, withKey bool) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if withKey && c.key != "" {
		req.Header.Set("X-API-Key", c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (c *ctl) list(k kind) []map[string]any {
	c.t.Helper()
	code, b := c.do("GET", k.list, nil)
	if code != 200 {
		c.t.Fatalf("GET %s: %d %s", k.list, code, b)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		c.t.Fatalf("GET %s: %v: %s", k.list, err, b)
	}
	return out
}

// get returns the object with the given controller id (by listing, which
// works for every kind, including those whose item GET answers 405).
func (c *ctl) get(k kind, id string) map[string]any {
	c.t.Helper()
	for _, o := range c.list(k) {
		if fmt.Sprint(o[k.idField]) == id {
			return o
		}
	}
	c.t.Fatalf("%s: no object with id %s", k.collection, id)
	return nil
}

// put replaces one object with a full PUT of the live object after edit,
// the way another tool (the UniFi UI, a script) would change it.
func (c *ctl) put(k kind, id string, edit func(map[string]any)) {
	c.t.Helper()
	o := c.get(k, id)
	edit(o)
	code, b := c.do("PUT", k.collection+"/"+id, o)
	if code/100 != 2 {
		c.t.Fatalf("raw PUT %s/%s: %d %s", k.collection, id, code, b)
	}
}

// classic sends one raw classic request (/api/s/<site>/<path>) and returns
// the envelope's data.
func (c *ctl) classic(method, path string, body any) (int, []map[string]any) {
	c.t.Helper()
	code, b := c.doURL(method, c.base+"/api/s/"+site+"/"+path, body, true)
	var env struct {
		Meta map[string]any   `json:"meta"`
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		c.t.Fatalf("%s classic %s: %d %.300s", method, path, code, b)
	}
	return code, env.Data
}

// classicGet lists a classic collection and returns the object with the id.
func (c *ctl) classicGet(path, id string) map[string]any {
	c.t.Helper()
	code, data := c.classic("GET", path, nil)
	if code != 200 {
		c.t.Fatalf("GET classic %s: %d", path, code)
	}
	for _, o := range data {
		if fmt.Sprint(o["_id"]) == id {
			return o
		}
	}
	return nil
}

// classicPut merges edit into the live classic object (the way the UniFi UI
// or a script would change it).
func (c *ctl) classicPut(path, id string, edit map[string]any) {
	c.t.Helper()
	o := c.classicGet(path, id)
	if o == nil {
		c.t.Fatalf("classic %s: no object %s", path, id)
	}
	for k, v := range edit {
		o[k] = v
	}
	if code, _ := c.classic("PUT", path+"/"+id, o); code != 200 {
		c.t.Fatalf("raw classic PUT %s/%s: %d", path, id, code)
	}
}

// withPrefix lists the objects of k whose name starts with prefix.
func (c *ctl) withPrefix(k kind, prefix string) []map[string]any {
	var out []map[string]any
	for _, o := range c.list(k) {
		if n, _ := o["name"].(string); strings.HasPrefix(n, prefix) {
			out = append(out, o)
		}
	}
	return out
}

// field reads a dotted path out of a decoded JSON object.
func field(o map[string]any, path string) (any, bool) {
	var cur any = o
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// expect checks dotted wire paths. A nil want means absent or null.
func expect(t *testing.T, what string, o map[string]any, want map[string]any) {
	t.Helper()
	paths := make([]string, 0, len(want))
	for p := range want {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		got, ok := field(o, p)
		w := want[p]
		if w == nil {
			if ok && got != nil {
				t.Errorf("%s: %s = %v, want absent or null", what, p, got)
			}
			continue
		}
		wj, _ := json.Marshal(w)
		gj, _ := json.Marshal(got)
		if !ok || string(wj) != string(gj) {
			t.Errorf("%s: %s = %s (present %v), want %s", what, p, gj, ok, wj)
		}
	}
}

// --- Pulumi CLI ----------------------------------------------------------------

type cli struct {
	t    *testing.T
	bin  string
	dir  string // the program copy
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
	cmd.Stdout = &out
	cmd.Stderr = &errb
	start := time.Now()
	err := cmd.Run()
	p.t.Logf("$ pulumi %s (%s)", strings.Join(args, " "), time.Since(start).Round(time.Second))
	// Keep every transcript for diagnosis; the e2e uses no secrets.
	f, ferr := os.OpenFile(p.logs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if ferr == nil {
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

func env(t *testing.T, name string) string {
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s is not set; run this through `make test-e2e-private` (test/e2e-private/run.sh)", name)
	}
	return v
}

// previewStep is one step of `pulumi preview --json`.
type previewStep struct {
	Op           string                     `json:"op"`
	URN          string                     `json:"urn"`
	DiffReasons  []string                   `json:"diffReasons"`
	DetailedDiff map[string]json.RawMessage `json:"detailedDiff"`
}

func changes(t *testing.T, out string) []previewStep {
	t.Helper()
	var doc struct {
		Steps []previewStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("preview --json: %v\n%s", err, tail(out, 40))
	}
	var ch []previewStep
	for _, s := range doc.Steps {
		switch s.Op {
		case "same", "read", "refresh":
		default:
			ch = append(ch, s)
		}
	}
	return ch
}

// --- the test ------------------------------------------------------------------

func TestPrivateE2E(t *testing.T) {
	bin := env(t, "E2E_PULUMI")
	work := env(t, "E2E_WORK")
	mode := os.Getenv("E2E_MODE")
	if mode == "" {
		mode = "direct"
	}

	// provider is the stack config that points the provider at the
	// controller; base is the Network root the raw checks use.
	var provider map[string]string
	var base string
	c := &ctl{t: t, http: &http.Client{Timeout: 30 * time.Second}}
	switch mode {
	case "direct":
		base = strings.TrimRight(env(t, "E2E_BASE_URL"), "/")
		provider = map[string]string{"unifi:networkBaseUrl": base, "unifi:siteId": "default"}
	case "edge":
		host := env(t, "E2E_API_HOST")
		c.key = env(t, "UNIFI_APIKEY")
		// The seeded controller's edge has a self-signed certificate, as the
		// provider's allowInsecure accepts.
		c.http.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- throwaway test controller
		base = "https://" + host + "/proxy/network"
		provider = map[string]string{"unifi:apiHost": host, "unifi:allowInsecure": "true", "unifi:siteId": "default"}
	default:
		t.Fatalf("E2E_MODE=%q: want direct or edge", mode)
	}
	c.base = base
	prefix := fmt.Sprintf("e2e-%d", time.Now().Unix())

	prog := filepath.Join(work, "program")
	copyDir(t, "program", prog)
	home := filepath.Join(work, "pulumi-home")
	state := filepath.Join(work, "state")
	for _, d := range []string{home, state} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p := &cli{t: t, bin: bin, dir: prog, logs: filepath.Join(work, "pulumi.log"), env: append(childEnv(mode == "edge"),
		"PULUMI_HOME="+home,
		"PULUMI_BACKEND_URL=file://"+state,
		"PULUMI_SKIP_UPDATE_CHECK=1",
		"PULUMI_CONFIG_PASSPHRASE=e2e-private-throwaway",
		"PULUMI_DIY_BACKEND_NO_LEGACY_WARNING=1",
	)}
	t.Logf("mode %s, work dir %s, object prefix %s, controller %s", mode, work, prefix, base)

	// Teardown: destroy whatever the stacks still hold, then sweep any object
	// this run created that a failed step left behind.
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			_, _ = p.run("destroy", "--yes", "--skip-preview", "-s", "main")
		}
		for _, k := range kinds {
			for _, o := range c.withPrefix(k, prefix) {
				id := fmt.Sprint(o[k.idField])
				code, _ := c.do("DELETE", k.collection+"/"+id, nil)
				t.Logf("swept leftover %s %s (%v): %d", k.collection, id, o["name"], code)
				if destroyed {
					t.Errorf("destroy left %s %s behind", k.collection, id)
				}
			}
		}
	})

	stackInit := func(name string, cfg map[string]string) {
		for k, v := range provider {
			cfg[k] = v
		}
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
	stripID := func(key, id string) string {
		t.Helper()
		if !strings.HasPrefix(id, "private:") || len(id) <= len("private:") {
			t.Fatalf("%s id %q: want private:<id> (stored ids name their API)", key, id)
		}
		return strings.TrimPrefix(id, "private:")
	}
	step := func(t *testing.T, name string, fn func(t *testing.T)) {
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}

	if mode == "edge" {
		step(t, "0-edge-needs-the-key", func(t *testing.T) {
			// Post-wizard, the edge refuses the private API without the key
			// (probe b4/b5), so every later step proves key-authenticated
			// private reads and writes.
			for _, u := range []string{base + "/v2/api/site/" + site + "/apgroups", base + "/api/s/" + site + "/stat/sysinfo"} {
				if code, _ := c.doURL("GET", u, nil, false); code != http.StatusUnauthorized {
					t.Errorf("GET %s without the key: HTTP %d, want 401", u, code)
				}
				if code, b := c.doURL("GET", u, nil, true); code != http.StatusOK {
					t.Errorf("GET %s with the key: HTTP %d: %.200s", u, code, b)
				}
			}
		})
	}

	stackInit("main", map[string]string{
		"unifi-e2e-private:prefix": prefix,
		"unifi-e2e-private:step":   "1",
		"unifi-e2e-private:mode":   "main",
	})

	var ids map[string]string // program key -> controller id
	step(t, "1-create", func(t *testing.T) {
		p.must("up", "--yes", "--skip-preview", "-s", "main")
		ids = map[string]string{}
		for key, id := range outputs("main") {
			ids[key] = stripID(key, id)
		}
		if len(ids) != len(kinds) {
			t.Fatalf("outputs hold %d ids, want %d: %v", len(ids), len(kinds), ids)
		}
		expect(t, "qos", c.get(kindOf("qos"), ids["qos"]), map[string]any{
			"name": prefix + "-qos", "objective": "LIMIT", "schedule.mode": "ALWAYS",
			"source.matching_target": "ANY", "destination.matching_target": "ANY",
			"destination.port_matching_type": "SPECIFIC", "destination.port": "443",
			"download_limit_kbps": 1000,
		})
		expect(t, "apg", c.get(kindOf("apg"), ids["apg"]), map[string]any{
			"name": prefix + "-apg", "device_macs": []string{},
		})
		expect(t, "nmg", c.get(kindOf("nmg"), ids["nmg"]), map[string]any{
			"name": prefix + "-nmg", "type": "CLIENTS", "members": []string{"02:00:5e:00:00:10"},
		})
		expect(t, "fab", c.get(kindOf("fab"), ids["fab"]), map[string]any{
			"name": prefix + "-fab", "app_ids": []int{5}, "schedule.mode": "ALWAYS",
		})
	})

	step(t, "2-read-noop", func(t *testing.T) {
		// Rule 1: what was declared reads back unchanged, so a refresh then a
		// preview and a second up are no-ops.
		p.must("refresh", "--yes", "--skip-preview", "-s", "main")
		p.must("preview", "--expect-no-changes", "-s", "main")
		p.must("up", "--yes", "--expect-no-changes", "-s", "main")
	})

	step(t, "3-update-keeps-unmanaged", func(t *testing.T) {
		// Another tool changes fields the program never declared.
		c.put(kindOf("qos"), ids["qos"], func(o map[string]any) {
			o["enabled"] = true
			o["download_burst"] = "SHORT"
		})
		c.put(kindOf("fab"), ids["fab"], func(o map[string]any) {
			o["client_macs"] = []string{"02:00:5e:00:00:20"}
		})
		// Step 2 changes managed fields and drops destination.port_matching_type
		// and destination.port (G-Q1: back to ANY and cleared).
		p.must("config", "set", "-s", "main", "unifi-e2e-private:step", "2")
		p.must("up", "--yes", "--skip-preview", "-s", "main")
		got := outputs("main")
		for key, id := range got {
			if stripID(key, id) != ids[key] {
				t.Errorf("%s was replaced (%s -> %s); want an in-place update", key, ids[key], id)
			}
		}
		expect(t, "qos", c.get(kindOf("qos"), ids["qos"]), map[string]any{
			"download_limit_kbps":            3000,
			"destination.matching_target":    "ANY",
			"destination.port_matching_type": "ANY", // dropped -> model default
			"destination.port":               nil,   // dropped Optional -> cleared
			"enabled":                        true,  // unmanaged, kept
			"download_burst":                 "SHORT",
			"name":                           prefix + "-qos",
		})
		expect(t, "apg", c.get(kindOf("apg"), ids["apg"]), map[string]any{
			"name": prefix + "-apg-renamed", "device_macs": []string{},
		})
		expect(t, "nmg", c.get(kindOf("nmg"), ids["nmg"]), map[string]any{
			"members": []string{"02:00:5e:00:00:10", "02:00:5e:00:00:11"}, "type": "CLIENTS",
		})
		expect(t, "fab", c.get(kindOf("fab"), ids["fab"]), map[string]any{
			"app_ids": []int{5, 6}, "schedule.mode": "EVERY_DAY", "schedule.time_all_day": true,
			"client_macs": []string{"02:00:5e:00:00:20"}, // unmanaged, kept
		})
		p.must("preview", "--refresh", "--expect-no-changes", "-s", "main")
	})

	step(t, "4-managed-drift-shows", func(t *testing.T) {
		c.put(kindOf("qos"), ids["qos"], func(o map[string]any) { o["download_limit_kbps"] = 777 })
		ch := changes(t, p.must("preview", "--refresh", "--json", "-s", "main"))
		if len(ch) != 1 {
			t.Fatalf("preview after a managed drift: %d changes, want 1: %+v", len(ch), ch)
		}
		s := ch[0]
		var dd []string
		for k := range s.DetailedDiff {
			dd = append(dd, k)
		}
		if s.Op != "update" || !strings.HasSuffix(s.URN, "::qos") ||
			!reflect.DeepEqual(dd, []string{"downloadLimitKbps"}) {
			t.Fatalf("preview after a managed drift: op %s urn %s detailedDiff %v diffReasons %v; want an update of qos on downloadLimitKbps only",
				s.Op, s.URN, dd, s.DiffReasons)
		}
		p.must("up", "--yes", "--skip-preview", "--refresh", "-s", "main")
		expect(t, "qos", c.get(kindOf("qos"), ids["qos"]), map[string]any{
			"download_limit_kbps": 3000, "enabled": true, "download_burst": "SHORT",
		})
		p.must("preview", "--expect-no-changes", "-s", "main")
	})

	step(t, "5-import-by-natural-key", func(t *testing.T) {
		// Rule 3: a fresh stack adopts live objects by natural key, both the
		// bare form and field=value, then declares them with no changes.
		stackInit("imp", map[string]string{
			"unifi-e2e-private:prefix": prefix,
			"unifi-e2e-private:step":   "2",
			"unifi-e2e-private:mode":   "import",
		})
		defer p.run("stack", "rm", "imp", "--force", "--yes") // leaves the objects to "main"

		// Zero matches is an error that names the candidates.
		if out, err := p.run("import", "unifi:network:ApGroup", "missing", prefix+"-no-such-group",
			"--yes", "--protect=false", "--generate-code=false", "--skip-preview", "-s", "imp"); err == nil ||
			!strings.Contains(out, "no unifi:network:ApGroup has") {
			t.Fatalf("import of a missing natural key: err %v; want the zero-match error\n%s", err, tail(out, 20))
		}

		imports := map[string]any{"resources": []map[string]any{
			{"type": "unifi:network:ApGroup", "name": "apg-imported", "id": prefix + "-apg-renamed",
				"properties": []string{"name", "deviceMacs"}},
			{"type": "unifi:network:NetworkMembersGroup", "name": "nmg-imported", "id": "name=" + prefix + "-nmg",
				"properties": []string{"name", "type", "members"}},
			{"type": "unifi:network:FirewallAppBlock", "name": "fab-imported", "id": prefix + "-fab"},
		}}
		f := filepath.Join(work, "imports.json")
		b, _ := json.Marshal(imports)
		if err := os.WriteFile(f, b, 0o644); err != nil {
			t.Fatal(err)
		}
		p.must("import", "-f", f, "--yes", "--protect=false", "--generate-code=false", "--skip-preview", "-s", "imp")

		var st struct {
			Deployment struct {
				Resources []struct {
					URN string `json:"urn"`
					ID  string `json:"id"`
				} `json:"resources"`
			} `json:"deployment"`
		}
		if err := json.Unmarshal([]byte(p.must("stack", "export", "-s", "imp")), &st); err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, r := range st.Deployment.Resources {
			for _, key := range []string{"apg", "nmg", "fab"} {
				if strings.HasSuffix(r.URN, "::"+key+"-imported") {
					found[key] = r.ID
				}
			}
		}
		for _, key := range []string{"apg", "nmg", "fab"} {
			if found[key] != "private:"+ids[key] {
				t.Errorf("imported %s id %q, want private:%s", key, found[key], ids[key])
			}
		}
		p.must("preview", "--expect-no-changes", "-s", "imp")
	})

	step(t, "6-destroy", func(t *testing.T) {
		p.must("destroy", "--yes", "--skip-preview", "-s", "main")
		destroyed = true
		for _, k := range kinds {
			if left := c.withPrefix(k, prefix); len(left) != 0 {
				t.Errorf("after destroy, %s still holds %d e2e objects: %v", k.collection, len(left), left)
			}
		}
		p.must("stack", "rm", "main", "--yes")
	})

	if mode == "edge" {
		classicSteps(t, c, p, prefix, stackInit, step)
	}
}

// classicSteps are the classic rows of the edge tier
// (docs/design/private-binding.md §S2.8, §S2.11): the classic UserGroup
// (create, refresh no-op, update keeps unmanaged, managed drift, duplicate
// name refused, import by name, drop-reset, destroy) and the SettingNtp
// singleton (adopt, change, no-op, drift, drop-reset, destroy = release),
// after which the original ntp settings are put back and checked by re-read.
func classicSteps(t *testing.T, c *ctl, p *cli, prefix string, stackInit func(string, map[string]string), step func(*testing.T, string, func(*testing.T))) {
	const ug, ntp = "rest/usergroup", "rest/setting/ntp"
	code, docs := c.classic("GET", ntp, nil)
	if code != 200 || len(docs) != 1 {
		t.Fatalf("GET %s: %d, %d documents; want the one ntp document", ntp, code, len(docs))
	}
	original := docs[0]
	ntpID := fmt.Sprint(original["_id"])
	restored, destroyed := false, false
	restore := func(t *testing.T) {
		if code, _ := c.classic("PUT", ntp+"/"+ntpID, original); code != 200 {
			t.Errorf("restoring the ntp settings: HTTP %d", code)
			return
		}
		got := c.classicGet(ntp, ntpID)
		for k, v := range original {
			if fmt.Sprint(got[k]) != fmt.Sprint(v) {
				t.Errorf("ntp settings after the restore: %s = %v, want %v", k, got[k], v)
			}
		}
		restored = true
	}
	t.Cleanup(func() {
		if !destroyed {
			_, _ = p.run("destroy", "--yes", "--skip-preview", "-s", "classic")
		}
		_, groups := c.classic("GET", ug, nil)
		for _, o := range groups {
			if n, _ := o["name"].(string); strings.HasPrefix(n, prefix) {
				code, _ := c.classic("DELETE", ug+"/"+fmt.Sprint(o["_id"]), nil)
				t.Logf("swept leftover usergroup %v: %d", o["name"], code)
				if destroyed {
					t.Errorf("destroy left usergroup %v behind", o["name"])
				}
			}
		}
		if !restored {
			restore(t)
		}
	})
	groupsNamed := func(name string) []map[string]any {
		_, all := c.classic("GET", ug, nil)
		var out []map[string]any
		for _, o := range all {
			if o["name"] == name {
				out = append(out, o)
			}
		}
		return out
	}
	stackOut := func(stack string) map[string]string {
		var o struct {
			IDs map[string]string `json:"ids"`
		}
		if err := json.Unmarshal([]byte(p.must("stack", "output", "--json", "-s", stack)), &o); err != nil {
			t.Fatal(err)
		}
		return o.IDs
	}
	stackInit("classic", map[string]string{
		"unifi-e2e-private:prefix": prefix,
		"unifi-e2e-private:step":   "1",
		"unifi-e2e-private:mode":   "classic",
	})
	var ugID string
	step(t, "7-classic-create-and-adopt", func(t *testing.T) {
		p.must("up", "--yes", "--skip-preview", "-s", "classic")
		ids := stackOut("classic")
		if ids["ntp"] != "private:"+ntpID {
			t.Fatalf("SettingNtp id %q, want private:%s (adopted, not created)", ids["ntp"], ntpID)
		}
		ugID = strings.TrimPrefix(ids["ug"], "private:")
		g := c.classicGet(ug, ugID)
		if g == nil || g["name"] != prefix+"-ug" || fmt.Sprint(g["qos_rate_max_down"]) != "500" {
			t.Fatalf("usergroup after create: %v", g)
		}
		if _, docs := c.classic("GET", ntp, nil); len(docs) != 1 {
			t.Fatalf("adopt must not create a second ntp document: %d", len(docs))
		}
		expect(t, "ntp", c.classicGet(ntp, ntpID), map[string]any{
			"setting_preference": "manual", "ntp_server_1": "time-e2e-1.example.invalid",
			"ntp_server_2": original["ntp_server_2"], // unmanaged, kept
		})
	})

	step(t, "8-classic-read-noop", func(t *testing.T) {
		p.must("refresh", "--yes", "--skip-preview", "-s", "classic")
		p.must("preview", "--expect-no-changes", "-s", "classic")
		p.must("up", "--yes", "--expect-no-changes", "-s", "classic")
	})

	step(t, "9-classic-update-keeps-unmanaged", func(t *testing.T) {
		c.classicPut(ug, ugID, map[string]any{"qos_rate_max_up": 250})
		c.classicPut(ntp, ntpID, map[string]any{"ntp_server_2": "time-e2e-unmanaged.example.invalid"})
		p.must("config", "set", "-s", "classic", "unifi-e2e-private:step", "2")
		p.must("up", "--yes", "--skip-preview", "-s", "classic")
		if got := strings.TrimPrefix(stackOut("classic")["ug"], "private:"); got != ugID {
			t.Errorf("usergroup replaced (%s -> %s); want an in-place update", ugID, got)
		}
		expect(t, "usergroup", c.classicGet(ug, ugID), map[string]any{
			"qos_rate_max_down": 800, "qos_rate_max_up": 250, // managed changed, unmanaged kept
		})
		expect(t, "ntp", c.classicGet(ntp, ntpID), map[string]any{
			"ntp_server_1": "time-e2e-2.example.invalid", "ntp_server_2": "time-e2e-unmanaged.example.invalid",
			"setting_preference": "manual",
		})
		p.must("preview", "--refresh", "--expect-no-changes", "-s", "classic")
	})

	step(t, "10-classic-managed-drift-shows", func(t *testing.T) {
		c.classicPut(ug, ugID, map[string]any{"qos_rate_max_down": 999})
		c.classicPut(ntp, ntpID, map[string]any{"ntp_server_1": "time-e2e-drift.example.invalid"})
		ch := changes(t, p.must("preview", "--refresh", "--json", "-s", "classic"))
		got := map[string][]string{}
		for _, s := range ch {
			if s.Op != "update" {
				t.Errorf("drift: op %s on %s, want update", s.Op, s.URN)
			}
			var dd []string
			for k := range s.DetailedDiff {
				dd = append(dd, k)
			}
			sort.Strings(dd)
			got[s.URN[strings.LastIndex(s.URN, "::")+2:]] = dd
		}
		if !reflect.DeepEqual(got, map[string][]string{"ug": {"qosRateMaxDown"}, "ntp": {"ntpServer1"}}) {
			t.Fatalf("drift: %v; want ug qosRateMaxDown and ntp ntpServer1", got)
		}
		p.must("up", "--yes", "--skip-preview", "--refresh", "-s", "classic")
		expect(t, "usergroup", c.classicGet(ug, ugID), map[string]any{"qos_rate_max_down": 800, "qos_rate_max_up": 250})
		expect(t, "ntp", c.classicGet(ntp, ntpID), map[string]any{"ntp_server_1": "time-e2e-2.example.invalid"})
		p.must("preview", "--expect-no-changes", "-s", "classic")
	})

	step(t, "11-classic-duplicate-name-refused", func(t *testing.T) {
		// The classic API accepts a duplicate name (probe f1); the provider
		// refuses it (rule 3) and names the object to import instead.
		stackInit("cdup", map[string]string{
			"unifi-e2e-private:prefix": prefix,
			"unifi-e2e-private:step":   "2",
			"unifi-e2e-private:mode":   "classic-dup",
		})
		defer p.run("stack", "rm", "cdup", "--force", "--yes")
		out, err := p.run("up", "--yes", "--skip-preview", "-s", "cdup")
		if err == nil || !strings.Contains(out, "already exists (id private:"+ugID+")") {
			t.Fatalf("up of a taken name: err %v; want the already-exists refusal\n%s", err, tail(out, 20))
		}
		if n := len(groupsNamed(prefix + "-ug")); n != 1 {
			t.Fatalf("%d usergroups named %s-ug, want 1", n, prefix)
		}
	})

	step(t, "12-classic-import", func(t *testing.T) {
		stackInit("cimp", map[string]string{
			"unifi-e2e-private:prefix": prefix,
			"unifi-e2e-private:step":   "2",
			"unifi-e2e-private:mode":   "classic-import",
		})
		defer p.run("stack", "rm", "cimp", "--force", "--yes") // leaves both to "classic"
		p.must("import", "unifi:network:UserGroup", "ug-imported", prefix+"-ug",
			"--yes", "--protect=false", "--generate-code=false", "--skip-preview", "-s", "cimp")
		p.must("import", "unifi:network:SettingNtp", "ntp-imported", "ntp",
			"--yes", "--protect=false", "--generate-code=false", "--skip-preview", "-s", "cimp")
		var st struct {
			Deployment struct {
				Resources []struct {
					URN string `json:"urn"`
					ID  string `json:"id"`
				} `json:"resources"`
			} `json:"deployment"`
		}
		if err := json.Unmarshal([]byte(p.must("stack", "export", "-s", "cimp")), &st); err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, r := range st.Deployment.Resources {
			for _, key := range []string{"ug", "ntp"} {
				if strings.HasSuffix(r.URN, "::"+key+"-imported") {
					found[key] = r.ID
				}
			}
		}
		if found["ug"] != "private:"+ugID || found["ntp"] != "private:"+ntpID {
			t.Fatalf("imported ids %v; want private:%s and private:%s", found, ugID, ntpID)
		}
		p.must("preview", "--expect-no-changes", "-s", "cimp")
	})

	step(t, "13-classic-drop-resets", func(t *testing.T) {
		// G-Q1: a managed field the program drops resets to its default.
		p.must("config", "set", "-s", "classic", "unifi-e2e-private:step", "3")
		p.must("up", "--yes", "--skip-preview", "-s", "classic")
		expect(t, "usergroup", c.classicGet(ug, ugID), map[string]any{"qos_rate_max_down": -1, "qos_rate_max_up": 250})
		// Both ntp fields are dropped: back to "auto", and the first server to
		// the value the controller itself restores in auto mode (the seed's).
		expect(t, "ntp", c.classicGet(ntp, ntpID), map[string]any{
			"setting_preference": "auto", "ntp_server_1": original["ntp_server_1"],
		})
		p.must("preview", "--refresh", "--expect-no-changes", "-s", "classic")
	})

	step(t, "14-classic-destroy-releases", func(t *testing.T) {
		before := c.classicGet(ntp, ntpID)
		p.must("destroy", "--yes", "--skip-preview", "-s", "classic")
		destroyed = true
		if n := len(groupsNamed(prefix + "-ug")); n != 0 {
			t.Errorf("after destroy, %d usergroups named %s-ug remain", n, prefix)
		}
		// Release: the settings stay as the stack left them.
		after := c.classicGet(ntp, ntpID)
		if after == nil {
			t.Fatal("destroy must not delete the ntp settings")
		}
		for k, v := range before {
			if fmt.Sprint(after[k]) != fmt.Sprint(v) {
				t.Errorf("destroy changed ntp %s: %v -> %v; want it released as is", k, v, after[k])
			}
		}
		p.must("stack", "rm", "classic", "--yes")
		restore(t)
	})
}

// childEnv is os.Environ() without the provider's UNIFI_* variables (apiKey,
// apiHost, siteId, networkBaseUrl, allowInsecure), so none of a developer's
// real controller settings leak into this tier. keepKey passes UNIFI_APIKEY on
// (edge mode: run.sh set it from the seed's throwaway key).
func childEnv(keepKey bool) []string {
	drop := map[string]bool{
		"UNIFI_APIKEY": !keepKey, "UNIFI_API_HOST": true, "UNIFI_SITEID": true,
		"UNIFI_NETWORK_BASE_URL": true, "UNIFI_ALLOW_INSECURE": true,
	}
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !drop[name] {
			out = append(out, kv)
		}
	}
	return out
}
