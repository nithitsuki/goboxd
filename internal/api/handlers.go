package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nithitsuki/goboxd/internal/cgroupv2"
	"github.com/nithitsuki/goboxd/internal/config"
	"github.com/nithitsuki/goboxd/internal/models"
	"github.com/nithitsuki/goboxd/internal/runner"
)

// lastInternalErr records the last sandbox infrastructure failure for /info.
var (
	lastInternalErrMu sync.Mutex
	lastInternalErr   time.Time
)

// jobStatsSnapshot is a thread-safe copy of the stats.
type jobStatsSnapshot struct {
	InFlight       int
	Total          int64
	FailedInternal int64
	LastErrorAt    *time.Time
}

// GetStats returns a snapshot of the current job stats (fed by the metrics
// tracker in metrics.go).
func GetStats() jobStatsSnapshot {
	s := jobStatsSnapshot{
		InFlight:       int(atomic.LoadInt64(&metrics.InFlight)),
		Total:          atomic.LoadInt64(&metrics.TotalRuns),
		FailedInternal: atomic.LoadInt64(&metrics.Errors),
	}
	lastInternalErrMu.Lock()
	defer lastInternalErrMu.Unlock()
	if !lastInternalErr.IsZero() {
		t := lastInternalErr
		s.LastErrorAt = &t
	}
	return s
}

// Concurrency semaphore — bounded global limit, requests queue when full.
var (
	jobSem     chan struct{}
	jobSemOnce sync.Once
	maxJobs    int
)

func initSemaphore() {
	n := runtime.NumCPU()
	if e := os.Getenv("GOBOXD_MAX_JOBS"); e != "" {
		if v, err := strconv.Atoi(e); err == nil && v > 0 {
			n = v
		}
	}
	maxJobs = n
	jobSem = make(chan struct{}, n)
	// Fill the semaphore so acquire is send, release is receive
	for i := 0; i < n; i++ {
		jobSem <- struct{}{}
	}
}

func acquireSlot() {
	jobSemOnce.Do(initSemaphore)
	<-jobSem
}

func releaseSlot() {
	jobSem <- struct{}{}
}

const maxRequestBytes = 256 * 1024 // 256 KiB limit
const maxTests = 50                // max test cases per request
const maxFieldBytes = 64 * 1024    // 64 KiB per stdin/expected_stdout field

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

// computeTopLevelStatus determines the top-level run status per the API contract.
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
	ready := probeReadiness()
	w.Header().Set("Content-Type", "application/json")
	if ready.AllOK {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	// The full breakdown is returned on success AND failure: operators need
	// the per-component state either way.
	if err := json.NewEncoder(w).Encode(ready); err != nil {
		log.Printf("failed to write readyz response: %v", err)
	}
}

// readyState holds the /readyz probe results.
type readyState struct {
	AllOK     bool                   `json:"-"`
	Status    string                 `json:"status"`
	Nsjail    *readyProbe            `json:"nsjail"`
	Languages map[string]*readyProbe `json:"languages"`
}

type readyProbe struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

func probeReadiness() readyState {
	state := readyState{
		AllOK:     true,
		Status:    "ok",
		Nsjail:    probeNsjail(),
		Languages: make(map[string]*readyProbe),
	}
	if !state.Nsjail.OK {
		state.AllOK = false
	}

	for lid, lc := range config.DefaultRegistry {
		var p *readyProbe
		if len(lc.SmokeCmd) > 0 {
			// Explicit smoke command from the YAML (languages whose build/run
			// binary cannot answer --version).
			p = probeExecArgs(lc.SmokeCmd[0], lc.SmokeCmd[1:]...)
		} else {
			probeCmd := lc.RunCmd[0]
			if len(lc.BuildCmd) > 0 {
				probeCmd = lc.BuildCmd[0]
			}
			p = probeExec(probeCmd, "--version")
		}
		state.Languages[lid] = p
		if !p.OK {
			state.AllOK = false
		}
	}

	if !state.AllOK {
		state.Status = "degraded"
	}
	return state
}

// probeNsjail checks nsjail via --help (nsjail does not support --version).
func probeNsjail() *readyProbe {
	cmd := exec.Command("nsjail", "--help")
	if err := cmd.Run(); err != nil {
		return &readyProbe{
			OK:    false,
			Error: fmt.Sprintf("nsjail not found or failed: %v", err),
		}
	}
	return &readyProbe{
		OK:      true,
		Version: "3.6",
	}
}

func probeExec(binary, arg string) *readyProbe {
	return probeExecArgs(binary, arg)
}

func probeExecArgs(binary string, args ...string) *readyProbe {
	out, err := exec.Command(binary, args...).Output()
	if err == nil {
		return &readyProbe{
			OK:      true,
			Version: strings.TrimSpace(string(out)),
		}
	}
	// The command failed, try just confirming the binary exists
	if path, lookupErr := exec.LookPath(binary); lookupErr == nil {
		return &readyProbe{
			OK:      true,
			Version: path,
		}
	}
	return &readyProbe{
		OK:    false,
		Error: fmt.Sprintf("%s not found: %v", binary, err),
	}
}

func HandleInfo(w http.ResponseWriter, r *http.Request) {
	// Git commit from build info
	commit := "dev"
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				commit = s.Value
				if len(commit) > 7 {
					commit = commit[:7]
				}
				break
			}
		}
	}

	// Probe nsjail
	nsjailProbe := probeNsjail()
	nsjailPath := "/usr/bin/nsjail"
	if _, err := exec.LookPath("nsjail"); err == nil {
		nsjailPath, _ = exec.LookPath("nsjail")
	}
	nsjailVersion := ""
	if nsjailProbe.OK {
		nsjailVersion = nsjailProbe.Version
	}

	// Probe each language for its real version
	langs := make([]map[string]interface{}, 0, len(config.DefaultRegistry))
	for _, lc := range config.DefaultRegistry {
		ver := lc.Name
		probeCmd := lc.RunCmd[0]
		if len(lc.BuildCmd) > 0 {
			probeCmd = lc.BuildCmd[0]
		}
		if p := probeExec(probeCmd, "--version"); p.OK {
			ver = strings.SplitN(p.Version, "\n", 2)[0]
		}
		langs = append(langs, map[string]interface{}{
			"id":      lc.ID,
			"name":    lc.Name,
			"version": ver,
			"default_run_limits": map[string]interface{}{
				"wall_time_s":   lc.DefaultLimits.WallTimeS,
				"memory_kb":     lc.DefaultLimits.MemoryKB,
				"max_processes": lc.DefaultLimits.MaxProcesses,
			},
		})
	}

	// Disk free for jail dir
	diskFree := diskFreeBytes("/tmp")

	// Stats from the global tracker
	s := GetStats()

	info := map[string]interface{}{
		"build_info": map[string]interface{}{
			"version":    "0.1.0",
			"commit":     commit,
			"go_version": runtime.Version(),
		},
		"nsjail": map[string]interface{}{
			"path":    nsjailPath,
			"version": nsjailVersion,
		},
		"cgroupv2": map[string]interface{}{
			"active": cgroupv2.Default().Active(),
			"mount":  cgroupv2.Default().Root(),
		},
		"languages": langs,
		"limits": map[string]interface{}{
			"max_source_bytes":    262144,
			"max_tests":           50,
			"max_concurrent_jobs": maxJobs,
		},
		"stats": map[string]interface{}{
			"in_flight_jobs":           s.InFlight,
			"jobs_total":               s.Total,
			"jobs_failed_internal":     s.FailedInternal,
			"last_internal_error_at":   s.LastErrorAt,
			"disk_free_bytes_jail_dir": diskFree,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(info); err != nil {
		log.Printf("failed to write info response: %v", err)
	}
}

func diskFreeBytes(path string) int64 {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0
	}
	return int64(fs.Bavail) * int64(fs.Bsize)
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
	for i, tc := range req.Tests {
		if len(tc.Stdin) > maxFieldBytes {
			writeError(w, "test_too_large", fmt.Sprintf("tests[%d].stdin exceeds %d bytes", i, maxFieldBytes))
			return
		}
		if len(tc.ExpectedStdout) > maxFieldBytes {
			writeError(w, "test_too_large", fmt.Sprintf("tests[%d].expected_stdout exceeds %d bytes", i, maxFieldBytes))
			return
		}
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

	// Downward-only limits: client-requested build/run limits may never
	// exceed the configured YAML maxima, and must be positive. The YAML
	// defaults are the effective caps (piston model).
	if !validateStageLimits(w, req.Build, lc.BuildLimits, len(lc.BuildCmd) > 0, "build") {
		return
	}
	if !validateStageLimits(w, req.Run, lc.RunLimits, true, "run") {
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

	// Execute with concurrency semaphore + metrics tracking
	atomic.AddInt64(&metrics.Queued, 1)
	acquireSlot()
	atomic.AddInt64(&metrics.Queued, -1)
	start := time.Now()
	atomic.AddInt64(&metrics.InFlight, 1)
	defer func() {
		atomic.AddInt64(&metrics.InFlight, -1)
		releaseSlot()
	}()
	buildRes, testsRes, err := runner.ExecuteRun(r.Context(), req, lc)
	if r.Context().Err() != nil {
		// The client is gone, so the response cannot be delivered and the
		// write is skipped. The context may have been cancelled mid-run
		// (killing it on purpose) or after the run completed — either way
		// the run is recorded as a cancellation.
		if err != nil {
			// The run also hit an infrastructure failure; surface it via
			// /info and count it as an error even though the client never
			// sees the response.
			log.Printf("Internal error during execution (client gone): %v", err)
			lastInternalErrMu.Lock()
			lastInternalErr = time.Now()
			lastInternalErrMu.Unlock()
			recordRun("cancelled", time.Since(start), true)
			return
		}
		recordRun("cancelled", time.Since(start), false)
		return
	}
	if err != nil {
		log.Printf("[handler] ExecuteRun error for lang=%s: %v | build.Status=%s build.Stderr=%s",
			req.Language, err, buildRes.Status, buildRes.Stderr)
		// If buildRes already has internal_error status, return 200 with it
		// (per the API contract: internal_error is a status in the response body, not a 5xx)
		if buildRes.Status == "internal_error" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(models.RunResponse{
				Status: "internal_error",
				Build:  buildRes,
			}); err != nil {
				log.Printf("failed to write internal_error response: %v", err)
			}
			recordRun("internal_error", time.Since(start), true)
			return
		}
		log.Printf("Internal error during execution: %v", err)
		lastInternalErrMu.Lock()
		lastInternalErr = time.Now()
		lastInternalErrMu.Unlock()
		writeInternalError(w, "sandbox execution failed")
		recordRun("internal_error", time.Since(start), true)
		return
	}

	topStatus := computeTopLevelStatus(buildRes, testsRes)

	// If build failed, construct not_executed entries for all tests per the API contract
	if topStatus == "build_failed" || topStatus == "internal_error" {
		if testsRes == nil && len(req.Tests) > 0 {
			testsRes = make([]models.TestResult, len(req.Tests))
		}
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
	recordRun(topStatus, time.Since(start), topStatus == "internal_error")
}

// validateStageLimits enforces the downward-only limit contract for one stage.
// Returns false (after writing the error response) when a limit is invalid or
// exceeds the configured maximum. Interpreted languages (hasBuild == false)
// reject any build limits: they have no build stage.
func validateStageLimits(w http.ResponseWriter, stage *models.StageConfig, max config.Limits, hasBuild bool, stageName string) bool {
	if stage == nil || stage.Limits == nil {
		return true
	}
	if !hasBuild {
		writeError(w, "invalid_limit", fmt.Sprintf("%s.limits are not allowed: this language has no %s stage", stageName, stageName))
		return false
	}
	check := func(field string, value int, maximum int) bool {
		if value <= 0 {
			writeError(w, "invalid_limit", fmt.Sprintf("%s.limits.%s must be positive", stageName, field))
			return false
		}
		if value > maximum {
			writeError(w, "limit_exceeded", fmt.Sprintf("%s.limits.%s of %d exceeds maximum of %d", stageName, field, value, maximum))
			return false
		}
		return true
	}
	l := stage.Limits
	if l.WallTimeS != nil && !check("wall_time_s", *l.WallTimeS, max.WallTimeS) {
		return false
	}
	if l.MemoryKB != nil && !check("memory_kb", *l.MemoryKB, max.MemoryKB) {
		return false
	}
	if l.MaxProcesses != nil && !check("max_processes", *l.MaxProcesses, max.MaxProcesses) {
		return false
	}
	return true
}
