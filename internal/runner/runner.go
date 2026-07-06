// Package runner executes untrusted user code inside nsjail sandboxes.
//
// Each request gets a unique temporary jail directory created via
// os.MkdirTemp. The source code is written to disk inside the jail,
// optionally compiled (for compiled languages), then executed against
// each test case. Every invocation of the compiler or the user's program
// is wrapped in nsjail for namespace isolation, resource limits, and
// filesystem containment.
//
// Resource limits (wall time, memory, process count) are enforced by
// nsjail's --time_limit and --rlimit flags. Output is capped at 64 KiB
// per stream to prevent unbounded memory consumption.
//
// Infrastructure errors (nsjail itself failing) are distinguished from
// user-code errors so they produce internal_error status rather than
// misleading build_failed or runtime_error.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/thesouldev/goboxd/internal/config"
	"github.com/thesouldev/goboxd/internal/models"
)

// expandFlags replaces {{flags}} in cmdArgs with the provided flags.
// If no flags are given and {{flags}} is present, it is removed entirely.
func expandFlags(cmdArgs []string, flags []string) []string {
	result := make([]string, 0, len(cmdArgs))
	for _, arg := range cmdArgs {
		if arg == "{{flags}}" {
			if len(flags) > 0 {
				result = append(result, flags...)
			}
			continue
		}
		result = append(result, arg)
	}
	return result
}

// maxOutputBytes caps the output to prevent unbounded child output OOMs (Security Hole #6)
const maxOutputBytes = 64 * 1024 // 64 KiB

func ExecuteRun(req models.RunRequest, lc config.LanguageConfig) (models.BuildResult, []models.TestResult, error) {
	buildRes := models.BuildResult{
		Status:     "ok",
		Stdout:     "",
		Stderr:     "",
		DurationMs: 0,
	}

	// Security Hole #5: UID collisions. os.MkdirTemp guarantees unique, non-colliding directories.
	jailDir, err := os.MkdirTemp("", "goboxd-jail-*")
	if err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = fmt.Sprintf("failed to create jail dir: %v", err)
		return buildRes, nil, fmt.Errorf("failed to create jail dir: %w", err)
	}
	// Security Hole #7: Stale jail directories. Defer cleanup immediately.
	defer func() {
		if err := os.RemoveAll(jailDir); err != nil {
			fmt.Fprintf(os.Stderr, "failed to remove jail dir: %v\n", err)
		}
	}()

	srcName := req.SourceFilename
	if srcName == "" {
		srcName = lc.SourceFilename
	}

	srcDir := filepath.Join(jailDir, "app")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = fmt.Sprintf("failed to create app dir: %v", err)
		return buildRes, nil, fmt.Errorf("failed to create app dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, srcName), []byte(req.Source), 0644); err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = fmt.Sprintf("failed to write source: %v", err)
		return buildRes, nil, fmt.Errorf("failed to write source: %w", err)
	}

	// Build step for compiled languages
	buildStart := time.Now()
	if len(lc.BuildCmd) > 0 {
		buildRes, err = runBuild(jailDir, req, lc)
		buildRes.DurationMs = int(time.Since(buildStart).Milliseconds())
		if err != nil {
			return buildRes, nil, err
		}
		if buildRes.Status != "ok" {
			// Build failed, don't run tests
			return buildRes, nil, nil
		}
	} else {
		buildRes.DurationMs = 0
	}

	var results []models.TestResult
	for _, tc := range req.Tests {
		res := runSingleTest(tc, lc, jailDir, req.Run)
		results = append(results, res)
	}

	return buildRes, results, nil
}

// runBuild compiles the source inside nsjail using lc.BuildCmd.
func runBuild(jailDir string, req models.RunRequest, lc config.LanguageConfig) (models.BuildResult, error) {
	wallTime := lc.BuildLimits.WallTimeS
	memKB := lc.BuildLimits.MemoryKB
	procs := lc.BuildLimits.MaxProcesses
	if req.Build != nil && req.Build.Limits != nil {
		if req.Build.Limits.WallTimeS != nil {
			wallTime = *req.Build.Limits.WallTimeS
		}
		if req.Build.Limits.MemoryKB != nil {
			memKB = *req.Build.Limits.MemoryKB
		}
		if req.Build.Limits.MaxProcesses != nil {
			procs = *req.Build.Limits.MaxProcesses
		}
	}

	cmdArgs := make([]string, len(lc.BuildCmd))
	copy(cmdArgs, lc.BuildCmd)
	flags := []string{}
	if req.Build != nil && len(req.Build.Flags) > 0 {
		flags = req.Build.Flags
	}
	cmdArgs = expandFlags(cmdArgs, flags)

	stdout, stderr, err := execInJail(jailDir, cmdArgs, wallTime, memKB, procs)
	res := models.BuildResult{
		Status: "ok",
		Stdout: stdout,
		Stderr: stderr,
	}
	if err != nil {
		log.Printf("[runner] build error for %s: %v | stdout: %s | stderr: %s", req.Language, err, stdout, stderr)
		if isInfraError(err) {
			res.Status = "internal_error"
			return res, err
		}
		res.Status = "failed"
	}
	return res, nil
}

// isInfraError checks if the error is from infrastructure (pipe, start, nsjail) vs user code.
// We only flag explicit pipe/start failures and nsjail crashing with a signal.
// Exit codes (including 255) are NOT infrastructure — they come from the user's program
// (nsjail propagates the inner exit code). Flagging them as infra would turn legitimate
// runtime/build errors into internal_error.
func isInfraError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	log.Printf("[runner] isInfraError check: %s", msg)
	if strings.Contains(msg, "pipe:") || strings.Contains(msg, "start:") {
		return true
	}
	return false
}

// nsjailArgs builds the common nsjail arguments for both build and run steps.
// cgroup flags are not used — Docker Desktop does not expose a writable cgroup
// hierarchy (the pids controller can be enabled but adding the pid to the child
// cgroup.procs fails with EOPNOTSUPP). Falls back to --rlimit_nproc.
// --max_cpus caps CPU usage; tune via GOBOXD_MAX_CPUS env var.
func nsjailArgs(appDir string, wallTime, memKB, procs int) []string {
	memBytes := memKB * 1024
	maxCPUs := os.Getenv("GOBOXD_MAX_CPUS")
	args := []string{
		"-Q",
		"--log", "/dev/null",
		"-Mo",
		"-T", "/tmp",
		"--bindmount", appDir + ":/app:rw",
		"--cwd", "/app",
		"--chroot", "/",
		"--proc_path", "/proc",
		"--time_limit", strconv.Itoa(wallTime),
		"--rlimit_as", strconv.Itoa(memBytes),
		"--rlimit_nproc", strconv.Itoa(procs),
		"--rlimit_fsize", "100",
		"--rlimit_nofile", "65536",
	}
	if maxCPUs != "" {
		args = append(args, "--max_cpus", maxCPUs)
	}
	args = append(args,
		"-B", "/etc",
		"-E", "PATH=/usr/local/bin:/usr/bin:/bin",
		"-E", "HOME=/tmp",
		"-E", "GOCACHE=/tmp/go-cache",
		"-B", "/usr",
		"-B", "/lib",
		"-B", "/lib64",
		"-B", "/bin",
		"-B", "/dev",
		"-B", "/var/lib",
		"--",
	)
	return args
}

func execInJail(jailDir string, cmdArgs []string, wallTime, memKB, procs int) (string, string, error) {
	appDir := filepath.Join(jailDir, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", "", fmt.Errorf("app dir: %w", err)
	}

	args := nsjailArgs(appDir, wallTime, memKB, procs)
	args = append(args, cmdArgs...)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(wallTime)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nsjail", args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("start: %w", err)
	}

	// Read stdout/stderr concurrently to avoid pipe buffer deadlocks
	outChan := make(chan string, 1)
	errChan := make(chan string, 1)
	go func() {
		defer func() { _ = recover() }()
		outChan <- readCapped(stdoutPipe)
	}()
	go func() {
		defer func() { _ = recover() }()
		errChan <- readCapped(stderrPipe)
	}()

	stdout := <-outChan
	stderr := <-errChan

	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		stderr += "\n... [build timed out]"
		log.Printf("[runner] build timed out (wall=%ds) stdout=%d stderr=%d", wallTime, len(stdout), len(stderr))
		return stdout, stderr, fmt.Errorf("build timed out")
	}
	if err != nil {
		log.Printf("[runner] nsjail build error: %v | stderr: %s", err, stderr)
		return stdout, stderr, err
	}
	return stdout, stderr, nil
}

func runSingleTest(tc models.TestCase, lc config.LanguageConfig, jailDir string, runOpts *models.StageConfig) models.TestResult {
	start := time.Now()

	// Use language-specific run limits, not build limits
	wallTime := lc.RunLimits.WallTimeS
	if runOpts != nil && runOpts.Limits != nil && runOpts.Limits.WallTimeS != nil {
		wallTime = *runOpts.Limits.WallTimeS
	}

	memKB := lc.RunLimits.MemoryKB
	if runOpts != nil && runOpts.Limits != nil && runOpts.Limits.MemoryKB != nil {
		memKB = *runOpts.Limits.MemoryKB
	}
	procs := lc.RunLimits.MaxProcesses
	if runOpts != nil && runOpts.Limits != nil && runOpts.Limits.MaxProcesses != nil {
		procs = *runOpts.Limits.MaxProcesses
	}

	appDir := filepath.Join(jailDir, "app")
	args := nsjailArgs(appDir, wallTime, memKB, procs)
	runFlags := []string{}
	if runOpts != nil && len(runOpts.Flags) > 0 {
		runFlags = runOpts.Flags
	}
	args = append(args, expandFlags(lc.RunCmd, runFlags)...)

	// Go context deadline matches nsjail's time_limit so both fire together
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(wallTime)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nsjail", args...)

	if tc.Stdin != "" {
		cmd.Stdin = bytes.NewBufferString(tc.Stdin)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return failResult("internal_error", err.Error(), start)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return failResult("internal_error", err.Error(), start)
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[runner] nsjail start failed: %v", err)
		return failResult("internal_error", err.Error(), start)
	}

	outChan := make(chan string)
	errChan := make(chan string)

	go func() {
		defer func() { _ = recover() }()
		outChan <- readCapped(stdoutPipe)
	}()
	go func() {
		defer func() { _ = recover() }()
		errChan <- readCapped(stderrPipe)
	}()

	stdoutRaw := <-outChan
	stderrRaw := <-errChan

	err = cmd.Wait()
	duration := int(time.Since(start).Milliseconds())

	log.Printf("[runner] nsjail exited: err=%v | stdout_len=%d stderr_len=%d | lang=%s",
		err, len(stdoutRaw), len(stderrRaw), lc.ID)

	// Check for nsjail infrastructure failures first (nsjail itself crashed,
	// not the user code). Treat these as internal errors.
	if err != nil && isInfraError(err) {
		log.Printf("[runner] nsjail infra error: %v | stderr: %s", err, stderrRaw)
		return models.TestResult{
			Status:       "internal_error",
			Stdout:       stdoutRaw,
			Stderr:       stderrRaw,
			DurationMs:   duration,
			MemoryPeakKB: 0,
		}
	}

	memPeak := readMemoryPeakKB(cmd.ProcessState)
	status := computeTestStatus(ctx, err, stdoutRaw, tc.ExpectedStdout, cmd.ProcessState, memPeak, memKB, wallTime, duration)

	return models.TestResult{
		Status:       status,
		Stdout:       stdoutRaw,
		Stderr:       stderrRaw,
		DurationMs:   duration,
		MemoryPeakKB: memPeak,
	}
}

// signalKillReason checks if the process was killed by a signal and determines why.
func signalKillReason(ps *os.ProcessState) string {
	if ps == nil {
		return ""
	}
	status, ok := ps.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	sig := status.Signal()
	switch sig {
	case syscall.SIGKILL:
		return "time_exceeded"
	case syscall.SIGSEGV, syscall.SIGABRT:
		return "memory_exceeded"
	default:
		return "runtime_error"
	}
}

// readMemoryPeakKB reads peak memory from nsjail cgroup or falls back to 0.
// readMemoryPeakKB reads peak memory via getrusage (child processes).
// This captures nsjail + user process memory without needing cgroups.
func readMemoryPeakKB(ps *os.ProcessState) int {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &ru); err == nil && ru.Maxrss > 0 {
		return int(ru.Maxrss)
	}
	return 0
}

func computeTestStatus(ctx context.Context, err error, stdout, expected string, ps *os.ProcessState, memPeakKB int, memLimitKB int, wallTime int, durationMs int) string {
	// Check context deadline first (Go killed the process)
	if ctx.Err() == context.DeadlineExceeded {
		return "time_exceeded"
	}
	if err != nil {
		if reason := signalKillReason(ps); reason != "" {
			return reason
		}
		// nsjail exits non-zero when enforcing --time_limit.
		// Signal detection failed (ps is nil or no signal), so check duration.
		if wallTime > 0 && durationMs >= (wallTime-1)*1000 {
			return "time_exceeded"
		}
		return "runtime_error"
	}
	if expected == "" {
		return "accepted"
	}
	if stdout == expected {
		return "accepted"
	}
	if strings.TrimSpace(stdout) == strings.TrimSpace(expected) {
		return "output_whitespace_mismatch"
	}
	return "wrong_output"
}

// SweepOrphans removes jail dirs older than 30 minutes. Call at startup.
func SweepOrphans() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "orphan sweep: reading temp dir: %v\n", err)
		return
	}
	now := time.Now()
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "goboxd-jail-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > 30*time.Minute {
			path := filepath.Join(os.TempDir(), e.Name())
			if err := os.RemoveAll(path); err != nil {
				fmt.Fprintf(os.Stderr, "orphan sweep: removing %s: %v\n", path, err)
			}
		}
	}
}

// readCapped reads from r up to maxOutputBytes+1, truncates with a marker if capped.
func readCapped(r io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(r, maxOutputBytes+1))
	if len(raw) > maxOutputBytes {
		raw = raw[:maxOutputBytes]
		raw = append(raw, []byte("\n... [output truncated]")...)
	}
	return string(raw)
}

func failResult(status, stderr string, start time.Time) models.TestResult {
	return models.TestResult{
		Status:     status,
		Stdout:     "",
		Stderr:     stderr,
		DurationMs: int(time.Since(start).Milliseconds()),
	}
}
