// Package fakectl is an in-memory stand-in for the Network application's v2
// API, for Tier-1 tests of the private binding, engine and dispatcher. It
// records every request so tests can assert request shapes.
package fakectl

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
)

// Coll is one collection.
type Coll struct {
	Path       string // collection (create) path, e.g. "things"
	ListPath   string // GET path for the list; defaults to Path
	IDField    string
	ItemGet    bool // false: GET <path>/{id} answers 405
	CreateList bool // POST takes and returns a list
	// OnCreate may fill controller-side fields into a new object.
	OnCreate func(obj map[string]any)
	objs     map[string]map[string]any
	next     int
}

// Request is one recorded request.
type Request struct {
	Method, Path, APIKey string
	Body                 any
}

// Server is the fake controller.
type Server struct {
	*httptest.Server
	Site string
	// NetworkVersion is what classic stat/sysinfo reports (default
	// "0.0.0-test", the synthetic surface's pin).
	NetworkVersion string
	// RequireKey, when set, answers 401 to any request without this
	// X-API-Key (the authenticated edge after the wizard).
	RequireKey string
	mu   sync.Mutex
	cols map[string]*Coll
	reqs []Request
}

// New starts a fake controller serving the collections under
// /v2/api/site/<site>/.
func New(site string, cols ...*Coll) *Server {
	s := &Server{Site: site, NetworkVersion: "0.0.0-test", cols: map[string]*Coll{}}
	for _, c := range cols {
		if c.ListPath == "" {
			c.ListPath = c.Path
		}
		c.objs = map[string]map[string]any{}
		s.cols[c.Path] = c
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Put stores an object as the controller holds it (bypassing the API).
func (s *Server) Put(path string, obj map[string]any) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cols[path]
	id, _ := obj[c.IDField].(string)
	if id == "" {
		c.next++
		id = fmt.Sprintf("%s-%04d", strings.ReplaceAll(path, "/", "-"), c.next)
		obj[c.IDField] = id
	}
	c.objs[id] = clone(obj)
	return id
}

// Object returns the stored object (a copy), or nil.
func (s *Server) Object(path, id string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.cols[path].objs[id]
	if o == nil {
		return nil
	}
	return clone(o)
}

// Count is the number of stored objects in a collection.
func (s *Server) Count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cols[path].objs)
}

// Requests returns the recorded requests and clears the log.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.reqs
	s.reqs = nil
	return out
}

func clone(o map[string]any) map[string]any {
	b, _ := json.Marshal(o)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
	}
	var logged any // a separate copy: the handler mutates body
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &logged)
	}
	s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, APIKey: r.Header.Get("X-API-Key"), Body: logged})
	if s.RequireKey != "" && r.Header.Get("X-API-Key") != s.RequireKey {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if r.URL.Path == "/api/s/"+s.Site+"/stat/sysinfo" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"meta": map[string]any{"rc": "ok"},
			"data": []any{map[string]any{"version": s.NetworkVersion}}})
		return
	}
	base := "/v2/api/site/" + s.Site + "/"
	if !strings.HasPrefix(r.URL.Path, base) {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, base)
	for _, c := range s.cols {
		if rel == c.ListPath && r.Method == http.MethodGet {
			ids := make([]string, 0, len(c.objs))
			for id := range c.objs {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			list := make([]any, 0, len(ids))
			for _, id := range ids {
				list = append(list, c.objs[id])
			}
			writeJSON(w, http.StatusOK, list)
			return
		}
		if rel == c.Path && r.Method == http.MethodPost {
			objs := []any{body}
			if c.CreateList {
				l, ok := body.([]any)
				if !ok {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "want a list"})
					return
				}
				objs = l
			}
			var out []any
			for _, o := range objs {
				m, ok := o.(map[string]any)
				if !ok {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "want an object"})
					return
				}
				c.next++
				id := fmt.Sprintf("%s-%04d", strings.ReplaceAll(c.Path, "/", "-"), c.next)
				m[c.IDField] = id
				if c.OnCreate != nil {
					c.OnCreate(m)
				}
				c.objs[id] = clone(m)
				out = append(out, c.objs[id])
			}
			if c.CreateList {
				writeJSON(w, http.StatusOK, out)
			} else {
				writeJSON(w, http.StatusOK, out[0])
			}
			return
		}
		if id, ok := strings.CutPrefix(rel, c.Path+"/"); ok && !strings.Contains(id, "/") {
			obj, exists := c.objs[id]
			switch r.Method {
			case http.MethodGet:
				if !c.ItemGet {
					writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
					return
				}
				if !exists {
					writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
					return
				}
				writeJSON(w, http.StatusOK, obj)
			case http.MethodPut:
				if !exists {
					writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
					return
				}
				m, ok := body.(map[string]any)
				if !ok {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "want an object"})
					return
				}
				m[c.IDField] = id
				c.objs[id] = clone(m) // a full replace, like the controller's PUT
				writeJSON(w, http.StatusOK, c.objs[id])
			case http.MethodDelete:
				if !exists {
					writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
					return
				}
				delete(c.objs, id)
				w.WriteHeader(http.StatusOK)
			default:
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			}
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "no route"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
