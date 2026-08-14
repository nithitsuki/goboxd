package api

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openAPISpec []byte

// HandleOpenAPI serves the embedded OpenAPI 3 document. The document is
// hand-maintained (no codegen, no framework) so the public contract is
// reviewable as plain JSON.
func HandleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPISpec)
}
