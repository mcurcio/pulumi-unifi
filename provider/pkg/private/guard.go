package private

import (
	"context"
	"fmt"
	"sync"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/private/engine"
)

// versionGuard is the engine.Binding the dispatcher gives the private engine:
// the private binding, behind the version guard of
// docs/design/private-binding.md §S2.6. Before the first private call it reads
// the controller's Network version and compares it with the version the
// surface was built for (the pin). A mismatch fails every private kind, with
// both versions in the message; official kinds never pass through here. A
// success or a mismatch is cached for the life of the provider; a failure to
// read the version (the controller not up yet) is not, so the next call
// retries.
type versionGuard struct {
	engine.Binding
	// want is the pin's Network version; empty disables the guard (a surface
	// built without a model serves no private kinds).
	want string
	// read returns the controller's Network version.
	read func(context.Context) (string, error)

	mu     sync.Mutex
	done   bool
	result error
}

func (g *versionGuard) check(ctx context.Context) error {
	if g.want == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return g.result
	}
	got, err := g.read(ctx)
	if err != nil {
		return fmt.Errorf("reading the controller's UniFi Network version for the private kinds: %w", err)
	}
	g.done = true
	if !sameVersion(got, g.want) {
		g.result = fmt.Errorf("the private kinds (unifi:network:*) are built for UniFi Network %s, but the controller runs %s; "+
			"they are served for that one version only (rebuild with PRIVATE=1 from the matching UniFi OS Server pin); "+
			"the official resources are not affected", g.want, got)
	}
	return g.result
}

// sameVersion: the controller reports the bare version ("10.4.57"); a build
// suffix after a non-digit separator is accepted.
func sameVersion(got, want string) bool {
	if got == want {
		return true
	}
	if len(got) > len(want) && got[:len(want)] == want {
		c := got[len(want)]
		return c < '0' || c > '9'
	}
	return false
}

func (g *versionGuard) List(ctx context.Context, k *engine.Kind) ([]map[string]any, error) {
	if err := g.check(ctx); err != nil {
		return nil, err
	}
	return g.Binding.List(ctx, k)
}

func (g *versionGuard) Get(ctx context.Context, k *engine.Kind, id string) (map[string]any, bool, error) {
	if err := g.check(ctx); err != nil {
		return nil, false, err
	}
	return g.Binding.Get(ctx, k, id)
}

func (g *versionGuard) Create(ctx context.Context, k *engine.Kind, body any) (any, error) {
	if err := g.check(ctx); err != nil {
		return nil, err
	}
	return g.Binding.Create(ctx, k, body)
}

func (g *versionGuard) Update(ctx context.Context, k *engine.Kind, id string, body map[string]any) (any, error) {
	if err := g.check(ctx); err != nil {
		return nil, err
	}
	return g.Binding.Update(ctx, k, id, body)
}

func (g *versionGuard) Delete(ctx context.Context, k *engine.Kind, id string) (bool, error) {
	if err := g.check(ctx); err != nil {
		return false, err
	}
	return g.Binding.Delete(ctx, k, id)
}
