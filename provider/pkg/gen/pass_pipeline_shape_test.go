package gen

import "testing"

// TestDiscriminatorInjectedOnRealPipeline is the combined integration assertion
// for the Track-D slice (discriminator-inject + token-rename): after the full
// pipeline, every discriminated resource — identified by its entity-prefixed
// token name — has its discriminator (type/management) pinned to a Const+Default
// and absent from requiredInputs, and the four flat resources carry no
// const-pinned discriminator. It is the consumer-surface proof behind the
// reference shapes in the Python SDK (e.g. DnsARecord's type fixed to A_RECORD).
//
// It asserts on the post-rename token names (so it depends on both passes) and
// checks Const==Default + not-required rather than re-deriving the value from the
// renamed token (the rename runs after injection, so the token no longer
// pascal-cases back to the discriminator value).
func TestDiscriminatorInjectedOnRealPipeline(t *testing.T) {
	pkg, _ := runPipelineTyped(t)

	// Expected discriminated resources (post-rename) → discriminator property.
	discProp := map[string]string{
		"unifi:sites/v1:DnsARecord":                "type",
		"unifi:sites/v1:DnsAaaaRecord":             "type",
		"unifi:sites/v1:DnsCnameRecord":            "type",
		"unifi:sites/v1:DnsMxRecord":               "type",
		"unifi:sites/v1:DnsSrvRecord":              "type",
		"unifi:sites/v1:DnsTxtRecord":              "type",
		"unifi:sites/v1:DnsForwardDomain":          "type",
		"unifi:sites/v1:WifiBroadcastStandard":     "type",
		"unifi:sites/v1:WifiBroadcastIotOptimized": "type",
		"unifi:sites/v1:ManagedNetworkGateway":     "management",
		"unifi:sites/v1:ManagedNetworkSwitch":      "management",
		"unifi:sites/v1:ManagedNetworkUnmanaged":   "management",
		"unifi:sites/v1:TrafficMatchIpv4":          "type",
		"unifi:sites/v1:TrafficMatchMac":           "type",
		"unifi:sites/v1:TrafficMatchIpv4Addresses": "type",
		"unifi:sites/v1:TrafficMatchIpv6Addresses": "type",
		"unifi:sites/v1:TrafficMatchPorts":         "type",
	}
	flat := map[string]bool{
		"unifi:sites/v1:FirewallZone":   true,
		"unifi:sites/v1:FirewallPolicy": true,
		"unifi:sites/v1:Voucher":        true,
		"unifi:sites/v1:AdoptDevice":    true,
	}

	for tok, prop := range discProp {
		res, ok := pkg.Resources[tok]
		if !ok {
			t.Errorf("expected discriminated resource %q to exist (spec/rename drift?)", tok)
			continue
		}
		ps, present := res.InputProperties[prop]
		if !present {
			t.Errorf("%s: discriminator property %q missing after pipeline", tok, prop)
			continue
		}
		if ps.Const == nil || ps.Const == "" {
			t.Errorf("%s: %s.Const not pinned after injection (got %v)", tok, prop, ps.Const)
		}
		if ps.Const != ps.Default {
			t.Errorf("%s: %s.Const (%v) != Default (%v); both must be the injected value", tok, prop, ps.Const, ps.Default)
		}
		for _, r := range res.RequiredInputs {
			if r == prop {
				t.Errorf("%s: %q still in requiredInputs after injection", tok, prop)
			}
		}
	}

	// Flat resources must carry no const-pinned discriminator on type/management.
	for tok := range flat {
		res, ok := pkg.Resources[tok]
		if !ok {
			continue
		}
		for _, prop := range []string{"type", "management"} {
			if ps, present := res.InputProperties[prop]; present && ps.Const != nil {
				t.Errorf("flat resource %s unexpectedly has a const-pinned %q", tok, prop)
			}
		}
	}
}
