package private

import (
	"context"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	core "github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
)

// The official singletons (AclRuleOrdering) are served by the engine through
// the official binding, never by the framework (docs/design/private-binding.md
// §S2.5). These handlers translate gRPC to the engine's lifecycle and back.

func (d *Dispatcher) checkSingleton(sg *core.Singleton, req *pulumirpc.CheckRequest) (*pulumirpc.CheckResponse, error) {
	news, err := unmarshal(req.GetNews())
	if err != nil {
		return nil, err
	}
	if news == nil {
		news = resource.PropertyMap{}
	}
	// Fill the schema default, as the framework does for its resources.
	if v, ok := news[core.PropUnlisted]; !ok || v.IsNull() {
		news[core.PropUnlisted] = resource.NewStringProperty(core.UnlistedLast)
	}
	inputs, err := marshal(news)
	if err != nil {
		return nil, err
	}
	resp := &pulumirpc.CheckResponse{Inputs: inputs}
	for _, f := range d.ordering.Check(sg, news) {
		resp.Failures = append(resp.Failures, &pulumirpc.CheckFailure{Property: f.Property, Reason: f.Reason})
	}
	return resp, nil
}

func (d *Dispatcher) diffSingleton(sg *core.Singleton, req *pulumirpc.DiffRequest) (*pulumirpc.DiffResponse, error) {
	oldS := req.GetOldInputs()
	if oldS == nil {
		oldS = req.GetOlds()
	}
	olds, err := unmarshal(oldS)
	if err != nil {
		return nil, err
	}
	news, err := unmarshal(req.GetNews())
	if err != nil {
		return nil, err
	}
	r := d.ordering.DiffInputs(sg, olds, news)
	resp := &pulumirpc.DiffResponse{Changes: pulumirpc.DiffResponse_DIFF_NONE, HasDetailedDiff: true,
		DetailedDiff: map[string]*pulumirpc.PropertyDiff{}, Diffs: r.Changed, Replaces: r.Replaces}
	if len(r.Changed) > 0 {
		resp.Changes = pulumirpc.DiffResponse_DIFF_SOME
	}
	replace := map[string]bool{}
	for _, p := range r.Replaces {
		replace[p] = true
	}
	for _, p := range r.Changed {
		kind := pulumirpc.PropertyDiff_UPDATE
		if replace[p] {
			kind = pulumirpc.PropertyDiff_UPDATE_REPLACE
		}
		resp.DetailedDiff[p] = &pulumirpc.PropertyDiff{Kind: kind, InputDiff: true}
	}
	return resp, nil
}

func (d *Dispatcher) createSingleton(ctx context.Context, sg *core.Singleton, req *pulumirpc.CreateRequest) (*pulumirpc.CreateResponse, error) {
	if req.GetPreview() {
		return &pulumirpc.CreateResponse{Properties: req.GetProperties()}, nil
	}
	in, err := unmarshal(req.GetProperties())
	if err != nil {
		return nil, err
	}
	c, err := d.ordering.Create(ctx, sg, in)
	if err != nil {
		return nil, err
	}
	props, err := marshal(c.Outputs)
	if err != nil {
		return nil, err
	}
	return &pulumirpc.CreateResponse{Id: c.ID, Properties: props}, nil
}

func (d *Dispatcher) readSingleton(ctx context.Context, sg *core.Singleton, req *pulumirpc.ReadRequest) (*pulumirpc.ReadResponse, error) {
	in, err := unmarshal(req.GetInputs())
	if err != nil {
		return nil, err
	}
	importing := len(req.GetInputs().GetFields()) == 0 && len(req.GetProperties().GetFields()) == 0
	r, err := d.ordering.ReadOrder(ctx, sg, req.GetId(), in, importing)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return &pulumirpc.ReadResponse{}, nil // gone
	}
	props, err := marshal(r.Outputs)
	if err != nil {
		return nil, err
	}
	inputs, err := marshal(r.Inputs)
	if err != nil {
		return nil, err
	}
	return &pulumirpc.ReadResponse{Id: r.ID, Properties: props, Inputs: inputs}, nil
}

func (d *Dispatcher) updateSingleton(ctx context.Context, sg *core.Singleton, req *pulumirpc.UpdateRequest) (*pulumirpc.UpdateResponse, error) {
	if req.GetPreview() {
		return &pulumirpc.UpdateResponse{Properties: req.GetNews()}, nil
	}
	news, err := unmarshal(req.GetNews())
	if err != nil {
		return nil, err
	}
	c, err := d.ordering.Update(ctx, sg, req.GetId(), news)
	if err != nil {
		return nil, err
	}
	props, err := marshal(c.Outputs)
	if err != nil {
		return nil, err
	}
	return &pulumirpc.UpdateResponse{Properties: props}, nil
}
