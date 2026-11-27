#!/usr/bin/env bash
# Read a throwaway, un-set-up controller's objects from inside its container (Network's internal
# :8081 answers without credentials before the first-run wizard). Never point this at a real controller.
#   check/live_fetch.sh <container> <out-dir>
set -euo pipefail
C="$1"; OUT="$2"; mkdir -p "$OUT/classic" "$OUT/v2"
g() { docker exec "$C" curl -s -m 20 "http://127.0.0.1:8081$1"; }
for k in networkconf wlanconf portconf usergroup firewallgroup firewallrule routing wlangroup radiusprofile dhcpoption portforward dynamicdns user hotspotop; do
  g "/api/s/default/rest/$k" > "$OUT/classic/$k.json"
done
g /api/s/default/get/setting > "$OUT/classic/setting.json"
g /v2/api/site/default/firewall-policies/defaults > "$OUT/v2/firewall-policies-defaults.json"
for c in apgroups radius/profiles; do g "/v2/api/site/default/$c" > "$OUT/v2/$(tr / _ <<<"$c").json"; done
