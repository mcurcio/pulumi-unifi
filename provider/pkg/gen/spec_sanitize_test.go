package gen

import (
	"encoding/json"
	"testing"
)

// sanitizeMap is a convenience that runs SanitizeSpecBytes over a JSON document
// expressed as a Go map and returns the result re-decoded.
func sanitizeMap(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	in, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	out, err := SanitizeSpecBytes(in)
	if err != nil {
		t.Fatalf("SanitizeSpecBytes: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return got
}

func schemasOf(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	comps, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatalf("no components in output")
	}
	schemas, ok := comps["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("no schemas in output")
	}
	return schemas
}

// TestSanitizeKeysAndRefs verifies invalid component keys are normalized and all
// $ref pointers to them are rewritten in lockstep.
func TestSanitizeKeysAndRefs(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"ACL rule": map[string]any{"type": "object"},
				"Wrapper": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"rule": map[string]any{"$ref": "#/components/schemas/ACL rule"},
					},
				},
			},
		},
	}
	got := sanitizeMap(t, doc)
	schemas := schemasOf(t, got)

	if _, ok := schemas["ACL_rule"]; !ok {
		t.Errorf("expected sanitized key ACL_rule, keys present: %v", keys(schemas))
	}
	wrapper := schemas["Wrapper"].(map[string]any)
	props := wrapper["properties"].(map[string]any)
	rule := props["rule"].(map[string]any)
	if ref := rule["$ref"]; ref != "#/components/schemas/ACL_rule" {
		t.Errorf("ref not rewritten: got %v", ref)
	}
}

// TestSanitizeKeyCollisionIsError verifies two keys collapsing to the same
// identifier is a hard error rather than a silent overwrite.
func TestSanitizeKeyCollisionIsError(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"A B": map[string]any{"type": "object"},
				"A/B": map[string]any{"type": "object"},
			},
		},
	}
	in, _ := json.Marshal(doc)
	if _, err := SanitizeSpecBytes(in); err == nil {
		t.Fatalf("expected collision error, got nil")
	}
}

// TestEmptyLicenseDropped verifies the empty info.license object (which fails
// validation) is removed.
func TestEmptyLicenseDropped(t *testing.T) {
	doc := map[string]any{
		"info": map[string]any{
			"title":   "t",
			"license": map[string]any{},
		},
		"components": map[string]any{"schemas": map[string]any{}},
	}
	got := sanitizeMap(t, doc)
	info := got["info"].(map[string]any)
	if _, ok := info["license"]; ok {
		t.Errorf("expected empty license to be dropped, still present: %v", info["license"])
	}
}

// TestTypelessTopLevelNormalized verifies a referenced component with no
// type-determining keyword becomes a free-form object (preserving description),
// which keeps pulschema from panicking on an empty type slice.
func TestTypelessTopLevelNormalized(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"FilterExpression": map[string]any{"description": "a filter"},
			},
		},
	}
	got := sanitizeMap(t, doc)
	schemas := schemasOf(t, got)
	fe := schemas["FilterExpression"].(map[string]any)

	if fe["type"] != "object" {
		t.Errorf("expected type=object, got %v", fe["type"])
	}
	if fe["additionalProperties"] != true {
		t.Errorf("expected additionalProperties=true, got %v", fe["additionalProperties"])
	}
	if fe["description"] != "a filter" {
		t.Errorf("expected description preserved, got %v", fe["description"])
	}
}

// TestTypelessInlinePropertyNormalized verifies a description-only inline
// property is normalized to a free-form object.
func TestTypelessInlinePropertyNormalized(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"AclRule": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"destinationFilter": map[string]any{"description": "Traffic destination filter"},
					},
				},
			},
		},
	}
	got := sanitizeMap(t, doc)
	schemas := schemasOf(t, got)
	rule := schemas["AclRule"].(map[string]any)
	props := rule["properties"].(map[string]any)
	df := props["destinationFilter"].(map[string]any)

	if df["type"] != "object" || df["additionalProperties"] != true {
		t.Errorf("expected typeless inline property normalized to free-form object, got %v", df)
	}
}

// TestNullItemsBecomeFreeForm verifies a null items schema (which would cause a
// nil dereference in pulschema) becomes a free-form object.
func TestNullItemsBecomeFreeForm(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"ArrayHolder": map[string]any{
					"type":  "array",
					"items": nil,
				},
			},
		},
	}
	got := sanitizeMap(t, doc)
	schemas := schemasOf(t, got)
	holder := schemas["ArrayHolder"].(map[string]any)
	items, ok := holder["items"].(map[string]any)
	if !ok {
		t.Fatalf("expected items to be an object, got %T", holder["items"])
	}
	if items["type"] != "object" || items["additionalProperties"] != true {
		t.Errorf("expected null items normalized to free-form object, got %v", items)
	}
}

// TestNonStringEnumDropped verifies enum constraints on non-string schemas are
// removed (pulschema only supports string enums) while the type is kept.
func TestNonStringEnumDropped(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"Holder": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"freq": map[string]any{
							"type": "number",
							"enum": []any{2.4, 5.0},
						},
					},
				},
			},
		},
	}
	got := sanitizeMap(t, doc)
	schemas := schemasOf(t, got)
	holder := schemas["Holder"].(map[string]any)
	props := holder["properties"].(map[string]any)
	freq := props["freq"].(map[string]any)

	if _, ok := freq["enum"]; ok {
		t.Errorf("expected numeric enum dropped, still present: %v", freq["enum"])
	}
	if freq["type"] != "number" {
		t.Errorf("expected type preserved as number, got %v", freq["type"])
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
