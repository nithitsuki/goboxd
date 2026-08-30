// Per-client-IP admission throttle for POST /run.
//
// goboxd sits behind the Cloudflare Tunnel, so every request's RemoteAddr is
// the local tunnel relay — useless as a rate-limit key. The real client IP is
// the CF-Connecting-IP header Cloudflare injects at the edge, which is the
// only path into this container (loopback publish). RemoteAddr is the
// fallback for direct local calls.
//
// This is the last-resort backstop. The primary controls are the per-user
// limit in the Code Royale frontend and the per-IP rule at the Cloudflare
// edge; this limiter exists so a caller that bypasses both (direct loopback
// access, or an edge misconfiguration) still cannot saturate the bounded
// concurrency gate.
//
// Environment (read once on first use; changing them requires a restart
// because the token buckets are sized from these values):
//   - GOBOXD_RATE_LIMIT_RPS: sustained /run requests per second per client IP.
//     Unset or <= 0 disables the limiter (development/integration default).
//   - GOBOXD_RATE_BURST: allowed burst capacity. Defaults to 2 × RPS.
package api

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rateLimitCfg is the loaded throttle configuration.
type rateLimitCfg struct {
	rps   float64 // sustained requests per second per client IP
	burst int     // allowed burst capacity in tokens
}

var (
	rateLimitOnce sync.Once
	rateLimitConf rateLimitCfg
)

func loadRateLimit() rateLimitCfg {
	raw := strings.TrimSpace(os.Getenv("GOBOXD_RATE_LIMIT_RPS"))
	if raw == "" {
		return rateLimitCfg{}
	}
	rps, err := strconv.ParseFloat(raw, 64)
	if err != nil || rps <= 0 {
		return rateLimitCfg{}
	}
	burst := int(rps) * 2
	if b, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GOBOXD_RATE_BURST"))); err == nil && b > 0 {
		burst = b
	}
	return rateLimitCfg{rps: rps, burst: burst}
}

// clientIP returns the caller's IP: the edge-set CF-Connecting-IP when
// present, otherwise the RemoteAddr host.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		if h, _, err := net.SplitHostPort(ip); err == nil {
			return h
		}
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipBucket is a token-bucket reservation for a single client IP.
type ipBucket struct {
	tokens  float64
	lastRef time.Time
}

// rateLimiter is a concurrent per-IP token bucket. Stale buckets are pruned
// when they have been idle for a full window, so memory stays proportional to
// recently active callers.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
}

const bucketIdleTTL = 10 * time.Minute

// allow consumes one token for ip, refilling at the configured rate. It
// reports false when the burst capacity is exhausted.
func (rl *rateLimiter) allow(cfg rateLimitCfg, ip string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Prune idle buckets so a long-lived server cannot accumulate one entry
	// per client IP forever.
	for k, b := range rl.buckets {
		if now.Sub(b.lastRef) > bucketIdleTTL {
			delete(rl.buckets, k)
		}
	}

	b, ok := rl.buckets[ip]
	if !ok {
		b = &ipBucket{tokens: float64(cfg.burst), lastRef: now}
		rl.buckets[ip] = b
	}
	elapsed := now.Sub(b.lastRef).Seconds()
	if elapsed > 0 {
		b.tokens += cfg.rps * elapsed
		if b.tokens > float64(cfg.burst) {
			b.tokens = float64(cfg.burst)
		}
		b.lastRef = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// newRateLimitMiddleware builds the throttle for a fixed configuration. Split
// out so tests can drive it without touching the process environment.
func newRateLimitMiddleware(cfg rateLimitCfg, next http.Handler) http.Handler {
	if cfg.rps <= 0 {
		return next
	}
	rl := &rateLimiter{buckets: make(map[string]*ipBucket)}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/run" {
			if !rl.allow(cfg, clientIP(r), time.Now()) {
				w.Header().Set("Retry-After", "1")
				writeErrorStatus(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimitMiddleware throttles POST /run per client IP. It is a no-op until
// GOBOXD_RATE_LIMIT_RPS is set, which keeps development and integration runs
// behaviorally identical to the historical open behavior.
func RateLimitMiddleware(next http.Handler) http.Handler {
	rateLimitOnce.Do(func() { rateLimitConf = loadRateLimit() })
	return newRateLimitMiddleware(rateLimitConf, next)
}
