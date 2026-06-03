# Tier-1 mock — spec-driven Prism + read/write-path tests

Fast, offline, deterministic verification of the read **and write** paths with **no
real controller**. A [Prism](https://stoplight.io/open-source/prism) mock serves the
**generated** OpenAPI doc; the provider reads a data source and dispatches
create/update/delete against it through the real framework code.

## What runs

```
provider (https) ──▶ Caddy :8443 (TLS terminate) ──▶ Prism :4010 (mock) ──▶ openapi_generated.yml
```

- **Prism** mocks `provider/cmd/pulumi-resource-unifi/openapi_generated.yml` — the
  doc the provider actually speaks (sanitized component keys, absolute server path).
  It serves the spec's path keys verbatim under `/v1/...` (so Caddy strips the
  `/proxy/network/integration` prefix) and runs single-process (`--no-multiprocess`;
  the image's default multiprocess mode crashes on boot). Serving the raw vendored
  spec would 404.
- **Caddy** terminates TLS (the provider speaks `https://`) and strips the
  `/proxy/network/integration` prefix so requests reach Prism's `/v1/...` routes —
  it stands in for the controller's edge proxy. The tests trust the self-signed cert
  via the provider's `allowInsecure=true` flag (A3), not `SSL_CERT_FILE` (which Go's
  default transport ignores on macOS); see [gen-certs.sh](gen-certs.sh).

## Run

```sh
make test-mock
```

That target (see the repo `Makefile`) regenerates certs, brings the stack up, runs
the integration read- and write-dispatch tests with the env gates set, then tears down.

Manual equivalent:

```sh
./test/mock/gen-certs.sh
docker compose -f test/mock/docker-compose.yml up -d --wait
UNIFI_MOCK_ADDR=127.0.0.1:8443 \
  go -C provider test -tags integration \
    -run 'TestReadPathAgainstMock|TestWritePathAgainstMock' ./pkg/provider/
docker compose -f test/mock/docker-compose.yml down
```

## Coverage split

End-to-end through the provider, this tier proves **URL composition + live TLS +
JSON decode** for both a no-`siteId` data source (`getCountrie`) and a `sites/v1` one
(`getWifiBroadcastPage`, so a configured `{siteId}` is substituted over the wire — a
uuid, because the spec types `siteId` as `format:uuid` and Prism enforces it, so the
controller's `default` alias would 422 here), plus **write dispatch** —
`TestWritePathAgainstMock` runs create → update → delete for `FirewallZone`, proving
the framework builds spec-valid POST/PUT/DELETE and extracts the create-response `id`.

Wire facts Prism structurally cannot prove are covered by no-Docker tests in the
default `make test` gate: `TestWirePath` (`httptest` TLS) asserts the bare
`X-API-Key` and `{siteId}` substitution on the actual wire; the auth header
name/value also have unit coverage (`TestFixOpenAPIDocInjectsAuth`,
`TestGetAuthorizationHeaderReturnsBareKey`).

**Prism's limits (by design):**
- **Stateless** — it answers from static spec examples, so the write test is a
  dispatch-and-parse check, not a true round-trip: the `FirewallZone` is never
  persisted, every create returns the same example `id`, and update/delete parse a
  200 without mutating state. The real round-trip is the infra-gated Tier-2 test
  (`test/e2e/`).
- **No auth enforcement** — a missing/wrong `X-API-Key` still passes here, which is
  why the wire assertion lives in `TestWirePath`.
- **Weak pagination oracle** — example pages don't chain, so page aggregation
  (`aggregatePages`) is unit-tested directly rather than across real Prism pages.
- **No live data** — responses are spec-derived examples, not a real controller.
