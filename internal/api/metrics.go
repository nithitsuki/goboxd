// Package api implements the HTTP handlers, routing, request validation,
// and structured logging for the goboxd service.
package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// latencyBucketsMs are the fixed histogram buckets for run duration (ms).
var latencyBucketsMs = []int64{50, 100, 250, 500, 1000, 2000, 5000}

// Metrics holds the live counters behind /metrics and the dashboard.
// All counters are updated from HandleRun only (single writer per request
// path, but the reads from /metrics may race: atomics and a mutex keep the
// snapshot consistent).
type Metrics struct {
	InFlight  int64 // atomic: runs currently executing
	Queued    int64 // atomic: requests blocked on the semaphore
	TotalRuns int64 // atomic: POST /run completions
	Errors    int64 // atomic: runs ending internal_error or HTTP 5xx
	statusMu  sync.Mutex
	Statuses  map[string]int64 // per-status counts
	latencyMu sync.Mutex
	Latency   map[string]int64 // bucket label -> count
	startedAt time.Time
}

var metrics = &Metrics{
	Statuses:  make(map[string]int64),
	Latency:   make(map[string]int64),
	startedAt: time.Now(),
}

// recordRun records one completed run: its top-level status and duration.
func recordRun(status string, duration time.Duration, isError bool) {
	atomic.AddInt64(&metrics.TotalRuns, 1)
	if isError {
		atomic.AddInt64(&metrics.Errors, 1)
	}
	metrics.statusMu.Lock()
	metrics.Statuses[status]++
	metrics.statusMu.Unlock()

	ms := duration.Milliseconds()
	bucket := "5000+"
	for _, b := range latencyBucketsMs {
		if ms <= b {
			bucket = "<=" + itoa(b)
			break
		}
	}
	metrics.latencyMu.Lock()
	metrics.Latency[bucket]++
	metrics.latencyMu.Unlock()
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// Snapshot returns a consistent copy of the metrics for /metrics.
func Snapshot() map[string]interface{} {
	metrics.statusMu.Lock()
	statuses := make(map[string]int64, len(metrics.Statuses))
	for k, v := range metrics.Statuses {
		statuses[k] = v
	}
	metrics.statusMu.Unlock()

	metrics.latencyMu.Lock()
	latency := make(map[string]int64, len(metrics.Latency))
	for k, v := range metrics.Latency {
		latency[k] = v
	}
	metrics.latencyMu.Unlock()

	return map[string]interface{}{
		"in_flight":            atomic.LoadInt64(&metrics.InFlight),
		"queue_depth":          atomic.LoadInt64(&metrics.Queued),
		"total_runs":           atomic.LoadInt64(&metrics.TotalRuns),
		"error_count":          atomic.LoadInt64(&metrics.Errors),
		"status_counts":        statuses,
		"latency_histogram_ms": latency,
		"uptime_s":             int64(time.Since(metrics.startedAt).Seconds()),
	}
}

// HandleMetrics serves the live metrics snapshot as JSON.
func HandleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(Snapshot())
}

//go:embed dashboard.html
var dashboardHTML []byte

// HandleDashboard serves the embedded live dashboard page. The page polls
// /metrics with vanilla JS; no external assets.
func HandleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(dashboardHTML)
}
