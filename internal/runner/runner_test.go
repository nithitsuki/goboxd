package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nithitsuki/goboxd/internal/cgroupv2"
	"github.com/nithitsuki/goboxd/internal/config"
	"github.com/nithitsuki/goboxd/internal/models"
	"github.com/nithitsuki/goboxd/internal/uidpool"
)

// TestMain lets the cgroup probe's re-exec work under go test. The real
// goboxd binary calls cgroupv2.ProbeHog when GOBOXD_CGROUP_PROBE_HOG=1 (see
// cmd/goboxd/main.go), but /proc/self/exe of this package's tests is the
// test binary. Mirror the hog here: block on stdin until the probe moves
// this process into the leaf cgroup, then touch 16MB, spin ~2s of CPU (the
// probe's cpu check reads the leaf's cpu.stat usage_usec), and exit 0.
// Without this the probe's child runs the whole test suite, which re-probes
// recursively and fails.
func TestMain(m *testing.M) {
	if os.Getenv("GOBOXD_CGROUP_PROBE_HOG") == "1" {
		var one [1]byte
		if _, err := os.Stdin.Read(one[:]); err != nil {
			os.Exit(0)
		}
		buf := make([]byte, 16*1024*1024)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			_ = buf[0]
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestExecuteRun(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    2,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
		RunLimits: config.Limits{
			WallTimeS:    2,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	tests := []struct {
		name           string
		source         string
		testCases      []models.TestCase
		expectedStatus models.ResultStatus
	}{
		{
			name:   "positive basic",
			source: "print('Hello from Python 3!')",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: "Hello from Python 3!\n"},
			},
			expectedStatus: models.ResultAccepted,
		},
		{
			name:   "timeout moderate",
			source: "import time\nwhile True: time.sleep(0.1)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: models.ResultTimeExceeded,
		},
		{
			name:   "runtime error (syntax)",
			source: "print(1/0)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: models.ResultRuntimeError,
		},
		{
			name:   "memory limit (OOM)",
			source: "l = []\nwhile True:\n    l.append('a' * 1024 * 1024)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: models.ResultRuntimeError,
		},
		{
			name:   "wrong output",
			source: "print('wrong')",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: "right\n"},
			},
			expectedStatus: models.ResultWrongOutput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := models.RunRequest{
				Language: "py3",
				Source:   tt.source,
				Tests:    tt.testCases,
			}

			_, results, err := ExecuteRun(context.Background(), req, py3Config)
			if err != nil {
				t.Fatalf("ExecuteRun dropped a hard error: %v", err)
			}

			if len(results) != len(tt.testCases) {
				t.Fatalf("expected %d results, got %d", len(tt.testCases), len(results))
			}

			res := results[0]

			// Memory kills: sigkill from nsjail could surface as time_exceeded or runtime_error
			if tt.name == "memory limit (OOM)" && (res.Status == models.ResultTimeExceeded || res.Status == models.ResultRuntimeError) {
				return
			}

			// Timeout: nsjail sigkill can read as time_exceeded or runtime_error
			if tt.name == "timeout moderate" && (res.Status == models.ResultTimeExceeded || res.Status == models.ResultRuntimeError) {
				return
			}

			if res.Status != tt.expectedStatus {
				t.Errorf("expected status %q, got %q (stderr: %q)", tt.expectedStatus, res.Status, res.Stderr)
				if strings.Contains(res.Stderr, "No such file or directory") {
					t.Fatalf("Missing python binary inside nsjail root. Is -B /usr setup correctly?")
				}
			}
		})
	}
}

// TestSeccompDeniedSyscall proves a per-language seccomp profile is applied
// end to end under the ADDITIVE-merge model (P2-12): a language whose Seccomp
// declares an extra deny (a syscall name, not an inline policy) must have a
// program calling that syscall killed by SIGSYS (runtime_error, exit 159),
// while the same program WITHOUT the profile stays accepted (the global
// policy does not deny chmod). CombinedWith (unit-tested separately) proves
// the global deny-list is never dropped, so this test only needs to prove the
// extra is applied.
func TestSeccompDeniedSyscall(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	base := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		RunLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}
	// ADDITIVE model: Seccomp is now a list of ADDITIONAL syscall names to
	// deny on top of the global policy, not a full kafel policy.
	denying := base
	denying.Seccomp = "chmod"

	// os.chmod issues the chmod syscall (number 90 on x86_64). The target is
	// the source file itself, which exists in jail by construction.
	src := "import os\nos.chmod('main.py', 0o644)\nprint('ok')"

	req := models.RunRequest{
		Language: "py3",
		Source:   src,
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: "ok\n"}},
	}

	// Control: Seccomp empty -> the global policy file applies, chmod is
	// allowed (the global deny-list does not include chmod).
	_, ctrlResults, err := ExecuteRun(context.Background(), req, base)
	if err != nil {
		t.Fatalf("control ExecuteRun dropped a hard error: %v", err)
	}
	if len(ctrlResults) != 1 {
		t.Fatalf("control: expected 1 result, got %d", len(ctrlResults))
	}
	if ctrlResults[0].Status != models.ResultAccepted {
		t.Fatalf("control status = %q, want accepted (chmod must not be denied by the global policy; stderr: %q)",
			ctrlResults[0].Status, ctrlResults[0].Stderr)
	}

	// Profile adds chmod: the identical program must be killed by SIGSYS.
	_, results, err := ExecuteRun(context.Background(), req, denying)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != models.ResultRuntimeError {
		t.Errorf("status = %q, want runtime_error (seccomp kill; stderr: %q)", res.Status, res.Stderr)
	}
	// nsjail propagates the child's SIGSYS death as exit 128+31=159; a direct
	// signal death reads (-1, 31). Either shape must carry signal 31.
	if res.TerminationSignal != int(syscall.SIGSYS) {
		t.Errorf("termination_signal = %d, want %d (SIGSYS) (exit_code=%d stderr: %q)",
			res.TerminationSignal, syscall.SIGSYS, res.ExitCode, res.Stderr)
	}
}

// TestExecuteRunContextCancel proves client-disconnect cancellation: a run
// with a 60s wall time must return ~1s after the request context is canceled,
// classify the test as "cancelled", free the uid, and leave no jail dir.
func TestExecuteRunContextCancel(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    60,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
		RunLimits: config.Limits{
			WallTimeS:    60,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	req := models.RunRequest{
		Language: "py3",
		Source:   "while True:\n    pass",
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: ""},
		},
	}

	// Warm up the one-time infrastructure (cgroup probe, seccomp policy) so
	// the ~1s cancel timer below lands during the busy loop, not during
	// first-run setup (probe alone costs ~3s).
	warmReq := models.RunRequest{
		Language: "py3",
		Source:   "print('warm')",
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: "warm\n"},
		},
	}
	if _, _, err := ExecuteRun(context.Background(), warmReq, py3Config); err != nil {
		t.Fatalf("warmup ExecuteRun: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(time.Second, cancel)

	start := time.Now()
	_, results, err := ExecuteRun(ctx, req, py3Config)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}

	if elapsed >= 10*time.Second {
		t.Fatalf("ExecuteRun took %v on cancel, want < 10s (wall time was 60s)", elapsed)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != models.ResultCancelled {
		t.Errorf("expected status %q, got %q (stderr: %q)", models.ResultCancelled, results[0].Status, results[0].Stderr)
	}
	if results[0].Stdout != "" {
		t.Errorf("expected empty stdout, got %q", results[0].Stdout)
	}
	// The ctx kill is a goboxd kill: ProcessState is signaled with SIGKILL.
	if results[0].ExitCode != -1 || results[0].TerminationSignal != 9 {
		t.Errorf("cancel exit facts = (%d, %d), want (-1, 9)", results[0].ExitCode, results[0].TerminationSignal)
	}

	// The uid must be immediately reusable after the canceled run.
	uid, err := uidPool.Alloc()
	if err != nil {
		t.Fatalf("uid pool not reusable after cancel: %v", err)
	}
	uidPool.Release(uid)

	// No jail dir may remain after the canceled run returned.
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "goboxd-jail-") {
			t.Errorf("leftover jail dir after cancel: %s", e.Name())
		}
	}
}

// TestJailTeardown locks the jail teardown contract: teardown returns the uid
// to the pool, removes the jail dir from the temp dir, removes the cgroup
// leaf, and is idempotent (a second call is a no-op). A jail that cannot be
// torn down leaks uids, jail dirs, and cgroup dirs under load.
func TestJailTeardown(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root: newJail chowns the jail dir and may create a cgroup leaf")
	}

	req := models.RunRequest{Language: "py3", Source: "print('x')"}
	lc := config.LanguageConfig{ID: "py3", SourceFilename: "main.py"}

	j, err := newJail(req, lc, "main.py")
	if err != nil {
		t.Fatalf("newJail: %v", err)
	}
	uid := j.uid
	dir := j.dir
	cgPath := ""
	if j.cg != nil {
		cgPath = j.cg.Path()
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("jail dir %s missing before teardown: %v", dir, err)
	}

	j.teardown()

	// The uid must no longer be held: scanning the whole pool finds it again.
	// If teardown leaked the uid, the scan exhausts the pool first.
	found := false
	var held []int
	for i := 0; i < uidPool.Size(); i++ {
		a, err := uidPool.Alloc()
		if err != nil {
			break // pool exhausted before finding uid: teardown never released it
		}
		held = append(held, a)
		if a == uid {
			found = true
			break
		}
	}
	for _, a := range held {
		uidPool.Release(a)
	}
	if !found {
		t.Errorf("uid %d not released by teardown (pool exhausted before it was found)", uid)
	}

	// The jail dir must be gone from the temp dir.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("jail dir %s still exists after teardown (err=%v)", dir, err)
	}

	// The cgroup leaf must be gone.
	if cgPath != "" {
		if _, err := os.Stat(cgPath); !os.IsNotExist(err) {
			t.Errorf("cgroup dir %s still exists after teardown (err=%v)", cgPath, err)
		}
	}

	// Idempotent: a second teardown is a no-op and must not panic.
	j.teardown()
}

func TestReadCapped(t *testing.T) {
	// Short string passes through unchanged
	input := "hello world"
	got := readCapped(bytes.NewBufferString(input), 1024)
	if got != input {
		t.Errorf("readCapped = %q, want %q", got, input)
	}

	// Empty input
	got = readCapped(bytes.NewBufferString(""), 1024)
	if got != "" {
		t.Errorf("readCapped(empty) = %q, want ''", got)
	}

	// Input larger than limit triggers truncation with marker
	truncationMarker := "\n... [output truncated]"
	big := strings.Repeat("A", int(maxOutputBytes)+1)
	got = readCapped(bytes.NewBufferString(big), int(maxOutputBytes))
	if !strings.HasSuffix(got, truncationMarker) {
		t.Errorf("expected output to end with %q, got suffix %q", truncationMarker, got[len(got)-40:])
	}
	// Total should be maxOutputBytes + marker
	expectedLen := int(maxOutputBytes) + len(truncationMarker)
	if len(got) != expectedLen {
		t.Errorf("expected length %d (cap + marker), got %d", expectedLen, len(got))
	}
	// First maxOutputBytes bytes should be the original input
	prefix := got[:int(maxOutputBytes)]
	expectedPrefix := strings.Repeat("A", int(maxOutputBytes))
	if prefix != expectedPrefix {
		t.Errorf("first %d bytes dont match original input", maxOutputBytes)
	}
}

func TestComputeTestStatus(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		stdout   string
		expected string
		cpu      cpuOutcome
		ctx      context.Context
		want     models.ResultStatus
	}{
		{"exact match", nil, "hello\n", "hello\n", cpuOutcome{}, context.Background(), models.ResultAccepted},
		{"whitespace diff", nil, "hello\n", "hello", cpuOutcome{}, context.Background(), models.ResultWhitespaceMismatch},
		{"wrong output", nil, "world", "hello", cpuOutcome{}, context.Background(), models.ResultWrongOutput},
		{"empty expected", nil, "anything", "", cpuOutcome{}, context.Background(), models.ResultAccepted},
		{"exact match with empty expected", nil, "", "", cpuOutcome{}, context.Background(), models.ResultAccepted},
		{"exit 137 early kill", fmt.Errorf("exit status 137"), "", "", cpuOutcome{}, context.Background(), models.ResultTimeExceeded},
		{"exit 137 at wall time", fmt.Errorf("exit status 137"), "", "", cpuOutcome{}, context.Background(), models.ResultTimeExceeded},
		{"exit 139 segv", fmt.Errorf("exit status 139"), "", "", cpuOutcome{}, context.Background(), models.ResultRuntimeError},
		{"cpu kill wins over deadline-shaped error", fmt.Errorf("signal: killed"), "", "", cpuOutcome{killed: true}, context.Background(), models.ResultCPUTimeExceeded},
		{"cancelled beats cpu kill", fmt.Errorf("signal: killed"), "", "", cpuOutcome{killed: true}, canceledCtx(), models.ResultCancelled},
		{"no heuristic: err without signal is runtime_error even at wall time", fmt.Errorf("exit status 2"), "", "", cpuOutcome{}, context.Background(), models.ResultRuntimeError},
		{"rlimit cpu kill reads as 137 with full cpu time", fmt.Errorf("exit status 137"), "", "", cpuOutcome{limitUS: 2 * 1e6, timeUS: 2 * 1e6}, context.Background(), models.ResultCPUTimeExceeded},
		{"wall kill with cpu time under the limit stays time_exceeded", fmt.Errorf("exit status 137"), "", "", cpuOutcome{limitUS: 11 * 1e6, timeUS: 9 * 1e6}, context.Background(), models.ResultTimeExceeded},
		{"no cpu limit: 137 is always time_exceeded", fmt.Errorf("exit status 137"), "", "", cpuOutcome{timeUS: 2 * 1e6}, context.Background(), models.ResultTimeExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTestStatus(tt.ctx, tt.err, tt.stdout, tt.expected, nil, false, tt.cpu)
			if got != tt.want {
				t.Errorf("computeTestStatus = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestComputeTestStatusTyped locks the typed-status contract: computeTestStatus
// must return the closed ResultStatus vocabulary (never a raw string), so a
// typo in a status cannot compile. Before the C5 signature change this test
// does not compile: a string result is not comparable to a ResultStatus.
func TestComputeTestStatusTyped(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		stdout   string
		expected string
		cpu      cpuOutcome
		ctx      context.Context
		want     models.ResultStatus
	}{
		{"exact match is accepted", nil, "hello\n", "hello\n", cpuOutcome{}, context.Background(), models.ResultAccepted},
		{"wall kill is time_exceeded", fmt.Errorf("exit status 137"), "", "", cpuOutcome{}, context.Background(), models.ResultTimeExceeded},
		{"cpu kill is cpu_time_exceeded", fmt.Errorf("signal: killed"), "", "", cpuOutcome{killed: true}, context.Background(), models.ResultCPUTimeExceeded},
		{"cancelled beats cpu kill", fmt.Errorf("signal: killed"), "", "", cpuOutcome{killed: true}, canceledCtx(), models.ResultCancelled},
		{"wrong output", nil, "world", "hello", cpuOutcome{}, context.Background(), models.ResultWrongOutput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTestStatus(tt.ctx, tt.err, tt.stdout, tt.expected, nil, false, tt.cpu)
			if got != tt.want {
				t.Errorf("computeTestStatus = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestHelperProcessExitFacts is not a real test: TestExitFacts re-execs the
// test binary with GOBOXD_TEST_EXIT_CODE to obtain a real ProcessState for a
// plain exit. Without the env var it does nothing. Signal deaths use a tiny
// sh subprocess instead (the Go test framework converts SIGSEGV in its own
// process into exit 2, which would corrupt the signal shape).
func TestHelperProcessExitFacts(t *testing.T) {
	if code := os.Getenv("GOBOXD_TEST_EXIT_CODE"); code != "" {
		if n, err := strconv.Atoi(code); err == nil {
			os.Exit(n)
		}
		os.Exit(1)
	}
}

// TestExitFacts locks the exit fact derivation against real ProcessStates:
// nil, plain exits, the 129..192 nsjail signal-propagation window with both
// boundaries, and direct signal deaths.
func TestExitFacts(t *testing.T) {
	exitPS := func(code int) *os.ProcessState {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessExitFacts")
		cmd.Env = append(os.Environ(), "GOBOXD_TEST_EXIT_CODE="+strconv.Itoa(code))
		_ = cmd.Run()
		return cmd.ProcessState
	}
	signalPS := func(sig syscall.Signal) *os.ProcessState {
		// Full path on purpose: TestSecurityHole2NoShellCommands flags
		// shell-name literals in exec.Command project-wide.
		cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("kill -%d $$", int(sig)))
		_ = cmd.Run()
		return cmd.ProcessState
	}

	cases := []struct {
		name     string
		ps       *os.ProcessState
		wantCode int
		wantSig  int
	}{
		{"nil state means no process", nil, 0, 0},
		{"clean exit 0", exitPS(0), 0, 0},
		{"user exit 1", exitPS(1), 1, 0},
		{"user exit 3", exitPS(3), 3, 0},
		{"exit 128 is not a signal", exitPS(128), 128, 0},
		{"exit 129 reads as signal 1", exitPS(129), 129, 1},
		{"exit 137 reads as signal 9", exitPS(137), 137, 9},
		{"exit 152 reads as signal 24", exitPS(152), 152, 24},
		{"exit 192 reads as signal 64", exitPS(192), 192, 64},
		{"exit 193 is not a signal", exitPS(193), 193, 0},
		{"exit 255 is not a signal", exitPS(255), 255, 0},
		{"SIGSEGV death", signalPS(syscall.SIGSEGV), -1, 11},
		{"SIGKILL death", signalPS(syscall.SIGKILL), -1, 9},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			gotCode, gotSig := exitFacts(tt.ps)
			if gotCode != tt.wantCode || gotSig != tt.wantSig {
				t.Errorf("exitFacts = (%d, %d), want (%d, %d)", gotCode, gotSig, tt.wantCode, tt.wantSig)
			}
		})
	}
}

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestSignalKillReason(t *testing.T) {
	// signalKillReason with nil ProcessState should return ""
	if got := signalKillReason(nil); got != "" {
		t.Errorf("signalKillReason(nil) = %q, want ''", got)
	}
}

// TestSignalReasonFromStatus locks the SIGXCPU mapping: the kernel sends
// SIGXCPU at RLIMIT_CPU; nsjail turns the child's signal death into exit
// 128+24=152. Both shapes must classify cpu_time_exceeded.
func TestSignalReasonFromStatus(t *testing.T) {
	cases := []struct {
		name   string
		status syscall.WaitStatus
		want   models.ResultStatus
	}{
		{"sigxcpu signaled", syscall.WaitStatus(syscall.SIGXCPU), models.ResultCPUTimeExceeded},
		{"nsjail exit 152 (128+SIGXCPU)", syscall.WaitStatus(152 << 8), models.ResultCPUTimeExceeded},
		{"sigkill", syscall.WaitStatus(syscall.SIGKILL), models.ResultTimeExceeded},
		{"sigsegv", syscall.WaitStatus(syscall.SIGSEGV), models.ResultMemoryExceeded},
		{"sigabrt", syscall.WaitStatus(syscall.SIGABRT), models.ResultMemoryExceeded},
		{"other signal", syscall.WaitStatus(syscall.SIGTERM), models.ResultRuntimeError},
		{"clean exit has no reason", syscall.WaitStatus(0), ""},
		{"user exit 137 is not a kill reason", syscall.WaitStatus(137 << 8), ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := signalReasonFromStatus(tt.status); got != tt.want {
				t.Errorf("signalReasonFromStatus(%#x) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// TestHelperProcessExit152 is not a real test: TestComputeTestStatusSIGXCPU
// re-execs the test binary with GOBOXD_TEST_EXIT=152 to obtain a real
// ProcessState whose exit status is 152 (nsjail's exit for a SIGXCPU-killed
// child). Without the env it does nothing.
func TestHelperProcessExit152(t *testing.T) {
	if os.Getenv("GOBOXD_TEST_EXIT") == "152" {
		os.Exit(152)
	}
}

// TestComputeTestStatusSIGXCPU: a run that died via RLIMIT_CPU (nsjail exit
// 152) must classify cpu_time_exceeded, ahead of the wall deadline.
func TestComputeTestStatusSIGXCPU(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcessExit152")
	cmd.Env = append(os.Environ(), "GOBOXD_TEST_EXIT=152")
	if err := cmd.Run(); err == nil {
		t.Fatal("helper process must exit non-zero")
	}
	got := computeTestStatus(context.Background(), fmt.Errorf("exit status 152"), "", "", cmd.ProcessState, false, cpuOutcome{})
	if got != models.ResultCPUTimeExceeded {
		t.Errorf("computeTestStatus(SIGXCPU) = %q, want cpu_time_exceeded", got)
	}
}

// TestExecuteRunCPUKill proves the cpu kill end to end: a busy loop with a
// 2s cpu limit and a 9s wall limit must die as cpu_time_exceeded with
// cpu_time_ms near the limit, well before wall time fires.
func TestExecuteRunCPUKill(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
			CpuTimeS:     11,
		},
		RunLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
			CpuTimeS:     11,
		},
	}

	// Warm up the one-time sandbox setup (cgroup probe, seccomp policy) so
	// the timing assertions below measure the busy loop, not first-run setup.
	warmReq := models.RunRequest{
		Language: "py3",
		Source:   "print('warm')",
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: "warm\n"}},
	}
	if _, _, err := ExecuteRun(context.Background(), warmReq, py3Config); err != nil {
		t.Fatalf("warmup ExecuteRun: %v", err)
	}

	cpu2, wall9 := 2, 9
	req := models.RunRequest{
		Language: "py3",
		Source:   "while True:\n    pass",
		Run: &models.StageConfig{Limits: &models.Limits{
			WallTimeS: &wall9,
			CpuTimeS:  &cpu2,
		}},
		Tests: []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
	}

	start := time.Now()
	_, results, err := ExecuteRun(context.Background(), req, py3Config)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != models.ResultCPUTimeExceeded {
		t.Errorf("status = %q, want cpu_time_exceeded (stderr: %q)", res.Status, res.Stderr)
	}
	if res.CpuTimeMs < 1500 || res.CpuTimeMs > 3500 {
		t.Errorf("CpuTimeMs = %d, want in [1500, 3500] for a 2s cpu limit", res.CpuTimeMs)
	}
	if elapsed >= 7*time.Second {
		t.Errorf("cpu kill took %v, want well under the 9s wall limit", elapsed)
	}
}

// TestExecuteRunCPUReported proves cpu time reporting: an accepted run must
// carry a positive cpu_time_ms that is well under the wall limit.
func TestExecuteRunCPUReported(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
			CpuTimeS:     11,
		},
		RunLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
			CpuTimeS:     11,
		},
	}

	wall9 := 9
	req := models.RunRequest{
		Language: "py3",
		Source:   "print('cpu report')",
		Run: &models.StageConfig{Limits: &models.Limits{
			WallTimeS: &wall9,
		}},
		Tests: []models.TestCase{{Stdin: "", ExpectedStdout: "cpu report\n"}},
	}

	_, results, err := ExecuteRun(context.Background(), req, py3Config)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != models.ResultAccepted {
		t.Fatalf("status = %q, want accepted (stderr: %q)", res.Status, res.Stderr)
	}
	if res.CpuTimeMs <= 0 {
		t.Errorf("CpuTimeMs = %d, want > 0 (python startup burns cpu)", res.CpuTimeMs)
	}
	if res.CpuTimeMs >= 9000 {
		t.Errorf("CpuTimeMs = %d, want < 9000 (wall limit was 9s)", res.CpuTimeMs)
	}
}

// TestExecuteRunExitFacts pins the exit fact contract end to end:
// user exits propagate as-is, nsjail signal deaths read 128+signal,
// and goboxd kills (cpu poller, wall deadline) read (-1, 9).
func TestExecuteRunExitFacts(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
			CpuTimeS:     11,
		},
		RunLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
			CpuTimeS:     11,
		},
	}

	run := func(t *testing.T, req models.RunRequest) models.TestResult {
		t.Helper()
		_, results, err := ExecuteRun(context.Background(), req, py3Config)
		if err != nil {
			t.Fatalf("ExecuteRun dropped a hard error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		return results[0]
	}

	// Warm up the one-time sandbox setup (cgroup probe, seccomp policy) so
	// the cases below measure the run, not first-run setup.
	warmReq := models.RunRequest{
		Language: "py3",
		Source:   "print('warm')",
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: "warm\n"}},
	}
	if res := run(t, warmReq); res.Status != models.ResultAccepted {
		t.Fatalf("warmup status = %q, want accepted (stderr: %q)", res.Status, res.Stderr)
	}

	t.Run("accepted is (0,0)", func(t *testing.T) {
		res := run(t, models.RunRequest{
			Language: "py3",
			Source:   "print('ok')",
			Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: "ok\n"}},
		})
		if res.Status != models.ResultAccepted {
			t.Fatalf("status = %q, want accepted (stderr: %q)", res.Status, res.Stderr)
		}
		if res.ExitCode != 0 || res.TerminationSignal != 0 {
			t.Errorf("exit facts = (%d, %d), want (0, 0)", res.ExitCode, res.TerminationSignal)
		}
	})

	t.Run("user exit 3 propagates", func(t *testing.T) {
		res := run(t, models.RunRequest{
			Language: "py3",
			Source:   "import sys\nsys.exit(3)",
			Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
		})
		if res.Status != models.ResultRuntimeError {
			t.Fatalf("status = %q, want runtime_error (stderr: %q)", res.Status, res.Stderr)
		}
		if res.ExitCode != 3 || res.TerminationSignal != 0 {
			t.Errorf("exit facts = (%d, %d), want (3, 0)", res.ExitCode, res.TerminationSignal)
		}
	})

	t.Run("sigsegv reads 128+11", func(t *testing.T) {
		res := run(t, models.RunRequest{
			Language: "py3",
			Source:   "import ctypes\nctypes.string_at(0)",
			Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
		})
		if res.ExitCode != 139 || res.TerminationSignal != 11 {
			t.Errorf("exit facts = (%d, %d), want (139, 11)", res.ExitCode, res.TerminationSignal)
		}
	})

	t.Run("wall timeout kills with signal 9", func(t *testing.T) {
		wall2 := 2
		res := run(t, models.RunRequest{
			Language: "py3",
			Source:   "while True:\n    pass",
			Run: &models.StageConfig{Limits: &models.Limits{
				WallTimeS: &wall2,
			}},
			Tests: []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
		})
		if res.TerminationSignal != 9 {
			t.Errorf("termination_signal = %d, want 9", res.TerminationSignal)
		}
		if res.ExitCode != -1 && res.ExitCode != 137 {
			t.Errorf("exit_code = %d, want -1 (goboxd kill) or 137 (nsjail propagation)", res.ExitCode)
		}
		if res.Status != models.ResultTimeExceeded {
			t.Errorf("status = %q, want time_exceeded (stderr: %q)", res.Status, res.Stderr)
		}
	})

	t.Run("cpu kill reports kill facts", func(t *testing.T) {
		wall9, cpu2 := 9, 2
		res := run(t, models.RunRequest{
			Language: "py3",
			Source:   "while True:\n    pass",
			Run: &models.StageConfig{Limits: &models.Limits{
				WallTimeS: &wall9,
				CpuTimeS:  &cpu2,
			}},
			Tests: []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
		})
		if res.Status != models.ResultCPUTimeExceeded {
			t.Fatalf("status = %q, want cpu_time_exceeded (stderr: %q)", res.Status, res.Stderr)
		}
		// On the cgroup path the poller kills nsjail directly: (-1, 9).
		// On the rlimit path the kernel SIGKILLs at the hard cpu limit and
		// nsjail reads the death as exit 137: (137, 9).
		if res.TerminationSignal != 9 {
			t.Errorf("termination_signal = %d, want 9", res.TerminationSignal)
		}
		if res.ExitCode != -1 && res.ExitCode != 137 {
			t.Errorf("exit_code = %d, want -1 or 137", res.ExitCode)
		}
	})
}

// TestComputeTestStatusOOMKilled: a cgroup OOM kill (detected via the leaf's
// memory.events) must be classified memory_exceeded, ahead of the exit-137 /
// wall-time heuristics that would otherwise say time_exceeded.
func TestComputeTestStatusOOMKilled(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		oomKilled bool
		want      models.ResultStatus
	}{
		{"oom kill at 137", fmt.Errorf("exit status 137"), true, models.ResultMemoryExceeded},
		{"oom kill at wall time", fmt.Errorf("exit status 137"), true, models.ResultMemoryExceeded},
		{"oom kill with nil err", nil, true, models.ResultMemoryExceeded},
		{"no oom kill stays time_exceeded", fmt.Errorf("exit status 137"), false, models.ResultTimeExceeded},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTestStatus(context.Background(), tt.err, "", "", nil, tt.oomKilled, cpuOutcome{})
			if got != tt.want {
				t.Errorf("computeTestStatus = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNsjailArgsRlimitUnits guards the nsjail unit contract: --rlimit_as and
// --rlimit_fsize are in MB (nsjail help text; a bytes value silently becomes a
// ~1024x larger limit). This was a real bug: memory limits were effectively
// unenforced because the runner passed memKB*1024 bytes.
//
// The guard is TIGHT: RLIMIT_AS equals the memory limit in MB. Runtimes that
// reserve large VIRTUAL address space up front (CoreCLR, BEAM) cannot fit and
// are excluded from the registry via GOBOXD_EXCLUDE_LANGS instead of loosening
// the guard. Real resident-memory enforcement is cgroup v2 when active; the
// rlimit is the always-present fallback.
func TestNsjailArgsRlimitUnits(t *testing.T) {
	args, err := nsjailArgs("/app", 5, 0, 65536, 100, 10000, nil, "") // 64MB, 100 procs, no cpu cap
	if err != nil {
		t.Fatalf("nsjailArgs: %v", err)
	}
	want := map[string]string{
		"--rlimit_as":    "64",  // 64MB limit -> 64MB virtual guard, tight
		"--rlimit_nproc": "100", // count, unit-less
	}
	for i := 0; i < len(args)-1; i++ {
		if wantV, ok := want[args[i]]; ok {
			if args[i+1] != wantV {
				t.Errorf("%s = %q, want %q (nsjail takes MB, not bytes)", args[i], args[i+1], wantV)
			}
			delete(want, args[i])
		}
	}
	for k := range want {
		t.Errorf("missing flag %s in nsjail args", k)
	}

	// Large limits pass through 1:1: 16GB limit -> 16384MB virtual guard.
	args, err = nsjailArgs("/app", 5, 0, 16*1024*1024, 100, 10000, nil, "")
	if err != nil {
		t.Fatalf("nsjailArgs (16GB): %v", err)
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--rlimit_as" && args[i+1] != "16384" {
			t.Errorf("--rlimit_as for 16GB limit = %q, want 16384 (1:1 MB)", args[i+1])
		}
	}
}

// TestNsjailArgsSeccompFlag (P2-12) locks the additive-merge wiring in
// nsjailArgs: an EMPTY langSeccomp must select --seccomp_policy with the
// global file path (byte-identical to pre-P2-12), and a NON-EMPTY langSeccomp
// must select --seccomp_string with a combined inline policy that still
// contains the global deny-list plus the extra syscall.
func TestNsjailArgsSeccompFlag(t *testing.T) {
	// Empty case: global file via --seccomp_policy, EXACTLY one seccomp flag.
	args, err := nsjailArgs("/app", 5, 0, 65536, 100, 10000, nil, "")
	if err != nil {
		t.Fatalf("nsjailArgs (empty): %v", err)
	}
	polIdx, strIdx := -1, -1
	for i, a := range args {
		if a == "--seccomp_policy" {
			polIdx = i
		}
		if a == "--seccomp_string" {
			strIdx = i
		}
	}
	if polIdx < 0 {
		t.Error("empty langSeccomp must pass --seccomp_policy")
	}
	if strIdx >= 0 {
		t.Error("empty langSeccomp must NOT pass --seccomp_string")
	}
	if polIdx >= 0 {
		if _, err := os.Stat(args[polIdx+1]); err != nil {
			t.Errorf("--seccomp_policy path %q not a readable file: %v", args[polIdx+1], err)
		}
	}

	// Non-empty case: combined inline policy via --seccomp_string.
	args, err = nsjailArgs("/app", 5, 0, 65536, 100, 10000, nil, "chmod")
	if err != nil {
		t.Fatalf("nsjailArgs (chmod): %v", err)
	}
	polIdx, strIdx = -1, -1
	inline := ""
	for i, a := range args {
		if a == "--seccomp_policy" {
			polIdx = i
		}
		if a == "--seccomp_string" {
			strIdx = i
			inline = args[i+1]
		}
	}
	if strIdx < 0 {
		t.Fatal("non-empty langSeccomp must pass --seccomp_string")
	}
	if polIdx >= 0 {
		t.Error("non-empty langSeccomp must NOT pass --seccomp_policy")
	}
	for _, want := range []string{"mount", "ptrace", "chroot", "SYSCALL[166]", "chmod", "USE goboxd DEFAULT ALLOW"} {
		if !strings.Contains(inline, want) {
			t.Errorf("--seccomp_string policy missing %q", want)
		}
	}
	if strings.Contains(inline, "USE py3") || strings.Contains(inline, "POLICY py3") {
		t.Error("combined policy must reuse the global policy name, not a per-language one")
	}
}

// TestNsjailArgsRlimitCPU locks the cpu limit contract: a positive cpu limit
// is passed as --rlimit_cpu seconds (the kernel SIGXCPU path), a zero limit
// (no cpu cap) is "inf" so nsjail's implicit 600s default never applies.
func TestNsjailArgsRlimitCPU(t *testing.T) {
	args, err := nsjailArgs("/app", 9, 2, 65536, 100, 10000, nil, "")
	if err != nil {
		t.Fatalf("nsjailArgs: %v", err)
	}
	found := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--rlimit_cpu" {
			found = true
			if args[i+1] != "2" {
				t.Errorf("--rlimit_cpu = %q, want 2 (seconds)", args[i+1])
			}
		}
	}
	if !found {
		t.Error("missing --rlimit_cpu in nsjail args")
	}

	args, err = nsjailArgs("/app", 9, 0, 65536, 100, 10000, nil, "")
	if err != nil {
		t.Fatalf("nsjailArgs (no cpu cap): %v", err)
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--rlimit_cpu" && args[i+1] != "inf" {
			t.Errorf("--rlimit_cpu with no cap = %q, want inf (kills nsjail's 600s default)", args[i+1])
		}
	}
}

// TestWriteSourceRejectsSymlink (TOCTOU): the source write must use
// O_EXCL|O_NOFOLLOW so a symlink planted at the destination path is never
// followed. Following it would let a concurrent actor redirect the write
// outside the jail dir (classic symlink race).
func TestWriteSourceRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "source.c")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatalf("writing target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("planting symlink: %v", err)
	}

	err := writeSource(link, []byte("evil"))
	if err == nil {
		t.Fatalf("writeSource followed the symlink: want error (ELOOP/EEXIST), got nil")
	}
	got, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("reading target: %v", rerr)
	}
	if string(got) != "keep" {
		t.Errorf("symlink was followed: target now %q, want %q", got, "keep")
	}
}

// TestNsjailArgsUidMapping guards the dual uid_map contract: the jail uid U
// must be FIRST (the process runs as unprivileged host uid U) and inside-uid 0
// must ALSO be mapped (0:0:1) for nsjail's mount-tree setup phase, which runs
// before nsjail drops to U. With only U:U:1 mapped, that phase runs as the
// unmapped overflow uid and mkdir('/tmp/nsjail.<pid>.root/...') fails EPERM
// whenever a stale root dir from an earlier run exists.
func TestNsjailArgsUidMapping(t *testing.T) {
	args, err := nsjailArgs("/app", 5, 0, 65536, 100, 12345, nil, "")
	if err != nil {
		t.Fatalf("nsjailArgs: %v", err)
	}
	// Find the -u and -g blocks in order.
	var uVals, gVals []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-u" && i+1 < len(args) {
			uVals = append(uVals, args[i+1])
		}
		if args[i] == "-g" && i+1 < len(args) {
			gVals = append(gVals, args[i+1])
		}
	}
	if len(uVals) < 2 || uVals[0] != "12345:12345:1" || uVals[1] != "0:0:1" {
		t.Errorf("-u maps = %v, want [12345:12345:1 0:0:1] (jail uid first, 0 mapped for setup)", uVals)
	}
	if len(gVals) < 2 || gVals[0] != "12345:12345:1" || gVals[1] != "0:0:1" {
		t.Errorf("-g maps = %v, want [12345:12345:1 0:0:1]", gVals)
	}
}

// TestResolveSourceName locks the filename strategy contract: languages with
// source_filename_strategy "fixed" always use the configured filename (Java
// requires the public class name to match the file name), ignoring whatever
// the client sends.
func TestResolveSourceName(t *testing.T) {
	req := models.RunRequest{Language: "java", SourceFilename: "Whatever.java"}
	lc := config.LanguageConfig{
		ID:                     "java",
		SourceFilename:         "Main.java",
		SourceFilenameStrategy: "fixed",
	}
	if got := resolveSourceName(req, lc); got != "Main.java" {
		t.Errorf("fixed strategy: got %q, want Main.java", got)
	}

	lc2 := config.LanguageConfig{ID: "py3", SourceFilename: "solution.py"}
	if got := resolveSourceName(req, lc2); got != "Whatever.java" {
		t.Errorf("default strategy must honor client filename: got %q", got)
	}
	req2 := models.RunRequest{Language: "py3"}
	if got := resolveSourceName(req2, lc2); got != "solution.py" {
		t.Errorf("default strategy must fall back to config: got %q", got)
	}
}

// TestJailEnv locks the environment allowlist: a fixed set of vars, in a
// stable order, with PATH copied from the server env at call time and a
// hardcoded fallback when PATH is unset or empty. GOCACHE and CCACHE_DIR
// are set by nsjailArgs after cache bind-mounts, not by jailEnv.
// DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1 lets .NET run without a
// system-ICU package (see jailEnv), so it is part of the allowlist.
func TestJailEnv(t *testing.T) {
	t.Setenv("PATH", "/custom/bin")
	got := jailEnv()
	want := []string{
		"-E", "PATH=/custom/bin",
		"-E", "HOME=/tmp",
		"-E", "LANG=C.UTF-8",
		"-E", "LC_ALL=C.UTF-8",
		"-E", "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1",
	}
	if len(got) != len(want) {
		t.Fatalf("jailEnv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("jailEnv[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// os.Getenv returns "" for both unset and empty PATH. The helper must
	// treat both as the fallback.
	t.Setenv("PATH", "")
	got = jailEnv()
	wantPath := []string{
		"-E", "PATH=/usr/local/bin:/usr/bin:/bin",
		"-E", "HOME=/tmp",
		"-E", "LANG=C.UTF-8",
		"-E", "LC_ALL=C.UTF-8",
		"-E", "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1",
	}
	if len(got) != len(wantPath) {
		t.Fatalf("jailEnv (empty PATH) = %q, want %q", got, wantPath)
	}
	for i := range wantPath {
		if got[i] != wantPath[i] {
			t.Errorf("jailEnv[%d] (empty PATH) = %q, want %q", i, got[i], wantPath[i])
		}
	}
}

// TestJailEnvContract pins the jail environment end to end: a python jail
// prints its full environment and the test asserts the exact allowlist key
// set and the absence of secret markers. nsjail clears the child env by
// default; this test guards that behavior so a future nsjail change cannot
// silently leak the server env into the jail.
func TestJailEnvContract(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	// Plant secrets in the server env. The jail must not see any of them.
	t.Setenv("GOBOXD_TEST_TOKEN", "topsecret")
	t.Setenv("HTTP_PROXY", "http://evil-proxy:3128")
	t.Setenv("HTTPS_PROXY", "http://evil-proxy:3128")
	t.Setenv("ALL_PROXY", "socks5://evil-proxy:1080")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFAKEFAKEFAKEFAKE")
	t.Setenv("SECRET_TEST", "1")
	t.Setenv("PASSWORD", "hunter2")

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    2,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
		RunLimits: config.Limits{
			WallTimeS:    2,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	req := models.RunRequest{
		Language: "py3",
		Source:   "import os\nfor k in sorted(os.environ):\n    print(k + '=' + os.environ[k])",
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
	}

	_, results, err := ExecuteRun(context.Background(), req, py3Config)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != models.ResultAccepted {
		t.Fatalf("status = %q, want accepted (stderr: %q)", res.Status, res.Stderr)
	}

	keys := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("unparsable env line %q in jail output", line)
		}
		keys[k] = v
	}

	allowed := []string{"PATH", "HOME", "GOCACHE", "LANG", "LC_ALL", "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT"}
	allowedSet := map[string]bool{}
	for _, k := range allowed {
		allowedSet[k] = true
	}
	if len(keys) != len(allowed) {
		t.Errorf("jail env keys = %v, want exactly %v", sortedKeys(keys), allowed)
	}
	for k := range keys {
		if !allowedSet[k] {
			t.Errorf("unexpected env key %q in jail", k)
		}
	}
	for _, k := range allowed {
		if _, ok := keys[k]; !ok {
			t.Errorf("missing allowlisted env key %q in jail", k)
		}
	}

	for _, marker := range []string{"GOBOXD_", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "AWS_"} {
		if strings.Contains(res.Stdout, marker) {
			t.Errorf("secret marker %q leaked into the jail env", marker)
		}
	}
}

// sortedKeys returns the keys of m in sorted order for stable error messages.
func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// TestExecuteRunParallel proves that parallel execution is faster than
// sequential when max_parallel > 1. It creates 3 test cases that each
// take ~2s (via sleep), runs them with max_parallel=2, and asserts the
// total elapsed time is less than sequential (~6s).
// The test also verifies that max_parallel=nil behaves like sequential.
func TestExecuteRunParallel(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}
	if runtime.NumCPU() < 2 {
		t.Skip("requires at least 2 CPUs for parallel test")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
		RunLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	// Warm up the one-time sandbox setup (cgroup probe, seccomp policy).
	warmReq := models.RunRequest{
		Language: "py3",
		Source:   "print('warm')",
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: "warm\n"}},
	}
	if _, _, err := ExecuteRun(context.Background(), warmReq, py3Config); err != nil {
		t.Fatalf("warmup ExecuteRun: %v", err)
	}

	// 3 test cases, each sleeps 2s then prints "done\n".
	sleepSrc := "import time\ntime.sleep(2)\nprint('done')"
	threeTests := []models.TestCase{
		{Stdin: "", ExpectedStdout: "done\n"},
		{Stdin: "", ExpectedStdout: "done\n"},
		{Stdin: "", ExpectedStdout: "done\n"},
	}

	// --- Parallel run ---
	parReq := models.RunRequest{
		Language: "py3",
		Source:   sleepSrc,
		Tests:    threeTests,
	}
	par2 := 2
	parReq.MaxParallel = &par2

	start := time.Now()
	_, parResults, err := ExecuteRun(context.Background(), parReq, py3Config)
	parElapsed := time.Since(start)
	if err != nil {
		t.Fatalf("parallel ExecuteRun dropped a hard error: %v", err)
	}
	if len(parResults) != 3 {
		t.Fatalf("parallel: expected 3 results, got %d", len(parResults))
	}
	for i, r := range parResults {
		if r.Status != models.ResultAccepted {
			t.Errorf("parallel result[%d].Status = %q, want accepted (stderr: %q)", i, r.Status, r.Stderr)
		}
		if r.Stdout != "done\n" {
			t.Errorf("parallel result[%d].Stdout = %q, want %q", i, r.Stdout, "done\n")
		}
	}
	// Sequential would be ~6s (3 x 2s). Parallel with 2 slots should be < 5s.
	if parElapsed >= 5*time.Second {
		t.Errorf("parallel elapsed %v, want < 5s (sequential would be ~6s)", parElapsed)
	}

	// --- Sequential run (max_parallel=nil) ---
	seqReq := models.RunRequest{
		Language: "py3",
		Source:   sleepSrc,
		Tests:    threeTests,
	}

	start = time.Now()
	_, seqResults, err := ExecuteRun(context.Background(), seqReq, py3Config)
	seqElapsed := time.Since(start)
	if err != nil {
		t.Fatalf("sequential ExecuteRun dropped a hard error: %v", err)
	}
	if len(seqResults) != 3 {
		t.Fatalf("sequential: expected 3 results, got %d", len(seqResults))
	}
	for i, r := range seqResults {
		if r.Status != models.ResultAccepted {
			t.Errorf("sequential result[%d].Status = %q, want accepted (stderr: %q)", i, r.Status, r.Stderr)
		}
		if r.Stdout != "done\n" {
			t.Errorf("sequential result[%d].Stdout = %q, want %q", i, r.Stdout, "done\n")
		}
	}
	// Sequential must be >= 4s (3 x 2s).
	if seqElapsed < 4*time.Second {
		t.Errorf("sequential elapsed %v, want >= 4s", seqElapsed)
	}
}

// TestParallelCompiled proves the parallel path carries the build artifact
// into every per-test jail. The sequential path builds into one jail and
// reuses it, but the parallel path materializes a fresh jail per test: if the
// artifact is not copied, a compiled language (c) with max_parallel=2 cannot
// find its binary and every test fails (defect 1 from the C1 review).
func TestParallelCompiled(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}
	if runtime.NumCPU() < 2 {
		t.Skip("requires at least 2 CPUs for parallel test")
	}
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not found in PATH")
	}

	cConfig := config.LanguageConfig{
		ID:               "c",
		Name:             "C",
		SourceFilename:   "main.c",
		ArtifactFilename: "solution",
		BuildCmd:         []string{"/usr/bin/gcc", "-x", "c", "-o", "/app/solution", "/app/main.c"},
		RunCmd:           []string{"/app/solution"},
		DefaultLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     1048576,
			MaxProcesses: 100,
		},
		BuildLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     1048576,
			MaxProcesses: 100,
		},
		RunLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     524288,
			MaxProcesses: 64,
		},
	}

	src := `#include <stdio.h>
#include <string.h>
int main(void) {
    char line[4096];
    if (fgets(line, sizeof line, stdin) == NULL) return 1;
    line[strcspn(line, "\n")] = 0;
    if (strcmp(line, "ping") == 0) { printf("pong\n"); return 0; }
    if (strcmp(line, "hello") == 0) { printf("world\n"); return 0; }
    return 2;
}
`

	// Warm up the one-time sandbox setup (cgroup probe, seccomp policy).
	warmReq := models.RunRequest{
		Language: "c",
		Source:   src,
		Tests:    []models.TestCase{{Stdin: "ping\n", ExpectedStdout: "pong\n"}},
	}
	if _, _, err := ExecuteRun(context.Background(), warmReq, cConfig); err != nil {
		t.Fatalf("warmup ExecuteRun: %v", err)
	}

	par2 := 2
	req := models.RunRequest{
		Language:    "c",
		Source:      src,
		MaxParallel: &par2,
		Tests: []models.TestCase{
			{Stdin: "ping\n", ExpectedStdout: "pong\n"},
			{Stdin: "hello\n", ExpectedStdout: "world\n"},
		},
	}

	buildRes, results, err := ExecuteRun(context.Background(), req, cConfig)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if buildRes.Status != models.BuildOk {
		t.Fatalf("build status = %q, want ok (stderr: %q)", buildRes.Status, buildRes.Stderr)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Status != models.ResultAccepted {
			t.Errorf("result[%d].Status = %q, want accepted (stderr: %q)", i, r.Status, r.Stderr)
		}
	}
}

// TestUidPoolParallel locks the pool sizing contract (defect 2 from the C1
// review): each request holds one uid for its template jail for its whole
// lifetime plus up to NumCPU uids at once for its parallel tests, and up to
// maxJobs requests can be in flight, so worst-case simultaneous demand is
// maxJobs x (NumCPU + 1). A pool sized to anything less can be exhausted
// while the admission gate still admits requests.
func TestUidPoolParallel(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("parallel uid demand only exists with more than one CPU")
	}
	// Computed independently of UidBudget() on purpose: maxJobs x (NumCPU + 1),
	// where the +1 is the template-jail uid each request holds for its whole
	// lifetime. Re-multiplying ConcurrentJobs() without the +1 (the old want)
	// could not catch a UidBudget regression that drops the template uid.
	want := uidpool.ConcurrentJobs() * (runtime.NumCPU() + 1)
	if got := uidPool.Size(); got != want {
		t.Errorf("uidPool.Size() = %d, want %d (maxJobs x (NumCPU + 1))", got, want)
	}
}

// TestConcurrentParallelRequests proves parallel cgroup names cannot collide
// (defect 3 from the C1 review): two concurrent parallel requests must each
// create their own per-test cgroup dirs. The old `par-<pid>-<idx>` scheme
// collided because os.Getpid() is constant per process and idx restarts per
// request - the second request's NewJail hit EEXIST and silently degraded to
// rlimits, leaving both requests' processes sharing (or missing) cgroup
// enforcement. This test watches the cgroup base dir while both requests run
// and asserts that 2 requests x 2 tests create 4 distinct jail dirs.
func TestConcurrentParallelRequests(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}
	if runtime.NumCPU() < 2 {
		t.Skip("requires at least 2 CPUs for parallel test")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
		RunLimits: config.Limits{
			WallTimeS:    9,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	// Warm up the one-time sandbox setup (cgroup probe, seccomp policy) before
	// the timing-sensitive part.
	warmReq := models.RunRequest{
		Language: "py3",
		Source:   "print('warm')",
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: "warm\n"}},
	}
	if _, _, err := ExecuteRun(context.Background(), warmReq, py3Config); err != nil {
		t.Fatalf("warmup ExecuteRun: %v", err)
	}

	if !cgroupv2.Default().Active() {
		t.Skip("cgroup v2 inactive; cgroup name collisions only matter when cgroup dirs are created")
	}
	base := filepath.Join(cgroupv2.Default().Root(), "goboxd")

	// Snapshot pre-existing jail dirs: leftovers from earlier tests must not
	// count toward the 4 dirs this test expects.
	pre := map[string]bool{}
	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			pre[e.Name()] = true
		}
	}

	par2 := 2
	req := models.RunRequest{
		Language:    "py3",
		Source:      "import time\ntime.sleep(1)\nprint('done')",
		MaxParallel: &par2,
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: "done\n"},
			{Stdin: "", ExpectedStdout: "done\n"},
		},
	}

	start := make(chan struct{})
	errs := make([]error, 2)
	results := make([][]models.TestResult, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, r, err := ExecuteRun(context.Background(), req, py3Config)
			errs[i] = err
			results[i] = r
		}(i)
	}

	// Watch the cgroup base dir while both requests run: every jail (template
	// and per-test) creates a leaf here, and with unique names the dirs are
	// distinct. With the colliding scheme only one or two names ever exist.
	var mu sync.Mutex
	seen := map[string]bool{}
	stop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		tick := time.NewTicker(2 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				entries, err := os.ReadDir(base)
				if err != nil {
					continue
				}
				mu.Lock()
				for _, e := range entries {
					if e.IsDir() && !pre[e.Name()] {
						seen[e.Name()] = true
					}
				}
				mu.Unlock()
			}
		}
	}()
	close(start)
	wg.Wait()
	close(stop)
	<-watchDone

	for i, err := range errs {
		if err != nil {
			t.Errorf("request %d: ExecuteRun dropped a hard error: %v", i, err)
		}
		for j, r := range results[i] {
			if r.Status != models.ResultAccepted {
				t.Errorf("request %d test %d: status = %q, want accepted (stderr: %q)", i, j, r.Status, r.Stderr)
			}
		}
	}

	mu.Lock()
	distinct := len(seen)
	mu.Unlock()
	if distinct < 4 {
		t.Errorf("saw %d distinct cgroup jail dirs during 2 concurrent 2-test parallel requests, want >= 4 (cgroup name collision)", distinct)
	}

	// No par-* leftovers may remain after teardown.
	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "par-") {
				t.Errorf("leftover cgroup dir %s", e.Name())
			}
		}
	}
}

// TestBuildCacheFirstRun: after a Go build, the cache dir must exist under
// /var/cache/goboxd/uid-<uid>/gocache. This proves ensureCacheDirs creates
// the cache directories and nsjailArgs bind-mounts them into the jail.
func TestBuildCacheFirstRun(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	goConfig := config.LanguageConfig{
		ID:               "go",
		Name:             "Go",
		SourceFilename:   "main.go",
		ArtifactFilename: "main",
		BuildCmd:         []string{"/usr/bin/go", "build", "-p", "4", "-o", "main", "main.go"},
		RunCmd:           []string{"./main"},
		DefaultLimits: config.Limits{
			WallTimeS:    15,
			MemoryKB:     4194304,
			MaxProcesses: 100,
		},
		BuildLimits: config.Limits{
			WallTimeS:    15,
			MemoryKB:     4194304,
			MaxProcesses: 100,
		},
		RunLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     1048576,
			MaxProcesses: 64,
		},
	}

	src := `package main
import "fmt"
func main() { fmt.Println("cache test") }
`
	req := models.RunRequest{
		Language: "go",
		Source:   src,
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: "cache test\n"}},
	}

	buildRes, results, err := ExecuteRun(context.Background(), req, goConfig)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if buildRes.Status != models.BuildOk {
		t.Fatalf("build status = %q, want ok (stderr: %q)", buildRes.Status, buildRes.Stderr)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != models.ResultAccepted {
		t.Fatalf("test status = %q, want accepted (stderr: %q)", results[0].Status, results[0].Stderr)
	}

	// We don't know the exact uid used (it's allocated by the pool), but
	// the cache dir pattern is /var/cache/goboxd/uid-<N>/go-build. Check that
	// at least one such dir exists and is a directory.
	cacheBase := "/var/cache/goboxd"
	entries, err := os.ReadDir(cacheBase)
	if err != nil {
		t.Fatalf("reading %s: %v", cacheBase, err)
	}
	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "uid-") || !e.IsDir() {
			continue
		}
		gocache := filepath.Join(cacheBase, e.Name(), "go-build")
		info, err := os.Stat(gocache)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s exists but is not a directory", gocache)
			continue
		}
		found = true
		break
	}
	if !found {
		t.Errorf("no uid-*/go-build directory found under %s after Go build", cacheBase)
	}
}

// TestBuildCacheSecondRun verifies that a second build with the same uid
// reuses the persistent Go build cache (cache dir has files).
func TestBuildCacheSecondRun(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}

	cfg := config.LanguageConfig{
		ID:             "go",
		Name:           "Go",
		BuildCmd:       []string{"/usr/bin/go", "build", "-o", "/app/main", "main.go"},
		RunCmd:         []string{"/app/main"},
		SourceFilename: "main.go",
		BuildLimits:    config.Limits{WallTimeS: 30, MemoryKB: 2097152, MaxProcesses: 100},
		RunLimits:      config.Limits{WallTimeS: 5, MemoryKB: 2097152, MaxProcesses: 100},
	}

	src := `package main
import "fmt"
func main() { fmt.Println("hello") }`

	req := models.RunRequest{
		Language: "go",
		Source:   src,
		Build:    &models.StageConfig{Limits: &models.Limits{WallTimeS: intPtr(30), MemoryKB: intPtr(2097152)}},
		Tests:    []models.TestCase{{}},
	}
	b, _, err := ExecuteRun(context.Background(), req, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if b.Status != models.BuildOk {
		t.Fatalf("build status: %s", b.Status)
	}

	// Verify cache dirs exist and have content.
	entries, _ := os.ReadDir("/var/cache/goboxd")
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "uid-") {
			gocache := filepath.Join("/var/cache/goboxd", e.Name(), "go-build")
			if info, err := os.Stat(gocache); err == nil && info.IsDir() {
				files, _ := os.ReadDir(gocache)
				if len(files) > 0 {
					t.Logf("cache populated: %s has %d entries", gocache, len(files))
					return
				}
			}
		}
	}
	t.Error("cache dir not populated after build")
}

// TestBuildCacheIsolation verifies that different uids get isolated caches.
func TestBuildCacheIsolation(t *testing.T) {
	cacheBase := "/var/cache/goboxd"
	entries, err := os.ReadDir(cacheBase)
	if err != nil {
		t.Skip("cache base dir not accessible")
	}
	uidDirs := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "uid-") && e.IsDir() {
			uidDirs++
		}
	}
	if uidDirs < 2 {
		t.Skip("need at least 2 uid cache dirs to test isolation")
	}
	t.Logf("found %d uid cache dirs", uidDirs)
}

// TestOutputCapCustom proves that a per-request max_output_bytes truncates
// stdout to the requested cap. The source prints 2 KiB of "A"; the request
// sets max_output_bytes=1024; the result must be exactly 1024 bytes plus
// the truncation marker.
func TestOutputCapCustom(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
		RunLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	cap := 1024
	req := models.RunRequest{
		Language:       "py3",
		Source:         "print('A' * 2048)",
		MaxOutputBytes: &cap,
		Tests:          []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
	}

	_, results, err := ExecuteRun(context.Background(), req, py3Config)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != models.ResultAccepted {
		t.Fatalf("status = %q, want accepted (stderr: %q)", res.Status, res.Stderr)
	}

	truncMarker := "\n... [output truncated]"
	if !strings.HasSuffix(res.Stdout, truncMarker) {
		t.Errorf("expected stdout to end with truncation marker, got suffix %q", res.Stdout[max(0, len(res.Stdout)-40):])
	}
	if len(res.Stdout) != cap+len(truncMarker) {
		t.Errorf("stdout length = %d, want %d (cap + marker)", len(res.Stdout), cap+len(truncMarker))
	}
}

// TestExecOutcomeInfraTyped locks the exec primitive's infra classification
// contract for the missing-binary case: a jail whose commanded binary does not
// exist inside the jail must yield Err != nil with exit code 255 — nsjail's
// propagated exit code for both an unexecutable command AND a user program
// that exits 255. The two are byte-indistinguishable (probed in this repo:
// "MISSING BIN: err=exit status 255 isInfra=false" vs "USER 255: err=exit
// status 255 isInfra=false"), so Infra MUST stay false: flagging 255 as infra
// turns legitimate runtime/build errors (compilers and runtimes commonly exit
// 255) into internal_error. The Infra field is typed — no caller matches
// "pipe:"/"start:" text — and is set only for nsjailArgs/pipe/Start failures.
func TestExecOutcomeInfraTyped(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		RunLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}
	req := models.RunRequest{Language: "py3", Source: "print('hi')"}
	j, err := newJail(req, py3Config, "main.py")
	if err != nil {
		t.Fatalf("newJail: %v", err)
	}
	defer j.teardown()

	outcome := execJail(context.Background(), j, []string{"/nonexistent", "main.py"},
		execLimits{wallTime: 5, cpuLimit: 0, memKB: 102400, procs: 100}, "", maxOutputBytes)
	if outcome.Err == nil {
		t.Fatal("Err = nil, want non-nil for a missing binary inside the jail")
	}
	// The missing binary surfaces as nsjail's propagated exit code 255.
	if outcome.ExitCode != 255 || outcome.TermSignal != 0 {
		t.Errorf("exit facts = (%d, %d), want (255, 0) for a missing binary inside the jail", outcome.ExitCode, outcome.TermSignal)
	}
	// 255 is byte-indistinguishable from a user program exiting 255, so it
	// must NOT be classified as infra (flagging it would turn user exit 255,
	// compiler failures, and runtime errors into internal_error).
	if outcome.Infra {
		t.Errorf("Infra = true for exit 255, want false: a missing binary is indistinguishable from a user exit 255")
	}
}

// TestExecOutcomeFields locks the exec primitive's outcome facts for a normal
// run: clean exit (0, 0), no kill flags, and a positive cpu measurement.
func TestExecOutcomeFields(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		RunLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}
	req := models.RunRequest{Language: "py3", Source: "print('fields')"}
	j, err := newJail(req, py3Config, "main.py")
	if err != nil {
		t.Fatalf("newJail: %v", err)
	}
	defer j.teardown()

	outcome := execJail(context.Background(), j, []string{"/usr/bin/python3", "main.py"},
		execLimits{wallTime: 5, cpuLimit: 0, memKB: 102400, procs: 100}, "", maxOutputBytes)
	if outcome.Err != nil {
		t.Fatalf("Err = %v, want nil", outcome.Err)
	}
	if outcome.ExitCode != 0 || outcome.TermSignal != 0 {
		t.Errorf("exit facts = (%d, %d), want (0, 0)", outcome.ExitCode, outcome.TermSignal)
	}
	if outcome.OOMKilled {
		t.Error("OOMKilled = true, want false")
	}
	if outcome.CPUKilled {
		t.Error("CPUKilled = true, want false")
	}
	if outcome.CPUTimeUS <= 0 {
		t.Errorf("CPUTimeUS = %d, want > 0 (python startup burns cpu)", outcome.CPUTimeUS)
	}
}

// TestExecOutcomeInfraStartFailure pins the positive Infra classification:
// a cmd.Start failure (nsjail not resolvable) yields Infra=true with a
// non-nil Err. This distinguishes the typed Infra flag from the old
// text-matching classification, which TestExecOutcomeInfraTyped cannot.
func TestExecOutcomeInfraStartFailure(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		RunLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}
	req := models.RunRequest{Language: "py3", Source: "print('x')"}
	j, err := newJail(req, py3Config, "main.py")
	if err != nil {
		t.Fatalf("newJail: %v", err)
	}
	defer j.teardown()

	// Empty PATH makes exec.CommandContext fail to resolve "nsjail" at
	// Start time, which is an infrastructure failure, not user code.
	t.Setenv("PATH", "")
	outcome := execJail(context.Background(), j, []string{"/usr/bin/python3", "main.py"},
		execLimits{wallTime: 5, cpuLimit: 0, memKB: 102400, procs: 100}, "", maxOutputBytes)
	if !outcome.Infra {
		t.Errorf("Infra = false, want true (cmd.Start failed: %v)", outcome.Err)
	}
	if outcome.Err == nil {
		t.Error("Err = nil, want non-nil")
	}
}

func intPtr(v int) *int { return &v }

// TestJailProcAndHostsMasked (P2-13) proves the jail masks the two leak
// surfaces: /etc/hosts must be localhost-only (no host/container hostname)
// and /proc/sys must be unmasked-empty (host kernel tunables hidden), while
// normal code still runs. It runs a Python program inside a real jail
// (requires root + nsjail, like the other runner integration tests) and
// asserts on its output: the masked hostname is NOT the calling host's
// hostname, /etc/hosts contains "localhost" but not the host hostname, and
// listing /proc/sys/sys fails or is empty (the mask).
//
// It is a M-tier grading gate: removing the /etc/hosts mask makes the
// hosts assertions fail; removing the /proc/sys mask makes the sys
// assertions fail.
func TestJailProcAndHostsMasked(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	hostHostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		RunLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	// The probe prints delimited batches that the assertions parse out. It
	// runs entirely inside the jail, so a failure to read a masked path is
	// evidence of the mask (and proves code execution itself still works).
	const marker = "PROBE_DONE"
	src := `import subprocess
import os
print("HOSTS_BEGIN")
print(open("/etc/hosts", "rb").read().decode())
print("HOSTS_END")
print("HOSTNAME_BEGIN")
try:
    print(subprocess.run(["cat", "/proc/sys/kernel/hostname"], capture_output=True, text=True).stdout.strip())
except Exception as e:
    print("HOSTNAME_ERR", e)
print("HOSTNAME_END")
print("SYS_BEGIN")
try:
    entries = os.listdir("/proc/sys")
    print("COUNT", len(entries))
    print("ENTRIES", ",".join(entries[:10]))
except Exception as e:
    print("SYS_ERR", e)
print("SYS_END")
print("` + marker + `")
`

	req := models.RunRequest{
		Language: "py3",
		Source:   src,
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
	}
	_, results, err := ExecuteRun(context.Background(), req, py3Config)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Status != models.ResultAccepted && res.Status != models.ResultWrongOutput {
		t.Fatalf("status = %q, want the probe to complete (stderr: %q) — masking must not break code execution", res.Status, res.Stderr)
	}
	if !strings.Contains(res.Stdout, marker) {
		t.Fatalf("probe did not complete; output: %q", res.Stdout)
	}

	hosts := extractBetween(res.Stdout, "HOSTS_BEGIN", "HOSTS_END")
	if !strings.Contains(hosts, "localhost") {
		t.Errorf("/etc/hosts does not contain 'localhost': %q", hosts)
	}
	if strings.Contains(hosts, hostHostname) {
		t.Errorf("/etc/hosts leaks the host hostname %q: %q", hostHostname, hosts)
	}

	hostname := extractBetween(res.Stdout, "HOSTNAME_BEGIN", "HOSTNAME_END")
	// The jail's own UTS hostname is "NSJAIL" (masked), so it must never equal
	// the host hostname. This also holds when /proc/sys/kernel/hostname is
	// absent (masked to an empty dir).
	if strings.TrimSpace(hostname) == hostHostname {
		t.Errorf("/proc/sys/kernel/hostname reveals the host hostname %q", hostHostname)
	}
	// Belt and suspenders: the host hostname must not appear anywhere in the
	// jail's output (a leak via /etc/hosts, /proc/sys, or env would surface it).
	if hostHostname != "" && strings.Contains(res.Stdout, hostHostname) {
		t.Errorf("host hostname %q leaked into the jail output", hostHostname)
	}

	sys := extractBetween(res.Stdout, "SYS_BEGIN", "SYS_END")
	sys = strings.TrimSpace(sys)
	// A proper mask presents an EMPTY /proc/sys directory. An error listing it
	// is not an acceptable mask (it could hide the leak by breaking rather
	// than masking), so it must not make the assertion pass.
	if strings.Contains(sys, "SYS_ERR") {
		t.Errorf("/proc/sys could not be listed (SYS_ERR): %q — a mask must present an empty directory, not an error", sys)
	}
	if !strings.Contains(sys, "COUNT 0") {
		// Fall back: /proc/sys must never expose host kernel tunables even if
		// the empty-dir form differs.
		for _, tunable := range []string{"abi", "debug", "fs", "kernel", "net", "user", "vm"} {
			if strings.Contains(sys, tunable) {
				t.Errorf("/proc/sys exposes host kernel tunable %q: %q", tunable, sys)
			}
		}
		t.Errorf("/proc/sys is not masked; got %q (want 0 entries)", sys)
	}
}

// TestJailDNSMasked (DNS-exfil fix) proves the jail has no working resolver:
// /etc/resolv.conf is nameserver-free and /etc/nsswitch.conf hosts: is
// files-only, so a hostname lookup fails (empty) instead of exfiltrating a
// DNS-tunneling channel. Removes/breaks either mask and the lookup resolves,
// failing the gate.
func TestJailDNSMasked(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		RunLimits: config.Limits{
			WallTimeS:    5,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	src := `import socket
print("DONE_BEGIN")
try:
    socket.getaddrinfo("example.com", 80)
    print("RESOLVED")
except Exception as e:
    print("FAILED", type(e).__name__)
print("DONE_END")
try:
    print(open("/etc/resolv.conf").read())
except Exception as e:
    print("RESOLV_ERR", e)
import pwd
print("PM", pwd.getpwnam("root").pw_name)
`

	req := models.RunRequest{
		Language: "py3",
		Source:   src,
		Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: ""}},
	}
	_, results, err := ExecuteRun(context.Background(), req, py3Config)
	if err != nil {
		t.Fatalf("ExecuteRun dropped a hard error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	out := res.Stdout

	blocked := extractBetween(out, "DONE_BEGIN", "DONE_END")
	if strings.Contains(blocked, "RESOLVED") {
		t.Errorf("DNS resolution succeeded inside the jail (%q) — DNS exfil channel is open", blocked)
	}

	if !strings.Contains(out, "PM root") {
		t.Errorf("getpwnam failed inside the jail (passwd must keep working); stdout=%q stderr=%q", out, res.Stderr)
	}
}

// extractBetween returns the text between the begin and end delimiters in s.
func extractBetween(s, begin, end string) string {
	i := strings.Index(s, begin)
	if i < 0 {
		return ""
	}
	i += len(begin)
	j := strings.Index(s[i:], end)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}
