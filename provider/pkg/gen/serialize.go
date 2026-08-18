package gen

import (
	"encoding/json"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// MarshalSchemaJSON is the single source of truth for how the Pulumi package
// schema is serialized to its committed/embedded byte form. It clears the
// version (injected at build time, never baked into the frozen golden) and
// indents deterministically. Both the gen entrypoint (mustWritePulumiSchema)
// and the golden test (runPipeline) MUST call this; there is no second path.
func MarshalSchemaJSON(pkgSpec pschema.PackageSpec) ([]byte, error) {
	// The version is injected into the binary at build time, not baked into the
	// committed schema, so generation stays deterministic across versions.
	pkgSpec.Version = ""
	return json.MarshalIndent(pkgSpec, "", "    ")
}

// MarshalMetadataJSON is the single source of truth for metadata.json bytes.
// It indents (json.MarshalIndent) so the committed golden produces reviewable
// diffs — a change from the historical compact json.Marshal, applied in this
// prep bead BEFORE the golden is frozen so no later reflow churn occurs.
func MarshalMetadataJSON(metadata openapigen.ProviderMetadata) ([]byte, error) {
	return json.MarshalIndent(metadata, "", "    ")
}
