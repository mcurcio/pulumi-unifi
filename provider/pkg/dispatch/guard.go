package dispatch

import (
	"context"
	"fmt"
	"sync"

	"github.com/mcurcio/pulumi-unifi/provider/pkg/engine"
)

// versionGuard is the version guard of docs/design/private-binding.md §S2.6,
// shared by the private bindings (v2 and classic) the dispatcher gives the
// engine (wrap). Before the first private call it reads the controller's
// Network version and compares it with the version the surface was built for
// (the pin). A mismatch fails every private kind, with both versions in the
// message; official kinds never pass through here. A success or a mismatch is
// cached until the next Configure (which builds a new guard); a failure to
// read the version (the controller not up yet) is not, so the next call
// retries.
type versionGuard struct {
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

// wrap puts the guard in front of a private binding.
func (g *versionGuard) wrap(b engine.Binding) engine.Binding { return &guarded{Binding: b, g: g} }

// guarded is a private binding behind the guard; API and SiteID pass through.
type guarded struct {
	engine.Binding
	g *versionGuard
}

func (g *guarded) check(ctx context.Context) error { return g.g.check(ctx) }

func (g *guarded) List(ctx context.Context, k *engine.Kind) ([]map[string]any, error) {
	if err := g.check(ctx); err != nil {
		return nil, err
	}
	return g.Binding.List(ctx, k)
}

func (g *guarded) Get(ctx context.Context, k *engine.Kind, id string) (map[string]any, bool, error) {
	if err := g.check(ctx); err != nil {
		return nil, false, err
	}
	return g.Binding.Get(ctx, k, id)
}

func (g *guarded) Create(ctx context.Context, k *engine.Kind, body any) (any, error) {
	if err := g.check(ctx); err != nil {
		return nil, err
	}
	return g.Binding.Create(ctx, k, body)
}

func (g *guarded) Update(ctx context.Context, k *engine.Kind, id string, body map[string]any) (any, error) {
	if err := g.check(ctx); err != nil {
		return nil, err
	}
	return g.Binding.Update(ctx, k, id, body)
}

func (g *guarded) Delete(ctx context.Context, k *engine.Kind, id string) (bool, error) {
	if err := g.check(ctx); err != nil {
		return false, err
	}
	return g.Binding.Delete(ctx, k, id)
}
