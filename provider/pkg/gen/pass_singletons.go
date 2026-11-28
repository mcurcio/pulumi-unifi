package gen

import (
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// singletonsPass builds the schema of each mappings.yaml `singletons:` entry
// (docs/design/private-binding.md §S2.5) from the spec's own PUT body for that
// path. Its path is in excludedPaths, so pulschema never saw it and the framework
// has no CRUD entry for it: the engine serves it through the official binding,
// and the private generator copies the entry into the plugin's surface.
//
// The resource is generated, not hand-written: the managed list is the PUT
// body's `property` (it must be a required array of strings), and the merge
// strategy contributes its own knobs. For leadingBlock:
//
//   - input  <property>   the managed ids; they lead the live order, in order
//   - input  unlisted     LAST (default) | REJECT: what happens to live ids the
//     program does not list
//   - input  siteId       the per-resource site override (described and marked
//     replaceOnChanges by the later passes, like every resource)
//   - output effectiveOrder the full live order
//
// Cross-checks, loud: the path must exist with GET and PUT; the token must not be
// a live framework token or have a CRUD entry. Deterministic: entries are sorted
// by token and every map is keyed.
func singletonsPass(s *GenState) error {
	for _, sg := range Singletons() {
		if err := addSingleton(s, sg); err != nil {
			return fmt.Errorf("singletons: %s: %w", sg.Token, err)
		}
	}
	return nil
}

func addSingleton(s *GenState, sg Singleton) error {
	if _, ok := s.Pkg.Resources[sg.Token]; ok {
		return fmt.Errorf("token is already a framework resource")
	}
	if _, ok := s.Pkg.Functions[sg.Token]; ok {
		return fmt.Errorf("token is already a function")
	}
	if _, ok := s.Meta.ResourceCRUDMap[sg.Token]; ok {
		return fmt.Errorf("token has a framework CRUD entry; a singleton is served outside the framework")
	}
	item := s.Doc.Paths.Find(sg.Path)
	if item == nil || item.Get == nil || item.Put == nil {
		return fmt.Errorf("path %s must exist in the spec with GET and PUT", sg.Path)
	}
	body, err := jsonBody(item.Put)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", sg.Path, err)
	}
	var wire string
	for name := range body.Properties {
		if openapigen.ToSdkName(name) == sg.Property {
			wire = name
		}
	}
	if wire == "" {
		return fmt.Errorf("PUT %s body has no property %q", sg.Path, sg.Property)
	}
	if !namesOf(body.Required)[wire] {
		return fmt.Errorf("PUT %s body does not require %q", sg.Path, wire)
	}
	list := body.Properties[wire].Value
	if list == nil || !list.Type.Is(openapi3.TypeArray) || list.Items == nil || list.Items.Value == nil ||
		!list.Items.Value.Type.Is(openapi3.TypeString) {
		return fmt.Errorf("PUT %s body property %q must be an array of strings", sg.Path, wire)
	}

	mod, short := splitToken(sg.Token)
	unlistedTok := mod + short + "Unlisted"
	if _, ok := s.Pkg.Types[unlistedTok]; ok {
		return fmt.Errorf("type %s already exists", unlistedTok)
	}
	s.Pkg.Types[unlistedTok] = pschema.ComplexTypeSpec{
		ObjectTypeSpec: pschema.ObjectTypeSpec{
			Type:        "string",
			Description: "What happens to the live entries the program does not list.",
		},
		Enum: []pschema.EnumValueSpec{
			{Name: "Last", Value: "LAST", Description: "They follow the listed entries, in their current relative order."},
			{Name: "Reject", Value: "REJECT", Description: "The update fails and names every unlisted entry."},
		},
	}
	unlistedRef := pschema.TypeSpec{Ref: "#/types/" + unlistedTok}
	ids := pschema.TypeSpec{Type: "array", Items: &pschema.TypeSpec{Type: "string"}}
	managed := pschema.PropertySpec{
		TypeSpec: ids,
		Description: "The entries this resource orders. They form the leading block of the site's order, " +
			"in this order (first match wins). Entries not listed here follow them (see unlisted).",
	}
	unlisted := pschema.PropertySpec{
		TypeSpec:    unlistedRef,
		Default:     "LAST",
		Description: "What happens to live entries not listed: LAST keeps them after the listed ones, in their current relative order; REJECT fails the update.",
	}
	s.Pkg.Resources[sg.Token] = pschema.ResourceSpec{
		ObjectTypeSpec: pschema.ObjectTypeSpec{
			Type: "object",
			Properties: map[string]pschema.PropertySpec{
				sg.Property: managed,
				"unlisted":  unlisted,
				"effectiveOrder": {
					TypeSpec:    ids,
					Description: "The site's full live order, as last read.",
				},
			},
			Required: []string{"effectiveOrder", sg.Property, "unlisted"},
		},
		InputProperties: map[string]pschema.PropertySpec{
			sg.Property: managed,
			"unlisted":  unlisted,
			"siteId":    {TypeSpec: pschema.TypeSpec{Type: "string"}},
		},
		RequiredInputs: []string{sg.Property},
	}
	return nil
}

// jsonBody returns an operation's application/json request body schema.
func jsonBody(op *openapi3.Operation) (*openapi3.Schema, error) {
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil, fmt.Errorf("no request body")
	}
	media := op.RequestBody.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		return nil, fmt.Errorf("no application/json body schema")
	}
	return media.Schema.Value, nil
}
