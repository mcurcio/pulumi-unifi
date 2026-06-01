# Build plan

Handoff for the development session. This repo is currently a **docs-only scaffold** — no Go code,
codegen, or SDK exists yet. Read [DESIGN.md](DESIGN.md) first for the architecture; this file is
the execution sequence.

The work happens **entirely in this repo** (`~/Code/pulumi-unifi`). Do not modify the separate
`iac` repo — consuming the SDK from `iac` is a later, separate effort.

## Prerequisites

- Go toolchain (provider plugin is a Go gRPC binary).
- Pulumi CLI (`pulumi version`) — for `pulumi package gen-sdk`.
- Python 3 + a venv tool for SDK smoke tests.
- A reachable UniFi controller on **UniFi OS** (cloud console or UDM Pro) with an `X-API-KEY`
  minted (Network app → Settings → Integrations). Legacy self-hosted controllers don't support API keys.

## Phase 1 — Bootstrap & vendor the spec

- `openapi/fetch.sh`: download a chosen `unifi-network/<version>.json` from a **pinned beezly
  commit SHA**; write the SHA + version into the file header / a `SOURCE` note for provenance.
- Vendor the spec into `openapi/unifi-network-<version>.json`.
- Scaffold the Go module under `provider/`:
  - `provider/cmd/pulumi-resource-unifi/` — the plugin binary (wraps `pulumi-provider-framework`).
  - `provider/cmd/pulumi-gen-unifi/` — codegen entrypoint (spec → pulschema → schema/metadata/SDKs).
- `Makefile` targets: `generate`, `build`, `sdk`, `test`, `publish`.

**Done when:** `make build` produces a `pulumi-resource-unifi` binary (even if it serves nothing yet).

## Phase 2 — Wire codegen (spec → schema → SDK)

- In `pulumi-gen-unifi`, run pulschema over the vendored spec → `schema.json` + `metadata.json`
  (commit both).
- Set `PluginDownloadURL` in the schema (templated `${VERSION}/${OS}/${ARCH}`).
- `make sdk` → `pulumi package gen-sdk --language python` → `sdk/python` (`pulumi_unifi`).

**Done when:** `make generate` is deterministic (re-run → no diff) and `sdk/python` imports cleanly.

## Phase 3 — Read path end-to-end

- Wire provider config (`apiUrl`, `apiKey` secret → `X-API-KEY`, `allowInsecure`, `siteId`) into the
  `pulumi-provider-framework` runtime.
- Expose at least one **data source** (e.g. list networks or list WiFi broadcasts).

**Done when:** a scratch Pulumi program (Python, temp dir — **not** in `iac`) configured with a real
`X-API-KEY` reads live data from the controller.

## Phase 4 — Resource grouping & writes

- Author `provider/pkg/resources/grouping.{go,yaml}`: map `operationId`s → resource tokens + verbs +
  ID extraction (see DESIGN §4).
- Promote entities with create/update/delete to full CRUD **resources** (today: WiFi broadcasts /
  SSIDs + any EA write endpoints in the pinned version). GET-only entities stay **data sources**.
- Validate polymorphic `oneOf`+`discriminator` mapping (WiFi broadcast standard vs IoT variants).

**Done when:** the scratch program round-trips create → update → delete on a **throwaway test SSID**
and a no-op `pulumi up` follows a successful apply (clean read/diff).

## Phase 5 — CI & release

- GitHub Actions: on `openapi/` version bump, regenerate schema + SDK and open a PR.
- Build plugin binaries per OS/arch; cut a release with the `PluginDownloadURL`-matching asset names.
- Publish the Python SDK (PyPI or tagged git ref).

**Done when:** a tagged release's `PluginDownloadURL` resolves and `pulumi up` auto-installs the
plugin on a clean machine/container.

## Verification checklist

- [ ] `make generate` deterministic — re-run produces no diff in `schema.json` / `metadata.json` / `sdk/`.
- [ ] `make build` produces the plugin binary; `make test` (Go) green.
- [ ] `pip install sdk/python` + `import pulumi_unifi` works; resource & data-source classes present and typed.
- [ ] Scratch program reads live controller data via `X-API-KEY`.
- [ ] Scratch program round-trips create → update → delete on a throwaway test SSID; second `up` is a no-op.
- [ ] Tagged-release `PluginDownloadURL` auto-installs the plugin on a clean environment.

## Notes / gotchas

- **Spec license:** beezly repo has no license — keep the spec as a pinned, attributed codegen input.
- **`oneOf`+`discriminator`** is the riskiest schema feature; verify pulschema emits correct Pulumi
  tagged unions before building on it.
- **Write coverage** grows through 2026; revisit/bump the pinned spec to pull in networks, firewall,
  DNS, port forwards, fixed-IP clients as their write endpoints ship.
- **`siteId`** — decide single-site default vs multi-site; expose a per-resource override.
