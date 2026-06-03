package gen

import (
	"github.com/getkin/kin-openapi/openapi3"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// GenState is the mutable bundle a post-pulschema pass operates on: the Pulumi
// package schema, the CRUD/name metadata, and the pulschema-updated OpenAPI doc.
// All three are held by pointer so a pass mutates the canonical artifacts in
// place (no by-value copy of the large openapi3.T / PackageSpec structs) and the
// run loop threads the same state through every pass. GenState is the only
// contract a pass touches.
type GenState struct {
	Pkg  *pschema.PackageSpec
	Meta *openapigen.ProviderMetadata
	Doc  *openapi3.T
}

// pass is one named post-process step over GenState. Naming each step keeps the
// ordered slice self-documenting (for logging + deterministic ordering) and lets
// each pass be tested in isolation, without any interface ceremony.
type pass struct {
	name string
	fn   func(*GenState) error
}
