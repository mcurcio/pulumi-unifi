# Parse `javap -v -p` output; emit VERB<TAB>path<TAB>class for each Spring MVC mapping (class prefix + method path).
# usage: xargs -a mapping-classes.txt javap -v -p -cp <classes> > mapping.javap; python3 -I routes.py mapping.javap
import re, sys
txt = open(sys.argv[1], encoding='utf-8', errors='replace').read()
ANN = re.compile(r'org\.springframework\.web\.bind\.annotation\.(Get|Post|Put|Delete|Patch|Request)Mapping\(\s*(.*?)\n\s*\)', re.S)
for block in re.split(r'\nClassfile ', txt):
    m = re.search(r'\n(?:public |final |abstract )*(?:class|interface) ([\w.$]+)', block)
    cname = m.group(1) if m else '?'
    # class-level annotations appear after the last method, at end of block ("RuntimeVisibleAnnotations" with no indentation of methods)
    # Heuristic: split methods by lines starting with two spaces and a signature ending in ');'
    parts = re.split(r'\n  (?=\S[^\n]*\);\n)', block)
    head_prefix = ['']
    tail = parts[-1]
    # class-level annotations are in the trailing section after "SourceFile"/"InnerClasses" etc.
    cls_tail = tail.split('\n}\n')[-1] if '\n}\n' in tail else ''
    for am in ANN.finditer(cls_tail):
        if am.group(1) == 'Request':
            v = re.search(r'(?:value|path)=\[([^\]]*)\]', am.group(2))
            if v: head_prefix = [s.strip().strip('"') for s in v.group(1).split(',')]
    for p in parts[1:]:
        body = p.split('\n}\n')[0] if p is tail else p
        for am in ANN.finditer(body):
            kind, args = am.groups()
            v = re.search(r'(?:value|path)=\[([^\]]*)\]', args)
            paths = [s.strip().strip('"') for s in v.group(1).split(',')] if v else ['']
            meth = re.search(r'method=\[([^\]]*)\]', args)
            verb = kind.upper() if kind != 'Request' else (re.sub(r'.*\.', '', meth.group(1)) if meth else 'ANY')
            for pre in head_prefix:
                for pa in paths:
                    print(f'{verb}\t{pre}{pa}\t{cname}')
