// The Tier-2 Pulumi CLI driver (docs/design/private-binding.md §S2.10, §S2.11):
// the official ACL rules, their ordering and the alias migration, driven
// through the pinned Pulumi CLI and the PRIVATE=0 SDK against the seeded live
// controller. Standard library only, kept out of the provider module so the
// default build and `make test` never see it.
module github.com/mcurcio/pulumi-unifi/test/e2e/cli

go 1.26
