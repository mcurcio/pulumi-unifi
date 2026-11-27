# Accuracy check: dto.py's request models for the Integration API's own controllers vs the
# official integration.json shipped in the same image.
#   python3 -I check/spec_check.py <classes-dir> <routes.tsv> <integration.json>
import importlib.util, json, os, sys
spec_ = importlib.util.spec_from_file_location('dto', os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'dto.py'))
dto = importlib.util.module_from_spec(spec_); spec_.loader.exec_module(dto)
dto.init(sys.argv[1], sys.argv[2])
spec = json.load(open(sys.argv[3])); S = spec['components']['schemas']
def deref(s):
    while '$ref' in s: s = S[s['$ref'].split('/')[-1]]
    return s
def props(s, pre='', out=None):
    out = {} if out is None else out
    s = deref(s)
    for alt in s.get('oneOf', []) + s.get('anyOf', []) + s.get('allOf', []): props(alt, pre, out)
    for k, v in s.get('properties', {}).items():
        v2 = deref(v); items = deref(v2.get('items', {})) if v2.get('type') == 'array' else {}
        e = v2.get('enum') or items.get('enum')
        out.setdefault(pre + k, {'required': k in s.get('required', []), 'enum': sorted(e) if e else None})
        if v2.get('type') == 'object' or any(x in v2 for x in ('properties', 'oneOf', 'allOf')): props(v2, pre + k + '.', out)
        if items: props(items, pre + k + '[].', out)
    return out
tot = dict(bodies=0, spec_fields=0, found=0, required_agree=0, enums=0, enums_agree=0); notes = []
for path, item in spec['paths'].items():
    for verb in ('post', 'put', 'patch'):
        sch = (((item.get(verb) or {}).get('requestBody') or {}).get('content') or {}).get('application/json', {}).get('schema')
        if not sch: continue
        m = dto.route_model(verb.upper(), path)
        if not m['model']: notes.append(f'NO BODY {verb} {path}'); continue
        tot['bodies'] += 1; got = dto.flat(m['model'])
        for k, w in props(sch).items():
            tot['spec_fields'] += 1; f = got.get(k)
            if not f: notes.append(f'MISSING {verb} {path} {k}'); continue
            tot['found'] += 1
            if bool(f.get('required')) == w['required']: tot['required_agree'] += 1
            else: notes.append(f'REQUIRED {verb} {path} {k} spec={w["required"]} bytecode={bool(f.get("required"))}')
            if w['enum']:
                tot['enums'] += 1; ev = sorted(f.get('values') or (f.get('items') or {}).get('values') or [])
                if ev == w['enum']: tot['enums_agree'] += 1
                else: notes.append(f'ENUM {verb} {path} {k} spec={w["enum"]} bytecode={ev}')
print(json.dumps(tot)); print('\n'.join(notes))
