package gen

import (
	"fmt"
	"sort"
	"strings"
)

// outputOnlyPass drops each mappings.yaml `outputOnly:` input (`<token>.<property>`)
// from the resource's inputs and requiredInputs while keeping the output
// (docs/design/private-binding.md §S2.3). It exists for spec inputs that have no
// effect and are owned elsewhere: the ACL rule `index` is deprecated, ignored by
// the server (probe a3) and renumbered by the ordering PUT (probe a5), so
// declaring it could only produce drift; AclRuleOrdering owns the order.
//
// A dead entry (no live resource, or no such input) is a loud error, and the
// output must exist (an input with no output would vanish from the state).
func outputOnlyPass(s *GenState) error {
	return applyOutputOnly(s, outputOnlyPins())
}

func applyOutputOnly(s *GenState, pins []string) error {
	sorted := append([]string(nil), pins...)
	sort.Strings(sorted)
	for _, pin := range sorted {
		i := strings.LastIndex(pin, ".")
		if i <= 0 || i == len(pin)-1 {
			return fmt.Errorf("outputOnly: %q is not <token>.<property>", pin)
		}
		tok, prop := pin[:i], pin[i+1:]
		res, ok := s.Pkg.Resources[tok]
		if !ok {
			return fmt.Errorf("outputOnly: %q: %s is not a live resource (dead entry)", pin, tok)
		}
		if _, ok := res.InputProperties[prop]; !ok {
			return fmt.Errorf("outputOnly: %q: %s has no input %s (dead entry)", pin, tok, prop)
		}
		if _, ok := res.Properties[prop]; !ok {
			return fmt.Errorf("outputOnly: %q: %s has no output %s to keep", pin, tok, prop)
		}
		delete(res.InputProperties, prop)
		res.RequiredInputs = removeString(res.RequiredInputs, prop)
		s.Pkg.Resources[tok] = res
	}
	return nil
}
