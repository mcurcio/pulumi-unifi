# Getting Started

This walks through a minimal Pulumi program (Python) that creates a single
`DnsARecord` on your UniFi controller.

!!! note "Prerequisites"
    Complete [Installation](installation.md) (build/install the plugin and
    install the Python SDK) and [Authentication](authentication.md) (mint a key)
    first.

## 1. Create a project

```bash
mkdir unifi-dns && cd unifi-dns
pulumi new python
```

Install the SDK into the project's virtualenv (pre-release: from the built
tree):

```bash
pip install /path/to/pulumi-unifi/sdk/python
```

## 2. Configure the provider

```bash
pulumi config set unifi:apiHost 192.168.1.1
pulumi config set --secret unifi:apiKey <your-api-key>
pulumi config set unifi:allowInsecure true   # self-signed controller cert
```

## 3. Write the program

Replace the contents of `__main__.py`:

```python
import pulumi
import pulumi_unifi.sites.v1 as sites

record = sites.DnsARecord(
    "example",
    domain="example.lan",
    ipv4_address="192.168.1.50",
    enabled=True,          # required
    ttl_seconds=300,
)

pulumi.export("domain", record.domain)
pulumi.export("ipv4_address", record.ipv4_address)
```

Notes:

- `enabled` is the only required input; `type` defaults to the constant
  `A_RECORD` for this resource.
- Omit `site_id` to use the provider-level `siteId` (defaults to `default`).
  Setting it per resource moves the record to another site (a replacement).

## 4. Deploy

```bash
pulumi up
```

Pulumi shows the planned `create` for the `DnsARecord`; confirm to apply. On
success the exported `domain` and `ipv4_address` are printed.

## 5. Clean up

```bash
pulumi destroy
```

For more, see [Examples](examples.md) and the auto-generated
[Reference](reference.md).
