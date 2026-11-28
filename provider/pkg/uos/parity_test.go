//go:build uosoracle

package uos

// The differential oracle (make uos-parity): the Go extractor against the
// research prototype (tools/uos-extract) and against the official spec that
// ships in the same image. Needs the pinned installer (UOS_INSTALLER) or the
// prototype's classes/ directory (UOS_ORACLE_CLASSES), plus the prototype's
// outputs: UOS_ORACLE_ROUTES (routes.tsv), UOS_ORACLE_CRUD (crud.json) and,
// for the spec score, the image's integration.json (read from the installer,
// or UOS_ORACLE_SPEC). Nothing here is committed: all inputs are local files.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The bar the prototype set on 5.1.15 and 5.1.42 (docs/research).
const (
	minSpecRoutes   = 73
	minSpecFields   = 157
	minSpecRequired = 154
)

// The slice-1 kinds must match the prototype field for field.
var sliceKinds = []string{"qos-rules", "apgroups", "firewall-app-blocks", "network-members-group"}

var (
	oracleOnce sync.Once
	oracleExt  *Extractor
	oracleSpec []byte
	oracleErr  error
)

func oracle(t *testing.T) (*Extractor, []byte) {
	t.Helper()
	oracleOnce.Do(func() {
		var idx *Index
		if inst := os.Getenv("UOS_INSTALLER"); inst != "" {
			ex, err := ExtractInstaller(inst, ExtractOptions{})
			if err != nil {
				oracleErr = err
				return
			}
			idx, oracleSpec = NewIndex(NewZipClasses(ex.Deps)), ex.IntegrationJSON
		} else if dir := os.Getenv("UOS_ORACLE_CLASSES"); dir != "" {
			dc, err := NewDirClasses(dir)
			if err != nil {
				oracleErr = err
				return
			}
			idx = NewIndex(dc)
		} else {
			oracleErr = fmt.Errorf("set UOS_INSTALLER or UOS_ORACLE_CLASSES")
			return
		}
		if p := os.Getenv("UOS_ORACLE_SPEC"); p != "" {
			oracleSpec, oracleErr = os.ReadFile(p)
			if oracleErr != nil {
				return
			}
		}
		oracleExt, oracleErr = NewExtractor(idx)
	})
	if oracleErr != nil {
		t.Fatalf("oracle setup: %v", oracleErr)
	}
	return oracleExt, oracleSpec
}

func TestParityRoutes(t *testing.T) {
	p := os.Getenv("UOS_ORACLE_ROUTES")
	if p == "" {
		t.Skip("UOS_ORACLE_ROUTES not set")
	}
	e, _ := oracle(t)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			want[l] = true
		}
	}
	got := map[string]bool{}
	for _, r := range e.Routes() {
		got[r.Verb+"\t"+r.Path+"\t"+r.Class] = true
	}
	var missing, extra []string
	for l := range want {
		if !got[l] {
			missing = append(missing, l)
		}
	}
	for l := range got {
		if !want[l] {
			extra = append(extra, l)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	t.Logf("routes: prototype %d, go %d, missing %d, extra %d", len(want), len(got), len(missing), len(extra))
	for _, l := range extra {
		t.Logf("extra (go only): %s", l)
	}
	if len(missing) > 0 {
		t.Errorf("routes the prototype finds and the Go extractor misses:\n%s", strings.Join(missing, "\n"))
	}
}

// pyFlat mirrors dto.py's flat() over the prototype's JSON.
func pyFlat(m map[string]any, pre string, out map[string]map[string]any) {
	if m == nil {
		return
	}
	switch m["type"] {
	case "array":
		it, _ := m["items"].(map[string]any)
		pyFlat(it, pre+"[]", out)
	case "object":
		fs, _ := m["fields"].(map[string]any)
		for k, v := range fs {
			vm, _ := v.(map[string]any)
			out[pre+k] = vm
			pyFlat(vm, pre+k+".", out)
		}
		vs, _ := m["variants"].(map[string]any)
		for _, v := range vs {
			sub := map[string]map[string]any{}
			vm, _ := v.(map[string]any)
			pyFlat(vm, pre, sub)
			for k, f := range sub {
				if _, ok := out[k]; !ok {
					out[k] = f
				}
			}
		}
	}
}

// goFlat does the same over the Go intermediate model.
func goFlat(m *tmodel, pre string, out map[string]*tmodel) {
	if m == nil {
		return
	}
	switch m.Type {
	case "array":
		goFlat(m.Items, pre+"[]", out)
	case "object":
		for _, f := range m.Fields {
			out[pre+f.Name] = f.M
			goFlat(f.M, pre+f.Name+".", out)
		}
		for _, v := range m.Variants {
			sub := map[string]*tmodel{}
			goFlat(v.M, pre, sub)
			for k, f := range sub {
				if _, ok := out[k]; !ok {
					out[k] = f
				}
			}
		}
	}
}

// facts renders the comparable facts of one field in a common form.
func pyFacts(f map[string]any) map[string]any {
	out := map[string]any{"type": f["type"], "required": f["required"] == true, "optional": f["optional"] == true,
		"readOnly": f["readOnly"] == true}
	if v, ok := f["values"].([]any); ok && f["type"] == "enum" {
		var s []string
		for _, x := range v {
			s = append(s, fmt.Sprint(x))
		}
		out["enum"] = s
	}
	if d, ok := f["default"]; ok {
		if d == "computed" {
			out["default"] = "computed"
		} else {
			b, _ := json.Marshal(d)
			out["default"] = normNum(string(b))
		}
	}
	if c, ok := f["constraints"].(map[string]any); ok {
		if m, ok := c["custom"].(string); ok {
			out["custom"] = m
		}
	}
	return out
}

func goFacts(f *tmodel) map[string]any {
	out := map[string]any{"type": f.Type, "required": f.Required, "optional": f.Optional, "readOnly": f.ReadOnly}
	if f.Type == "enum" {
		out["enum"] = f.Values
	}
	if f.HasDefault {
		if f.DefaultComputed {
			out["default"] = "computed"
		} else {
			b, _ := json.Marshal(f.Default)
			out["default"] = normNum(string(b))
		}
	}
	if len(f.Custom) > 0 {
		out["custom"] = f.Custom[len(f.Custom)-1]
		if len(f.Custom) > 1 {
			out["custom"] = strings.Join(f.Custom, " | ")
		}
	}
	return out
}

// normNum makes 1 and 1.0 compare equal.
func normNum(s string) string { return strings.TrimSuffix(s, ".0") }

func TestParityCrud(t *testing.T) {
	p := os.Getenv("UOS_ORACLE_CRUD")
	if p == "" {
		t.Skip("UOS_ORACLE_CRUD not set")
	}
	e, _ := oracle(t)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var py []map[string]any
	if err := json.Unmarshal(b, &py); err != nil {
		t.Fatal(err)
	}
	goKinds := map[string]CrudKind{}
	for _, ck := range e.Crud(V2Prefix) {
		goKinds[ck.Collection] = ck
	}
	isSlice := map[string]bool{}
	for _, k := range sliceKinds {
		isSlice[k] = true
	}
	totalPaths, agree := 0, 0
	for _, pk := range py {
		coll := pk["collection"].(string)
		gk, ok := goKinds[coll]
		if !ok {
			t.Errorf("collection %s: prototype has it, Go does not", coll)
			continue
		}
		if gk.Verbs != pk["verbs"] {
			t.Errorf("collection %s: verbs go=%s prototype=%s", coll, gk.Verbs, pk["verbs"])
		}
		req, _ := pk["request"].(map[string]any)
		pm, _ := req["model"].(map[string]any)
		pf := map[string]map[string]any{}
		pyFlat(pm, "", pf)
		gf := map[string]*tmodel{}
		goFlat(gk.Request, "", gf)
		var diffs []string
		for path, f := range pf {
			totalPaths++
			g, ok := gf[path]
			if !ok {
				diffs = append(diffs, "missing "+path)
				continue
			}
			a, bb := pyFacts(f), goFacts(g)
			// dto.py keeps one custom validator message per field (the last of
			// a Python set); the Go extractor keeps all of them, sorted. They
			// agree when the prototype's message is among Go's.
			if pc, ok := a["custom"].(string); ok {
				for _, m := range g.Custom {
					if m == pc {
						bb["custom"] = pc
					}
				}
			}
			if reflect.DeepEqual(a, bb) {
				agree++
				continue
			}
			diffs = append(diffs, fmt.Sprintf("%s: prototype %v go %v", path, a, bb))
		}
		for path := range gf {
			if _, ok := pf[path]; !ok {
				diffs = append(diffs, "extra (go only) "+path)
			}
		}
		sort.Strings(diffs)
		if len(diffs) == 0 {
			continue
		}
		if isSlice[coll] {
			t.Errorf("slice kind %s differs from the prototype:\n  %s", coll, strings.Join(diffs, "\n  "))
		} else {
			t.Logf("kind %s: %d differences:\n  %s", coll, len(diffs), strings.Join(diffs, "\n  "))
		}
	}
	for c := range goKinds {
		found := false
		for _, pk := range py {
			if pk["collection"] == c {
				found = true
			}
		}
		if !found {
			t.Logf("collection %s: Go only", c)
		}
	}
	t.Logf("crud: prototype %d collections, go %d; request field facts agreeing %d/%d", len(py), len(goKinds), agree, totalPaths)
}

// ---- spec check (port of tools/uos-extract/check/spec_check.py) ----

type specProp struct {
	required bool
	enum     []string
}

func TestParitySpecCheck(t *testing.T) {
	e, specBytes := oracle(t)
	if specBytes == nil {
		t.Skip("no integration.json (set UOS_INSTALLER or UOS_ORACLE_SPEC)")
	}
	var spec map[string]any
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	deref := func(s map[string]any) map[string]any {
		for {
			r, ok := s["$ref"].(string)
			if !ok {
				return s
			}
			s, _ = schemas[r[strings.LastIndex(r, "/")+1:]].(map[string]any)
			if s == nil {
				return map[string]any{}
			}
		}
	}
	var props func(s map[string]any, pre string, out map[string]specProp)
	props = func(s map[string]any, pre string, out map[string]specProp) {
		s = deref(s)
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			if alts, ok := s[key].([]any); ok {
				for _, a := range alts {
					props(a.(map[string]any), pre, out)
				}
			}
		}
		req := map[string]bool{}
		if rl, ok := s["required"].([]any); ok {
			for _, r := range rl {
				req[fmt.Sprint(r)] = true
			}
		}
		ps, _ := s["properties"].(map[string]any)
		keys := make([]string, 0, len(ps))
		for k := range ps {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v2 := deref(ps[k].(map[string]any))
			var items map[string]any
			if v2["type"] == "array" {
				if it, ok := v2["items"].(map[string]any); ok {
					items = deref(it)
				}
			}
			var en []string
			src := v2["enum"]
			if src == nil && items != nil {
				src = items["enum"]
			}
			if l, ok := src.([]any); ok {
				for _, x := range l {
					en = append(en, fmt.Sprint(x))
				}
				sort.Strings(en)
			}
			if _, ok := out[pre+k]; !ok {
				out[pre+k] = specProp{required: req[k], enum: en}
			}
			_, hasP := v2["properties"]
			_, hasO := v2["oneOf"]
			_, hasA := v2["allOf"]
			if v2["type"] == "object" || hasP || hasO || hasA {
				props(v2, pre+k+".", out)
			}
			if items != nil {
				props(items, pre+k+"[].", out)
			}
		}
	}
	// Routes: every spec path+verb must be in the route table.
	routeSet := map[string]bool{}
	for _, r := range e.Routes() {
		routeSet[r.Verb+" "+r.Path] = true
	}
	paths := spec["paths"].(map[string]any)
	pathKeys := make([]string, 0, len(paths))
	for p := range paths {
		pathKeys = append(pathKeys, p)
	}
	sort.Strings(pathKeys)
	specRoutes, routesFound := 0, 0
	bodies, fields, found, reqAgree, enums, enumAgree := 0, 0, 0, 0, 0, 0
	var notes []string
	for _, p := range pathKeys {
		item := paths[p].(map[string]any)
		for _, verb := range []string{"get", "post", "put", "patch", "delete"} {
			op, ok := item[verb].(map[string]any)
			if !ok {
				continue
			}
			specRoutes++
			if routeSet[strings.ToUpper(verb)+" "+p] {
				routesFound++
			} else {
				notes = append(notes, "ROUTE MISSING "+verb+" "+p)
			}
			if verb == "get" || verb == "delete" {
				continue
			}
			rb, _ := op["requestBody"].(map[string]any)
			ct, _ := rb["content"].(map[string]any)
			aj, _ := ct["application/json"].(map[string]any)
			sch, _ := aj["schema"].(map[string]any)
			if sch == nil {
				continue
			}
			m := e.BodyModel(strings.ToUpper(verb), p)
			if m == nil {
				notes = append(notes, "NO BODY "+verb+" "+p)
				continue
			}
			bodies++
			got := map[string]*tmodel{}
			goFlat(m, "", got)
			want := map[string]specProp{}
			props(sch, "", want)
			wk := make([]string, 0, len(want))
			for k := range want {
				wk = append(wk, k)
			}
			sort.Strings(wk)
			for _, k := range wk {
				w := want[k]
				fields++
				f := got[k]
				if f == nil {
					notes = append(notes, fmt.Sprintf("MISSING %s %s %s", verb, p, k))
					continue
				}
				found++
				if f.Required == w.required {
					reqAgree++
				} else {
					notes = append(notes, fmt.Sprintf("REQUIRED %s %s %s spec=%v bytecode=%v", verb, p, k, w.required, f.Required))
				}
				if w.enum != nil {
					enums++
					var ev []string
					if f.Type == "enum" {
						ev = append(ev, f.Values...)
					} else if f.Items != nil && f.Items.Type == "enum" {
						ev = append(ev, f.Items.Values...)
					}
					sort.Strings(ev)
					if reflect.DeepEqual(ev, w.enum) {
						enumAgree++
					} else {
						notes = append(notes, fmt.Sprintf("ENUM %s %s %s spec=%v bytecode=%v", verb, p, k, w.enum, ev))
					}
				}
			}
		}
	}
	t.Logf("spec check: routes %d/%d, bodies %d, fields %d/%d, required %d/%d, enums %d/%d",
		routesFound, specRoutes, bodies, found, fields, reqAgree, found, enumAgree, enums)
	for _, n := range notes {
		t.Log(n)
	}
	if routesFound < minSpecRoutes || routesFound < specRoutes {
		t.Errorf("routes %d/%d: below the bar (%d, and all spec routes)", routesFound, specRoutes, minSpecRoutes)
	}
	if found < minSpecFields {
		t.Errorf("request fields found %d: below the prototype's %d", found, minSpecFields)
	}
	if reqAgree < minSpecRequired {
		t.Errorf("required agreement %d: below the prototype's %d", reqAgree, minSpecRequired)
	}
}
