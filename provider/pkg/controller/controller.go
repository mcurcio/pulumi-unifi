// Package controller is the controller-access layer (docs/design/private-binding.md
// §S2.2, L1): one HTTP client per configured controller (TLS policy, the
// X-API-Key header, 429 retry), the endpoint roots derived from apiHost, and the
// site resolver. Bindings build on it; it knows nothing about kinds, envelopes
// or lifecycles.
package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// IntegrationBasePath is where the edge serves the official Integration API;
// spec paths (/v1/...) are relative to it.
const IntegrationBasePath = "/proxy/network/integration"

// Client calls one controller. The zero value is not usable; use New.
type Client struct {
	// Host is apiHost: a bare host[:port] reached over https.
	Host string
	// apiKey is sent as X-API-Key. Never logged, never in an error.
	apiKey string
	http   *http.Client
	// Sleep waits between 429 retries (tests replace it).
	Sleep func(time.Duration)
	// MaxRetries is the number of retries after a 429.
	MaxRetries int
}

// Config is what the provider configuration gives the controller layer.
type Config struct {
	APIHost       string
	APIKey        string
	AllowInsecure bool
	// Transport overrides the TLS policy (tests); nil uses the default.
	Transport http.RoundTripper
}

// New builds a client. With AllowInsecure the transport skips TLS
// verification (self-signed controller certificates); otherwise the system
// trust store (or SSL_CERT_FILE) applies.
func New(c Config) *Client {
	rt := c.Transport
	if rt == nil {
		rt = transport(c.AllowInsecure)
	}
	return &Client{Host: c.APIHost, apiKey: c.APIKey, http: &http.Client{Timeout: 60 * time.Second, Transport: rt},
		Sleep: time.Sleep, MaxRetries: 3}
}

func transport(insecure bool) *http.Transport {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if insecure {
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- opt-in allowInsecure for self-signed controller certs
	}
	return t
}

// HasKey reports whether an API key is configured.
func (c *Client) HasKey() bool { return c.apiKey != "" }

// IntegrationRoot is the official API root, e.g.
// https://192.168.1.1/proxy/network/integration.
func (c *Client) IntegrationRoot() (string, error) {
	if c.Host == "" {
		return "", fmt.Errorf("apiHost is not set (set unifi:apiHost or the UNIFI_API_HOST env var)")
	}
	return "https://" + c.Host + IntegrationBasePath, nil
}

// Error is a non-2xx answer: the status and a short server message (never an
// HTML page, never the request headers).
type Error struct {
	Method, URL string
	Status      int
	// Code is the server's error code when the body carries one
	// (e.g. api.acl-rule.ordering.not-all-acl-rules-specified).
	Code    string
	Message string
}

func (e *Error) Error() string {
	msg := e.Message
	if e.Code != "" && !strings.Contains(msg, e.Code) {
		msg = e.Code + ": " + msg
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, msg)
}

// Do sends one JSON request and decodes a JSON answer into out (when out is
// non-nil and the body is non-empty). A 429 is retried up to MaxRetries times,
// honouring Retry-After (seconds, capped at 30 s).
func (c *Client) Do(ctx context.Context, method, url string, body, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = b
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.apiKey != "" {
			req.Header.Set("X-API-Key", c.apiKey)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, url, err)
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if rerr != nil {
			return fmt.Errorf("%s %s: reading the response: %w", method, url, rerr)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < c.MaxRetries {
			c.Sleep(retryAfter(resp.Header.Get("Retry-After"), attempt))
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return newError(method, url, resp.StatusCode, raw)
		}
		if out == nil || len(bytes.TrimSpace(raw)) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: decoding the response: %w", method, url, err)
		}
		return nil
	}
}

func retryAfter(h string, attempt int) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && s >= 0 {
		if s > 30 {
			s = 30
		}
		return time.Duration(s) * time.Second
	}
	return time.Duration(1<<attempt) * time.Second
}

// newError keeps the server's code and message from a JSON error body; a non-
// JSON body (an HTML error page) is reduced to its status text.
func newError(method, url string, status int, raw []byte) *Error {
	e := &Error{Method: method, URL: url, Status: status}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' && json.Unmarshal(trimmed, &body) == nil {
		e.Code, e.Message = body.Code, body.Message
		if e.Message == "" && body.Error != nil {
			e.Message = body.Error.Message
		}
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
		if len(trimmed) > 0 && trimmed[0] != '<' && len(trimmed) <= 300 {
			e.Message = string(trimmed)
		}
	}
	return e
}
