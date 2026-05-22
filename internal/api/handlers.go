package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

const maxRequestBytes = 256 * 1024 // 256 KiB limit

func writeError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	resp := APIError{}
	resp.Error.Code = code
	resp.Error.Message = message
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("failed to write error response: %v", err)
	}
}

// isValidFilename addresses Security Hole #1 (Path traversal)
func isValidFilename(name string) bool {
	if name == "" {
		return true // optional field
	}
	if len(name) > 64 {
		return false
	}
	if strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	return true
}

func HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintln(w, `{"status":"ok"}`); err != nil {
		log.Printf("failed to write healthz response: %v", err)
	}
}

func HandleReadyz(w http.ResponseWriter, r *http.Request) {
	// Stub for Stage 2 plug-and-play validation
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintln(w, `{"status":"ok"}`); err != nil {
		log.Printf("failed to write readyz response: %v", err)
	}
}

func HandleRun(w http.ResponseWriter, r *http.Request) {
	// Addresses Security Hole #4 (No request size limits)
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	
	var req RunRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields() // Strict JSON mapping
	
	if err := decoder.Decode(&req); err != nil {
		var msg string
		if err.Error() == "http: request body too large" {
			msg = "request payload exceeds 256 KiB limit"
		} else {
			msg = "invalid json payload"
		}
		writeError(w, "invalid_request", msg)
		return
	}

	// Basic Validations
	if req.Language == "" {
		writeError(w, "missing_language", "language field is required")
		return
	}
	if req.Source == "" {
		writeError(w, "missing_source", "source field is required")
		return
	}
	if len(req.Tests) == 0 {
		writeError(w, "missing_tests", "at least one test is required")
		return
	}
	
	// Complex Validations
	if !isValidFilename(req.SourceFilename) || !isValidFilename(req.ArtifactFilename) {
		writeError(w, "invalid_filename", "filename must be a single path component without traversal characters")
		return
	}

	// Placeholder success (Execution block to be implemented later)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintln(w, `{"status":"accepted", "tests":[]}`); err != nil {
		log.Printf("failed to write run response: %v", err)
	}
}
