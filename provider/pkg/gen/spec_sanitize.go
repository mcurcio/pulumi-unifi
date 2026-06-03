package gen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// invalidIdentChars matches any run of characters not allowed in an OpenAPI 3
// component key (the allowed set is [a-zA-Z0-9._-]).
var invalidIdentChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

const schemaRefPrefix = "#/components/schemas/"

// SanitizeSpecBytes rewrites the raw spec JSON so every components.schemas key
// conforms to the OpenAPI 3 identifier charset, updating all $ref and
// discriminator-mapping pointers to match. The beezly capture names many
// schemas with spaces/parentheses (e.g. "ACL rule", "Teleport client
// (connection) details"), which kin-openapi rejects during validation before
// our FixOpenAPIDoc can run — so this must operate on the raw bytes first.
//
// Case is preserved (runs of invalid chars collapse to a single "_"), which
// keeps otherwise-similar names distinct (e.g. "IP Address selector" vs
// "IP address selector"). Collisions are treated as a hard error.
//
// The transform is deterministic: keys are processed in sorted order and
// encoding/json marshals map keys in sorted order.
func SanitizeSpecBytes(data []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("unmarshaling spec: %w", err)
	}

	// The capture ships an empty `info.license: {}`, which fails validation
	// (a present license must have a non-empty name). Upstream declares no
	// license, so drop the empty object entirely.
	if info, ok := doc["info"].(map[string]any); ok {
		if lic, ok := info["license"].(map[string]any); ok {
			if name, _ := lic["name"].(string); name == "" {
				delete(info, "license")
			}
		}
	}

	components, ok := doc["components"].(map[string]any)
	if !ok {
		return data, nil
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return data, nil
	}

	// Build the old->new rename map for schema keys.
	renamedRefs := map[string]string{} // old full ref path -> new full ref path
	newSchemas := make(map[string]any, len(schemas))
	used := make(map[string]string, len(schemas)) // sanitized -> original, for collision detection

	oldKeys := make([]string, 0, len(schemas))
	for k := range schemas {
		oldKeys = append(oldKeys, k)
	}
	sort.Strings(oldKeys)

	for _, old := range oldKeys {
		sanitized := sanitizeIdent(old)
		if prior, clash := used[sanitized]; clash {
			return nil, fmt.Errorf("schema key collision: %q and %q both sanitize to %q", prior, old, sanitized)
		}
		used[sanitized] = old
		newSchemas[sanitized] = schemas[old]
		if sanitized != old {
			renamedRefs[schemaRefPrefix+old] = schemaRefPrefix+sanitized
		}
	}
	components["schemas"] = newSchemas

	// Replace empty `{}` sub-schemas (JSON-Schema "any") with a free-form object.
	// pulschema cannot derive a Pulumi property type from a constraint-less
	// schema (e.g. WifiSecurityConfigurationDetailObject.radiusConfiguration).
	//
	// Top-level component schemas get the same treatment: a referenced component
	// with no type-determining keys (e.g. "FilterExpression": {}, or one carrying
	// only an `example`) makes pulschema's propertyTypeSpec index an empty type
	// slice and panic. Normalize those to a free-form object in place, preserving
	// any human-facing description/title.
	for key, schema := range newSchemas {
		if m, ok := schema.(map[string]any); ok && isTypelessSchema(m) {
			normalizeTypelessSchema(m)
		}
		patchEmptySchemas(newSchemas[key])
	}

	rewriteRefs(doc, renamedRefs)

	return json.Marshal(doc)
}

// freeFormObject returns a fresh schema meaning "an object with arbitrary
// properties", used to replace constraint-less `{}` schemas.
func freeFormObject() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

// isEmptySchema reports whether a sub-schema position carries no constraints.
// A JSON `null` (the key present with a null value, e.g. `"items": null`) counts
// as empty too — pulschema would otherwise dereference a nil schema.
func isEmptySchema(node any) bool {
	if node == nil {
		return true
	}
	m, ok := node.(map[string]any)
	return ok && len(m) == 0
}

// typeDeterminingKeys are the schema keywords pulschema relies on to derive a
// Pulumi type. A schema (or referenced component) lacking all of them yields no
// usable type and crashes the generator.
var typeDeterminingKeys = []string{
	"type", "$ref", "properties", "allOf", "oneOf", "anyOf",
	"enum", "items", "additionalProperties", "patternProperties",
}

// isTypelessSchema reports whether a schema has no type-determining keyword.
func isTypelessSchema(m map[string]any) bool {
	for _, k := range typeDeterminingKeys {
		if _, ok := m[k]; ok {
			return false
		}
	}
	return true
}

// normalizeTypelessSchema rewrites m in place into a free-form object, keeping
// only human-facing metadata (description/title) from the original.
func normalizeTypelessSchema(m map[string]any) {
	desc, hasDesc := m["description"]
	title, hasTitle := m["title"]
	for k := range m {
		delete(m, k)
	}
	m["type"] = "object"
	m["additionalProperties"] = true
	if hasDesc {
		m["description"] = desc
	}
	if hasTitle {
		m["title"] = title
	}
}

// isStringEnum reports whether an enum should be kept as a Pulumi string enum:
// the schema's type is "string", or (type absent) every enum value is a string.
func isStringEnum(typ any, enum []any) bool {
	if ts, ok := typ.(string); ok {
		return ts == "string"
	}
	if typ != nil {
		// Composite/array type declarations are not supported as enums here.
		return false
	}
	for _, v := range enum {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

// patchEmptySchemas walks a schema node and replaces empty `{}` schemas found in
// any sub-schema position with a free-form object.
func patchEmptySchemas(node any) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}

	// pulschema only supports string-valued enums. Drop enum constraints on
	// numeric/boolean schemas (e.g. broadcastingFrequenciesGHz, a number enum),
	// leaving the underlying type intact.
	if enum, ok := m["enum"].([]any); ok && !isStringEnum(m["type"], enum) {
		delete(m, "enum")
	}

	// Single sub-schema positions.
	for _, key := range []string{"items", "additionalProperties", "not", "if", "then", "else", "contains", "propertyNames"} {
		if sv, ok := m[key]; ok {
			// additionalProperties is often a bool (true/false); leave it alone.
			if _, isBool := sv.(bool); !isBool {
				m[key] = patchSubSchema(sv)
			}
		}
	}

	// Maps of named sub-schemas.
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if mm, ok := m[key].(map[string]any); ok {
			for k, sv := range mm {
				mm[k] = patchSubSchema(sv)
			}
		}
	}

	// Arrays of sub-schemas.
	for _, key := range []string{"allOf", "oneOf", "anyOf", "prefixItems"} {
		if arr, ok := m[key].([]any); ok {
			for i, sv := range arr {
				arr[i] = patchSubSchema(sv)
			}
		}
	}
}

// patchSubSchema returns a usable replacement for a sub-schema value: a null or
// empty `{}` schema, or any schema carrying no type-determining keyword (e.g. a
// description-only `{"description": "..."}`), becomes a free-form object so
// pulschema can derive a type. Otherwise it recurses and returns the value.
func patchSubSchema(sv any) any {
	if isEmptySchema(sv) {
		return freeFormObject()
	}
	if m, ok := sv.(map[string]any); ok && isTypelessSchema(m) {
		normalizeTypelessSchema(m)
		return m
	}
	patchEmptySchemas(sv)
	return sv
}

// sanitizeIdent collapses each run of disallowed characters into a single "_"
// and trims leading/trailing "_".
func sanitizeIdent(name string) string {
	return strings.Trim(invalidIdentChars.ReplaceAllString(name, "_"), "_")
}

// rewriteRefs walks the document and rewrites $ref string values and
// discriminator mapping targets that point at a renamed schema. Discriminator
// mapping values may be either a full ref path or a bare schema name.
func rewriteRefs(node any, renamedRefs map[string]string) {
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			if nw, found := renamedRefs[ref]; found {
				v["$ref"] = nw
			}
		}
		if disc, ok := v["discriminator"].(map[string]any); ok {
			if mapping, ok := disc["mapping"].(map[string]any); ok {
				for key, target := range mapping {
					ts, ok := target.(string)
					if !ok {
						continue
					}
					if nw, found := renamedRefs[ts]; found {
						mapping[key] = nw
					} else if nw, found := renamedRefs[schemaRefPrefix+ts]; found {
						mapping[key] = strings.TrimPrefix(nw, schemaRefPrefix)
					}
				}
			}
		}
		for _, child := range v {
			rewriteRefs(child, renamedRefs)
		}
	case []any:
		for _, child := range v {
			rewriteRefs(child, renamedRefs)
		}
	}
}
