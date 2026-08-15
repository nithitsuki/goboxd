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
	"sync"
	"syscall"
	"time"

	"github.com/nithitsuki/goboxd/internal/cgroupv2"
	"github.com/nithitsuki/goboxd/internal/config"
	"github.com/nithitsuki/goboxd/internal/models"
	"github.com/nithitsuki/goboxd/internal/seccomp"
	"github.com/nithitsuki/goboxd/internal/uidpool"
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

// uidPool hands out a distinct unprivileged uid per jail (piston/isolate
// model). Sized to the same bound as the API concurrency semaphore so the pool
// can never be exhausted while jobs are admitted.
var uidPool = uidpool.New(uidpool.ConcurrentJobs())

func ExecuteRun(ctx context.Context, req models.RunRequest, lc config.LanguageConfig) (models.BuildResult, []models.TestResult, error) {
	buildRes := models.BuildResult{
		Status:     "ok",
		Stdout:     "",
		Stderr:     "",
		DurationMs: 0,
	}

	// Allocate this jail's unprivileged uid. Never share a uid between jails:
	// an allocation failure is an infrastructure error, not a silent fallback.
	uid, err := uidPool.Alloc()
	if err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = err.Error()
		return buildRes, nil, fmt.Errorf("allocating jail uid: %w", err)
	}
	defer uidPool.Release(uid)

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

	// The jail dir starts root-owned 0700; hand it to this jail's uid so the
	// jailed process can read/write its own files while other uids (other
	// jails) cannot even traverse it. The runner (root) keeps access for
	// cleanup and artifact reads.
	if err := os.Chown(jailDir, uid, uid); err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = fmt.Sprintf("failed to chown jail dir: %v", err)
		return buildRes, nil, fmt.Errorf("failed to chown jail dir: %w", err)
	}

	// Per-jail cgroup v2 directory (memory+pids limits enforced by nsjail
	// inside it). Any setup failure degrades to the rlimit path for this
	// request — limits stay enforced either way, never a gap.
	var jailCg *cgroupv2.Jail
	if cgroupv2.Default().Active() {
		jailCg, err = cgroupv2.Default().NewJail(filepath.Base(jailDir))
		if err != nil {
			log.Printf("[runner] cgroup jail setup failed, using rlimit fallback for this request: %v", err)
			jailCg = nil
		} else {
			defer jailCg.Teardown()
		}
	}

	srcName := resolveSourceName(req, lc)

	srcDir := filepath.Join(jailDir, "app")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = fmt.Sprintf("failed to create app dir: %v", err)
		return buildRes, nil, fmt.Errorf("failed to create app dir: %w", err)
	}
	if err := os.Chown(srcDir, uid, uid); err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = fmt.Sprintf("failed to chown app dir: %v", err)
		return buildRes, nil, fmt.Errorf("failed to chown app dir: %w", err)
	}
	srcPath := filepath.Join(srcDir, srcName)
	if err := writeSource(srcPath, []byte(req.Source)); err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = fmt.Sprintf("failed to write source: %v", err)
		return buildRes, nil, fmt.Errorf("failed to write source: %w", err)
	}
	if err := os.Chown(srcPath, uid, uid); err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = fmt.Sprintf("failed to chown source: %v", err)
		return buildRes, nil, fmt.Errorf("failed to chown source: %w", err)
	}

	// Build step for compiled languages
	buildStart := time.Now()
	if len(lc.BuildCmd) > 0 {
		buildRes, err = runBuild(ctx, jailDir, req, lc, uid, jailCg)
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
		if ctx.Err() != nil {
			break
		}
		res := runSingleTest(ctx, tc, lc, jailDir, req.Run, uid, jailCg)
		results = append(results, res)
	}

	return buildRes, results, nil
}

// runBuild compiles the source inside nsjail using lc.BuildCmd.
func runBuild(ctx context.Context, jailDir string, req models.RunRequest, lc config.LanguageConfig, uid int, jailCg *cgroupv2.Jail) (models.BuildResult, error) {
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

	stdout, stderr, _, err := execInJail(ctx, jailDir, cmdArgs, wallTime, memKB, procs, uid, jailCg)
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
// Each jail runs as its own unprivileged uid via -u/-g (inside:outside:count
// mapping U:U:1, keeping the user namespace): the jailed process is host-uid U
// with no capabilities, so an escape cannot reach root or other jails' files.
// cgroup flags are added when cgroup v2 is active (see cgroupv2 package);
// rlimit flags are ALWAYS kept as the fallback enforcement path. Docker
// Desktop does not expose a writable cgroup hierarchy, so it always runs on
// the rlimit path.
// --max_cpus caps CPU usage; tune via GOBOXD_MAX_CPUS env var.
func nsjailArgs(appDir string, wallTime, memKB, procs, uid int, jailCg *cgroupv2.Jail) ([]string, error) {
	// nsjail's --rlimit_as and --rlimit_fsize take MEGABYTES, not bytes
	// (nsjail help text; passing bytes silently yields a ~1024x larger limit,
	// which is how memory limits went unenforced before this fix).
	//
	// The guard is TIGHT: RLIMIT_AS equals the memory limit in MB. RLIMIT_AS
	// caps VIRTUAL address space, not resident memory; runtimes that reserve
	// large virtual regions up front (CoreCLR, BEAM) cannot fit under a tight
	// guard and are excluded from the registry via GOBOXD_EXCLUDE_LANGS.
	// Real resident-memory enforcement is cgroup v2 (memory.max) when active.
	memMB := memKB / 1024
	if memMB < 1 {
		memMB = 1
	}
	maxCPUs := os.Getenv("GOBOXD_MAX_CPUS")

	// Materialize the embedded seccomp policy (once) and pass it to nsjail.
	// nsjail compiles it with kafel at jail startup and applies the filter to
	// the jailed process. Failure here is an infrastructure error: the jail
	// must not start without its seccomp policy.
	seccompPolicy, err := seccomp.PolicyPath()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-Q",
		"--log", "/dev/null",
		// Run as this jail's unprivileged uid inside the jail. The dual map is
		// required: U:U:1 FIRST (the process runs as host uid U, unprivileged,
		// no caps, so it cannot setuid back to 0) and 0:0:1 second (nsjail's
		// mount-tree setup phase runs as inside-uid 0 before dropping to U;
		// unmapped, that phase is the overflow uid and mkdir on the jail root
		// dir fails EPERM when a stale root from an earlier run exists).
		"-u", fmt.Sprintf("%d:%d:1", uid, uid),
		"-u", "0:0:1",
		"-g", fmt.Sprintf("%d:%d:1", uid, uid),
		"-g", "0:0:1",
		"-Mo",
		// Jail /tmp as 256MB tmpfs. nsjail's -T default is only 4MB, which
		// is too small for compilers that spill temp files (swiftc needs
		// ~16MB for a Foundation build; go builds also cache into /tmp).
		"-m", "none:/tmp:tmpfs:size=268435456",
		// The jail runs in its own network namespace. Do not bring up
		// loopback: the jailed process must have zero network interfaces,
		// not even localhost (complete network isolation).
		"--iface_no_lo",
		"--bindmount", appDir + ":/app:rw",
		"--cwd", "/app",
		"--chroot", "/",
		"--proc_path", "/proc",
		"--time_limit", strconv.Itoa(wallTime),
		"--rlimit_as", strconv.Itoa(memMB),
		"--rlimit_nproc", strconv.Itoa(procs),
		"--rlimit_fsize", "100",
		"--rlimit_nofile", "65536",
	}
	// cgroup v2: when active, nsjail creates a NSJAIL.<pid> leaf under the
	// per-jail dir, moves the child there, and enforces memory.max/pids.max
	// (--cgroup_mem_max bytes, swap disabled so the OOM kill is deterministic,
	// --cgroup_pids_max procs). rlimit flags above are ALWAYS kept: they are
	// the fallback path when cgroup v2 is unavailable.
	if jailCg != nil {
		// --cgroup_mem_max is in BYTES (per nsjail help text; differs from
		// the MB rlimit flags above).
		memBytes := memKB * 1024
		args = append(args,
			"--use_cgroupv2",
			"--cgroupv2_mount", jailCg.Path(),
			"--cgroup_mem_max", strconv.Itoa(memBytes),
			"--cgroup_mem_swap_max", "0",
			"--cgroup_pids_max", strconv.Itoa(procs),
		)
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
		// Deny-list seccomp policy (DEFAULT ALLOW): blocks escape primitives
		// such as mount, ptrace, and kernel-module loading.
		"--seccomp_policy", seccompPolicy,
		"--",
	)
	return args, nil
}

func execInJail(ctx context.Context, jailDir string, cmdArgs []string, wallTime, memKB, procs, uid int, jailCg *cgroupv2.Jail) (string, string, bool, error) {
	appDir := filepath.Join(jailDir, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", "", false, fmt.Errorf("app dir: %w", err)
	}

	args, err := nsjailArgs(appDir, wallTime, memKB, procs, uid, jailCg)
	if err != nil {
		return "", "", false, fmt.Errorf("start: %w", err)
	}
	args = append(args, cmdArgs...)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(wallTime)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nsjail", args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", false, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", false, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", "", false, fmt.Errorf("start: %w", err)
	}

	// Poll the leaf cgroup's memory.events while the job runs: a cgroup OOM
	// kill lands as SIGKILL (exit 137), indistinguishable from a wall-time
	// kill without this. The leaf path is NSJAIL.<nsjail pid> under the
	// per-jail dir.
	var (
		oomMu     sync.Mutex
		isOOMKill bool
	)
	if jailCg != nil {
		oomStop := make(chan struct{})
		oomDone := make(chan struct{})
		go func() {
			defer close(oomDone)
			t := time.NewTicker(100 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-oomStop:
					return
				case <-t.C:
					n, err := jailCg.OOMKills()
					if err == nil && n > 0 {
						oomMu.Lock()
						isOOMKill = true
						oomMu.Unlock()
					}
				}
			}
		}()
		defer func() {
			close(oomStop)
			<-oomDone
		}()
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
	oomMu.Lock()
	oomKilled := isOOMKill
	oomMu.Unlock()
	if ctx.Err() == context.DeadlineExceeded {
		stderr += "\n... [build timed out]"
		log.Printf("[runner] build timed out (wall=%ds) stdout=%d stderr=%d", wallTime, len(stdout), len(stderr))
		return stdout, stderr, oomKilled, fmt.Errorf("build timed out")
	}
	if err != nil {
		log.Printf("[runner] nsjail build error: %v | stderr: %s", err, stderr)
		return stdout, stderr, oomKilled, err
	}
	return stdout, stderr, oomKilled, nil
}

func runSingleTest(ctx context.Context, tc models.TestCase, lc config.LanguageConfig, jailDir string, runOpts *models.StageConfig, uid int, jailCg *cgroupv2.Jail) models.TestResult {
	start := time.Now()

	if ctx.Err() != nil {
		return failResult("cancelled", "", start)
	}

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
	args, err := nsjailArgs(appDir, wallTime, memKB, procs, uid, jailCg)
	if err != nil {
		return failResult("internal_error", err.Error(), start)
	}
	// Measure this test's peak in isolation: memory.peak accumulates across
	// build + tests, so reset it before each exec.
	if jailCg != nil {
		if err := jailCg.ResetPeak(); err != nil {
			log.Printf("[runner] resetting cgroup peak: %v", err)
		}
	}
	runFlags := []string{}
	if runOpts != nil && len(runOpts.Flags) > 0 {
		runFlags = runOpts.Flags
	}
	args = append(args, expandFlags(lc.RunCmd, runFlags)...)

	// Go context deadline matches nsjail's time_limit so both fire together
	ctx, cancel := context.WithTimeout(ctx, time.Duration(wallTime)*time.Second)
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

	// cgroup OOM detection: poll the leaf's memory.events while the job runs
	// (a cgroup OOM kill is SIGKILL/exit 137, same as a wall-time kill).
	var (
		oomMu     sync.Mutex
		isOOMKill bool
	)
	if jailCg != nil {
		oomStop := make(chan struct{})
		oomDone := make(chan struct{})
		go func() {
			defer close(oomDone)
			t := time.NewTicker(100 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-oomStop:
					return
				case <-t.C:
					n, err := jailCg.OOMKills()
					if err == nil && n > 0 {
						oomMu.Lock()
						isOOMKill = true
						oomMu.Unlock()
					}
				}
			}
		}()
		defer func() {
			close(oomStop)
			<-oomDone
		}()
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

	oomMu.Lock()
	oomKilled := isOOMKill
	oomMu.Unlock()

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

	// Per-jail peak from the cgroup when active (includes the nsjail leaf's
	// usage); global RUSAGE_CHILDREN fallback otherwise.
	memPeak := 0
	if jailCg != nil {
		memPeak = jailCg.PeakKB()
	} else {
		memPeak = readMemoryPeakKB(cmd.ProcessState)
	}
	status := computeTestStatus(ctx, err, stdoutRaw, tc.ExpectedStdout, cmd.ProcessState, memPeak, memKB, wallTime, duration, oomKilled)

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

func computeTestStatus(ctx context.Context, err error, stdout, expected string, ps *os.ProcessState, memPeakKB int, memLimitKB int, wallTime int, durationMs int, oomKilled bool) string {
	// cgroup OOM kill (detected via the leaf's memory.events) is definitive:
	// the process died for memory, not wall time.
	if oomKilled {
		return "memory_exceeded"
	}
	// Client disconnect / request-context cancellation beats everything else:
	// the run was killed on purpose, not by its limits.
	if ctx.Err() == context.Canceled {
		return "cancelled"
	}
	// Check context deadline first (Go killed the process)
	if ctx.Err() == context.DeadlineExceeded {
		return "time_exceeded"
	}
	if err != nil {
		if reason := signalKillReason(ps); reason != "" {
			return reason
		}
		// nsjail exits 128+signal when its child was killed by a signal.
		// 137 (SIGKILL) covers wall-time kills, Go-context kills, and
		// container cgroup OOM kills that happen before the wall clock
		// fires (the duration heuristic below cannot catch those).
		if strings.Contains(err.Error(), "exit status 137") {
			return "time_exceeded"
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

// writeSource writes the user's source into the jail. O_NOFOLLOW|O_EXCL makes
// a symlink planted at srcPath fail the open instead of being followed
// (TOCTOU defense): the write must never land outside the jail's app dir.
func writeSource(srcPath string, data []byte) error {
	f, err := os.OpenFile(srcPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// resolveSourceName picks the source filename for a request. Languages with
// strategy "fixed" always use the configured filename (Java: the public class
// name must match the file name); others honor the client's filename and fall
// back to the configured one.
func resolveSourceName(req models.RunRequest, lc config.LanguageConfig) string {
	if lc.SourceFilenameStrategy == "fixed" {
		return lc.SourceFilename
	}
	if req.SourceFilename != "" {
		return req.SourceFilename
	}
	return lc.SourceFilename
}
