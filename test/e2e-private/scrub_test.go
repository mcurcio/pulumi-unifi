//go:build e2e_private

package e2e

import "testing"

func TestScrubKey(t *testing.T) {
	if got := scrubKey("a k3y b k3y", "k3y"); got != "a [redacted] b [redacted]" {
		t.Errorf("scrubKey = %q", got)
	}
	if got := scrubKey("a b", ""); got != "a b" {
		t.Errorf("empty key changed the text: %q", got)
	}
}
