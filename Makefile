PROJECT_NAME := pulumi-unifi
PACK         := unifi
PROVIDER     := pulumi-resource-$(PACK)
CODEGEN      := pulumi-gen-$(PACK)

# Static version (no pulumictl dependency). Override on the CLI: make VERSION=1.2.3
VERSION      ?= 0.0.1

PROVIDER_PKG := github.com/mcurcio/pulumi-unifi/provider
VERSION_PATH := $(PROVIDER_PKG)/pkg/version.Version

WORKING_DIR  := $(shell pwd)

# Single source of truth for the pinned spec version (openapi/pin.env, also
# sourced by openapi/fetch.sh). The spec filename derives from it, so a version
# bump touches only pin.env.
include openapi/pin.env
SPEC         := openapi/unifi-network-$(SPEC_VERSION).json
GEN_DIR      := provider/cmd/pulumi-resource-$(PACK)
SCHEMA_FILE  := $(GEN_DIR)/schema.json
BIN          := $(WORKING_DIR)/bin

LDFLAGS      := -ldflags "-X $(VERSION_PATH)=$(VERSION)"

.PHONY: ensure fetch gen generate_schema python_sdk generate build install test test-mock clean

MOCK_COMPOSE := test/mock/docker-compose.yml

# Resolve and tidy Go module dependencies.
ensure::
	cd provider && go mod tidy

# Fetch the pinned OpenAPI spec (build artifact — never committed). The file rule
# fetches only when the spec is absent; `make fetch` forces a re-verified refetch.
$(SPEC):
	./openapi/fetch.sh

fetch::
	./openapi/fetch.sh

# Build the code generator binary.
gen::
	cd provider && go build $(LDFLAGS) -o $(BIN)/$(CODEGEN) ./cmd/pulumi-gen-$(PACK)

# Run the code generator: spec -> schema.json + metadata.json + openapi_generated.yml.
generate_schema:: gen $(SPEC)
	$(BIN)/$(CODEGEN) -spec=$(SPEC) -out=$(GEN_DIR) -version=$(VERSION) schema

# Generate the Python SDK from the freshly generated schema. gen-sdk appends a
# language-named subdirectory, so --out=sdk yields sdk/python.
python_sdk:: generate_schema
	rm -rf sdk/python
	pulumi package gen-sdk --language python --out sdk $(SCHEMA_FILE)

# Deterministic umbrella: fetch + regenerate every build artifact from the pinned spec.
generate:: python_sdk

# Build the provider plugin binary. Depends on generate_schema so the //go:embed
# inputs (schema.json/metadata.json/openapi_generated.yml) exist at compile time.
build:: generate_schema
	cd provider && go build $(LDFLAGS) -o $(BIN)/$(PROVIDER) ./cmd/pulumi-resource-$(PACK)

# Install the plugin into the local Pulumi plugin cache for end-to-end testing.
install:: build
	pulumi plugin install resource $(PACK) $(VERSION) --file $(BIN)/$(PROVIDER)

# Unit gate: no Docker. Fetches the pinned spec first (codegen determinism tests
# read it), then runs the Go tests.
test:: $(SPEC)
	cd provider && go test -v -count=1 ./...

# Tier-1 read + write path integration tests against the Prism TLS mock (needs
# Docker). Regenerates certs, brings the mock up, runs the integration-tagged
# read- and write-dispatch tests, tears down regardless of outcome. The tests
# trust the mock's self-signed cert via allowInsecure=true (Caddy still serves
# real TLS); the CA-pinned secure path is a Tier-2 (test/e2e/) concern.
test-mock:: build
	./test/mock/gen-certs.sh
	docker compose -f $(MOCK_COMPOSE) up -d --wait
	UNIFI_MOCK_ADDR=127.0.0.1:8443 \
		go -C provider test -tags integration -count=1 -run 'TestReadPathAgainstMock|TestWritePathAgainstMock' ./pkg/provider/; \
		status=$$?; \
		docker compose -f $(MOCK_COMPOSE) down; \
		exit $$status

clean::
	rm -rf $(BIN)
