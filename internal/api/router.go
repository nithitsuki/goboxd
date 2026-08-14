// Package api implements the HTTP handlers, routing, request validation,
// and structured logging for the goboxd service.
//
// Routes are registered using Go 1.22's method-pattern mux syntax.
// All responses are JSON. POST /run requests are validated and dispatched
// to the runner package for sandboxed execution inside nsjail.
package api

import (
	"net/http"
	"time"
)

// NewRouter constructs and wires up the API routes with structured logging.
// Registered endpoints:
//
//	GET  /healthz     — liveness check
//	GET  /readyz      — readiness probe (checks nsjail + all languages)
//	GET  /info        — service metadata and runtime stats
//	POST /run         — execute untrusted code
//	GET  /playground           — web UI (if embedded via embed.FS)
//	GET  /testcases              — list all testcases
//	GET  /testcases/{lang}/{name} — get a specific testcase
func NewRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", HandleHealthz)
	mux.HandleFunc("GET /readyz", HandleReadyz)
	mux.HandleFunc("GET /info", HandleInfo)
	mux.HandleFunc("GET /openapi.json", HandleOpenAPI)
	mux.HandleFunc("GET /metrics", HandleMetrics)
	mux.HandleFunc("GET /dashboard", HandleDashboard)
	mux.HandleFunc("POST /run", HandleRun)
	mux.HandleFunc("GET /testcases", HandleTestcasesList)
	mux.HandleFunc("GET /testcases/{lang}/{name}", HandleTestcasesGet)
	if PlaygroundExists() {
		mux.Handle("GET /playground", http.RedirectHandler("/playground/", http.StatusMovedPermanently))
		mux.Handle("GET /playground/", http.StripPrefix("/playground", http.HandlerFunc(HandlePlayground)))
	}

	return RecoveryMiddleware(RequestIDMiddleware(LoggingMiddleware(mux)))
}

// NewServer builds the HTTP server with Slowloris mitigations: bounded header
// read time (10s), bounded total read time (60s), and idle connection reaping
// (120s). WriteTimeout stays 0: run responses stream output and must not be
// cut by a blanket write deadline.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
