"""The Tier-2 CLI driver program (docs/design/private-binding.md §S2.11).

Driven by test/e2e/cli/e2e_test.go through the Pulumi CLI against the seeded
live controller. Inputs come from stack config so one program serves every
step:

  prefix     unique name prefix for this run (every rule carries it)
  step       1: the initial declaration; 2: rules changed, order reversed;
             3: order back to [r2, r1]
  mode       "main" (two rules + their ordering) or "alias" (one rule, for the
             TrafficMatchIpv4 -> AclRuleIpv4 state migration)
  networkId  the official UUID of a network on the site (AclRuleMac requires it)

The API key comes from the UNIFI_APIKEY environment variable only.
"""

import pulumi
import pulumi_unifi as unifi

cfg = pulumi.Config()
prefix = cfg.require("prefix")
step = cfg.get_int("step") or 1
mode = cfg.get("mode") or "main"
v1 = unifi.sites.v1

if mode == "alias":
    legacy = v1.AclRuleIpv4(
        "legacy", name=f"{prefix}-legacy", enabled=True, action=v1.ACLRuleAction.BLOCK)
    pulumi.export("ids", {"legacy": legacy.id})
else:
    # step 2 changes the action and drops protocol_filter: the official PUT
    # replaces the whole rule, so the filter must be gone on the controller.
    r1 = v1.AclRuleIpv4(
        "r1",
        name=f"{prefix}-r1",
        enabled=True,
        action=v1.ACLRuleAction.BLOCK if step == 1 else v1.ACLRuleAction.ALLOW,
        protocol_filter=[v1.ProtocolFilterItem.TCP] if step == 1 else None,
    )
    r2 = v1.AclRuleMac(
        "r2",
        name=f"{prefix}-r2" if step == 1 else f"{prefix}-r2-renamed",
        enabled=True,
        action=v1.ACLRuleAction.ALLOW,
        network_id_filter=cfg.require("networkId"),
    )
    order = v1.AclRuleOrdering(
        "order",
        ordered_acl_rule_ids=[r1.id, r2.id] if step == 2 else [r2.id, r1.id],
    )
    pulumi.export("ids", {"r1": r1.id, "r2": r2.id, "order": order.id})
    pulumi.export("effectiveOrder", order.effective_order)
