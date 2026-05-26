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

	"github.com/thesouldev/goboxd/internal/config"
	"github.com/thesouldev/goboxd/internal/models"
	"github.com/thesouldev/goboxd/internal/runner"
)

// Stats tracker for /info endpoint
var (
	jobStats struct {
		InFlight       int64
		Total          int64
		FailedInternal int64
	}
	jobStatsMu      sync.Mutex
	lastInternalErr time.Time
)

// jobStatsSnapshot is a thread-safe copy of the stats.
type jobStatsSnapshot struct {
	InFlight       int
	Total          int64
	FailedInternal int64
	LastErrorAt    *time.Time
}

// GetStats returns a snapshot of the current job stats.
func GetStats() jobStatsSnapshot {
	jobStatsMu.Lock()
	defer jobStatsMu.Unlock()
	s := jobStatsSnapshot{
		InFlight:       int(atomic.LoadInt64(&jobStats.InFlight)),
		Total:          atomic.LoadInt64(&jobStats.Total),
		FailedInternal: atomic.LoadInt64(&jobStats.FailedInternal),
	}
	if !lastInternalErr.IsZero() {
		s.LastErrorAt = &lastInternalErr
	}
	return s
}

// Concurrency semaphore — bounded global limit, requests queue when full.
var (
	jobSem      chan struct{}
	jobSemOnce  sync.Once
	maxJobs     int
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

const maxRequestBytes = 256 * 1024  // 256 KiB limit
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
	ready := probeReadiness()
	w.Header().Set("Content-Type", "application/json")
	if ready.AllOK {
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			log.Printf("failed to write readyz OK response: %v", err)
		}
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(ready); err != nil {
			log.Printf("failed to write readyz degraded response: %v", err)
		}
	}
}

// readyState holds the /readyz probe results.
type readyState struct {
	AllOK     bool                           `json:"-"`
	Status    string                         `json:"status"`
	Nsjail    *readyProbe                    `json:"nsjail"`
	Languages map[string]*readyProbe         `json:"languages"`
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
		Nsjail:    probeExec("nsjail", "--version"),
		Languages: make(map[string]*readyProbe),
	}
	if !state.Nsjail.OK {
		state.AllOK = false
	}

	for lid, lc := range config.DefaultRegistry {
		probeCmd := lc.RunCmd[0]
		if len(lc.BuildCmd) > 0 {
			probeCmd = lc.BuildCmd[0]
		}
		p := probeExec(probeCmd, "--version")
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

func probeExec(binary, arg string) *readyProbe {
	out, err := exec.Command(binary, arg).Output()
	if err != nil {
		return &readyProbe{
			OK:    false,
			Error: fmt.Sprintf("%s not found or failed: %v", binary, err),
		}
	}
	return &readyProbe{
		OK:      true,
		Version: strings.TrimSpace(string(out)),
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
	nsjailProbe := probeExec("nsjail", "--version")
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
		ver := lc.Version
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
		"languages": langs,
		"limits": map[string]interface{}{
			"max_source_bytes":    262144,
			"max_tests":           50,
			"max_concurrent_jobs": maxJobs,
		},
		"stats": map[string]interface{}{
			"in_flight_jobs":         s.InFlight,
			"jobs_total":             s.Total,
			"jobs_failed_internal":   s.FailedInternal,
			"last_internal_error_at": s.LastErrorAt,
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
	return int64(fs.Bavail) * fs.Bsize
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

	// Execute with concurrency semaphore + stats tracking
	acquireSlot()
	atomic.AddInt64(&jobStats.InFlight, 1)
	atomic.AddInt64(&jobStats.Total, 1)
	buildRes, testsRes, err := runner.ExecuteRun(req, lc)
	atomic.AddInt64(&jobStats.InFlight, -1)
	releaseSlot()
	if err != nil {
		log.Printf("Internal error during execution: %v", err)
		atomic.AddInt64(&jobStats.FailedInternal, 1)
		jobStatsMu.Lock()
		lastInternalErr = time.Now()
		jobStatsMu.Unlock()
		writeInternalError(w, "sandbox execution failed")
		return
	}

	topStatus := computeTopLevelStatus(buildRes, testsRes)

	// If build failed, construct not_executed entries for all tests per spec
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
}
