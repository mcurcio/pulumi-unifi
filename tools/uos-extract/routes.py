# Route table from Spring MVC annotations in (obfuscated) controller bytecode.
#
#   python3 -I routes.py <classes-dir> > routes.tsv      # VERB<TAB>path<TAB>controller-class
#
# Class-level prefixes come from @RequestMapping on the class or from a custom annotation that
# is itself meta-annotated with @RequestMapping (how the Integration controllers get /v1/sites/{siteId}).
# Needs a JDK `javap` on PATH. Prototype: parses `javap -v` text, not class files.
import os, re, subprocess, sys

CP = sys.argv[1]
ENV = dict(os.environ, JAVA_TOOL_OPTIONS='', _JAVA_OPTIONS='')
MAPPING = re.compile(rb'org/springframework/web/bind/annotation/(Get|Post|Put|Delete|Patch|Request)Mapping')
ANN = re.compile(r'\n\s+([\w.$]+)(?:\((.*?)\n\s+\))?(?=\n)', re.S)
VERBS = {'Get': 'GET', 'Post': 'POST', 'Put': 'PUT', 'Delete': 'DELETE', 'Patch': 'PATCH'}

def javap(classes):
    out = subprocess.run(['javap', '-v', '-p', '-cp', CP, *classes], capture_output=True, text=True, env=ENV).stdout
    return {m.group(1): m.group(2) for m in re.finditer(
        r'(?:^|\n)Classfile [^\n]*\n(?:.*?\n)??(?:public |final |abstract )*(?:class|interface|@interface) ([\w.$]+)(.*?)(?=\nClassfile |\Z)', out, re.S)}

def strings(args, key):
    m = re.search(key + r'=\[([^\]]*)\]', args or '')
    return [s.strip().strip('"') for s in m.group(1).split(',')] if m else []

def annotations(section):
    """[(annotation type, args)] from a RuntimeVisibleAnnotations block."""
    return [(t, a or '') for t, a in ANN.findall(section) if '.' in t]

def class_section(body):
    tail = body.rsplit('\n}\n', 1)[-1]
    m = re.search(r'\nRuntimeVisibleAnnotations:\n(.*?)(?=\n[A-Z]\w+:|\Z)', '\n' + tail, re.S)
    return m.group(1) if m else ''

def main():
    files = []
    for root, _, names in os.walk(CP):
        for n in names:
            if n.endswith('.class'):
                p = os.path.join(root, n)
                with open(p, 'rb') as f:
                    if MAPPING.search(f.read()):
                        files.append(os.path.relpath(p, CP)[:-6].replace(os.sep, '.'))
    bodies = {}
    for i in range(0, len(files), 200):
        bodies.update(javap(files[i:i + 200]))
    meta = {}
    def prefixes_of(ann_type, args):
        if ann_type.endswith('annotation.RequestMapping'):
            return strings(args, '(?:value|path)')
        if not ann_type.startswith('com.ubnt'):
            return []
        if ann_type not in meta:
            b = javap([ann_type]).get(ann_type, '')
            meta[ann_type] = [p for t, a in annotations(class_section(b)) for p in prefixes_of(t, a)]
        return meta[ann_type]
    for cls, body in sorted(bodies.items()):
        pre = [p for t, a in annotations(class_section(body)) for p in prefixes_of(t, a)] or ['']
        for m in re.finditer(r'\n  \S[^\n]*\);\n(.*?)(?=\n  \S|\n}\n|\Z)', body, re.S):
            for t, a in annotations(m.group(1)):
                k = re.search(r'annotation\.(Get|Post|Put|Delete|Patch|Request)Mapping$', t)
                if not k:
                    continue
                verbs = [VERBS[k.group(1)]] if k.group(1) != 'Request' else \
                    [v.rsplit('.', 1)[-1] for v in strings(a, 'method')] or ['ANY']
                for p in pre:
                    for s in strings(a, '(?:value|path)') or ['']:
                        for v in verbs:
                            print(f'{v}\t{p}{s}\t{cls}')

main()
