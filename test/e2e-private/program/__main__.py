"""The private-API e2e program (docs/design/private-binding.md §11).

Driven by test/e2e-private/e2e_test.go through the Pulumi CLI. Inputs come
from stack config so one program serves every step:

  prefix  unique name prefix for this run (every object carries it)
  step    1: the initial declaration; 2: managed fields changed or dropped
  mode    "main" (the 4 kinds) or "import" (the objects a fresh stack adopts
          by natural key; declared exactly as step 2 declares them); edge mode
          only (docs/design/private-binding.md §S2.8, §S2.11): "classic" (the
          classic UserGroup and the SettingNtp singleton, steps 1-3),
          "classic-import" (both, as step 2 declares them, in a fresh stack)
          and "classic-dup" (a UserGroup whose name is taken)

The provider config comes from the driver's stack config: in direct mode the
Network application before the setup wizard (unifi:networkBaseUrl, no
apiKey); in edge mode the seeded controller's UniFi OS edge (unifi:apiHost,
unifi:allowInsecure, and the apiKey from the environment).
"""

import pulumi
import pulumi_unifi as unifi

cfg = pulumi.Config()
prefix = cfg.require("prefix")
step = cfg.get_int("step") or 1
mode = cfg.get("mode") or "main"


def ap_group(name: str) -> unifi.network.ApGroup:
    # step 2 renames it (its natural key; an in-place update, not a replace).
    # device_macs stays empty: the controller rejects MACs of devices it has
    # not adopted (api.err.InvalidDeviceInApGroup), and a fresh controller has
    # none.
    return unifi.network.ApGroup(
        name,
        name=f"{prefix}-apg" if step == 1 else f"{prefix}-apg-renamed",
        device_macs=[],
    )


def members_group(name: str) -> unifi.network.NetworkMembersGroup:
    return unifi.network.NetworkMembersGroup(
        name,
        name=f"{prefix}-nmg",
        type="CLIENTS",
        members=["02:00:5e:00:00:10"] if step == 1 else ["02:00:5e:00:00:10", "02:00:5e:00:00:11"],
    )


def app_block(name: str) -> unifi.network.FirewallAppBlock:
    # step 2 changes the app list and the schedule mode (a nested managed
    # leaf) and adds schedule.time_all_day.
    return unifi.network.FirewallAppBlock(
        name,
        name=f"{prefix}-fab",
        app_ids=[5] if step == 1 else [5, 6],
        schedule=unifi.network.FirewallAppBlockScheduleArgs(
            mode="ALWAYS" if step == 1 else "EVERY_DAY",
            **({} if step == 1 else {"time_all_day": True})),
    )


def user_group(name: str) -> unifi.network.UserGroup:
    # step 1: a download limit; step 2: changed; step 3: dropped (G-Q1: reset
    # to the recorded default -1, no limit).
    args = {} if step == 3 else {"qos_rate_max_down": 500 if step == 1 else 800}
    return unifi.network.UserGroup(name, name=f"{prefix}-ug", **args)


def setting_ntp(name: str) -> unifi.network.SettingNtp:
    # One per site: create adopts it, destroy releases it. step 3 drops both
    # managed fields: setting_preference resets to "auto", and with "auto" the
    # controller puts its own servers back (measured), so ntp_server1 is
    # dropped with it and resets to that recorded default.
    if step == 3:
        return unifi.network.SettingNtp(name)
    return unifi.network.SettingNtp(
        name,
        setting_preference="manual",
        ntp_server1="time-e2e-1.example.invalid" if step == 1 else "time-e2e-2.example.invalid")


if mode == "classic":
    ug = user_group("ug")
    ntp = setting_ntp("ntp")
    pulumi.export("ids", {"ug": ug.id, "ntp": ntp.id})
elif mode == "classic-import":
    ug = user_group("ug-imported")
    ntp = setting_ntp("ntp-imported")
    pulumi.export("ids", {"ug": ug.id, "ntp": ntp.id})
elif mode == "classic-dup":
    ug = user_group("ug-dup")
    pulumi.export("ids", {"ug": ug.id})
elif mode == "import":
    # An import adopts the natural key + required fields; app_ids and
    # schedule.time_all_day are declared beyond that, at their live values,
    # so they must preview as no change either.
    apg = ap_group("apg-imported")
    nmg = members_group("nmg-imported")
    fab = app_block("fab-imported")
    pulumi.export("ids", {"apg": apg.id, "nmg": nmg.id, "fab": fab.id})
else:
    # QosRule: step 2 changes download_limit_kbps (managed field A) and drops
    # destination.port_matching_type + destination.port (managed fields B):
    # G-Q1 resets them to their defaults (ANY, and cleared).
    if step == 1:
        dest = unifi.network.QosRuleDestinationArgs(
            matching_target="ANY", port_matching_type="SPECIFIC", port="443")
    else:
        dest = unifi.network.QosRuleDestinationArgs(matching_target="ANY")
    qos = unifi.network.QosRule(
        "qos",
        name=f"{prefix}-qos",
        objective="LIMIT",
        schedule=unifi.network.QosRuleScheduleArgs(mode="ALWAYS"),
        source=unifi.network.QosRuleSourceArgs(matching_target="ANY"),
        destination=dest,
        download_limit_kbps=1000 if step == 1 else 3000,
    )
    apg = ap_group("apg")
    nmg = members_group("nmg")
    fab = app_block("fab")
    pulumi.export("ids", {"qos": qos.id, "apg": apg.id, "nmg": nmg.id, "fab": fab.id})
