# Decoded @Value.Default values vs the controller's own GET .../firewall-policies/defaults.
#   python3 -I check/live_defaults.py <crud.json> <live-dir>/v2/firewall-policies-defaults.json
import json, sys
crud = {k['collection']: k for k in json.load(open(sys.argv[1]))}
live = json.load(open(sys.argv[2]))
def flat(m, pre=''):
    out = {}
    if m and m.get('type') == 'array': return flat(m.get('items'), pre + '[]')
    if m and m.get('type') == 'object':
        for k, v in m.get('fields', {}).items():
            out[pre + k] = v; out.update(flat(v, pre + k + '.'))
        for vm in (m.get('variants') or {}).values():
            for k, v in flat(vm, pre).items(): out.setdefault(k, v)
    return out
def jflat(o, pre=''):
    out = {}
    if isinstance(o, dict):
        for k, v in o.items(): out[pre + k] = v; out.update(jflat(v, pre + k + '.'))
    return out
lv = jflat(live); res = {'decoded': 0, 'in_live': 0, 'match': 0, 'mismatch': [], 'variant_only': []}
for p, f in flat(crud['firewall-policies']['request']['model']).items():
    if 'default' not in f or f['default'] == 'computed': continue
    res['decoded'] += 1
    if p not in lv: res['variant_only'].append(p); continue
    res['in_live'] += 1
    if lv[p] == f['default']: res['match'] += 1
    else: res['mismatch'].append([p, f['default'], lv[p]])
print(json.dumps(res, indent=1))
