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

// NetworkBasePath is where the edge serves the Network application itself:
// the private v2 (/v2/api/site/{name}/...) and classic (/api/s/{name}/...)
// APIs (docs/design/private-binding.md §S2.6).
const NetworkBasePath = "/proxy/network"

// Client calls one controller. The zero value is not usable; use New.
type Client struct {
	// Host is apiHost: a bare host[:port] reached over https.
	Host string
	// networkBase overrides the Network root (networkBaseUrl: the Network app
	// reached directly, e.g. http://127.0.0.1:8081 before the wizard).
	networkBase string
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
	// NetworkBaseURL overrides the Network root (the networkBaseUrl config
	// key); empty means the edge, https://<APIHost>/proxy/network.
	NetworkBaseURL string
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
	return &Client{Host: c.APIHost, networkBase: strings.TrimRight(c.NetworkBaseURL, "/"), apiKey: c.APIKey, http: &http.Client{Timeout: 60 * time.Second, Transport: rt},
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

// NetworkRoot is the Network application root the private bindings append
// their API paths to: the networkBaseUrl override, else the edge,
// https://<apiHost>/proxy/network.
func (c *Client) NetworkRoot() (string, error) {
	if c.networkBase != "" {
		return c.networkBase, nil
	}
	if c.Host == "" {
		return "", fmt.Errorf("the Network application is not reachable: set unifi:apiHost (the edge, https://<apiHost>/proxy/network) or unifi:networkBaseUrl")
	}
	return "https://" + c.Host + NetworkBasePath, nil
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

// Send sends one request and returns the status and body of the answer,
// whatever the status: the caller maps statuses to outcomes. It sets the
// X-API-Key header when a key is configured, and retries a 429 up to
// MaxRetries times, honouring Retry-After (seconds, capped at 30 s). Only a
// transport failure is an error; it never carries the key.
func (c *Client) Send(ctx context.Context, method, url string, body any) (int, []byte, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		payload = b
	}
	for attempt := 0; ; attempt++ {
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, rd)
		if err != nil {
			return 0, nil, err
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
			return 0, nil, fmt.Errorf("%s %s: %w", method, url, err)
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if rerr != nil {
			return resp.StatusCode, nil, fmt.Errorf("%s %s: reading the response: %w", method, url, rerr)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < c.MaxRetries {
			c.Sleep(retryAfter(resp.Header.Get("Retry-After"), attempt))
			continue
		}
		return resp.StatusCode, raw, nil
	}
}

// Do sends one JSON request (through Send) and decodes a JSON answer into out
// (when out is non-nil and the body is non-empty). A non-2xx answer is an
// *Error.
func (c *Client) Do(ctx context.Context, method, url string, body, out any) error {
	status, raw, err := c.Send(ctx, method, url, body)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return newError(method, url, status, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decoding the response: %w", method, url, err)
	}
	return nil
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
