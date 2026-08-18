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
	"runtime"
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

// cacheBaseDir is the root directory for per-UID build caches.
const cacheBaseDir = "/var/cache/goboxd"

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
// model). Sized to maxJobs x (NumCPU + 1): a request holds one uid for its
// template jail for the whole request plus up to NumCPU uids for its parallel
// tests, and up to maxJobs requests are in flight, so the pool can never be
// exhausted while the API admission gate admits jobs.
var uidPool = uidpool.New(uidpool.UidBudget())

func ExecuteRun(ctx context.Context, req models.RunRequest, lc config.LanguageConfig) (models.BuildResult, []models.TestResult, error) {
	buildRes := models.BuildResult{
		Status:     "ok",
		Stdout:     "",
		Stderr:     "",
		DurationMs: 0,
	}

	srcName := resolveSourceName(req, lc)

	// Materialize the template jail: uid, dir+chown, cgroup leaf, source.
	// The build runs into this jail; sequential tests reuse it, parallel
	// tests are seeded from it (see jail.go).
	tmpl, err := newJail(req, lc, srcName)
	if err != nil {
		buildRes.Status = "internal_error"
		buildRes.Stderr = err.Error()
		return buildRes, nil, fmt.Errorf("materializing jail: %w", err)
	}
	// Security Hole #7: Stale jail directories. teardown removes the jail dir
	// (plus the uid and cgroup leaf) on every path.
	defer tmpl.teardown()

	// Ensure per-UID cache directories exist and are chowned to the jail uid.
	// Failure here is non-fatal: builds work without cache, just slower.
	if err := ensureCacheDirs(tmpl.uid); err != nil {
		log.Printf("[runner] cache dir setup failed (builds will run without cache): %v", err)
	}

	// Build step for compiled languages
	buildStart := time.Now()
	if len(lc.BuildCmd) > 0 {
		buildRes, err = runBuild(ctx, tmpl.dir, req, lc, tmpl.uid, tmpl.cg)
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

	// Per-request output cap.
	outputCap := maxOutputBytes
	if req.MaxOutputBytes != nil && *req.MaxOutputBytes > 0 {
		outputCap = *req.MaxOutputBytes
	}

	// Check if parallel execution is requested.
	// effectiveParallel <= 1 means sequential.
	effectiveParallel := 1
	if req.MaxParallel != nil && *req.MaxParallel > 1 {
		effectiveParallel = *req.MaxParallel
		// Cap at runtime.NumCPU() to avoid oversubscription.
		if numCPU := runtime.NumCPU(); numCPU > 0 && effectiveParallel > numCPU {
			effectiveParallel = numCPU
		}
	}

	if effectiveParallel > 1 {
		// Parallel execution: bounded concurrency via semaphore channel.
		// Each test gets its own jail (nsjail cannot share a bind-mount
		// across concurrent processes), materialized fresh from the build
		// template so the source AND the build artifact land in every jail.
		results = make([]models.TestResult, len(req.Tests))
		var wg sync.WaitGroup
		sem := make(chan struct{}, effectiveParallel)
		for i, tc := range req.Tests {
			if ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{} // acquire semaphore
			go func(idx int, tc models.TestCase) {
				defer wg.Done()
				defer func() { <-sem }() // release semaphore

				parJail, err := newJail(req, lc, srcName)
				if err != nil {
					results[idx] = models.TestResult{
						Status: "internal_error",
						Stderr: err.Error(),
					}
					return
				}
				defer parJail.teardown()
				if err := parJail.seedFrom(tmpl); err != nil {
					results[idx] = models.TestResult{
						Status: "internal_error",
						Stderr: fmt.Sprintf("seeding jail: %v", err),
					}
					return
				}

				results[idx] = runSingleTest(ctx, tc, lc, parJail.dir, req.Run, parJail.uid, parJail.cg, outputCap)
			}(i, tc)
		}
		wg.Wait()
	} else {
		// Sequential execution (default): reuse the template jail for all tests.
		for _, tc := range req.Tests {
			if ctx.Err() != nil {
				break
			}
			res := runSingleTest(ctx, tc, lc, tmpl.dir, req.Run, tmpl.uid, tmpl.cg, outputCap)
			results = append(results, res)
		}
	}

	return buildRes, results, nil
}

// runBuild compiles the source inside nsjail using lc.BuildCmd.
func runBuild(ctx context.Context, jailDir string, req models.RunRequest, lc config.LanguageConfig, uid int, jailCg *cgroupv2.Jail) (models.BuildResult, error) {
	wallTime := lc.BuildLimits.WallTimeS
	memKB := lc.BuildLimits.MemoryKB
	procs := lc.BuildLimits.MaxProcesses
	outputCap := maxOutputBytes
	if req.MaxOutputBytes != nil && *req.MaxOutputBytes > 0 {
		outputCap = *req.MaxOutputBytes
	}
	cpuLimit := lc.BuildLimits.CpuTimeS
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
		if req.Build.Limits.CpuTimeS != nil {
			cpuLimit = *req.Build.Limits.CpuTimeS
		}
	}

	cmdArgs := make([]string, len(lc.BuildCmd))
	copy(cmdArgs, lc.BuildCmd)
	flags := []string{}
	if req.Build != nil && len(req.Build.Flags) > 0 {
		flags = req.Build.Flags
	}
	cmdArgs = expandFlags(cmdArgs, flags)

	stdout, stderr, _, cpuKilled, cpuUs, err := execInJail(ctx, jailDir, cmdArgs, wallTime, cpuLimit, memKB, procs, uid, jailCg, outputCap)
	if cpuKilled {
		log.Printf("[runner] build for %s hit its cpu limit (%ds)", req.Language, cpuLimit)
	}
	res := models.BuildResult{
		Status:    "ok",
		Stdout:    stdout,
		Stderr:    stderr,
		CpuTimeMs: int(cpuUs / 1000),
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

// jailEnv builds the -E argument pairs for nsjail from the environment
// allowlist. nsjail clears the child env by default, so these pairs are the
// complete jail environment. PATH is copied from the server env with a
// fallback to the hardcoded value; the other vars are fixed. GOCACHE and
// CCACHE_DIR are added by nsjailArgs after the cache bind-mounts. The
// values are read at call time (no cached global) so tests can use t.Setenv.
func jailEnv() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{
		"-E", "PATH=" + path,
		"-E", "HOME=/tmp",
		"-E", "LANG=C.UTF-8",
		"-E", "LC_ALL=C.UTF-8",
	}
}

// nsjailArgs builds the common nsjail arguments for both build and run steps.
// Each jail runs as its own unprivileged uid via -u/-g (inside:outside:count
// mapping U:U:1, keeping the user namespace): the jailed process is host-uid U
// with no capabilities, so an escape cannot reach root or other jails' files.
// cgroup flags are added when cgroup v2 is active (see cgroupv2 package);
// rlimit flags are ALWAYS kept as the fallback enforcement path. Docker
// Desktop does not expose a writable cgroup hierarchy, so it always runs on
// the rlimit path. The cpu limit follows the same split: the cgroup path
// polls and kills on cpu usage, the rlimit path gets a kernel SIGKILL at
// --rlimit_cpu seconds (nsjail sets the soft and hard limits equal).
// --max_cpus caps CPU usage; tune via GOBOXD_MAX_CPUS env var.
func nsjailArgs(appDir string, wallTime, cpuLimit, memKB, procs, uid int, jailCg *cgroupv2.Jail) ([]string, error) {
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
	// RLIMIT_CPU: armed only when the cgroup cannot enforce the cpu limit
	// (cgroup inactive or the cpu controller not delegated to this jail).
	// Exactly one authority is armed per exec: when the cgroup path is up,
	// the poller kills at the limit and an armed kernel timer would race it
	// (nsjail sets soft==hard, so the kernel SIGKILLs at the limit and wins
	// every race, reading as exit 137). "inf" keeps nsjail's implicit 600s
	// default off when there is no limit to enforce.
	if cpuLimit > 0 && (jailCg == nil || !jailCg.CPUActive()) {
		args = append(args, "--rlimit_cpu", strconv.Itoa(cpuLimit))
	} else {
		args = append(args, "--rlimit_cpu", "inf")
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
	)
	// The jail environment is an explicit allowlist. nsjail clears the child
	// env by default, so these -E flags are the complete jail environment:
	// nothing from the server env (credentials, GOBOXD_*, proxy vars) can
	// reach the jail.
	args = append(args, jailEnv()...)

	// Per-UID build cache bind-mounts. The cache dirs live under
	// /var/cache/goboxd/uid-<uid>/ and are bind-mounted into the jail
	// at /app/.gocache (inside the app dir, which is writable by the
	// jailed uid). GOCACHE points to the go-build subdir; CCACHE_DIR
	// points to the ccache subdir. If ccache is not installed on the
	// host, only GOCACHE is set.
	uidCacheDir := filepath.Join(cacheBaseDir, fmt.Sprintf("uid-%d", uid))
	gocacheDir := filepath.Join(uidCacheDir, "go-build")
	if info, err := os.Stat(gocacheDir); err == nil && info.IsDir() {
		args = append(args,
			"--bindmount", gocacheDir+":/app/.gocache:rw",
		)
	}
	args = append(args, "-E", "GOCACHE=/app/.gocache")

	// ccache: only bind-mount if the ccache binary exists on the host.
	if _, err := exec.LookPath("ccache"); err == nil {
		ccacheDir := filepath.Join(uidCacheDir, "ccache")
		if info, err := os.Stat(ccacheDir); err == nil && info.IsDir() {
			args = append(args,
				"--bindmount", ccacheDir+":/app/.ccache:rw",
			)
		}
		args = append(args, "-E", "CCACHE_DIR=/app/.ccache")
	}

	args = append(args,
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

func execInJail(ctx context.Context, jailDir string, cmdArgs []string, wallTime, cpuLimit, memKB, procs, uid int, jailCg *cgroupv2.Jail, outputCap int) (stdout, stderr string, oomKilled, cpuKilled bool, cpuTimeUs int64, err error) {
	appDir := filepath.Join(jailDir, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", "", false, false, 0, fmt.Errorf("app dir: %w", err)
	}

	args, err := nsjailArgs(appDir, wallTime, cpuLimit, memKB, procs, uid, jailCg)
	if err != nil {
		return "", "", false, false, 0, fmt.Errorf("start: %w", err)
	}
	args = append(args, cmdArgs...)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(wallTime)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nsjail", args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", false, false, 0, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", false, false, 0, fmt.Errorf("stderr pipe: %w", err)
	}

	// Baseline the cpu counters before Start: the cgroup usage is
	// hierarchical and cumulative across the jail (build + tests), so this
	// exec's cpu time is the post-Wait delta. The rusage snapshot is the
	// fallback measurement when the cpu controller is unavailable.
	baselineUS, baselineOK, rusageBefore := cpuBaseline(jailCg)
	// Same for the OOM counter: classification must be "oom_kill increased
	// since this exec", not "any oom_kill ever" (a leftover leaf from an
	// earlier exec of this jail carries its own kill count).
	oomBase := oomBaseline(jailCg)

	if err := cmd.Start(); err != nil {
		return "", "", false, false, 0, fmt.Errorf("start: %w", err)
	}

	// One poll goroutine covers both cgroup checks while the job runs:
	// an OOM kill in a leaf reads as memory_exceeded, and cpu usage at or
	// above the limit kills the process once and reads as cpu_time_exceeded.
	// Both would otherwise be indistinguishable from a wall-time SIGKILL.
	var (
		oomMu     sync.Mutex
		isOOMKill bool
		cpuMu     sync.Mutex
		isCPUKill bool
	)
	if jailCg != nil {
		stopPoller := startJailPoller(cmd, jailCg, oomBase, int64(cpuLimit)*1e6, baselineUS, baselineOK, &oomMu, &cpuMu, &isOOMKill, &isCPUKill)
		defer stopPoller()
	}

	// Read stdout/stderr concurrently to avoid pipe buffer deadlocks
	outChan := make(chan string, 1)
	errChan := make(chan string, 1)
	go func() {
		defer func() { _ = recover() }()
		outChan <- readCapped(stdoutPipe, outputCap)
	}()
	go func() {
		defer func() { _ = recover() }()
		errChan <- readCapped(stderrPipe, outputCap)
	}()

	stdout = <-outChan
	stderr = <-errChan

	err = cmd.Wait()
	oomMu.Lock()
	oomKilled = isOOMKill
	oomMu.Unlock()
	// Final OOM check after Wait: an OOM kill landing after the last poller
	// tick is already recorded in the leaf's memory.events.
	if !oomKilled && jailCg != nil {
		if since, e := jailCg.OOMKillsSince(oomBase); e == nil && since {
			oomKilled = true
		}
	}
	cpuMu.Lock()
	cpuKilled = isCPUKill
	cpuMu.Unlock()

	var rusageAfter syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &rusageAfter); err != nil {
		log.Printf("[runner] getrusage after exec: %v", err)
	}
	cpuTimeUs = cpuTimeUS(jailCg, baselineUS, baselineOK, &rusageBefore, &rusageAfter)

	if ctx.Err() == context.DeadlineExceeded {
		stderr += "\n... [build timed out]"
		log.Printf("[runner] build timed out (wall=%ds) stdout=%d stderr=%d", wallTime, len(stdout), len(stderr))
		return stdout, stderr, oomKilled, cpuKilled, cpuTimeUs, fmt.Errorf("build timed out")
	}
	if err != nil {
		log.Printf("[runner] nsjail build error: %v | stderr: %s", err, stderr)
		return stdout, stderr, oomKilled, cpuKilled, cpuTimeUs, err
	}
	return stdout, stderr, oomKilled, cpuKilled, cpuTimeUs, nil
}

// startJailPoller starts the merged OOM/CPU poll goroutine for one exec and
// returns a stop function (close the stop channel, then drain the done
// channel). The 50ms interval bounds cpu-kill latency. The cpu kill fires
// once per exec: the mutex guard makes the first tick that crosses the limit
// the only one that kills. The OOM check compares against oomBaseline, the
// kill count at this exec's Start: only a kill during THIS exec counts.
func startJailPoller(cmd *exec.Cmd, jailCg *cgroupv2.Jail, oomBaseline uint64, cpuLimitUS, baselineUS int64, baselineOK bool, oomMu, cpuMu *sync.Mutex, oomKilled, cpuKilled *bool) func() {
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				since, err := jailCg.OOMKillsSince(oomBaseline)
				if err == nil && since {
					oomMu.Lock()
					*oomKilled = true
					oomMu.Unlock()
				}
				if cpuLimitUS <= 0 || !baselineOK {
					continue
				}
				us, err := jailCg.CPUUsageUS()
				if err != nil || us-baselineUS < cpuLimitUS {
					continue
				}
				cpuMu.Lock()
				if !*cpuKilled {
					*cpuKilled = true
					if err := cmd.Process.Kill(); err != nil {
						log.Printf("[runner] killing cpu-exceeded process: %v", err)
					}
				}
				cpuMu.Unlock()
			}
		}
	}()
	return func() {
		close(stopCh)
		<-done
	}
}

// oomBaseline captures the jail's cumulative oom_kill count before Start:
// OOM classification must be "increased since this exec", never "any kill
// ever" (a leaf left by an earlier exec keeps its kill count).
func oomBaseline(jailCg *cgroupv2.Jail) uint64 {
	if jailCg == nil {
		return 0
	}
	n, err := jailCg.OOMKills()
	if err != nil {
		log.Printf("[runner] reading oom_kill baseline: %v", err)
		return 0
	}
	return n
}

// cpuBaseline captures the starting point for one exec's cpu measurement:
// the cgroup usage right before Start (the cpu.stat reset is best-effort;
// kernels without reset support keep the delta exact anyway) and a
// RUSAGE_CHILDREN snapshot for the rlimit fallback path. Both cgroup reads
// are gated on the cpu controller being active for this jail: on
// memory-only hosts cpu.stat does not exist, so reading it would log ENOENT
// noise on every exec.
func cpuBaseline(jailCg *cgroupv2.Jail) (baselineUS int64, baselineOK bool, rusageBefore syscall.Rusage) {
	if jailCg != nil && jailCg.CPUActive() {
		if err := jailCg.ResetCPU(); err != nil {
			log.Printf("[runner] resetting cgroup cpu.stat: %v", err)
		}
		if us, err := jailCg.CPUUsageUS(); err == nil {
			baselineUS, baselineOK = us, true
		} else {
			log.Printf("[runner] reading cgroup cpu.stat baseline: %v", err)
		}
	}
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &rusageBefore); err != nil {
		log.Printf("[runner] getrusage before exec: %v", err)
	}
	return baselineUS, baselineOK, rusageBefore
}

// cpuTimeUS measures one exec's cpu time. The cgroup delta is exact: it
// counts every thread of every descendant that ran in the jail, so it is the
// authority only while the cpu controller is active for this jail (the same
// condition as enforcement — an inert controller reads 0 forever). The
// rusage delta is the fallback otherwise. It is approximate:
// RUSAGE_CHILDREN is process-global, so concurrent jails and nsjail's own
// overhead can leak into it.
func cpuTimeUS(jailCg *cgroupv2.Jail, baselineUS int64, baselineOK bool, before, after *syscall.Rusage) int64 {
	if jailCg != nil && jailCg.CPUActive() && baselineOK {
		final, err := jailCg.CPUUsageUS()
		if err == nil && final >= baselineUS {
			return final - baselineUS
		}
		if err != nil {
			log.Printf("[runner] reading cgroup cpu.stat after exec: %v (falling back to rusage)", err)
		}
	}
	b := timevalUS(before.Utime) + timevalUS(before.Stime)
	a := timevalUS(after.Utime) + timevalUS(after.Stime)
	if a >= b {
		return a - b
	}
	return 0
}

func timevalUS(tv syscall.Timeval) int64 { return tv.Sec*1000000 + tv.Usec }

func runSingleTest(ctx context.Context, tc models.TestCase, lc config.LanguageConfig, jailDir string, runOpts *models.StageConfig, uid int, jailCg *cgroupv2.Jail, outputCap int) models.TestResult {
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
	cpuLimit := lc.RunLimits.CpuTimeS
	if runOpts != nil && runOpts.Limits != nil && runOpts.Limits.CpuTimeS != nil {
		cpuLimit = *runOpts.Limits.CpuTimeS
	}

	appDir := filepath.Join(jailDir, "app")
	args, err := nsjailArgs(appDir, wallTime, cpuLimit, memKB, procs, uid, jailCg)
	if err != nil {
		return failResult("internal_error", err.Error(), start)
	}
	// Measure this test's peak in isolation: memory.peak accumulates across
	// build + tests, so reset it before each exec. The cpu.stat usage is
	// handled the same way via a pre-Start baseline (the reset write is
	// best-effort; the delta is exact either way).
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

	// Baseline the cpu counters before Start (same contract as execInJail):
	// the jail's cpu.stat is cumulative, so this exec's cpu time is the
	// post-Wait delta.
	baselineUS, baselineOK, rusageBefore := cpuBaseline(jailCg)
	// OOM baseline: a kill in a leftover leaf from an earlier exec of this
	// jail must not count against THIS exec.
	oomBase := oomBaseline(jailCg)

	if err := cmd.Start(); err != nil {
		log.Printf("[runner] nsjail start failed: %v", err)
		return failResult("internal_error", err.Error(), start)
	}

	// Merged OOM/CPU poller: a cgroup OOM kill is memory_exceeded, cpu usage
	// at or above the limit kills the process once and is cpu_time_exceeded.
	// Both are SIGKILL-shaped without these checks.
	var (
		oomMu     sync.Mutex
		isOOMKill bool
		cpuMu     sync.Mutex
		isCPUKill bool
	)
	if jailCg != nil {
		stopPoller := startJailPoller(cmd, jailCg, oomBase, int64(cpuLimit)*1e6, baselineUS, baselineOK, &oomMu, &cpuMu, &isOOMKill, &isCPUKill)
		defer stopPoller()
	}

	outChan := make(chan string)
	errChan := make(chan string)

	go func() {
		defer func() { _ = recover() }()
		outChan <- readCapped(stdoutPipe, outputCap)
	}()
	go func() {
		defer func() { _ = recover() }()
		errChan <- readCapped(stderrPipe, outputCap)
	}()

	stdoutRaw := <-outChan
	stderrRaw := <-errChan

	err = cmd.Wait()
	duration := int(time.Since(start).Milliseconds())

	oomMu.Lock()
	oomKilled := isOOMKill
	oomMu.Unlock()
	// Final OOM check after Wait: a kill landing after the last poller tick
	// is already in the leaf's memory.events.
	if !oomKilled && jailCg != nil {
		if since, e := jailCg.OOMKillsSince(oomBase); e == nil && since {
			oomKilled = true
		}
	}
	cpuMu.Lock()
	cpuKilled := isCPUKill
	cpuMu.Unlock()

	var rusageAfter syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &rusageAfter); err != nil {
		log.Printf("[runner] getrusage after exec: %v", err)
	}
	cpuUs := cpuTimeUS(jailCg, baselineUS, baselineOK, &rusageBefore, &rusageAfter)

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
	status := computeTestStatus(ctx, err, stdoutRaw, tc.ExpectedStdout, cmd.ProcessState, oomKilled, cpuOutcome{
		limitUS: int64(cpuLimit) * 1e6,
		timeUS:  cpuUs,
		killed:  cpuKilled,
	})

	exitCode, termSig := exitFacts(cmd.ProcessState)
	return models.TestResult{
		Status:            status,
		Stdout:            stdoutRaw,
		Stderr:            stderrRaw,
		DurationMs:        duration,
		CpuTimeMs:         int(cpuUs / 1000),
		MemoryPeakKB:      memPeak,
		ExitCode:          exitCode,
		TerminationSignal: termSig,
	}
}

// exitFacts derives the exit facts for a finished process from its
// ProcessState. Semantics:
//   - nil state: no process ever started -> (0, 0)
//   - signaled (any signal death of nsjail, e.g. a goboxd kill or a host
//     OOM kill): (-1, signal)
//   - nsjail signal propagation (128+signal, signals 1..64): (code, code-128)
//   - anything else: a plain user exit (code, 0)
//
// Note the accepted ambiguity: a user program that exits 137 is
// indistinguishable from a SIGKILL (both read (137, 9)).
func exitFacts(ps *os.ProcessState) (exitCode, sig int) {
	if ps == nil {
		return 0, 0
	}
	if status, ok := ps.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return -1, int(status.Signal())
	}
	code := ps.ExitCode()
	if code >= 129 && code <= 192 {
		return code, code - 128
	}
	return code, 0
}

// signalKillReason checks if the process was killed by a signal and determines why.
func signalKillReason(ps *os.ProcessState) string {
	if ps == nil {
		return ""
	}
	status, ok := ps.Sys().(syscall.WaitStatus)
	if !ok {
		return ""
	}
	return signalReasonFromStatus(status)
}

// signalReasonFromStatus classifies a wait status into a result status.
// Pure (no os.ProcessState) so tests can feed it synthetic statuses.
func signalReasonFromStatus(status syscall.WaitStatus) string {
	if !status.Signaled() {
		// nsjail exits 128+signal when its child was killed by a signal:
		// SIGXCPU from RLIMIT_CPU reads as exit 152, not a signaled status.
		if status.ExitStatus() == 128+int(syscall.SIGXCPU) {
			return "cpu_time_exceeded"
		}
		return ""
	}
	sig := status.Signal()
	switch sig {
	case syscall.SIGKILL:
		return "time_exceeded"
	case syscall.SIGSEGV, syscall.SIGABRT:
		return "memory_exceeded"
	case syscall.SIGXCPU:
		return "cpu_time_exceeded"
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

// cpuOutcome carries one exec's cpu accounting and kill state into status
// classification.
type cpuOutcome struct {
	limitUS int64 // cpu limit in microseconds; 0 = no limit
	timeUS  int64 // measured cpu time in microseconds
	killed  bool  // the poller killed the process at the cgroup limit
}

func computeTestStatus(ctx context.Context, err error, stdout, expected string, ps *os.ProcessState, oomKilled bool, cpu cpuOutcome) string {
	// Client disconnect / request-context cancellation beats everything else:
	// the run was killed on purpose, not by its limits.
	if ctx.Err() == context.Canceled {
		return "cancelled"
	}
	// cgroup OOM kill (detected via the leaf's memory.events) is definitive:
	// the process died for memory, not wall time.
	if oomKilled {
		return "memory_exceeded"
	}
	// cpu limit: the poller killed the process when its cgroup usage hit the
	// limit, or the kernel SIGKILLed it at the RLIMIT_CPU limit (nsjail reads
	// the child's signal death as exit 137).
	if cpu.killed || (err != nil && signalKillReason(ps) == "cpu_time_exceeded") {
		return "cpu_time_exceeded"
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
		// fires (signal detection failed: ps is nil or nsjail exited).
		// On the rlimit path the kernel SIGKILLs at the cpu limit (nsjail
		// sets soft == hard), which also reads as 137. The measured cpu time
		// disambiguates: the kernel fires only after the limit's worth of
		// cpu time, so a wall kill can never carry more cpu time than the
		// limit. (The rusage fallback is approximate: concurrent jails can
		// inflate it on rlimit-only hosts.)
		if strings.Contains(err.Error(), "exit status 137") {
			if cpu.limitUS > 0 && cpu.timeUS >= cpu.limitUS {
				return "cpu_time_exceeded"
			}
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

// ensureCacheDirs creates the per-UID cache directories under /var/cache/goboxd/uid-<uid>/
// and chowns them to the jail uid. The dirs persist across requests so that
// repeated builds reuse the Go build cache and ccache.
// The subdirs are named to match Go's convention (go-build) so that the
// jail can mount uid-<uid>/ as /root/.cache and Go finds go-build inside.
func ensureCacheDirs(uid int) error {
	uidDir := filepath.Join(cacheBaseDir, fmt.Sprintf("uid-%d", uid))
	gocache := filepath.Join(uidDir, "go-build")
	ccache := filepath.Join(uidDir, "ccache")

	if err := os.MkdirAll(gocache, 0755); err != nil {
		if os.IsPermission(err) || os.IsNotExist(err) {
			log.Printf("[runner] /var/cache not writable, builds will run without cache: %v", err)
			return nil
		}
		return fmt.Errorf("creating gocache dir: %w", err)
	}
	if err := os.Chown(gocache, uid, uid); err != nil {
		return fmt.Errorf("chown gocache dir: %w", err)
	}

	if err := os.MkdirAll(ccache, 0755); err != nil {
		if os.IsPermission(err) || os.IsNotExist(err) {
			log.Printf("[runner] /var/cache not writable, builds will run without ccache: %v", err)
			return nil
		}
		return fmt.Errorf("creating ccache dir: %w", err)
	}
	if err := os.Chown(ccache, uid, uid); err != nil {
		return fmt.Errorf("chown ccache dir: %w", err)
	}

	return nil
}

// SweepCaches removes /var/cache/goboxd/uid-* dirs older than maxAge.
// Use maxAge=0 to remove all cache dirs (force sweep at shutdown).
func SweepCaches(maxAge time.Duration) {
	entries, err := os.ReadDir(cacheBaseDir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "cache sweep: reading %s: %v\n", cacheBaseDir, err)
		}
		return
	}
	now := time.Now()
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "uid-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if maxAge == 0 || now.Sub(info.ModTime()) > maxAge {
			path := filepath.Join(cacheBaseDir, e.Name())
			if err := os.RemoveAll(path); err != nil {
				fmt.Fprintf(os.Stderr, "cache sweep: removing %s: %v\n", path, err)
			}
		}
	}
}

// SweepOrphans removes jail dirs older than 30 minutes. Call at startup.
func SweepOrphans() { sweepJails(30 * time.Minute) }

// SweepAllJails removes every jail dir regardless of age. Call at shutdown
// after the drain, when no jail can be active.
func SweepAllJails() { sweepJails(0) }

// sweepJails removes jail dirs older than minAge from the temp dir.
func sweepJails(minAge time.Duration) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "jail sweep: reading temp dir: %v\n", err)
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
		if now.Sub(info.ModTime()) > minAge {
			path := filepath.Join(os.TempDir(), e.Name())
			if err := os.RemoveAll(path); err != nil {
				fmt.Fprintf(os.Stderr, "jail sweep: removing %s: %v\n", path, err)
			}
		}
	}
}

// readCapped reads from r up to limit+1, truncates with a marker if capped.
func readCapped(r io.Reader, limit int) string {
	raw, _ := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if len(raw) > limit {
		raw = raw[:limit]
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
