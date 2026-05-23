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
	"time"

	"github.com/thesouldev/goboxd/internal/config"
	"github.com/thesouldev/goboxd/internal/models"
)

// maxOutputBytes caps the output to prevent unbounded child output OOMs (Security Hole #6)
const maxOutputBytes = 64 * 1024 // 64 KiB

func ExecuteRun(req models.RunRequest, lc config.LanguageConfig) (*models.BuildResult, []models.TestResult, error) {
	// Security Hole #5: UID collisions. os.MkdirTemp guarantees unique, non-colliding directories.
	jailDir, err := os.MkdirTemp("", "goboxd-jail-*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create jail dir: %w", err)
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
		return nil, nil, fmt.Errorf("failed to write source: %w", err)
	}

	// TODO: Handle build stage if lc.BuildCmd exists

	var results []models.TestResult
	for _, tc := range req.Tests {
		res := runSingleTest(tc, lc, jailDir, req.Run)
		results = append(results, res)
	}

	return nil, results, nil
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

	// Minimal capabilities. Map required system dirs and mount jailDir to /app
	args := []string{
		"-Q",                             // Really quiet
		"--log", "/dev/null",             // Silence nsjail's internal warnings so they don't bleed into stderr
		"-Mo",                            // Mount options (don't keep mounted)
		"--time_limit", strconv.Itoa(wallTime),
		"--rlimit_as", strconv.Itoa(memBytes),
		"--rlimit_nproc", strconv.Itoa(procs),
		"--rlimit_fsize", "100",          // 100 MB max file size created by program
		"-E", "PATH=/usr/local/bin:/usr/bin:/bin", // Pass PATH so it can resolve program names
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

	// Append any user provided flags (whitelisting should happen before this stage ideally)
	if runOpts != nil {
		args = append(args, runOpts.Flags...)
	}

	// Set up context for strict timeout at the Go level as a backup
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

	// Read outputs concurrently with limits
	outChan := make(chan string)
	errChan := make(chan string)

	go func() {
		b, _ := io.ReadAll(io.LimitReader(stdoutPipe, maxOutputBytes))
		outChan <- string(b)
	}()
	go func() {
		b, _ := io.ReadAll(io.LimitReader(stderrPipe, maxOutputBytes))
		errChan <- string(b)
	}()

	stdoutRaw := <-outChan
	stderrRaw := <-errChan

	err = cmd.Wait()
	duration := int(time.Since(start).Milliseconds())

	status := "ok"
	if ctx.Err() == context.DeadlineExceeded || (err != nil && err.Error() == "signal: killed") {
		status = "timeout"
	} else if err != nil {
		status = "runtime_error"
	} else if tc.ExpectedStdout != "" && stdoutRaw != tc.ExpectedStdout {
		status = "wrong_answer"
	}

	return models.TestResult{
		Status:     status,
		Stdout:     stdoutRaw,
		Stderr:     stderrRaw,
		DurationMs: duration,
		// Placeholder for peak mem, parsing rusage or ps goes here in future
		MemoryPeakKB: 0,
	}
}

func failResult(status, stderr string, start time.Time) models.TestResult {
	return models.TestResult{
		Status:     status,
		Stdout:     "",
		Stderr:     stderr,
		DurationMs: int(time.Since(start).Milliseconds()),
	}
}
