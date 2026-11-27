// The private-API e2e tier (docs/design/private-binding.md §11): the Go
// driver (e2e_test.go, build tag e2e_private) and the static TCP forwarder
// (fwd/). Standard library only, kept out of the provider module so the
// default build and `make test` never see it.
module github.com/mcurcio/pulumi-unifi/test/e2e-private

go 1.26
