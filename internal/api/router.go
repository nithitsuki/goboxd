// Package api implements the HTTP handlers, routing, request validation,
// and structured logging for the goboxd service.
//
// Routes are registered using Go 1.22's method-pattern mux syntax.
// All responses are JSON. POST /run requests are validated and dispatched
// to the runner package for sandboxed execution inside nsjail.
package api

import "net/http"

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
	mux.HandleFunc("POST /run", HandleRun)
	mux.HandleFunc("GET /testcases", HandleTestcasesList)
	mux.HandleFunc("GET /testcases/{lang}/{name}", HandleTestcasesGet)
	if PlaygroundExists() {
		mux.Handle("GET /playground", http.RedirectHandler("/playground/", http.StatusMovedPermanently))
		mux.Handle("GET /playground/", http.StripPrefix("/playground", http.HandlerFunc(HandlePlayground)))
	}

	return RecoveryMiddleware(LoggingMiddleware(mux))
}
