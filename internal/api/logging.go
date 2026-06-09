// Package api implements the HTTP handlers, routing, request validation,
// and structured logging for the goboxd service.
package api

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

var reqIDCounter int64

// bodyRecorder wraps http.ResponseWriter to capture the status code and
// response body for structured request logging.
type bodyRecorder struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
}

func (b *bodyRecorder) WriteHeader(code int) {
	b.statusCode = code
	b.ResponseWriter.WriteHeader(code)
}

func (b *bodyRecorder) Write(data []byte) (int, error) {
	b.body.Write(data)
	return b.ResponseWriter.Write(data)
}

// LoggingMiddleware wraps an http.Handler to emit structured JSON request logs.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &bodyRecorder{ResponseWriter: w, statusCode: 200}
		rid := atomic.AddInt64(&reqIDCounter, 1)
		w.Header().Set("X-Request-Id", itoa64(rid))

		next.ServeHTTP(rec, r)

		// Extract status from POST /run response body
		runStatus := ""
		if r.Method == "POST" && r.URL.Path == "/run" && rec.statusCode == 200 {
			var resp struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(rec.body.Bytes(), &resp)
			runStatus = resp.Status
		}

		entry := struct {
			Time       string `json:"time"`
			Method     string `json:"method"`
			Path       string `json:"path"`
			Status     int    `json:"status"`
			DurationMs int64  `json:"duration_ms"`
			RunStatus  string `json:"run_status,omitempty"`
			RequestID  int64  `json:"request_id"`
		}{
			Time:       start.Format(time.RFC3339),
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     rec.statusCode,
			DurationMs: time.Since(start).Milliseconds(),
			RunStatus:  runStatus,
			RequestID:  rid,
		}

		data, _ := json.Marshal(entry)
		log.Println(string(data))
	})
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
