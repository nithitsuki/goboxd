package api

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/nithitsuki/goboxd/internal/buildinfo"
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

const maxRequestBytes = 256 * 1024 // 256 KiB limit
const maxTests = 50                // max test cases per request
const maxFieldBytes = 64 * 1024    // 64 KiB per stdin/expected_stdout field

func writeErrorStatus(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
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

func writeError(w http.ResponseWriter, code, message string) {
	writeErrorStatus(w, http.StatusBadRequest, code, message)
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
func computeTopLevelStatus(build models.BuildResult, tests []models.TestResult) models.ResultStatus {
	if build.Status == models.BuildInternalError {
		return models.ResultInternalError
	}
	// A zero BuildStatus ("") is invalid and must be treated as not-ok: a
	// forgotten status must never read as success (C5 decision).
	if !build.Status.Valid() {
		return models.ResultBuildFailed
	}
	if build.Status != models.BuildOk {
		return models.ResultBuildFailed
	}
	// Check for internal errors in tests first (those take precedence)
	for _, t := range tests {
		if t.Status == models.ResultInternalError {
			return models.ResultInternalError
		}
	}
	// First non-accepted test status becomes top-level. A status outside
	// the closed set (including the zero value) reads as not_executed so
	// a "" can never reach the wire top-level.
	for _, t := range tests {
		if t.Status != models.ResultAccepted {
			if !t.Status.Valid() {
				return models.ResultNotExecuted
			}
			return t.Status
		}
	}
	return models.ResultAccepted
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

// shuttingDown is set by StartShutdown when a termination signal arrives.
// probeReadiness reports it so readyz flips to 503 before the process exits.
// healthz stays ok: liveness never depends on shutdown state.
var shuttingDown atomic.Bool

// StartShutdown marks the process as shutting down. readyz then reports
// status shutting_down until the process exits.
func StartShutdown() { shuttingDown.Store(true) }

// StopAdmission stops the admission gate: new runs are rejected with
// 503 shutting_down and queued requests are released immediately.
func StopAdmission() { gate.Stop() }

func HandleInfo(w http.ResponseWriter, r *http.Request) {
	// Build metadata: ldflags-stamped values win (the Docker path). Fall back
	// to the Go toolchain's VCS stamping for a plain `go build` outside
	// Docker — where the .git directory is absent and stamping is skipped.
	commit := buildinfo.Commit
	if commit == "" || commit == "dev" {
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
	}

	// Probe nsjail (cached, shared with /readyz)
	nsjailProbe := probes.nsjail()
	nsjailPath := "/usr/bin/nsjail"
	if _, err := exec.LookPath("nsjail"); err == nil {
		nsjailPath, _ = exec.LookPath("nsjail")
	}
	nsjailVersion := ""
	if nsjailProbe.OK {
		nsjailVersion = nsjailProbe.Version
	}

	// Probe each language for its real version
	reg := config.Registry()
	langs := make([]map[string]interface{}, 0, len(reg))
	for _, lc := range reg {
		ver := lc.Name
		if p := probes.languageProbe(lc); p.OK {
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
				"cpu_time_s":    lc.DefaultLimits.CpuTimeS,
			},
			// P1-10: the measured-safe maxima clients may raise limits up to.
			// Equals default_run_limits when the language declares no ceiling.
			"ceiling_run_limits": map[string]interface{}{
				"wall_time_s":   lc.DefaultCeiling.WallTimeS,
				"memory_kb":     lc.DefaultCeiling.MemoryKB,
				"max_processes": lc.DefaultCeiling.MaxProcesses,
				"cpu_time_s":    lc.DefaultCeiling.CpuTimeS,
			},
		})
	}

	// Disk free for jail dir
	diskFree := diskFreeBytes("/tmp")

	// Stats from the global tracker
	s := GetStats()

	info := map[string]interface{}{
		"build_info": map[string]interface{}{
			"version":    buildinfo.Version,
			"commit":     commit,
			"build_date": buildinfo.BuildDate,
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
			"max_queued_jobs":     maxQueued,
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
	if req.MaxParallel != nil && *req.MaxParallel > 0 {
		if *req.MaxParallel > maxTests {
			writeError(w, "invalid_max_parallel", fmt.Sprintf("max_parallel of %d exceeds maximum of %d", *req.MaxParallel, maxTests))
			return
		}
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
	lc, ok := config.Registry()[req.Language]
	if !ok {
		writeError(w, "unknown_language", fmt.Sprintf("language '%s' is not in the registry", req.Language))
		return
	}

	// Per-language limit contract (P1-10): a client may lower any limit and
	// may raise a limit up to the per-language ceiling. The YAML limits are
	// the defaults; the ceiling is the measured-safe maximum (defaults to
	// the limits when the YAML declares no ceiling).
	if !validateStageLimits(w, req.Build, lc.BuildLimits, lc.BuildCeiling, len(lc.BuildCmd) > 0, "build") {
		return
	}
	if !validateStageLimits(w, req.Run, lc.RunLimits, lc.RunCeiling, true, "run") {
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

	// Output cap validation: per-request cap must not exceed the hard ceiling.
	if req.MaxOutputBytes != nil && *req.MaxOutputBytes > 0 {
		ceiling := 1048576 // 1 MB default
		if e := os.Getenv("GOBOXD_MAX_OUTPUT_BYTES"); e != "" {
			if v, err := strconv.Atoi(e); err == nil && v > 0 {
				ceiling = v
			}
		}
		if *req.MaxOutputBytes > ceiling {
			writeError(w, "limit_exceeded",
				fmt.Sprintf("max_output_bytes of %d exceeds ceiling of %d", *req.MaxOutputBytes, ceiling))
			return
		}
	}

	// Bounded admission: at most maxJobs in flight, maxQueued waiting. A
	// queued request gives up its ticket the moment the client disconnects.
	start := time.Now()
	if err := gate.acquire(r.Context()); err != nil {
		if errors.Is(err, errQueueFull) {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("X-Run-Status", "queue_full")
			writeErrorStatus(w, http.StatusServiceUnavailable, "queue_full",
				"server is at capacity: too many jobs queued, retry shortly")
			recordRun("queue_full", time.Since(start), true)
			return
		}
		status, isError := classifyAcquireErr(r.Context(), err)
		if isError {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("X-Run-Status", "shutting_down")
			writeErrorStatus(w, http.StatusServiceUnavailable, "shutting_down",
				"server is shutting down: retry shortly")
		}
		recordRun(status, time.Since(start), isError)
		return
	}
	atomic.AddInt64(&metrics.InFlight, 1)
	defer func() {
		atomic.AddInt64(&metrics.InFlight, -1)
		gate.release()
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
			recordRun(string(models.ResultCancelled), time.Since(start), true)
			return
		}
		recordRun(string(models.ResultCancelled), time.Since(start), false)
		return
	}
	if err != nil {
		log.Printf("[handler] ExecuteRun error for lang=%s: %v | build.Status=%s build.Stderr=%s",
			req.Language, err, buildRes.Status, buildRes.Stderr)
		// If buildRes already has internal_error status, return 200 with it
		// (per the API contract: internal_error is a status in the response body, not a 5xx)
		if buildRes.Status == models.BuildInternalError {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Run-Status", string(models.ResultInternalError))
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(models.RunResponse{
				Status: string(models.ResultInternalError),
				Build:  buildRes,
			}); err != nil {
				log.Printf("failed to write internal_error response: %v", err)
			}
			recordRun(string(models.ResultInternalError), time.Since(start), true)
			return
		}
		log.Printf("Internal error during execution: %v", err)
		lastInternalErrMu.Lock()
		lastInternalErr = time.Now()
		lastInternalErrMu.Unlock()
		writeInternalError(w, "sandbox execution failed")
		recordRun(string(models.ResultInternalError), time.Since(start), true)
		return
	}

	topStatus := computeTopLevelStatus(buildRes, testsRes)

	// If build failed, construct not_executed entries for all tests per the API contract
	if topStatus == models.ResultBuildFailed || topStatus == models.ResultInternalError {
		if testsRes == nil && len(req.Tests) > 0 {
			testsRes = make([]models.TestResult, len(req.Tests))
		}
		for i := range testsRes {
			testsRes[i].Status = models.ResultNotExecuted
		}
	}

	resp := models.RunResponse{
		Status: string(topStatus),
		Build:  buildRes,
		Tests:  testsRes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Run-Status", string(topStatus))
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("failed to write run response: %v", err)
	}
	recordRun(string(topStatus), time.Since(start), topStatus == models.ResultInternalError)
}

// classifyAcquireErr maps an admission failure to the recorded metric
// status. A dead client context wins over errShuttingDown: Stop's broadcast
// may close at the same moment a queued client disconnects, and a
// shutting_down record (an error) for a client that never saw the 503 would
// inflate the error counter.
func classifyAcquireErr(ctx context.Context, err error) (status string, isError bool) {
	if ctx.Err() == nil && errors.Is(err, errShuttingDown) {
		return "shutting_down", true
	}
	// Cancelled while queued: the client is gone, so the response cannot
	// be delivered and the write is skipped (P0-1 semantics).
	return string(models.ResultCancelled), false
}

// validateStageLimits enforces the per-language limit contract for one
// stage. A client may lower any limit and may raise a limit up to the
// per-language ceiling. The server rejects values above the ceiling with
// limit_exceeded and non-positive values with invalid_limit. Interpreted
// languages (hasBuild == false) reject any build limits: they have no build
// stage.
func validateStageLimits(w http.ResponseWriter, stage *models.StageConfig, max, ceiling config.Limits, hasBuild bool, stageName string) bool {
	if stage == nil || stage.Limits == nil {
		return true
	}
	if !hasBuild {
		writeError(w, "invalid_limit", fmt.Sprintf("%s.limits are not allowed: this language has no %s stage", stageName, stageName))
		return false
	}
	// ceilingAt returns the effective ceiling for one field. A zero ceiling
	// field means the stage max acts as the ceiling for that field: a YAML
	// ceiling block may raise only some fields.
	ceilingAt := func(fieldCeiling, fieldMax int) int {
		if fieldCeiling == 0 {
			return fieldMax
		}
		return fieldCeiling
	}
	check := func(field string, value int, maximum int, fieldCeiling int) bool {
		if value <= 0 {
			writeError(w, "invalid_limit", fmt.Sprintf("%s.limits.%s must be positive", stageName, field))
			return false
		}
		effective := ceilingAt(fieldCeiling, maximum)
		if value > effective {
			writeError(w, "limit_exceeded", fmt.Sprintf("%s.limits.%s of %d exceeds ceiling of %d", stageName, field, value, effective))
			return false
		}
		return true
	}
	l := stage.Limits
	if l.WallTimeS != nil && !check("wall_time_s", *l.WallTimeS, max.WallTimeS, ceiling.WallTimeS) {
		return false
	}
	if l.MemoryKB != nil && !check("memory_kb", *l.MemoryKB, max.MemoryKB, ceiling.MemoryKB) {
		return false
	}
	if l.MaxProcesses != nil && !check("max_processes", *l.MaxProcesses, max.MaxProcesses, ceiling.MaxProcesses) {
		return false
	}
	if l.CpuTimeS != nil {
		if *l.CpuTimeS <= 0 {
			writeError(w, "invalid_limit", fmt.Sprintf("%s.limits.cpu_time_s must be positive", stageName))
			return false
		}
		// A zero YAML max means the registry declares no cpu cap for this
		// language: the request may not invent one.
		if max.CpuTimeS == 0 {
			writeError(w, "invalid_limit", fmt.Sprintf("%s.limits.cpu_time_s is not allowed: this language has no cpu limit configured", stageName))
			return false
		}
		cpuCeiling := ceilingAt(ceiling.CpuTimeS, max.CpuTimeS)
		if *l.CpuTimeS > cpuCeiling {
			writeError(w, "limit_exceeded", fmt.Sprintf("%s.limits.cpu_time_s of %d exceeds ceiling of %d", stageName, *l.CpuTimeS, cpuCeiling))
			return false
		}
	}
	return true
}
