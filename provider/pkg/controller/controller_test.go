package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func tlsClient(t *testing.T, h http.HandlerFunc, key string) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	c := New(Config{APIHost: strings.TrimPrefix(srv.URL, "https://"), APIKey: key, AllowInsecure: true})
	c.Sleep = func(time.Duration) {}
	return c, srv
}

func TestKeyHeaderAndInsecureTLS(t *testing.T) {
	var got string
	c, _ := tlsClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}, "k1")
	root, _ := c.IntegrationRoot()
	var out map[string]bool
	if err := c.Do(context.Background(), "GET", root+"/v1/x", nil, &out); err != nil || !out["ok"] {
		t.Fatalf("Do: %v %v", err, out)
	}
	if got != "k1" {
		t.Errorf("X-API-Key = %q", got)
	}
	// Without allowInsecure the self-signed test cert is refused.
	strict := New(Config{APIHost: c.Host, APIKey: "k1"})
	if err := strict.Do(context.Background(), "GET", root+"/v1/x", nil, nil); err == nil {
		t.Error("a self-signed cert must fail without allowInsecure")
	}
}

func Test429IsRetried(t *testing.T) {
	n := 0
	c, _ := tlsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if n++; n < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}, "k")
	root, _ := c.IntegrationRoot()
	if err := c.Do(context.Background(), "GET", root+"/v1/x", nil, nil); err != nil || n != 3 {
		t.Errorf("err %v after %d calls, want success on the third", err, n)
	}
	n = -100 // always 429
	err := c.Do(context.Background(), "GET", root+"/v1/x", nil, nil)
	var ce *Error
	if !errors.As(err, &ce) || ce.Status != http.StatusTooManyRequests {
		t.Errorf("err %v, want a 429 after the retries", err)
	}
}

func TestErrorIsShortAndKeepsCode(t *testing.T) {
	c, _ := tlsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/proxy/network/integration/html" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("<html><body>" + strings.Repeat("x", 5000) + "</body></html>"))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"statusCode":400,"code":"api.request.invalid-reference","message":"unknown rule"}`))
	}, "secret-key")
	root, _ := c.IntegrationRoot()
	err := c.Do(context.Background(), "PUT", root+"/v1/x", map[string]any{"a": 1}, nil)
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != "api.request.invalid-reference" || !strings.Contains(err.Error(), "unknown rule") {
		t.Errorf("err %v", err)
	}
	err = c.Do(context.Background(), "GET", root+"/html", nil, nil)
	if err == nil || strings.Contains(err.Error(), "<html") || len(err.Error()) > 300 {
		t.Errorf("an HTML error page must be reduced to a short error, got %q", err)
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Error("the key leaked into an error")
	}
}

func TestSiteResolver(t *testing.T) {
	lists := 0
	c, _ := tlsClient(t, func(w http.ResponseWriter, r *http.Request) {
		lists++
		_, _ = w.Write([]byte(`{"data":[{"id":"8C7D8F04-0000-4000-8000-000000000001","internalReference":"default","name":"Default"}]}`))
	}, "k")
	r := &SiteResolver{C: c}
	ctx := context.Background()
	if id, err := r.UUID(ctx, "8c7d8f04-0000-4000-8000-000000000002"); err != nil || id != "8c7d8f04-0000-4000-8000-000000000002" || lists != 0 {
		t.Errorf("a UUID must be returned with no call: %s %v (%d calls)", id, err, lists)
	}
	for i := 0; i < 2; i++ {
		if id, err := r.UUID(ctx, "default"); err != nil || id != "8c7d8f04-0000-4000-8000-000000000001" {
			t.Errorf("name: %s %v", id, err)
		}
	}
	if lists != 1 {
		t.Errorf("%d site lists, want 1 (cached)", lists)
	}
	if _, err := r.UUID(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "default") {
		t.Errorf("unknown site: %v (want the known names listed)", err)
	}
}
