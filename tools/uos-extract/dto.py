# JSON field model of Spring request/response bodies, recovered from obfuscated bytecode via `javap`.
#
#   python3 -I dto.py <classes-dir> <routes.tsv> VERB PATH       one route's body model (JSON)
#   python3 -I dto.py <classes-dir> <routes.tsv> --crud [PREFIX]  every CRUD-shaped collection under
#                                                                 PREFIX (default /api/site/{siteName}/)
#
# Per field: type (bool/int/number/string/uuid/datetime/any/enum/object/array/map), required (@NotNull,
# @NotBlank), optional (Optional<>), default (@Value.Default: the constant when the body is a plain
# constant, else "computed"), constraints (Jakarta/Hibernate validation), enum wire values (@JsonValue or
# @JsonProperty on constants, else the constant's name), polymorphism (@JsonTypeInfo + @JsonSubTypes).
# --crud also models the item GET's response and marks response-only fields readOnly.
# Prototype: parses `javap` text. Needs a JDK `javap` on PATH.
import json, os, re, subprocess, sys

CP, ROUTES = (sys.argv[1], sys.argv[2]) if __name__ == '__main__' else (None, None)
ENV = dict(os.environ, JAVA_TOOL_OPTIONS='', _JAVA_OPTIONS='')
_cache = {}

def jp(cls, flags='-v'):
    if (cls, flags) not in _cache:
        _cache[cls, flags] = subprocess.run(['javap', flags, '-p', '-cp', CP, cls],
                                            capture_output=True, text=True, env=ENV).stdout
    return _cache[cls, flags]

def members(txt):
    """(signature, body) for every method and field in javap output."""
    return re.findall(r'\n  (\S[^\n]*;)\n(.*?)(?=\n  \S|\n}\n|\Z)', txt, re.S)

def code_of(cls, sig_name):
    c = jp(cls, '-c')
    m = re.search(r'\n  [^\n]* ' + re.escape(sig_name) + r'\(\);\n    Code:\n(.*?)(?=\n\n|\n}\n)', c, re.S)
    return m.group(1) if m else ''

ROUTE_TABLE = []

def init(classes_dir, routes_tsv):
    """For use as a module: point at a classes dir and a routes.py table."""
    global CP, ROUTE_TABLE
    CP = classes_dir
    ROUTE_TABLE[:] = [l.rstrip('\n').split('\t') for l in open(routes_tsv)]

# ---------- enums ----------
_enums = {}
def enum_info(cls):
    """{constant field name: wire value}, in declaration order."""
    if cls in _enums:
        return _enums[cls]
    v, c = jp(cls), jp(cls, '-c')
    # Which value does @JsonValue serialize? Follow the annotated method: name() -> the constant's
    # name; getfield -> the extra constructor string. (Lowercase aliases are often parse-only.)
    json_value = False
    for sig, body in members(v):
        mm = re.match(r'public [\w.$]+ (\w+)\(\);$', sig)
        if mm and 'annotation.JsonValue' in body:
            code = code_of(cls, mm.group(1))
            json_value = 'getfield' in code and 'Method name:()' not in code
    fprops = {}
    for sig, body in members(v):
        m = re.search(r'static final [\w.$]+ (\w+);$', sig)
        p = re.search(r'JsonProperty\(\s*value="([^"]*)"', body)
        if m and p:
            fprops[m.group(1)] = p.group(1)
    clinit = c.split('static {};', 1)[-1]
    out = {}
    for blk in re.split(r'\n\s+\d+: new\s', clinit)[1:]:
        put = re.search(r'putstatic\s+#\d+\s+// Field (\w+):', blk)
        if not put:
            continue
        lits = re.findall(r'// String (.*)', blk.split('invokespecial', 1)[0])
        name = lits[0] if lits else put.group(1)
        wire = fprops.get(put.group(1)) or (lits[1] if json_value and len(lits) == 2 else name)
        out[put.group(1)] = wire
    _enums[cls] = out
    return out

def static_value(owner, field, depth=0):
    """Resolve a static field to a literal: an enum constant, or a constant copied from one."""
    owner = owner.replace('/', '.')
    if ' extends java.lang.Enum<' in jp(owner):
        return enum_info(owner).get(field)
    if depth > 1:
        return None
    clinit = jp(owner, '-c').split('static {};', 1)[-1]
    m = re.search(r'getstatic\s+#\d+\s+// Field ([\w/$]+)\.(\w+):[^\n]*\n\s+\d+: putstatic\s+#\d+\s+// Field ' + re.escape(field) + ':', clinit)
    return static_value(m.group(1), m.group(2), depth + 1) if m else None

def default_of(cls, method):
    code = code_of(cls, method)
    ins = [re.sub(r'^\s*\d+: ', '', l).strip() for l in code.splitlines() if re.match(r'\s*\d+:', l)]
    if not ins or len(ins) > 3:
        return 'computed'
    a = ins[0]
    if a in ('iconst_0', 'iconst_1') and ins[-1] == 'ireturn' and len(ins) == 2:
        return {'iconst_0': False, 'iconst_1': True}[a] if code_ret_bool(cls, method) else int(a[-1])
    m = re.match(r'(?:iconst_(m?\d)|[bs]ipush\s+(-?\d+))', a)
    if m and len(ins) >= 2:
        n = m.group(1) or m.group(2)
        n = -1 if n == 'm1' else int(n)
        return bool(n) if 'java/lang/Boolean.valueOf' in ' '.join(ins) else n
    m = re.search(r'ldc\S*\s+#\d+\s+// (String|int|long|float|double) (.*)', a)
    if m:
        return m.group(2) if m.group(1) == 'String' else float(m.group(2).rstrip('lfd')) if '.' in m.group(2) else int(m.group(2).rstrip('lfd'))
    m = re.search(r'getstatic\s+#\d+\s+// Field ([\w/$]+)\.(\w+):', a)
    if m:
        v = static_value(m.group(1), m.group(2))
        return v if v is not None else 'computed'
    m = re.search(r'invokestatic\s+#\d+\s+// (?:Interface)?Method java/util/(List|Set|Map|Optional)\.(of|empty)', a)
    if m and m.group(2) in ('of', 'empty') and '(' not in a.split(':', 2)[-1][1:2]:
        return {'List': [], 'Set': [], 'Map': {}, 'Optional': None}[m.group(1)]
    return 'computed'

def code_ret_bool(cls, method):
    return re.search(r'\n  [^\n]* boolean ' + re.escape(method) + r'\(\);', jp(cls)) is not None

# ---------- types ----------
PRIM = {'boolean': 'bool', 'java.lang.Boolean': 'bool', 'int': 'int', 'long': 'int', 'short': 'int',
        'java.lang.Integer': 'int', 'java.lang.Long': 'int', 'java.lang.Short': 'int', 'double': 'number',
        'float': 'number', 'java.lang.Double': 'number', 'java.lang.Float': 'number', 'java.math.BigDecimal': 'number',
        'java.lang.String': 'string', 'java.util.UUID': 'uuid', 'java.time.Instant': 'datetime',
        'java.time.OffsetDateTime': 'datetime', 'java.time.LocalDate': 'date', 'java.lang.Object': 'any',
        'com.fasterxml.jackson.databind.JsonNode': 'any'}

def split_generic(t):
    t = t.strip()
    m = re.match(r'([\w.$]+)(?:<(.*)>)?(\[\])?$', t)
    return m.groups() if m else (t, None, None)

def model(t, depth=0, seen=frozenset()):
    base, arg, arr = split_generic(t.replace('? extends ', ''))
    if arr:
        return {'type': 'array', 'items': model(base, depth, seen)}
    if base == 'java.util.Optional':
        r = model(arg, depth, seen); r['optional'] = True; return r
    if base in ('java.util.List', 'java.util.Set', 'java.util.Collection', 'java.util.SortedSet'):
        return {'type': 'array', 'items': model(arg or 'java.lang.Object', depth, seen)}
    if base in ('java.util.Map', 'java.util.SortedMap'):
        val = arg.split(',', 1)[-1] if arg and ',' in arg else 'java.lang.Object'
        return {'type': 'map', 'values': model(val, depth, seen)}
    if base in PRIM:
        return {'type': PRIM[base]}
    if not base.startswith('com.ubnt') or depth > 8 or base in seen:
        return {'type': 'ref', 'class': base}
    seen = seen | {base}
    txt = jp(base)
    if ' extends java.lang.Enum<' in txt:
        return {'type': 'enum', 'values': list(dict.fromkeys(enum_info(base).values()))}
    out = {'type': 'object', 'fields': {}}
    disc = re.search(r'JsonTypeInfo\((.*?)\n\s+\)', txt, re.S)
    if disc:
        p = re.search(r'property="([^"]*)"', disc.group(1))
        out['discriminator'] = p.group(1) if p else None
    subs = re.findall(r'JsonSubTypes\$Type\(\s*value=class L([\w/$]+);\s*name="([^"]*)"', txt)
    for sig, body in members(txt):
        jpv = re.search(r'JsonProperty\(\s*value="([^"]*)"', body)
        m = re.match(r'(?:public |protected |abstract |default |static |final |synchronized )*([\w.$<>, \[\]?]+) (\w+)\(\);$', sig)
        if not jpv or not m or re.search(r'annotation\.JsonIgnore(\n|\(\s*value=true)', body):
            continue
        f = annotate(model(m.group(1), depth + 1, seen), body)
        if 'Value$Default' in body:
            f['default'] = default_of(base, m.group(2))
        out['fields'][jpv.group(1)] = f
    pojo = {}
    pojo_fields(base, pojo, depth, seen)   # field-level annotations, even when getters also carry @JsonProperty
    for k, f in pojo.items():
        if k not in out['fields']:
            out['fields'][k] = f
        else:
            g = out['fields'][k]
            for flag in ('required', 'readOnly', 'writeOnly'):
                if f.get(flag): g[flag] = True
            if f.get('constraints'): g['constraints'] = {**f['constraints'], **g.get('constraints', {})}
    if subs:
        out['variants'] = {n: model(c.replace('/', '.'), depth + 1, seen) for c, n in subs}
        d = out.get('discriminator')
        if d and d not in out['fields']:   # the type id rides in a property no field declares
            out['fields'][d] = {'type': 'enum', 'values': [n for _, n in subs], 'required': True}
    return out

def annotate(f, body):
    if re.search(r'constraints\.(NotNull|NotBlank|NotEmpty)\b', body) or 'RequiredMode;.REQUIRED' in body \
            or re.search(r'media\.Schema\([^)]*required=true', body, re.S):
        f['required'] = True
    if 'Access;.READ_ONLY' in body or 'AccessMode;.READ_ONLY' in body:
        f['readOnly'] = True
    if 'Access;.WRITE_ONLY' in body or 'AccessMode;.WRITE_ONLY' in body:
        f['writeOnly'] = True
    for cn, a in re.findall(r'(?:validation\.constraints|hibernate\.validator\.constraints)\.(\w+)\((.*?)\n\s+\)', body, re.S):
        if cn not in ('NotNull', 'NotBlank', 'NotEmpty'):
            f.setdefault('constraints', {})[cn] = re.sub(r'\s+', ' ', a).strip()
    for cn in re.findall(r'validation\.constraints\.(NotEmpty|NotBlank)\b', body):
        f.setdefault('constraints', {})[cn] = ''
    for ann in set(re.findall(r'\n\s+(com\.ubnt[\w.$]+)(?:\(|\n)', body)):
        msg = custom_constraint(ann)
        if msg:
            f.setdefault('constraints', {})['custom'] = msg
    return f

_custom = {}
def custom_constraint(ann):
    """A project annotation meta-annotated with @jakarta.validation.Constraint: return its message."""
    if ann not in _custom:
        t = jp(ann)
        msg = None
        if 'jakarta.validation.Constraint(' in t or 'jakarta/validation/Constraint;' in t:
            m = re.search(r'java\.lang\.String message\(\);.*?AnnotationDefault:\s*\n\s*default_value: s#\d+\s*\n\s*"([^"]*)"', t, re.S)
            msg = m.group(1) if m else 'custom constraint ' + ann.rsplit('.', 1)[-1]
        _custom[ann] = msg
    return _custom[ann]

def pojo_fields(cls, out, depth, seen):
    """Lombok/POJO style: instance fields keep their real names; Jackson uses them (or @JsonProperty)
    when a getter exists. Superclass fields are inherited."""
    txt = jp(cls)
    sup = re.search(r'\n(?:public |abstract |final )*class [\w.$]+ extends (com\.ubnt[\w.$]+)', txt)
    if sup:
        pojo_fields(sup.group(1), out, depth, seen)
    getters = set(re.findall(r'\n  public [^\n(]* ((?:get|is)\w+)\(\);', txt))
    for sig, body in members(txt):
        m = re.match(r'(?:private |protected |public )(?:final )?(?!static)([\w.$<>, \[\]?]+) (\w+);$', sig)
        if not m or ' static ' in ' ' + sig:
            continue
        name = m.group(2)
        has_getter = ('get' + name[:1].upper() + name[1:]) in getters or ('is' + name[:1].upper() + name[1:]) in getters
        if not has_getter and 'JsonProperty(' not in body:   # obfuscated getters: trust @JsonProperty
            continue
        if re.search(r'annotation\.JsonIgnore(\n|\(\s*value=true)', body):
            continue
        jpv = re.search(r'JsonProperty\(\s*value="([^"]*)"', body)
        out[jpv.group(1) if jpv else name] = annotate(model(m.group(1), depth + 1, seen), body)

# ---------- routes ----------
def handler(verb, path):
    ctrl = next((c for v, p, c in ROUTE_TABLE if v == verb and p == path), None)
    if not ctrl:
        return None, None
    txt = jp(ctrl)
    ann = verb.capitalize() + 'Mapping('
    for sig, body in members(txt):
        if ann not in body:
            continue
        vals = [s.strip().strip('"') for v in re.findall(r'(?:value|path)=\[([^\]]*)\]', body.split(ann, 1)[1].split(')', 1)[0]) for s in v.split(',')] or ['']
        if not any(path.endswith(x) for x in vals if x) and vals != ['']:
            continue
        if vals == [''] and not any(p == path for v2, p, c in ROUTE_TABLE if c == ctrl and v2 == verb):
            continue
        return ctrl, (sig, body)
    return ctrl, None

def body_type(verb, path):
    ctrl, h = handler(verb, path)
    if not h:
        return None
    sig, body = h
    params = [p.strip() for p in re.search(r'\((.*)\)', sig).group(1).split(', ')] if '()' not in sig else []
    gsig = re.search(r'Signature: #\d+\s+// .*', body)
    pa = re.search(r'RuntimeVisibleParameterAnnotations:(.*?)(?=\n    [A-Z]|\Z)', body, re.S)
    if pa:
        for idx, chunk in re.findall(r'parameter (\d+):(.*?)(?=parameter \d+:|\Z)', pa.group(1), re.S):
            if 'RequestBody' in chunk and int(idx) < len(params):
                return params[int(idx)]
    return None

def return_type(verb, path):
    ctrl, h = handler(verb, path)
    if not h:
        return None
    m = re.match(r'(?:public |protected |abstract |default |static |final |synchronized )*([\w.$<>, \[\]?]+) \w+\(', h[0])
    t = m.group(1) if m else None
    if t and t.startswith('org.springframework.http.ResponseEntity<'):
        t = t[len('org.springframework.http.ResponseEntity<'):-1]
    return None if t in (None, 'void') else t

def route_model(verb, path):
    bt = body_type(verb, path)
    return {'route': f'{verb} {path}', 'body': bt, 'model': model(bt) if bt else None}

def flat(m, pre=''):
    out = {}
    if not m:
        return out
    if m.get('type') == 'array':
        return flat(m.get('items'), pre + '[]')
    if m.get('type') == 'object':
        for k, v in m.get('fields', {}).items():
            out[pre + k] = v; out.update(flat(v, pre + k + '.'))
        for vm in (m.get('variants') or {}).values():
            for k, v in flat(vm, pre).items():
                out.setdefault(k, v)
    return out

def crud(prefix):
    verbs = {}
    for v, p, c in ROUTE_TABLE:
        verbs.setdefault(p, set()).add(v)
    kinds = []
    for p, vs in sorted(verbs.items()):
        if not p.startswith(prefix) or re.search(r'\{[^}]+\}$', p):
            continue
        items = [q for q in verbs if re.fullmatch(re.escape(p) + r'/\{[^}]+\}', q)]
        iv = set().union(*(verbs[q] for q in items)) if items else set()
        create = p if 'POST' in vs else (p + '/create' if 'POST' in verbs.get(p + '/create', ()) else None)
        if not create or not ({'PUT', 'DELETE'} & iv):
            continue
        item = items[0] if items else None
        req = route_model('POST', create)
        upd = route_model('PUT', item) if item and 'PUT' in iv else None
        rt = (return_type('GET', item) if item and 'GET' in iv else None) or return_type('POST', create)
        resp = model(rt) if rt else None
        if resp and resp.get('type') == 'array':
            resp = resp['items']
        reqf, respf = flat(req['model']), flat(resp)
        kinds.append({
            'collection': p[len(prefix):], 'create': 'POST ' + create, 'item': item,
            'verbs': ''.join(x if x2 else '-' for x, x2 in (('C', True), ('R', 'GET' in vs | iv), ('U', 'PUT' in iv), ('D', 'DELETE' in iv))),
            'request': req, 'update': upd, 'response': {'type': rt, 'model': resp},
            'readOnly': sorted(set(respf) - set(reqf)),
        })
    return kinds

if __name__ == '__main__':
    init(CP, ROUTES)
    if sys.argv[3] == '--crud':
        print(json.dumps(crud(sys.argv[4] if len(sys.argv) > 4 else '/api/site/{siteName}/'), indent=1))
    else:
        print(json.dumps(route_model(sys.argv[3], sys.argv[4]), indent=1))
