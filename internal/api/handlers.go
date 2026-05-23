package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"github.com/thesouldev/goboxd/internal/config"
	"github.com/thesouldev/goboxd/internal/models"
	"github.com/thesouldev/goboxd/internal/runner"
)

const maxRequestBytes = 256 * 1024 // 256 KiB limit
const maxTests = 50               // max test cases per request

func writeError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	resp := models.APIError{
		Error: models.ErrorDetail{
			Code:    code,
			Message: message,
		},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("failed to write error response: %v", err)
	}
}

func writeInternalError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	resp := models.APIError{
		Error: models.ErrorDetail{
			Code:    "internal_error",
			Message: message,
		},
	}
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

// validateFlags checks flags against the per-language allowlist (Security Hole #3)
func validateFlags(flags []string, allowlist []string) (bool, string) {
	if len(allowlist) == 0 {
		return true, "" // no restrictions
	}
	for _, flag := range flags {
		allowed := false
		for _, pattern := range allowlist {
			if strings.HasSuffix(pattern, "*") {
				prefix := strings.TrimSuffix(pattern, "*")
				if strings.HasPrefix(flag, prefix) {
					allowed = true
					break
				}
			} else if flag == pattern {
				allowed = true
				break
			}
		}
		if !allowed {
			return false, flag
		}
	}
	return true, ""
}

// computeTopLevelStatus determines the top-level run status per spec rules.
func computeTopLevelStatus(build models.BuildResult, tests []models.TestResult) string {
	if build.Status == "internal_error" {
		return "internal_error"
	}
	if build.Status != "ok" {
		return "build_failed"
	}
	// Check for internal errors in tests first (those take precedence)
	for _, t := range tests {
		if t.Status == "internal_error" {
			return "internal_error"
		}
	}
	// First non-accepted test status becomes top-level
	for _, t := range tests {
		if t.Status != "accepted" {
			return t.Status
		}
	}
	return "accepted"
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

func HandleInfo(w http.ResponseWriter, r *http.Request) {
	// Probe nsjail version
	nsjailVersion := "unknown"
	if out, err := exec.Command("nsjail", "--version").Output(); err == nil {
		nsjailVersion = strings.TrimSpace(string(out))
	}

	langs := make([]map[string]interface{}, 0, len(config.DefaultRegistry))
	for _, lc := range config.DefaultRegistry {
		langs = append(langs, map[string]interface{}{
			"id":      lc.ID,
			"name":    lc.Name,
			"version": lc.Version,
			"default_run_limits": map[string]interface{}{
				"wall_time_s":   lc.DefaultLimits.WallTimeS,
				"memory_kb":     lc.DefaultLimits.MemoryKB,
				"max_processes": lc.DefaultLimits.MaxProcesses,
			},
		})
	}

	info := map[string]interface{}{
		"build_info": map[string]interface{}{
			"version":    "0.1.0",
			"commit":     "dev",
			"go_version": runtime.Version(),
		},
		"nsjail": map[string]interface{}{
			"path":    "/usr/bin/nsjail",
			"version": nsjailVersion,
		},
		"languages": langs,
		"limits": map[string]interface{}{
			"max_source_bytes":    262144,
			"max_tests":           50,
			"max_concurrent_jobs": runtime.NumCPU(),
		},
		"stats": map[string]interface{}{
			"in_flight_jobs":           0,
			"jobs_total":               0,
			"jobs_failed_internal":     0,
			"last_internal_error_at":   nil,
			"disk_free_bytes_jail_dir": 0,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(info); err != nil {
		log.Printf("failed to write info response: %v", err)
	}
}

func HandleRun(w http.ResponseWriter, r *http.Request) {
	// Addresses Security Hole #4 (No request size limits)
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)

	var req models.RunRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

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
	if len(req.Tests) > maxTests {
		writeError(w, "too_many_tests", fmt.Sprintf("test count exceeds maximum of %d", maxTests))
		return
	}

	// Complex Validations
	if !isValidFilename(req.SourceFilename) || !isValidFilename(req.ArtifactFilename) {
		writeError(w, "invalid_filename", "filename must be a single path component without traversal characters")
		return
	}

	// Language lookup
	lc, ok := config.DefaultRegistry[req.Language]
	if !ok {
		writeError(w, "unknown_language", fmt.Sprintf("language '%s' is not in the registry", req.Language))
		return
	}

	// Flag allow-list validation (Security Hole #3)
	if req.Build != nil && len(req.Build.Flags) > 0 {
		if ok, bad := validateFlags(req.Build.Flags, lc.FlagAllowlist); !ok {
			writeError(w, "invalid_flags", fmt.Sprintf("disallowed build flag: %s", bad))
			return
		}
	}
	if req.Run != nil && len(req.Run.Flags) > 0 {
		if ok, bad := validateFlags(req.Run.Flags, lc.FlagAllowlist); !ok {
			writeError(w, "invalid_flags", fmt.Sprintf("disallowed run flag: %s", bad))
			return
		}
	}

	// Execute
	buildRes, testsRes, err := runner.ExecuteRun(req, lc)
	if err != nil {
		log.Printf("Internal error during execution: %v", err)
		writeInternalError(w, "sandbox execution failed")
		return
	}

	topStatus := computeTopLevelStatus(buildRes, testsRes)

	// If build failed, mark all tests as not_executed per spec
	if topStatus == "build_failed" || topStatus == "internal_error" {
		for i := range testsRes {
			testsRes[i].Status = "not_executed"
		}
	}

	resp := models.RunResponse{
		Status: topStatus,
		Build:  buildRes,
		Tests:  testsRes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("failed to write run response: %v", err)
	}
}
