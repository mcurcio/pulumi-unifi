# Private-API e2e tier (`make test-e2e-private`)

The live test of the private-API slice (`docs/design/private-binding.md` §11). It proves, against a
real UniFi OS Server, that the `PRIVATE=1` provider and Python SDK round-trip the 4 private kinds
(`unifi:network:QosRule`, `ApGroup`, `NetworkMembersGroup`, `FirewallAppBlock`) under the frame's
rules 1-3 and G-Q1.

**Offline/local tier, not stock CI.** The controller image runs systemd and needs `--privileged`
plus the host cgroup mount, and the pinned installer is 875 MB. Tier 1 (`make test`, `make
test-mock`) stays the portable gate.

## Run it

```sh
PULUMI=/path/to/pulumi-3.230.0/pulumi \
UOS_INSTALLER=/path/to/a/local/copy/of/the/pinned/installer \
make test-e2e-private
```

| Variable | Meaning |
|---|---|
| `PULUMI` | The Pulumi CLI (default: `pulumi` on `PATH`). Use the version `provider/go.mod` pins. |
| `UOS_INSTALLER` | Reuse a local installer; it is checked against `uos/pin.env` like `make uos-fetch`. Without it the installer is downloaded once into the cache. |
| `UNIFI_UOS_CACHE` | Cache root (default `~/.cache/pulumi-unifi/uos`). Holds `<sha256>/installer` and `<sha256>/image.tar`. |
| `E2E_PORT` | Host port for the forwarded Network application (default `18081`). |
| `E2E_RELOAD=1` | `docker load` the image again even if the tag exists. |
| `--keep-up` | Leave the controller and the scratch dir in place for debugging. |
| `-- <args>` | Extra `go test` arguments. |

Prereqs: Docker with privileged containers, Go 1.26, Python 3, `curl`. No API key and no
`hieutq` image are involved.

## What `run.sh` does

1. **Build.** `make PRIVATE=1 build python_sdk` (installer → model → merged schema → plugin and
   SDK), then a venv at `build/e2e-private/venv` (gitignored) with `sdk/python`.
2. **Image.** The `image.tar` of the **same pinned installer** (extracted by `pulumi-gen-unifi
   uos-extract -image-out` into the cache) is `docker load`ed and tagged
   `uosserver:uos-<version>-<sha256[:12]>`, so the model and the target controller are one version.
   The load is skipped when that tag already exists.
3. **Boot.** A fresh container with the research recipe (`--privileged --cgroupns=host`, cgroup
   mount, tmpfs, `APP_*`/`PRODUCT_NAME`/`FIRMWARE_PLATFORM`) and a **version-5** `UOS_UUID` (the
   Network app refuses a v4 UUID). No wizard, no key, no nginx edge.
4. **Reach.** `fwd/` (a static stdlib TCP forwarder) is built, copied in and started:
   `0.0.0.0:18081 → 127.0.0.1:8081`, the Network app's loopback-only port.
5. **Ready.** Polls `GET /v2/api/site/default/apgroups` for a JSON 200 (8 min limit).
6. **Drive.** `go test -tags e2e_private` runs `e2e_test.go`: the Pulumi CLI over `program/`
   with a temp `PULUMI_HOME`, a `file://` backend and a throwaway passphrase, config
   `unifi:networkBaseUrl=http://127.0.0.1:18081`, no `apiKey`.
7. **Teardown** (always, via `trap`): removes the container and the scratch dir. On failure the
   last Pulumi output is printed first.

## What the driver asserts

Every object is named `e2e-<unix time>-…`; every check is a raw HTTP read of the controller.

| Step | Assertion |
|---|---|
| 1 create | `up` creates all 4; each stored id is `private:<id>`; the raw object holds the declared values |
| 2 read, no-op | `refresh`, then `preview --expect-no-changes`, then a second `up --expect-no-changes` (rule 1) |
| 3 update | Raw PUTs change **unmanaged** fields (QosRule `enabled`, `download_burst`; FirewallAppBlock `client_macs`). Step 2 of the program changes managed fields on all 4 kinds and drops QosRule `destination.port_matching_type` and `destination.port`. After `up`: no replace, the managed values changed, the dropped fields are back at their defaults (`ANY`, cleared) and the unmanaged ones kept (rule 2, G-Q1); `preview --refresh` is clean |
| 4 drift | A raw PUT of a managed field (QosRule `download_limit_kbps`): `preview --refresh --json` shows exactly one update, of that resource, on that one property; `up --refresh` restores it and keeps the unmanaged fields |
| 5 import | A fresh stack: importing a missing natural key fails with the zero-match error; `pulumi import` adopts ApGroup (bare key), NetworkMembersGroup (`name=…`) and FirewallAppBlock by natural key; the stored ids equal the originals; `preview --expect-no-changes` with a program that declares more than the import adopted (rule 3) |
| 6 destroy | `destroy`; the raw lists hold none of the run's objects |

A failed step stops the run; cleanup then destroys the stack and sweeps any object with the run's
prefix.

## Files

| Path | Role |
|---|---|
| `run.sh` | The pipeline above |
| `e2e_test.go` | Go driver (`//go:build e2e_private`); standard library only |
| `program/` | The Python program; inputs from stack config (`prefix`, `step`, `mode`) |
| `fwd/` | Static TCP forwarder |
| `go.mod` | Own module, so the provider build and `make test` never see this tier |

Nothing here contains Ubiquiti files or extracted text; the installer, image and model live in the
cache and `build/` (both outside git).
