# Minimal-create round trip per v2 kind against a THROWAWAY, un-set-up controller: build a body from the
# model's required (and min-size) fields, POST it, GET it back, DELETE it. Never point this at a real one.
#   python3 -I check/roundtrip.py <crud.json> <kind,kind,...> ['{"kind": {extra fields}}'] [container]
import json, re, subprocess, sys
crud = {k['collection']: k for k in json.load(open(sys.argv[1]))}
CONTAINER = sys.argv[4] if len(sys.argv) > 4 else 'uos-probe'
kinds = sys.argv[2].split(',')
def call(method, path, body=None):
    cmd = ['docker', 'exec', CONTAINER, 'curl', '-s', '-m', '20', '-o', '/tmp/rt.out', '-w', '%{http_code}', '-X', method,
           '-H', 'Content-Type: application/json', f'http://127.0.0.1:8081/v2/api/site/default/{path}']
    if body is not None: cmd += ['-d', json.dumps(body)]
    code = subprocess.run(cmd, capture_output=True, text=True).stdout
    out = subprocess.run(['docker', 'exec', CONTAINER, 'cat', '/tmp/rt.out'], capture_output=True, text=True).stdout
    try: out = json.loads(out)
    except Exception: pass
    return code, out
n = [0]
def val(name, m):
    t = m.get('type')
    c = m.get('constraints') or {}
    if 'IPv4' in str(c.get('custom', '')) or 'ip address' in str(c.get('custom', '')).lower(): return '192.0.2.%d' % (n.__setitem__(0, n[0] + 1) or n[0])
    if t == 'array' and ('NotEmpty' in c or 'Size' in c): return [val(name, m.get('items') or {'type': 'string'})]
    if t == 'enum': return m['values'][0]
    if t == 'bool': return False
    if t in ('int', 'number'): return 1
    if t == 'uuid': return '00000000-0000-4000-8000-000000000001'
    if t == 'array': return []
    if t == 'map': return {}
    if t == 'object': return build(m)
    n[0] += 1
    if 'mac' in name: return '02:00:00:00:00:%02x' % n[0]
    if name.endswith('ip') or 'address' in name: return '192.0.2.%d' % n[0]
    return f'probe-{n[0]}'
def build(m):
    if m.get('variants'):
        d = m.get('discriminator'); vn, vm = next(iter(m['variants'].items()))
        body = {**build({'fields': m['fields']}), **build(vm)}
        if d: body[d] = vn
        return body
    need = lambda f: f.get('required') or 'Size' in (f.get('constraints') or {}) and 'min=' in f['constraints']['Size']
    return {k: val(k, f) for k, f in m.get('fields', {}).items() if need(f)}
EXTRA = json.loads(sys.argv[3]) if len(sys.argv) > 3 else {}
res = []
for k in kinds:
    c = crud[k]; create = c['create'].split(' ', 1)[1].split('/site/{siteName}/', 1)[1]
    mm = c['request']['model']
    body = [build(mm['items'])] if mm.get('type') == 'array' else build(mm)
    body = {**body, **EXTRA.get(k, {})} if isinstance(body, dict) else body
    code, out = call('POST', create, body)
    r = {'kind': k, 'post': code, 'sent': sorted(body) if isinstance(body, dict) else ['[array]']}
    if code.startswith('2'):
        obj = out[0] if isinstance(out, list) and out else out
        obj = obj if isinstance(obj, dict) else {}
        oid = obj.get('_id') or obj.get('id') if isinstance(obj, dict) else None
        r['returned_keys'] = len(obj) if isinstance(obj, dict) else None
        if oid and c['item']:
            item = c['item'].split('/site/{siteName}/', 1)[1]; item = re.sub(r'\{[^}]+\}$', oid, item)
            g, got = call('GET', item); r['get'] = g
            r['delete'] = call('DELETE', item)[0]
        r['obj'] = obj
    else:
        r['error'] = out if isinstance(out, dict) else str(out)[:300]
    res.append(r)
print(json.dumps(res, indent=1))
