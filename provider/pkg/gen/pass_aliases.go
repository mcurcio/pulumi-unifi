package gen

import (
	"fmt"
	"sort"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

// aliasesPass writes mappings.yaml `aliases:` into the golden as
// ResourceSpec.aliases (docs/design/private-binding.md §S2.3, §S2.4). A renamed
// resource keeps its former token as a type alias, so `gen-sdk` emits
// `Alias(type_=<old>)` into the generated resource options and the Pulumi engine
// moves existing state to the new token in place: no create, no delete. The
// plugin only ever sees the new token.
//
// Loud errors, so the table cannot rot across a spec bump: the key is not a live
// resource; an alias is itself a live resource or function token (two classes
// for one URN); two tokens share an alias; an alias repeats. Runs after the token
// rename, so keys are final token names. Deterministic: sorted keys, sorted
// aliases.
func aliasesPass(s *GenState) error {
	return applyAliases(s, aliasPins())
}

func applyAliases(s *GenState, pins map[string][]string) error {
	owner := map[string]string{}
	for _, tok := range sortedKeys(pins) {
		res, ok := s.Pkg.Resources[tok]
		if !ok {
			return fmt.Errorf("aliases: %q is not a live resource token (dead entry)", tok)
		}
		olds := append([]string(nil), pins[tok]...)
		sort.Strings(olds)
		res.Aliases = nil
		for _, old := range olds {
			if old == tok {
				return fmt.Errorf("aliases: %q aliases itself", tok)
			}
			if _, live := s.Pkg.Resources[old]; live {
				return fmt.Errorf("aliases: alias %q of %q is a live resource token", old, tok)
			}
			if _, live := s.Pkg.Functions[old]; live {
				return fmt.Errorf("aliases: alias %q of %q is a live function token", old, tok)
			}
			if prev, dup := owner[old]; dup {
				return fmt.Errorf("aliases: %q is an alias of both %q and %q", old, prev, tok)
			}
			owner[old] = tok
			res.Aliases = append(res.Aliases, pschema.AliasSpec{Type: old})
		}
		s.Pkg.Resources[tok] = res
	}
	return nil
}
