# Tier-2 e2e — real UniFi controller

End-to-end verification of the read path (and, later, write round-trips) against a
**real UniFi controller** serving the official Integration API. This is the CI
gate that proves the provider against the actual API, not a spec mock.

## Why UniFi OS Server (not the network-application image)

The official Integration API (`/proxy/network/integration/v1/...`, `X-API-KEY`)
is served **only by UniFi OS Server**. The common `linuxserver/unifi-network-application`
image runs the Network *application* but exposes neither the `/proxy/network/integration`
surface nor an API-key minting flow. So e2e requires a UniFi OS Server container.

## Status: provisioning is the open infra task

`docker-compose.yml` here is a **scaffold**. Before it can run, two things must be
solved (both flagged in the compose file):

1. **Pin an image.** Select a UniFi OS Server distributable at controller version
   **10.4.x** (matching the vendored spec) and set it as the `unifi` service image.
   This is an operational/licensing decision — do not guess a public tag.
2. **Provision headlessly.** The container has no headless first-run setup and no
   documented key-minting API. Choose one:
   - **Scripted UI automation** (Playwright): complete the first-run wizard, create
     a local admin, then Network app → Settings → Integrations → mint an API key.
     Capture the key for the test.
   - **Pre-baked volume snapshot**: provision once by hand, snapshot the `unifi-data`
     volume, and restore it at container start so the key is already present.

## TLS

The controller serves self-signed TLS. Two trust paths exist:

- **`allowInsecure=true`** (implemented, Phase 4 — `transport.go`): skips TLS
  verification. This is how the Tier-1 mock tier trusts its self-signed cert, and
  it works on Linux and macOS alike.
- **CA-pinned (`allowInsecure=false`, the production default):** the provider
  trusts the controller CA through the OS trust store. On Linux Go's default
  transport honors `SSL_CERT_FILE`; on macOS it verifies against the keychain and
  ignores `SSL_CERT_FILE`.

This Tier-2 e2e exists specifically to verify the **CA-pinned** path live (the
unique value over Tier-1, which uses `allowInsecure`). Export the controller's CA
cert during provisioning and point `SSL_CERT_FILE` at it (Linux), or trust it in
the keychain (macOS). The no-Docker partial of this is
`securetls_test.go` (E-M4.3).

## Running (once provisioned)

```sh
docker compose -f test/e2e/docker-compose.yml up -d
# wait for healthy (startup is minutes):
docker compose -f test/e2e/docker-compose.yml ps
# then, with the minted key + exported CA:
UNIFI_MOCK_ADDR=127.0.0.1:11443 \
UNIFI_APIKEY=<minted-key> \
SSL_CERT_FILE=$PWD/test/e2e/certs/ca.crt \
  go -C provider test -tags integration -run TestReadPathAgainstMock ./pkg/provider/
```

The same `TestReadPathAgainstMock` body works against either tier — only the host,
key, and CA differ. (Rename the env knob later if the mock/e2e split warrants it.)

## Sequencing

Tier-1 (`test/mock`) unblocks the read-path MVP immediately. Stand this Tier-2
provisioning up in parallel; if it slips, the MVP still lands on the mock with the
real-controller gate following. Escalate if provisioning balloons.
