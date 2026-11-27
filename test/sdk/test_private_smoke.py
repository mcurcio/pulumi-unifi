"""Python SDK smoke test for the private kinds (docs/design/private-binding.md).

Run by `make PRIVATE=1 test-sdk`: the SDK generated from schema.merged.json
must expose one CustomResource class per private token in
provider/pkg/private/testdata/tokens.txt under pulumi_unifi.network, plus the
networkBaseUrl provider argument. At PRIVATE=0 (stock CI) the module does not
exist and every test skips, unless UNIFI_REQUIRE_PRIVATE_SDK=1 (set by the
PRIVATE=1 target) turns the skip into a failure.
"""

import importlib
import inspect
import os
import pathlib

import pytest

pulumi_unifi = pytest.importorskip("pulumi_unifi")

TOKENS = pathlib.Path(__file__).resolve().parents[2] / "provider/pkg/private/testdata/tokens.txt"


def _network():
    try:
        return importlib.import_module("pulumi_unifi.network")
    except ImportError:
        if os.environ.get("UNIFI_REQUIRE_PRIVATE_SDK") == "1":
            pytest.fail("pulumi_unifi.network is missing but UNIFI_REQUIRE_PRIVATE_SDK=1")
        pytest.skip("NOT RUN (SDK built at PRIVATE=0: no private kinds)")


def _classes():
    out = []
    for line in TOKENS.read_text().splitlines():
        if line.startswith("resource:unifi:network:"):
            out.append(line.rsplit(":", 1)[1])
    return out


@pytest.mark.parametrize("name", _classes())
def test_private_resource_class(name):
    import pulumi

    net = _network()
    cls = getattr(net, name, None)
    assert cls is not None, f"expected pulumi_unifi.network.{name}"
    assert issubclass(cls, pulumi.CustomResource)


def test_provider_has_network_base_url():
    _network()
    params = inspect.signature(pulumi_unifi.ProviderArgs.__init__).parameters
    assert "network_base_url" in params
