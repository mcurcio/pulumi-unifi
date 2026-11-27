package uos

// Secret marking. It fails closed: a field is secret when ANY signal says so,
// and only an override (kinds.yaml notSecret:) can clear it.
//   - an "x_" name prefix (the controller's own convention for secrets);
//   - an entry in sensitive_metadata.json (the support-dump redaction list);
//   - WRITE_ONLY access in the bytecode;
//   - a name matching (?i)(password|passphrase|secret|psk|token|private_?key).

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/kind"
)

var reSecretName = regexp.MustCompile(`(?i)(password|passphrase|secret|psk|token|private_?key)`)

// Sensitive maps a Mongo collection name to its redacted field names.
type Sensitive map[string]map[string]bool

// ParseSensitive reads sensitive_metadata.json.
func ParseSensitive(b []byte) (Sensitive, error) {
	var doc struct {
		ByCollection map[string][]string `json:"sensitive_db_fields_by_collection"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", SensitiveMetaPath, err)
	}
	s := Sensitive{}
	for c, fs := range doc.ByCollection {
		s[c] = map[string]bool{}
		for _, f := range fs {
			s[c][f] = true
		}
	}
	return s, nil
}

// SecretName reports whether a wire name alone marks a field secret.
func SecretName(name string) bool {
	return strings.HasPrefix(name, "x_") || reSecretName.MatchString(name)
}

// mongoCollection is the classic kind's Mongo collection (wlanconf, setting).
func mongoCollection(k *kind.Kind) string {
	if k.API != kind.APIClassic {
		return ""
	}
	if strings.HasPrefix(k.Name, "Setting.") {
		return "setting"
	}
	return strings.ToLower(k.Name)
}

// MarkSecrets sets Secret on every field of the kind that a signal marks.
func MarkSecrets(k *kind.Kind, s Sensitive) {
	redacted := s[mongoCollection(k)]
	k.Walk(func(_ string, f *kind.Field) {
		markSecret(f, f.WireName, redacted)
	})
}

func markSecret(f *kind.Field, name string, redacted map[string]bool) {
	if SecretName(name) || redacted[name] || f.WriteOnly {
		f.Secret = true
	}
	// List items and map values inherit their field's name.
	if f.Items != nil {
		markSecret(f.Items, name, redacted)
	}
	if f.Values != nil {
		markSecret(f.Values, name, redacted)
	}
}
