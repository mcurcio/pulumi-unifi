package engine

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The layer boundaries of docs/design/private-binding.md §S2.2, checked on the
// non-test sources: the engine knows Kinds and lifecycles, not HTTP or which
// API; a binding knows wire formats, not Pulumi; the controller layer knows no
// kinds; codegen never imports the runtime layers beyond the surface format.
func TestLayerBoundaries(t *testing.T) {
	const mod = "github.com/mcurcio/pulumi-unifi/provider/pkg/"
	rules := []struct {
		dir       string
		forbidden []string
	}{
		{".", []string{"net/http", mod + "binding", mod + "controller", mod + "dispatch", mod + "gen", mod + "private", "github.com/pulumi/pulumi/sdk/v3/proto"}},
		{"../binding/official", []string{"github.com/pulumi/", mod + "dispatch", mod + "private"}},
		{"../binding/v2", []string{"github.com/pulumi/", mod + "dispatch", mod + "private", mod + "binding/classic"}},
		{"../binding/classic", []string{"github.com/pulumi/", mod + "dispatch", mod + "private", mod + "binding/v2"}},
		{"../binding/internal/netapp", []string{"github.com/pulumi/", mod + "engine", mod + "dispatch"}},
		{"../controller", []string{"github.com/pulumi/", mod + "engine", mod + "binding", mod + "dispatch"}},
		{"../dispatch", []string{"net/http", mod + "gen", mod + "private"}},
		{"../gen", []string{mod + "engine", mod + "binding", mod + "controller", mod + "dispatch"}},
		{"../private/gen", []string{mod + "binding", mod + "controller", mod + "dispatch"}},
	}
	for _, r := range rules {
		files, err := filepath.Glob(filepath.Join(r.dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no sources (%v)", r.dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			af, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range af.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range r.forbidden {
					if p == bad || strings.HasPrefix(p, bad+"/") || strings.HasPrefix(bad, "github.com/pulumi/") && strings.HasPrefix(p, bad) {
						t.Errorf("%s imports %s: crosses a layer boundary (private-binding.md §S2.2)", f, p)
					}
				}
			}
		}
	}
}
