# Changelog

All notable changes to this provider are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The provider's public
API surface is the frozen `provider/cmd/pulumi-resource-unifi/schema.json`
golden; the stability contract is described in
[docs/design/api-contract.md](docs/design/api-contract.md).

## [Unreleased]

### Breaking

<!--
List token-invariant breaking changes here, one bullet each: an added required
input, a removed output property, a type narrowing (string -> enum, enum value
removal, array -> scalar), an added replaceOnChanges, or a removed secret
(design api-contract.md §4.1). A non-empty bullet in THIS subsection is required
by TestNoUnacknowledgedBreakingDelta (`make schema-delta`) to merge such a
change; leave this subsection empty when there is no breaking change.
-->

- **ACL rule resources renamed.** `unifi:sites/v1:TrafficMatchIpv4` and
  `unifi:sites/v1:TrafficMatchMac` are now `unifi:sites/v1:AclRuleIpv4` and
  `unifi:sites/v1:AclRuleMac` (Python: `pulumi_unifi.sites.v1.AclRuleIpv4` /
  `AclRuleMac`; the `TrafficMatchIpv4`/`TrafficMatchMac` classes are gone). Each
  new token carries the old one as a type alias, so existing state moves to the
  new token in place, with no replace; programs must use the new class names.
  The traffic-matching-list resources keep their `TrafficMatch*` names.
- **`AclRuleMac.networkIdFilter` is required.** The spec requires it and the
  server answers 400 "networkIdFilter must not be null" without it; it was
  optional by a codegen loss (the variant's `allOf` required list was dropped).
- **`index` is no longer an input** of `AclRuleIpv4`/`AclRuleMac` (it stays an
  output). The spec marks it deprecated with no effect, and the server ignores
  it; the order of a site's rules is set with the new `AclRuleOrdering`.
- **Variant required inputs restored from the spec** (the same `allOf` codegen
  fix): `DnsARecord` (`domain`, `ipv4Address`, `ttlSeconds`), `DnsAaaaRecord`
  (`domain`, `ipv6Address`, `ttlSeconds`), `DnsCnameRecord` (`domain`,
  `targetDomain`, `ttlSeconds`), `DnsForwardDomain` (`domain`, `ipAddress`),
  `DnsMxRecord` (`domain`, `mailServerDomain`, `priority`), `DnsSrvRecord`
  (`domain`, `port`, `priority`, `protocol`, `serverDomain`, `service`,
  `weight`), `DnsTxtRecord` (`domain`, `text`).
- **Variant required inputs restored from the spec:** `ManagedNetworkGateway`
  (`cellularBackupEnabled`, `internetAccessEnabled`, `ipv4Configuration`,
  `isolationEnabled`), `ManagedNetworkSwitch` (`cellularBackupEnabled`,
  `deviceId`, `ipv4Configuration`, `isolationEnabled`).
- **Variant required inputs restored from the spec:** `TrafficMatchIpv4Addresses`,
  `TrafficMatchIpv6Addresses` and `TrafficMatchPorts` (`items`);
  `WifiBroadcastStandard` (`advertiseDeviceName`, `arpProxyEnabled`,
  `broadcastingFrequenciesGHz`, `bssTransitionEnabled`).

### Added

- `unifi:sites/v1:AclRuleOrdering`: the order of a site's ACL rules (first match
  wins), one per site, on the official `acl-rules/ordering` endpoint. The listed
  rules lead the order, in the declared order; unlisted rules follow (`LAST`,
  the default) or fail the update (`REJECT`). Destroy leaves the order as it is.

### Changed

### Fixed
