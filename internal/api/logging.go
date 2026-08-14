// Package api implements the HTTP handlers, routing, request validation,
// and structured logging for the goboxd service.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

// requestIDKey is the context key for the trace id.
type requestIDKey struct{}

// RequestIDFrom returns the trace id stored in the request context, or "".
func RequestIDFrom(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// RequestIDMiddleware assigns a trace id to every request: a client-supplied
// X-Request-Id is honored and echoed, otherwise a crypto-random id is
// generated. The id is stored in the context for the access logger.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			buf := make([]byte, 16)
			if _, err := rand.Read(buf); err != nil {
				id = time.Now().Format("20060102150405.000000000")
			} else {
				id = hex.EncodeToString(buf)
			}
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

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

// RecoveryMiddleware catches panics in handlers so a single bad request
// doesn't crash the entire server (which would reset all in-flight connections).
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC recovered: %v\n%s", rec, debug.Stack())
				// The ResponseWriter may have already been partially written to.
				// Try to write an internal error. If that fails, the connection
				// reset is unavoidable but at least the server stays up.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"internal server error"}}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware wraps an http.Handler to emit structured JSON request logs.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &bodyRecorder{ResponseWriter: w, statusCode: 200}
		rid := RequestIDFrom(r)

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
			RequestID  string `json:"request_id"`
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
