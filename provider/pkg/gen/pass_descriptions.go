package gen

import (
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
)

// descriptionsPass (D-M2.4) backfills a one-line top-level Description on every
// resource and every function. pulschema leaves these empty, so the Pulumi
// Registry page and every IDE hover for all 21 resources + 50 data sources is
// blank or filler (01-H3 / 03-F6).
//
// Derive-by-default, pin-by-exception:
//
//   - Default: the OpenAPI operation summary for the token's primary verb — the
//     create (POST) operation on a resource's collection path, the read (GET)
//     operation on a function's read path. The pinned spec has a summary on every
//     such operation, so the default is always available (asserted: a token with
//     no resolvable summary AND no override is a loud error).
//   - Override: mappings.yaml `descriptions.{resources,functions}` keyed by the
//     final token short name, for the cases where the shared collection summary
//     cannot distinguish a discriminated variant ("Create DNS Policy" describes
//     all 7 DNS variants identically) or where the resource is a batch/action RPC
//     whose CRUD-looking shape needs an honest caveat (Voucher / AdoptDevice,
//     D-M3.3).
//
// Runs late in the pipeline — after the token-rename and de-page passes — so the
// override keys match the final, consumer-facing token names and every token is
// annotated exactly once on the shipped set.
//
// Deterministic: each token's description is a pure function of its own crudMap
// path + the spec summary (or its override), independent of map iteration order.
// Keys are iterated sorted regardless.
func descriptionsPass(s *GenState) error {
	crudMap := s.Meta.ResourceCRUDMap
	doc := s.Doc

	for _, tok := range sortedKeys(s.Pkg.Resources) {
		short := tokenShortName(tok)
		desc, override := resourceDescriptionOverride(short)
		if !override {
			// A resource's primary verb is create (POST on its collection path).
			summary := ""
			if m := crudMap[tok]; m != nil && m.C != nil {
				summary = operationSummary(doc, *m.C, "post")
			}
			if summary == "" {
				return fmt.Errorf("descriptions: resource %q has no create-operation summary and no override in mappings.yaml; add a descriptions.resources entry", tok)
			}
			desc = summary
		}
		r := s.Pkg.Resources[tok]
		r.Description = desc
		s.Pkg.Resources[tok] = r
	}

	for _, tok := range sortedKeys(s.Pkg.Functions) {
		short := tokenShortName(tok)
		desc, override := functionDescriptionOverride(short)
		if !override {
			// A function's verb is read (GET on its read path).
			summary := ""
			if m := crudMap[tok]; m != nil && m.R != nil {
				summary = operationSummary(doc, *m.R, "get")
			}
			if summary == "" {
				return fmt.Errorf("descriptions: function %q has no read-operation summary and no override in mappings.yaml; add a descriptions.functions entry", tok)
			}
			desc = summary
		}
		f := s.Pkg.Functions[tok]
		f.Description = desc
		s.Pkg.Functions[tok] = f
	}

	return nil
}

// operationSummary returns the OpenAPI operation summary for (path, verb), or ""
// if the path/operation/summary is absent. verb is lowercase ("post"/"get").
func operationSummary(doc *openapi3.T, path, verb string) string {
	item := doc.Paths.Find(path)
	if item == nil {
		return ""
	}
	var op *openapi3.Operation
	switch verb {
	case "post":
		op = item.Post
	case "get":
		op = item.Get
	case "put":
		op = item.Put
	case "patch":
		op = item.Patch
	case "delete":
		op = item.Delete
	}
	if op == nil {
		return ""
	}
	return op.Summary
}
