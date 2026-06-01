package api

import "net/http"

// NewRouter constructs and wires up the API routes with structured logging.
func NewRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", HandleHealthz)
	mux.HandleFunc("GET /readyz", HandleReadyz)
	mux.HandleFunc("GET /info", HandleInfo)
	mux.HandleFunc("POST /run", HandleRun)
	if PlaygroundExists() {
		mux.Handle("GET /playground", http.RedirectHandler("/playground/", http.StatusMovedPermanently))
		mux.Handle("GET /playground/", http.StripPrefix("/playground", http.HandlerFunc(HandlePlayground)))
	}

	return LoggingMiddleware(mux)
}
