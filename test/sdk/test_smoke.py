"""Python SDK smoke test (E-M4.5).

Asserts the generated pulumi_unifi SDK is importable and exposes a stable set of
resource and data-source classes — including a discriminated variant (Standard)
— so a `gen-sdk`/schema-shape regression that drops a class or ships a broken,
uninstallable SDK fails CI instead of surfacing first in the consumer.

This file lives OUTSIDE the gitignored sdk/ tree (which `make python_sdk`
removes and regenerates) so it is committed. CI runs it after `make python_sdk`
against the freshly generated, pip-installed SDK:

    make python_sdk
    pip install ./sdk/python pulumi
    pytest test/sdk/test_smoke.py

It needs pulumi_unifi (and its `pulumi` dependency) importable on sys.path; when
they are absent it skips, so a bare local `pytest` without a prior SDK build is
graceful while CI (which builds first) runs it for real.
"""

import importlib

import pytest

pulumi_unifi = pytest.importorskip(
    "pulumi_unifi",
    reason="pulumi_unifi not importable; run `make python_sdk` and `pip install ./sdk/python pulumi` first",
)


def test_import_top_level():
    """The package imports and exposes its site submodule namespace."""
    assert pulumi_unifi is not None
    sites = importlib.import_module("pulumi_unifi.sites.v1")
    assert sites is not None


# Resource classes that must exist on pulumi_unifi.sites.v1. Includes a
# discriminated variant (Standard / IotOptimized — WiFi broadcast) and the flat
# FirewallZone, so both the per-variant split and ordinary resources are pinned.
EXPECTED_SITE_RESOURCES = [
    "WifiBroadcastStandard",
    "WifiBroadcastIotOptimized",
    "ManagedNetworkGateway",
    "FirewallZone",
    "FirewallPolicy",
    "DnsARecord",
]

# Data-source (function) symbols that must exist on pulumi_unifi.sites.v1.
EXPECTED_SITE_FUNCTIONS = [
    "get_firewall_zone",
    "list_wifi_broadcasts",
]


@pytest.mark.parametrize("name", EXPECTED_SITE_RESOURCES)
def test_site_resource_class_present(name):
    sites = importlib.import_module("pulumi_unifi.sites.v1")
    assert hasattr(sites, name), f"expected resource class pulumi_unifi.sites.v1.{name}"


@pytest.mark.parametrize("name", EXPECTED_SITE_FUNCTIONS)
def test_site_function_present(name):
    sites = importlib.import_module("pulumi_unifi.sites.v1")
    assert hasattr(sites, name), f"expected data-source function pulumi_unifi.sites.v1.{name}"


def test_discriminated_variant_is_a_resource():
    """Standard must be a real Pulumi CustomResource subclass, not a stub."""
    import pulumi

    sites = importlib.import_module("pulumi_unifi.sites.v1")
    assert issubclass(sites.WifiBroadcastStandard, pulumi.CustomResource)


def test_top_level_country_data_source():
    """A no-siteId data source (countries) is also reachable."""
    countries = importlib.import_module("pulumi_unifi.countries.v1")
    assert hasattr(countries, "get_country")
