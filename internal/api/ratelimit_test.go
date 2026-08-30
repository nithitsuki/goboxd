// Tests for the per-client-IP /run throttle (internal/api/ratelimit.go).
package api

import (
	"net/http"
	"net/url"
	"testing"
)

// okHandler is the terminal handler every middleware test delegates to.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// captureRecorder is a minimal header-capturing recorder; it never buffers a
// body, matching how the real handlers stream responses.
type captureRecorder struct {
	statusCode int
	header     http.Header
}

func (c *captureRecorder) Header() http.Header         { return c.header }
func (c *captureRecorder) WriteHeader(status int)      { c.statusCode = status }
func (c *captureRecorder) Write(b []byte) (int, error) { return len(b), nil }

// doRequest drives h with a synthetic request and returns the status + headers.
func doRequest(h http.Handler, method, path string, mutate func(*http.Request)) (int, http.Header) {
	req := &http.Request{
		Method:     method,
		URL:        &url.URL{Path: path},
		Header:     make(http.Header),
		RemoteAddr: "203.0.113.9:1234",
	}
	if mutate != nil {
		mutate(req)
	}
	rec := &captureRecorder{statusCode: 200, header: make(http.Header)}
	h.ServeHTTP(rec, req)
	return rec.statusCode, rec.Header()
}

func TestRateLimitDisabledWhenUnset(t *testing.T) {
	// A zero config must be a transparent passthrough, preserving the
	// historical open behavior for dev/integration runs.
	h := newRateLimitMiddleware(rateLimitCfg{}, okHandler())
	for i := 0; i < 100; i++ {
		if status, _ := doRequest(h, http.MethodPost, "/run", nil); status != http.StatusOK {
			t.Fatalf("disabled limiter request %d: got %d, want 200", i, status)
		}
	}
}

func TestRateLimitCapsBurst(t *testing.T) {
	cfg := rateLimitCfg{rps: 0.01, burst: 3} // refill is negligible inside the test
	h := newRateLimitMiddleware(cfg, okHandler())

	accepted, rejected := 0, 0
	for i := 0; i < 10; i++ {
		status, _ := doRequest(h, http.MethodPost, "/run", nil)
		switch status {
		case http.StatusOK:
			accepted++
		case http.StatusTooManyRequests:
			rejected++
		}
	}
	if accepted != 3 {
		t.Errorf("accepted %d requests, want 3 (the full burst capacity)", accepted)
	}
	if rejected == 0 {
		t.Error("expected at least one 429 after the burst was consumed")
	}
}

func TestRateLimitKeyedPerClientIP(t *testing.T) {
	cfg := rateLimitCfg{rps: 0.01, burst: 1}
	h := newRateLimitMiddleware(cfg, okHandler())

	// Two distinct CF-Connecting-IP values each get their own bucket.
	if status, _ := doRequest(h, http.MethodPost, "/run", func(r *http.Request) {
		r.Header.Set("Cf-Connecting-Ip", "198.51.100.1")
	}); status != http.StatusOK {
		t.Fatalf("client A first request: got %d, want 200", status)
	}
	if status, _ := doRequest(h, http.MethodPost, "/run", func(r *http.Request) {
		r.Header.Set("Cf-Connecting-Ip", "198.51.100.2")
	}); status != http.StatusOK {
		t.Fatalf("client B first request: got %d, want 200 (independent bucket)", status)
	}
	// Client A is now out of budget.
	if status, _ := doRequest(h, http.MethodPost, "/run", func(r *http.Request) {
		r.Header.Set("Cf-Connecting-Ip", "198.51.100.1")
	}); status != http.StatusTooManyRequests {
		t.Fatalf("client A second request: got %d, want 429", status)
	}
}

func TestRateLimitOnlyThrottlesRun(t *testing.T) {
	cfg := rateLimitCfg{rps: 0.0001, burst: 1}
	h := newRateLimitMiddleware(cfg, okHandler())

	// Read-only paths are never throttled, even after the bucket is spent.
	if status, _ := doRequest(h, http.MethodPost, "/run", nil); status != http.StatusOK {
		t.Fatalf("/run first request: got %d, want 200", status)
	}
	for i := 0; i < 5; i++ {
		if status, _ := doRequest(h, http.MethodGet, "/info", nil); status != http.StatusOK {
			t.Fatalf("GET /info request %d: got %d, want 200", i, status)
		}
	}
}

func TestRateLimit429CarriesRetryAfter(t *testing.T) {
	cfg := rateLimitCfg{rps: 0.01, burst: 1}
	h := newRateLimitMiddleware(cfg, okHandler())

	doRequest(h, http.MethodPost, "/run", nil)
	_, hdr := doRequest(h, http.MethodPost, "/run", nil)
	if got := hdr.Get("Retry-After"); got == "" {
		t.Error("429 response must carry a Retry-After header")
	}
}

func TestLoadRateLimitDefaults(t *testing.T) {
	t.Setenv("GOBOXD_RATE_LIMIT_RPS", "")
	if cfg := loadRateLimit(); cfg.rps != 0 {
		t.Errorf("unset env: got rps %v, want 0 (disabled)", cfg.rps)
	}
	t.Setenv("GOBOXD_RATE_LIMIT_RPS", "2")
	if cfg := loadRateLimit(); cfg.rps != 2 || cfg.burst != 4 {
		t.Errorf("rps=2: got %+v, want rps 2 burst 4 (2x default)", cfg)
	}
	t.Setenv("GOBOXD_RATE_BURST", "7")
	if cfg := loadRateLimit(); cfg.burst != 7 {
		t.Errorf("explicit burst: got %d, want 7", cfg.burst)
	}
	t.Setenv("GOBOXD_RATE_LIMIT_RPS", "not-a-number")
	if cfg := loadRateLimit(); cfg.rps != 0 {
		t.Errorf("invalid rps: got %v, want 0 (disabled)", cfg.rps)
	}
}

func TestClientIPPrefersConnectingIP(t *testing.T) {
	req := &http.Request{
		RemoteAddr: "10.0.0.5:4444",
		Header:     http.Header{"Cf-Connecting-Ip": []string{"198.51.100.7"}},
	}
	if ip := clientIP(req); ip != "198.51.100.7" {
		t.Fatalf("clientIP = %q, want the CF-Connecting-IP value", ip)
	}
	if ip := clientIP(&http.Request{RemoteAddr: "10.0.0.5:4444"}); ip != "10.0.0.5" {
		t.Fatalf("clientIP fallback = %q, want the RemoteAddr host", ip)
	}
}
