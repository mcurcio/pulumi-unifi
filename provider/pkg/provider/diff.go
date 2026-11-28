package provider

import (
	"context"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	openapigen "github.com/cloudy-sky-software/pulschema/pkg"
)

// OnDiff classifies the changes of a discriminated variant against its own
// update schema (docs/design/private-binding.md §S2.14).
//
// The framework decides "update in place" vs "replace" by looking each changed
// property up in the update operation's request-body schema, and replaces when
// the property is not there. For a oneOf+discriminator body it looks only at the
// shared base's properties, so every variant-only field (AclRuleIpv4's
// protocolFilter, AclRuleMac's networkIdFilter, DnsARecord's ipv4Address, ...)
// counted as "not updatable": changing or dropping one replaced the object,
// although the PUT accepts it. Measured live on AclRuleIpv4 (slice 2 step 1).
//
// This hook resolves the variant the new inputs name (the discriminator value)
// and classifies against the base's properties plus the variant's own, allOf
// members included. A body without a discriminator, or a variant it cannot
// resolve, falls through to the framework (nil response).
func (p *unifiProvider) OnDiff(_ context.Context, req *pulumirpc.DiffRequest, _ string, diff *resource.ObjectDiff, jsonReq *openapi3.MediaType) (*pulumirpc.DiffResponse, error) {
	if diff == nil || jsonReq == nil || jsonReq.Schema == nil || jsonReq.Schema.Value == nil {
		return nil, nil
	}
	news, err := plugin.UnmarshalProperties(req.GetNews(), plugin.MarshalOptions{KeepUnknowns: true, KeepSecrets: true})
	if err != nil {
		return nil, err
	}
	props := variantProperties(p.doc, jsonReq.Schema.Value, news)
	if props == nil {
		return nil, nil
	}
	var replaces, diffs []string
	classify := func(key resource.PropertyKey) {
		k := string(key)
		api := k
		if v, ok := p.metadata.SDKToAPINameMap[k]; ok {
			api = v
		}
		if props[api] {
			diffs = append(diffs, k)
		} else {
			replaces = append(replaces, k)
		}
	}
	for k := range diff.Adds {
		classify(k)
	}
	for k := range diff.Updates {
		classify(k)
	}
	for k := range diff.Deletes {
		classify(k)
	}
	sort.Strings(replaces)
	sort.Strings(diffs)
	return &pulumirpc.DiffResponse{Changes: pulumirpc.DiffResponse_DIFF_SOME, Replaces: replaces,
		Diffs: append(append([]string(nil), diffs...), replaces...)}, nil
}

// variantProperties returns the API property names a discriminated update body
// accepts for the variant the inputs name: the base's properties, the variant
// schema's, and those of its allOf members. nil when the body has no
// discriminator or the variant does not resolve.
func variantProperties(doc *openapi3.T, body *openapi3.Schema, news resource.PropertyMap) map[string]bool {
	d := body.Discriminator
	if d == nil || d.PropertyName == "" || len(d.Mapping) == 0 {
		return nil
	}
	v, ok := news[resource.PropertyKey(openapigen.ToSdkName(d.PropertyName))]
	if !ok || !v.IsString() {
		return nil
	}
	ref, ok := d.Mapping[v.StringValue()]
	if !ok {
		return nil
	}
	// The mapping names a component; a body that also lists the variants
	// under oneOf carries the same schema there.
	variant := ref.Value
	for _, m := range body.OneOf {
		if variant == nil && m != nil && m.Ref == ref.Ref {
			variant = m.Value
		}
	}
	if name := strings.TrimPrefix(ref.Ref, "#/components/schemas/"); variant == nil && doc != nil && doc.Components != nil {
		if sr := doc.Components.Schemas[name]; sr != nil {
			variant = sr.Value
		}
	}
	if variant == nil {
		return nil
	}
	out := map[string]bool{}
	var add func(s *openapi3.Schema, depth int)
	add = func(s *openapi3.Schema, depth int) {
		if s == nil || depth > 8 {
			return
		}
		for name := range s.Properties {
			out[name] = true
		}
		for _, m := range s.AllOf {
			if m != nil {
				add(m.Value, depth+1)
			}
		}
	}
	add(body, 0)
	add(variant, 0)
	return out
}
