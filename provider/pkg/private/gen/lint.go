package gen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
)

var (
	rePrivateToken = regexp.MustCompile(`^unifi:network:[A-Z][A-Za-z0-9]*$`)
	reSecretName   = regexp.MustCompile(`(?i)(password|passphrase|secret|psk|token|private_?key)`)
)

// Lint is the private surface lint (design §11): token grammar, no change to
// the golden's own entries, property naming, secrets flagged, and the schema
// and surface.json agreeing kind for kind. It also binds the merged schema
// with Pulumi's schema checker.
func Lint(golden, merged, surface []byte) error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	var g, m pschema.PackageSpec
	if err := json.Unmarshal(golden, &g); err != nil {
		return err
	}
	if err := json.Unmarshal(merged, &m); err != nil {
		return err
	}
	s, err := engine.ParseSurface(surface)
	if err != nil {
		return err
	}
	same := func(what, tok string, a, b any) {
		ab, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		if !bytes.Equal(ab, bb) {
			add("%s %s differs from the golden", what, tok)
		}
	}
	for tok, r := range g.Resources {
		same("resource", tok, r, m.Resources[tok])
	}
	for tok, f := range g.Functions {
		same("function", tok, f, m.Functions[tok])
	}
	for tok, t := range g.Types {
		same("type", tok, t, m.Types[tok])
	}
	for k, v := range g.Config.Variables {
		same("config", k, v, m.Config.Variables[k])
	}
	if len(m.Functions) != len(g.Functions) {
		add("the private fragment adds functions (none in slice 1)")
	}
	served := map[string]bool{}
	for _, k := range s.Kinds {
		if !k.IsPrivate() {
			// An official engine-served kind: its schema is the golden's own.
			if _, ok := g.Resources[k.Token]; !ok {
				add("official engine kind %s: not a resource of the official golden", k.Token)
			}
			continue
		}
		served[k.Token] = true
		if !rePrivateToken.MatchString(k.Token) {
			add("token %s is not unifi:network:<PascalCase>", k.Token)
		}
		if !strings.HasPrefix(k.Token, engine.PrivateTokenPrefix) {
			add("token %s is outside %s", k.Token, engine.PrivateTokenPrefix)
		}
		r, ok := m.Resources[k.Token]
		if !ok {
			add("%s: in surface.json but not in the merged schema", k.Token)
			continue
		}
		lintFields(k.Token, k.Fields, r.Properties, m.Types, add)
		if _, ok := r.Properties["id"]; ok {
			add("%s: has an `id` property", k.Token)
		}
	}
	for tok := range m.Resources {
		if _, official := g.Resources[tok]; official {
			continue
		}
		if !served[tok] {
			add("%s: in the merged schema but not served by surface.json", tok)
		}
	}
	for tok := range m.Types {
		if _, official := g.Types[tok]; !official && !rePrivateToken.MatchString(tok) {
			add("type %s is not unifi:network:<PascalCase>", tok)
		}
	}
	if len(s.Private()) > 0 {
		if _, ok := m.Config.Variables[ConfigBaseURL]; !ok {
			add("config %s is missing", ConfigBaseURL)
		}
		if _, ok := m.Provider.InputProperties[ConfigBaseURL]; !ok {
			add("provider input %s is missing", ConfigBaseURL)
		}
		if _, err := pschema.ImportSpec(m, nil, pschema.ValidationOptions{}); err != nil {
			add("the merged schema does not bind: %v", err)
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New("private surface lint:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

func lintFields(tok string, fs map[string]*engine.Field, props map[string]pschema.PropertySpec, types map[string]pschema.ComplexTypeSpec, add func(string, ...any)) {
	for _, f := range fs {
		if reserved[f.Property] || !reProp.MatchString(f.Property) {
			add("%s.%s: property name is reserved or not lowerCamel", tok, f.Property)
		}
		if strings.HasPrefix(f.Wire, "x_") && !f.Secret {
			add("%s.%s: an x_ field (the controller's secret convention) is not secret", tok, f.Property)
		}
		if reSecretName.MatchString(f.Wire) && !f.Secret {
			add("%s.%s: the name looks secret but the field is not (record it in kinds.yaml notSecret: to clear it)", tok, f.Property)
		}
		p, ok := props[f.Property]
		if !ok {
			add("%s.%s: in surface.json but not in the schema", tok, f.Property)
			continue
		}
		if p.Secret != f.Secret {
			add("%s.%s: secret flags differ between the schema and surface.json", tok, f.Property)
		}
		if f.Type == "object" && strings.HasPrefix(p.Ref, "#/types/") {
			t := types[strings.TrimPrefix(p.Ref, "#/types/")]
			lintFields(tok, f.Fields, t.Properties, types, add)
		}
	}
}
