// Package version exposes the provider's build version. The value is injected by
// the Go linker (-ldflags "-X .../version.Version=<v>") at build time (see the
// Makefile), so the binary reports its real release version while the committed
// default stays a placeholder.
package version

// Version is set by the Go linker (-X) at build time to the provider's semver.
var Version = "0.0.1"
