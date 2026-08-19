// The exec primitive (C2): one function owns the full nsjail exec sequence
// shared by the build and test paths. execInJail (build) and runSingleTest's
// inline sequence (test) were two forks of the same pipe/baseline/poller/
// capped-read/Wait/post-check machinery; both now call execJail and only
// interpret the returned ExecOutcome. Infrastructure failures are classified
// by a typed Infra field instead of callers matching error text for
// "pipe:"/"start:".
package runner

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// execLimits carries one exec's resource limits. wallTime and cpuLimit are in
// seconds, memKB in kilobytes, and procs is the process-count cap. All are
// passed through to nsjail unchanged (see nsjailArgs); per-exec overrides are
// resolved by the callers before building the struct.
type execLimits struct {
	wallTime int
	cpuLimit int
	memKB    int
	procs    int
}

// ExecOutcome is the structured result of one execJail call: the captured
// output streams, the kill/limit facts (cpu, oom), the cpu accounting, the
// exit facts, and whether the failure was infrastructure (nsjail itself)
// rather than the user program. Err carries the exec error when there was one
// (nil on a clean run). ps is the finished process state (nil when the
// process never started); it is unexported because only kill-reason
// classification (computeTestStatus) needs it.
type ExecOutcome struct {
	Stdout     string
	Stderr     string
	OOMKilled  bool
	CPUKilled  bool
	CPUTimeUS  int64
	ExitCode   int
	TermSignal int
	Infra      bool
	Err        error

	ps *os.ProcessState // unexported: kill-reason classification (computeTestStatus)
}

// execJail runs cmdArgs (via nsjail) inside the jail with the given limits
// and stdin, capping each output stream at outputCap bytes, and returns the
// full outcome. It owns the entire exec sequence — the nsjail args, the stdin
// buffer, the stdout/stderr pipes, the cpu/oom baselines, the merged 50ms
// OOM/CPU poller, the capped reads, Wait, the post-Wait OOM/CPU checks, the
// cpu accounting, the exit facts, and the typed Infra classification — so the
// build and test callers are thin interpreters over the outcome. The jailed
// command runs as j.uid with j's cgroup; appDir = j.appDir().
//
// Infra is set for failures of the infrastructure itself: nsjail args
// construction, stdout/stderr pipe creation, and process start — the same
// set the old isInfraError text-matching caught ("pipe:"/"start:"), now
// classified by construction instead of error text. Exit codes are NOT
// infra: nsjail propagates the jailed command's own exit code (including
// 255), so a user program, compiler, or runtime failing with 255 is
// byte-indistinguishable from nsjail's internal failure to exec the
// commanded binary — flagging 255 would turn legitimate runtime/build
// errors into internal_error.
func execJail(ctx context.Context, j *jail, cmdArgs []string, lim execLimits, stdin string, outputCap int) ExecOutcome {
	appDir := j.appDir()
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return ExecOutcome{Err: fmt.Errorf("app dir: %w", err)}
	}

	args, err := nsjailArgs(appDir, lim.wallTime, lim.cpuLimit, lim.memKB, lim.procs, j.uid, j.cg, j.seccomp)
	if err != nil {
		return ExecOutcome{Err: fmt.Errorf("start: %w", err), Infra: true}
	}
	args = append(args, cmdArgs...)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(lim.wallTime)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nsjail", args...)

	if stdin != "" {
		cmd.Stdin = bytes.NewBufferString(stdin)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return ExecOutcome{Err: fmt.Errorf("stdout pipe: %w", err), Infra: true}
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return ExecOutcome{Err: fmt.Errorf("stderr pipe: %w", err), Infra: true}
	}

	// Baseline the cpu counters before Start: the cgroup usage is hierarchical
	// and cumulative across the jail (build + tests), so this exec's cpu time
	// is the post-Wait delta. The rusage snapshot is the fallback measurement
	// when the cpu controller is unavailable. Same for the OOM counter: a kill
	// in a leftover leaf from an earlier exec of this jail must not count
	// against THIS exec.
	baselineUS, baselineOK, rusageBefore := cpuBaseline(j.cg)
	oomBase := oomBaseline(j.cg)

	if err := cmd.Start(); err != nil {
		log.Printf("[runner] nsjail start failed: %v", err)
		return ExecOutcome{Err: fmt.Errorf("start: %w", err), Infra: true}
	}

	// One poll goroutine covers both cgroup checks while the job runs: an OOM
	// kill in a leaf reads as memory_exceeded, and cpu usage at or above the
	// limit kills the process once and reads as cpu_time_exceeded. Both would
	// otherwise be indistinguishable from a wall-time SIGKILL.
	var (
		oomMu     sync.Mutex
		isOOMKill bool
		cpuMu     sync.Mutex
		isCPUKill bool
	)
	if j.cg != nil {
		stopPoller := startJailPoller(cmd, j.cg, oomBase, int64(lim.cpuLimit)*1e6, baselineUS, baselineOK, &oomMu, &cpuMu, &isOOMKill, &isCPUKill)
		defer stopPoller()
	}

	// Read stdout/stderr concurrently to avoid pipe buffer deadlocks.
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

	stdout := <-outChan
	stderr := <-errChan

	err = cmd.Wait()

	oomMu.Lock()
	oomKilled := isOOMKill
	oomMu.Unlock()
	// Final OOM check after Wait: an OOM kill landing after the last poller
	// tick is already recorded in the leaf's memory.events.
	if !oomKilled && j.cg != nil {
		if since, e := j.cg.OOMKillsSince(oomBase); e == nil && since {
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
	cpuTimeUs := cpuTimeUS(j.cg, baselineUS, baselineOK, &rusageBefore, &rusageAfter)

	exitCode, termSig := exitFacts(cmd.ProcessState)

	outcome := ExecOutcome{
		Stdout:     stdout,
		Stderr:     stderr,
		OOMKilled:  oomKilled,
		CPUKilled:  cpuKilled,
		CPUTimeUS:  cpuTimeUs,
		ExitCode:   exitCode,
		TermSignal: termSig,
		Err:        err,
		ps:         cmd.ProcessState,
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("[runner] exec timed out (wall=%ds) stdout=%d stderr=%d", lim.wallTime, len(stdout), len(stderr))
			return outcome
		}
		// No further infra classification here: a non-nil Wait error with a
		// real exit code is the jailed command's own result (nsjail propagates
		// the inner exit code, 255 included). A missing binary inside the jail
		// reads "exit status 255" — identical to a user program exiting 255.
	}
	return outcome
}
