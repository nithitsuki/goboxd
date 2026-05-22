package api

import "net/http"

// NewRouter constructs and wires up the API routes
func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()
	
	mux.HandleFunc("GET /healthz", HandleHealthz)
	mux.HandleFunc("GET /readyz", HandleReadyz)
	mux.HandleFunc("POST /run", HandleRun)
	
	return mux
}
