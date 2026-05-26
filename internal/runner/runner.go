package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

	if err := os.WriteFile(filepath.Join(jailDir, srcName), []byte(req.Source), 0644); err != nil {
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
	wallTime := 30
	memKB := 1048576
	procs := 100
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
	if req.Build != nil {
		cmdArgs = append(cmdArgs, req.Build.Flags...)
	}

	stdout, stderr, err := execInJail(jailDir, cmdArgs, wallTime, memKB, procs)
	res := models.BuildResult{
		Status: "ok",
		Stdout: stdout,
		Stderr: stderr,
	}
	if err != nil {
		// Distinguish infrastructure errors from compiler errors.
		// Pipe/start failures are infrastructure (return 500).
		// Compiler exit codes and timeouts are user errors (return 200 with build_failed).
		if isInfraError(err) {
			res.Status = "internal_error"
			return res, err
		}
		res.Status = "failed"
	}
	return res, nil
}

// isInfraError checks if the error is from infrastructure (pipe, start) vs user code.
func isInfraError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "pipe:") || strings.Contains(msg, "start:")
}

func execInJail(jailDir string, cmdArgs []string, wallTime, memKB, procs int) (string, string, error) {
	memBytes := memKB * 1024
	args := []string{
		"-Q",
		"--log", "/dev/null",
		"-Mo",
		"--time_limit", strconv.Itoa(wallTime),
		"--rlimit_as", strconv.Itoa(memBytes),
		"--rlimit_nproc", strconv.Itoa(procs),
		"--rlimit_fsize", "100",
		"-E", "PATH=/usr/local/bin:/usr/bin:/bin",
		"-B", "/bin",
		"-B", "/usr",
		"-B", "/lib",
		"-B", "/lib64",
		"-B", "/dev",
		"-B", "/etc",
		"-R", fmt.Sprintf("%s:/app", jailDir),
		"--cwd", "/app",
		"--",
	}
	args = append(args, cmdArgs...)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(wallTime+1)*time.Second)
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

	stdout := readCapped(stdoutPipe)
	stderr := readCapped(stderrPipe)

	err = cmd.Wait()
	if ctx.Err() == context.DeadlineExceeded {
		stderr += "\n... [build timed out]"
		return stdout, stderr, fmt.Errorf("build timed out")
	}
	if err != nil {
		return stdout, stderr, err
	}
	return stdout, stderr, nil
}

func runSingleTest(tc models.TestCase, lc config.LanguageConfig, jailDir string, runOpts *models.StageConfig) models.TestResult {
	start := time.Now()

	wallTime := lc.DefaultLimits.WallTimeS
	if runOpts != nil && runOpts.Limits != nil && runOpts.Limits.WallTimeS != nil {
		wallTime = *runOpts.Limits.WallTimeS
	}

	memKB := lc.DefaultLimits.MemoryKB
	if runOpts != nil && runOpts.Limits != nil && runOpts.Limits.MemoryKB != nil {
		memKB = *runOpts.Limits.MemoryKB
	}
	memBytes := memKB * 1024

	procs := lc.DefaultLimits.MaxProcesses
	if runOpts != nil && runOpts.Limits != nil && runOpts.Limits.MaxProcesses != nil {
		procs = *runOpts.Limits.MaxProcesses
	}

	args := []string{
		"-Q",
		"--log", "/dev/null",
		"-Mo",
		"--time_limit", strconv.Itoa(wallTime),
		"--rlimit_as", strconv.Itoa(memBytes),
		"--rlimit_nproc", strconv.Itoa(procs),
		"--rlimit_fsize", "100",
		"-E", "PATH=/usr/local/bin:/usr/bin:/bin",
		"-B", "/bin",
		"-B", "/usr",
		"-B", "/lib",
		"-B", "/lib64",
		"-B", "/dev",
		"-B", "/etc",
		"-R", fmt.Sprintf("%s:/app", jailDir),
		"--cwd", "/app",
		"--",
	}
	args = append(args, lc.RunCmd...)

	if runOpts != nil {
		args = append(args, runOpts.Flags...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(wallTime+1)*time.Second)
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
		return failResult("internal_error", err.Error(), start)
	}

	outChan := make(chan string)
	errChan := make(chan string)

	go func() {
		outChan <- readCapped(stdoutPipe)
	}()
	go func() {
		errChan <- readCapped(stderrPipe)
	}()

	stdoutRaw := <-outChan
	stderrRaw := <-errChan

	err = cmd.Wait()
	duration := int(time.Since(start).Milliseconds())

	status := computeTestStatus(ctx, err, stdoutRaw, tc.ExpectedStdout, cmd.ProcessState)

	return models.TestResult{
		Status:       status,
		Stdout:       stdoutRaw,
		Stderr:       stderrRaw,
		DurationMs:   duration,
		MemoryPeakKB: 0,
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

func computeTestStatus(ctx context.Context, err error, stdout, expected string, ps *os.ProcessState) string {
	if ctx.Err() == context.DeadlineExceeded {
		return "time_exceeded"
	}
	if err != nil {
		if reason := signalKillReason(ps); reason != "" {
			return reason
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
