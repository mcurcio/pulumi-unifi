# Examples

Short, self-contained examples (Python). See [Getting Started](getting-started.md)
for full setup, and the [Reference](reference.md) for every resource and data
source.

## Read a data source

Data sources are Pulumi functions (GET-only entities). This reads the list of
countries known to the controller:

```python
import pulumi
import pulumi_unifi.countries.v1 as countries

result = countries.get_country()

pulumi.export("country_count", result.count)
pulumi.export("countries", result.data)
```

## Create a managed resource

A managed resource has a full create/read/update/delete lifecycle. This creates
an A record:

```python
import pulumi
import pulumi_unifi.sites.v1 as sites

record = sites.DnsARecord(
    "web",
    domain="web.example.lan",
    ipv4_address="192.168.1.20",
    enabled=True,
    ttl_seconds=600,
)

pulumi.export("record_domain", record.domain)
```

## Combine them

You can feed a data-source output into a resource. For example, read an existing
record and export it alongside a newly created one:

```python
import pulumi
import pulumi_unifi.sites.v1 as sites

new_record = sites.DnsARecord(
    "api",
    domain="api.example.lan",
    ipv4_address="192.168.1.21",
    enabled=True,
    ttl_seconds=300,
)

pulumi.export("new_domain", new_record.domain)
```

See the [Reference](reference.md) for the full set of 19 resources and 50 data
sources exposed by the pinned spec.
