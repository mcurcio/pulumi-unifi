package gen

import "fmt"

// secretTypeProperties names, per generated complex-type token, the properties
// that carry credentials and must be marked `secret` so Pulumi redacts them in
// state and CLI output. The spec does not flag these, so we set them in the gen
// layer. A named entry that no longer resolves is a loud error (see the pass) —
// the spec changed out from under the rule.
//
// HotspotVoucherDetails.code is a guest-access credential (the voucher's secret
// code). The WiFi securityConfiguration in this spec exposes no passphrase, so
// there is nothing to mark there.
var secretTypeProperties = map[string][]string{
	"unifi:sites/v1:HotspotVoucherDetails": {"code"},
}

// markSecretFieldsPass sets Secret=true on the credential properties named in
// secretTypeProperties. It is deterministic (operates on a fixed map and the
// schema's typed property maps, independent of iteration order). A named type or
// property that is absent is a codegen-time error so the rule cannot silently
// rot when the spec/tokens change.
func markSecretFieldsPass(s *GenState) error {
	for typeTok, props := range secretTypeProperties {
		ct, ok := s.Pkg.Types[typeTok]
		if !ok {
			return fmt.Errorf("secret-fields: type %q not found in schema (spec/token drift?)", typeTok)
		}
		for _, prop := range props {
			ps, ok := ct.Properties[prop]
			if !ok {
				return fmt.Errorf("secret-fields: property %q not found on type %q (spec/token drift?)", prop, typeTok)
			}
			ps.Secret = true
			ct.Properties[prop] = ps
		}
		s.Pkg.Types[typeTok] = ct
	}
	return nil
}
