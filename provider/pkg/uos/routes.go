package uos

// The route table, from Spring MVC annotations in controller bytecode (a port
// of tools/uos-extract/routes.py onto the class-file parser).
//
// Class-level prefixes come from @RequestMapping on the class, or from a
// project annotation that is itself meta-annotated with @RequestMapping (how
// the Integration controllers get /v1/sites/{siteId}).

import (
	"sort"
	"strings"
)

const springAnn = "org.springframework.web.bind.annotation."

var mappingVerbs = map[string]string{
	springAnn + "GetMapping":     "GET",
	springAnn + "PostMapping":    "POST",
	springAnn + "PutMapping":     "PUT",
	springAnn + "DeleteMapping":  "DELETE",
	springAnn + "PatchMapping":   "PATCH",
	springAnn + "RequestMapping": "",
}

// Route is one verb+path served by a controller method.
type Route struct {
	Verb   string
	Path   string
	Class  string // controller binary name
	Method string // handler method name
	Desc   string // handler method descriptor
}

// Routes extracts the route table: sorted by verb, path and class, one entry
// per (verb, path, class) (the first handler in class-file order wins).
func Routes(x *Index) ([]Route, error) {
	var needles [][]byte
	for _, n := range []string{"GetMapping", "PostMapping", "PutMapping", "DeleteMapping", "PatchMapping", "RequestMapping"} {
		needles = append(needles, []byte("org/springframework/web/bind/annotation/"+n))
	}
	names, err := x.Scan(needles...)
	if err != nil {
		return nil, err
	}
	r := &routeExtractor{x: x, meta: map[string][]string{}}
	seen := map[[3]string]bool{}
	var out []Route
	for _, cn := range names {
		c := x.Class(cn)
		if c == nil {
			continue
		}
		pre := r.classPrefixes(c.Annotations)
		if len(pre) == 0 {
			pre = []string{""}
		}
		for _, m := range c.Methods {
			for _, a := range m.Annotations {
				verb, ok := mappingVerbs[a.Type]
				if !ok {
					continue
				}
				verbs := []string{verb}
				if verb == "" {
					verbs = nil
					if v := a.Elem("method"); v != nil {
						for _, e := range v.Array {
							if e.Tag == 'e' {
								verbs = append(verbs, e.EnumConst)
							}
						}
					}
					if len(verbs) == 0 {
						verbs = []string{"ANY"}
					}
				}
				vals := mappingPaths(&a)
				for _, p := range pre {
					for _, s := range vals {
						for _, v := range verbs {
							k := [3]string{v, p + s, cn}
							if seen[k] {
								continue
							}
							seen[k] = true
							out = append(out, Route{Verb: v, Path: p + s, Class: cn, Method: m.Name, Desc: m.Descriptor})
						}
					}
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Verb != b.Verb {
			return a.Verb < b.Verb
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Class < b.Class
	})
	return out, nil
}

// mappingPaths returns a mapping annotation's value= (else path=) strings, or
// [""] when it has none.
func mappingPaths(a *Annotation) []string {
	vals, ok := a.Strings("value")
	if !ok || len(vals) == 0 {
		vals, _ = a.Strings("path")
	}
	if len(vals) == 0 {
		return []string{""}
	}
	return vals
}

type routeExtractor struct {
	x    *Index
	meta map[string][]string
}

// classPrefixes resolves the path prefixes a class's annotations declare.
func (r *routeExtractor) classPrefixes(anns []Annotation) []string {
	var out []string
	for i := range anns {
		out = append(out, r.prefixesOf(&anns[i])...)
	}
	return out
}

func (r *routeExtractor) prefixesOf(a *Annotation) []string {
	if a.Type == springAnn+"RequestMapping" {
		v := mappingPaths(a)
		if len(v) == 1 && v[0] == "" {
			return nil
		}
		return v
	}
	if !strings.HasPrefix(a.Type, "com.ubnt") {
		return nil
	}
	if p, ok := r.meta[a.Type]; ok {
		return p
	}
	r.meta[a.Type] = nil // cycle guard
	var p []string
	if c := r.x.Class(a.Type); c != nil {
		p = r.classPrefixes(c.Annotations)
	}
	r.meta[a.Type] = p
	return p
}
