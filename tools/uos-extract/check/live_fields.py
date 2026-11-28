# Live check of api/fields: every value a controller returns must match its field rule; keys the
# files do not define are listed.   python3 -I check/live_fields.py <fields-dir> <live-dir>/classic
import json, os, re, sys
fields_dir, live_dir = sys.argv[1], sys.argv[2]
KIND = {'networkconf': 'NetworkConf', 'wlanconf': 'WlanConf', 'portconf': 'PortConf', 'usergroup': 'UserGroup',
        'firewallgroup': 'FirewallGroup', 'firewallrule': 'FirewallRule', 'routing': 'Routing', 'wlangroup': 'WlanGroup',
        'radiusprofile': 'RadiusProfile', 'dhcpoption': 'DhcpOption', 'portforward': 'PortForward', 'dynamicdns': 'DynamicDNS',
        'user': 'User', 'hotspotop': 'HotspotOp'}
SERVER = {'_id', 'site_id', 'attr_hidden_id', 'attr_no_delete', 'attr_no_edit', 'attr_hidden', 'external_id'}
def check(obj, spec, pre, st):
    for k, v in obj.items():
        p = pre + k
        if k in SERVER and not pre: continue
        if k not in spec: st['unknown'].append(p); continue
        r = spec[k]
        if isinstance(r, dict) and isinstance(v, dict): check(v, r, p + '.', st); continue
        if isinstance(r, list):
            el = r[0] if r else ''
            for x in (v if isinstance(v, list) else [v]):
                if isinstance(el, dict) and isinstance(x, dict): check(x, el, p + '[].', st)
                else: rule(x, el, p, st)
            continue
        rule(v, r, p, st)
def rule(v, r, p, st):
    st['values'] += 1
    if r == '' or not isinstance(r, str): st['untyped'] += 1; return
    s = json.dumps(v) if isinstance(v, (dict, list)) else ('true' if v is True else 'false' if v is False else str(v))
    try: ok = re.fullmatch(r if r.startswith('^') else '(?:' + r + ')', s) is not None
    except re.error: st['bad_regex'].append(p); return
    st['checked'] += 1
    if not ok: st['rejected'].append(f'{p}={s[:40]!r} rule={r[:50]!r}')
out = {}
for f in sorted(os.listdir(live_dir)):
    name = f[:-5]; data = json.load(open(os.path.join(live_dir, f))).get('data', [])
    if name == 'setting':
        spec = json.load(open(os.path.join(fields_dir, 'Setting.json')))
        st = {'objects': len(data), 'values': 0, 'checked': 0, 'untyped': 0, 'unknown': [], 'rejected': [], 'bad_regex': [], 'no_spec': []}
        for o in data:
            k = o.get('key'); sp = spec.get(k)
            if sp is None: st['no_spec'].append(k); continue
            check({x: y for x, y in o.items() if x not in SERVER | {'key'}}, sp, k + '.', st)
        out[name] = st; continue
    if name not in KIND: continue
    spec = json.load(open(os.path.join(fields_dir, KIND[name] + '.json')))
    st = {'objects': len(data), 'values': 0, 'checked': 0, 'untyped': 0, 'unknown': [], 'rejected': [], 'bad_regex': []}
    for o in data: check(o, spec, '', st)
    out[name] = st
for k, st in out.items():
    st['unknown'] = sorted(set(st['unknown'])); st['rejected'] = sorted(set(st['rejected']))
print(json.dumps(out, indent=1))
