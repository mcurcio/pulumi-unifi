# Recover a JSON field model for a Spring route's @RequestBody DTO from obfuscated bytecode via `javap`.
# usage: python3 -I dto.py <classes-dir> <VERB> <path> <routes.tsv>   (path exactly as routes.py printed it)
# Prototype: heuristic javap-text parsing; output is a research aid, not a codegen input yet.
import os, re, subprocess, sys, json
CP, VERB, PATH = sys.argv[1], sys.argv[2], sys.argv[3]
ROUTES = sys.argv[4]
env = dict(os.environ, JAVA_TOOL_OPTIONS='', _JAVA_OPTIONS='')
_cache = {}
def jp(cls, flags='-v'):
    k = (cls, flags)
    if k not in _cache:
        _cache[k] = subprocess.run(['javap', flags, '-p', '-cp', CP, cls], capture_output=True, text=True, env=env).stdout
    return _cache[k]
def methods(txt):
    # yields (signature line, body)
    for m in re.finditer(r'\n  ((?:public|private|protected|static|final|abstract|default|synchronized|\s)*[^\n(]*\([^\n]*\)[^\n]*;)\n(.*?)(?=\n  \S|\n}\n|\Z)', txt, re.S):
        yield m.group(1), m.group(2)
def ctrl_for(verb, path):
    for line in open(ROUTES):
        v, p, c = line.rstrip('\n').split('\t')
        if v == verb and p == path: return c
def leaf(path):
    return path.rsplit('/', 1)[-1]
def body_type(cls, verb, path):
    txt = jp(cls)
    ann = {'GET':'Get','POST':'Post','PUT':'Put','DELETE':'Delete','PATCH':'Patch'}[verb] + 'Mapping('
    tails = [path[i:] for i in range(len(path)) if path[i] == '/']
    for sig, body in methods(txt):
        if ann not in body: continue
        vals = re.findall(r'(?:value|path)=\[([^\]]*)\]', body)
        vs = [s.strip().strip('"') for v in vals for s in v.split(',')] or ['']
        if not any(x in tails for x in vs): continue   # method path must be a suffix of the route
        pa = re.search(r'RuntimeVisibleParameterAnnotations:(.*?)(?:\n    \S|\Z)', body, re.S)
        params = re.search(r'\(([^)]*)\)', sig).group(1).split(', ')
        if pa:
            for idx, chunk in re.findall(r'parameter (\d+):(.*?)(?=parameter \d+:|\Z)', pa.group(1), re.S):
                if 'RequestBody' in chunk: return params[int(idx)]
    return None
def strip_generic(t):
    m = re.match(r'([\w.$]+)(?:<(.*)>)?(\[\])?$', t.strip())
    return m.groups() if m else (t, None, None)
def model(t, depth=0, seen=None):
    seen = seen or set()
    base, arg, arr = strip_generic(t)
    if base in ('java.util.Optional',): r = model(arg, depth, seen); r['optional'] = True; return r
    if base in ('java.util.List', 'java.util.Set', 'java.util.Collection'): return {'type':'array', 'items': model(arg, depth, seen)}
    if base == 'java.util.Map': return {'type':'map', 'value': model(arg.split(',', 1)[-1], depth, seen) if arg else {}}
    prim = {'boolean':'bool','java.lang.Boolean':'bool','int':'int','long':'int','java.lang.Integer':'int','java.lang.Long':'int','double':'number','java.lang.Double':'number','float':'number','java.lang.String':'string','java.util.UUID':'uuid','java.time.Instant':'datetime','java.lang.Object':'any'}
    if base in prim: return {'type': prim[base]}
    if not base.startswith('com.ubnt') or depth > 6 or base in seen: return {'type': 'ref', 'class': base}
    seen = seen | {base}
    txt = jp(base)
    if ' extends java.lang.Enum<' in txt:
        c = jp(base, '-c')
        clinit = c.split('static {};', 1)[-1]
        lits = re.findall(r'// String (\S+)', clinit)
        return {'type':'enum', 'values': lits}
    out = {'type':'object', 'fields': {}}
    sub = re.findall(r'JsonSubTypes\$Type\(\s*value=class L([\w/$]+);\s*name="([^"]*)"', txt)
    if sub: out['subtypes'] = {n: c.replace('/', '.') for c, n in sub}
    disc = re.search(r'JsonTypeInfo\((.*?)\)', txt, re.S)
    if disc: p = re.search(r'property="([^"]*)"', disc.group(1)); out['discriminator'] = p.group(1) if p else None
    for sig, body in methods(txt):
        jpv = re.search(r'JsonProperty\(\s*value="([^"]*)"', body)
        if not jpv or '()' not in sig: continue
        g = re.search(r'Signature: #\d+\s+// \(\)(\S+)', body)
        rt = re.match(r'(?:public |abstract |default |static |final )*([\w.$<>, \[\]?]+) \w+\(\)', sig).group(1)
        f = model(rt, depth + 1, seen)
        if 'constraints.NotNull' in body or 'NotBlank' in body: f['required'] = True
        if 'Value$Default' in body: f['hasDefault'] = True
        for cn, args in re.findall(r'(?:constraints|hibernate\.validator\.constraints)\.(\w+)\(\s*(.*?)\s*\)', body, re.S):
            f.setdefault('constraints', {})[cn] = re.sub(r'\s+', ' ', args)
        if 'JsonIgnore' in body: continue
        out['fields'][jpv.group(1)] = f
    if out.get('subtypes'):
        out['variants'] = {n: model(c, depth + 1, seen) for n, c in out['subtypes'].items()}
    return out
ctrl = ctrl_for(VERB, PATH)
bt = body_type(ctrl, VERB, PATH) if ctrl else None
res = {'route': f'{VERB} {PATH}', 'controller': ctrl, 'body': bt}
if bt: res['model'] = model(bt)
print(json.dumps(res, indent=1))
