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

.PHONY: ensure fetch gen generate_schema python_sdk generate build install test test-mock test-sdk e2e-bootstrap test-e2e clean

MOCK_COMPOSE := test/mock/docker-compose.yml
# The Tier-2 live-e2e pipeline — project name, compose file, seed, volume, ports,
# readiness gate, teardown — is owned end-to-end by its single executable
# entrypoint, test/e2e/run.sh (called by the test-e2e target below).

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
#
# Bring-up + run + teardown are one shell block with `trap … EXIT`, so the stack
# is torn down even when `up --wait` itself fails (F-M5.4) — previously the down
# was welded to the test command's exit and a bring-up failure left it up.
test-mock:: build
	./test/mock/gen-certs.sh
	set -e; \
	trap 'docker compose -f $(MOCK_COMPOSE) down' EXIT; \
	docker compose -f $(MOCK_COMPOSE) up -d --wait; \
	echo "waiting for the mock to serve through TLS (Prism boot lags container state)..."; \
	ready=; \
	for i in $$(seq 1 60); do \
		code=$$(curl -sk -o /dev/null -w '%{http_code}' --max-time 2 https://127.0.0.1:8443/proxy/network/integration/v1/sites/default/wifi/broadcasts || echo 000); \
		case "$$code" in 000|502|503|504) sleep 0.5 ;; *) echo "mock ready (HTTP $$code, attempt $$i)"; ready=1; break ;; esac; \
	done; \
	[ -n "$$ready" ] || { echo "ERROR: mock not ready on https://127.0.0.1:8443 (last HTTP $$code)"; exit 1; }; \
	UNIFI_MOCK_ADDR=127.0.0.1:8443 \
		go -C provider test -tags integration -count=1 -run 'TestReadPathAgainstMock|TestWritePathAgainstMock' ./pkg/provider/

# Python SDK smoke test: regenerate the SDK, install it (+ pulumi + pytest) into
# a throwaway venv, and assert pulumi_unifi imports with its stable class set
# (incl. a discriminated variant). The test source lives in test/sdk/ (outside
# the gitignored sdk/ tree that python_sdk regenerates).
test-sdk:: python_sdk
	python3 -m venv $(WORKING_DIR)/.sdkvenv
	$(WORKING_DIR)/.sdkvenv/bin/pip install -q --upgrade pip
	$(WORKING_DIR)/.sdkvenv/bin/pip install -q pytest pulumi ./sdk/python
	$(WORKING_DIR)/.sdkvenv/bin/python -m pytest test/sdk/test_smoke.py -q

# Tier-2 ONE-TIME seed bake. Boots a blank heavy UniFi OS Server stack (the only
# image that authenticates the Integration API), pauses for the maintainer to
# complete the first-run wizard + mint an X-API-Key by hand, then graceful-stops
# and snapshots the whole uos_data volume (Mongo + Postgres-with-the-key-secret +
# configs) to test/e2e/unifi-seed.tgz (git-lfs) and the key/version to
# test/e2e/seed.env. Run ONCE (and again on a controller version bump). See
# test/e2e/README.md and test/e2e/mint/README.md.
e2e-bootstrap::
	./test/e2e/bootstrap.sh

# Tier-2 REPEATABLE live e2e (OFFLINE / local — needs systemd+caps, NOT stock CI).
# The whole pipeline lives in ONE executable entrypoint, test/e2e/run.sh (restore
# seed → boot heavy UniFi OS → wait for authenticated JSON → run the e2e tests →
# always tear down). This target just ensures the provider is built, then runs it;
# `test/e2e/run.sh` can also be invoked directly (it self-builds if needed). Needs
# Docker + git-lfs (the seed is an LFS object); see test/e2e/README.md.
test-e2e:: build
	./test/e2e/run.sh

clean::
	rm -rf $(BIN) $(WORKING_DIR)/.sdkvenv
