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
	"syscall"
	"testing"
	"time"

	"github.com/nithitsuki/goboxd/internal/config"
	"github.com/nithitsuki/goboxd/internal/models"
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
		expectedStatus string
	}{
		{
			name:   "positive basic",
			source: "print('Hello from Python 3!')",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: "Hello from Python 3!\n"},
			},
			expectedStatus: "accepted",
		},
		{
			name:   "timeout moderate",
			source: "import time\nwhile True: time.sleep(0.1)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: "time_exceeded",
		},
		{
			name:   "runtime error (syntax)",
			source: "print(1/0)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: "runtime_error",
		},
		{
			name:   "memory limit (OOM)",
			source: "l = []\nwhile True:\n    l.append('a' * 1024 * 1024)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: "runtime_error",
		},
		{
			name:   "wrong output",
			source: "print('wrong')",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: "right\n"},
			},
			expectedStatus: "wrong_output",
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
			if tt.name == "memory limit (OOM)" && (res.Status == "time_exceeded" || res.Status == "runtime_error") {
				return
			}

			// Timeout: nsjail sigkill can read as time_exceeded or runtime_error
			if tt.name == "timeout moderate" && (res.Status == "time_exceeded" || res.Status == "runtime_error") {
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
	if results[0].Status != "cancelled" {
		t.Errorf("expected status %q, got %q (stderr: %q)", "cancelled", results[0].Status, results[0].Stderr)
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

func TestReadCapped(t *testing.T) {
	// Short string passes through unchanged
	input := "hello world"
	got := readCapped(bytes.NewBufferString(input))
	if got != input {
		t.Errorf("readCapped = %q, want %q", got, input)
	}

	// Empty input
	got = readCapped(bytes.NewBufferString(""))
	if got != "" {
		t.Errorf("readCapped(empty) = %q, want ''", got)
	}

	// Input larger than maxOutputBytes triggers truncation with marker
	truncationMarker := "\n... [output truncated]"
	big := strings.Repeat("A", int(maxOutputBytes)+1)
	got = readCapped(bytes.NewBufferString(big))
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
		want     string
	}{
		{"exact match", nil, "hello\n", "hello\n", cpuOutcome{}, context.Background(), "accepted"},
		{"whitespace diff", nil, "hello\n", "hello", cpuOutcome{}, context.Background(), "output_whitespace_mismatch"},
		{"wrong output", nil, "world", "hello", cpuOutcome{}, context.Background(), "wrong_output"},
		{"empty expected", nil, "anything", "", cpuOutcome{}, context.Background(), "accepted"},
		{"exact match with empty expected", nil, "", "", cpuOutcome{}, context.Background(), "accepted"},
		{"exit 137 early kill", fmt.Errorf("exit status 137"), "", "", cpuOutcome{}, context.Background(), "time_exceeded"},
		{"exit 137 at wall time", fmt.Errorf("exit status 137"), "", "", cpuOutcome{}, context.Background(), "time_exceeded"},
		{"exit 139 segv", fmt.Errorf("exit status 139"), "", "", cpuOutcome{}, context.Background(), "runtime_error"},
		{"cpu kill wins over deadline-shaped error", fmt.Errorf("signal: killed"), "", "", cpuOutcome{killed: true}, context.Background(), "cpu_time_exceeded"},
		{"cancelled beats cpu kill", fmt.Errorf("signal: killed"), "", "", cpuOutcome{killed: true}, canceledCtx(), "cancelled"},
		{"no heuristic: err without signal is runtime_error even at wall time", fmt.Errorf("exit status 2"), "", "", cpuOutcome{}, context.Background(), "runtime_error"},
		{"rlimit cpu kill reads as 137 with full cpu time", fmt.Errorf("exit status 137"), "", "", cpuOutcome{limitUS: 2 * 1e6, timeUS: 2 * 1e6}, context.Background(), "cpu_time_exceeded"},
		{"wall kill with cpu time under the limit stays time_exceeded", fmt.Errorf("exit status 137"), "", "", cpuOutcome{limitUS: 11 * 1e6, timeUS: 9 * 1e6}, context.Background(), "time_exceeded"},
		{"no cpu limit: 137 is always time_exceeded", fmt.Errorf("exit status 137"), "", "", cpuOutcome{timeUS: 2 * 1e6}, context.Background(), "time_exceeded"},
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
		want   string
	}{
		{"sigxcpu signaled", syscall.WaitStatus(syscall.SIGXCPU), "cpu_time_exceeded"},
		{"nsjail exit 152 (128+SIGXCPU)", syscall.WaitStatus(152 << 8), "cpu_time_exceeded"},
		{"sigkill", syscall.WaitStatus(syscall.SIGKILL), "time_exceeded"},
		{"sigsegv", syscall.WaitStatus(syscall.SIGSEGV), "memory_exceeded"},
		{"sigabrt", syscall.WaitStatus(syscall.SIGABRT), "memory_exceeded"},
		{"other signal", syscall.WaitStatus(syscall.SIGTERM), "runtime_error"},
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
	if got != "cpu_time_exceeded" {
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
	if res.Status != "cpu_time_exceeded" {
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
	if res.Status != "accepted" {
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
	if res := run(t, warmReq); res.Status != "accepted" {
		t.Fatalf("warmup status = %q, want accepted (stderr: %q)", res.Status, res.Stderr)
	}

	t.Run("accepted is (0,0)", func(t *testing.T) {
		res := run(t, models.RunRequest{
			Language: "py3",
			Source:   "print('ok')",
			Tests:    []models.TestCase{{Stdin: "", ExpectedStdout: "ok\n"}},
		})
		if res.Status != "accepted" {
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
		if res.Status != "runtime_error" {
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
		if res.Status != "time_exceeded" {
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
		if res.Status != "cpu_time_exceeded" {
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
		want      string
	}{
		{"oom kill at 137", fmt.Errorf("exit status 137"), true, "memory_exceeded"},
		{"oom kill at wall time", fmt.Errorf("exit status 137"), true, "memory_exceeded"},
		{"oom kill with nil err", nil, true, "memory_exceeded"},
		{"no oom kill stays time_exceeded", fmt.Errorf("exit status 137"), false, "time_exceeded"},
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
	args, err := nsjailArgs("/app", 5, 0, 65536, 100, 10000, nil) // 64MB, 100 procs, no cpu cap
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
	args, err = nsjailArgs("/app", 5, 0, 16*1024*1024, 100, 10000, nil)
	if err != nil {
		t.Fatalf("nsjailArgs (16GB): %v", err)
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--rlimit_as" && args[i+1] != "16384" {
			t.Errorf("--rlimit_as for 16GB limit = %q, want 16384 (1:1 MB)", args[i+1])
		}
	}
}

// TestNsjailArgsRlimitCPU locks the cpu limit contract: a positive cpu limit
// is passed as --rlimit_cpu seconds (the kernel SIGXCPU path), a zero limit
// (no cpu cap) is "inf" so nsjail's implicit 600s default never applies.
func TestNsjailArgsRlimitCPU(t *testing.T) {
	args, err := nsjailArgs("/app", 9, 2, 65536, 100, 10000, nil)
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

	args, err = nsjailArgs("/app", 9, 0, 65536, 100, 10000, nil)
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
	args, err := nsjailArgs("/app", 5, 0, 65536, 100, 12345, nil)
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

// TestJailEnv locks the environment allowlist: exactly five vars, in a
// stable order, with PATH copied from the server env at call time and a
// hardcoded fallback when PATH is unset or empty.
func TestJailEnv(t *testing.T) {
	t.Setenv("PATH", "/custom/bin")
	got := jailEnv()
	want := []string{
		"-E", "PATH=/custom/bin",
		"-E", "HOME=/tmp",
		"-E", "GOCACHE=/tmp/go-cache",
		"-E", "LANG=C.UTF-8",
		"-E", "LC_ALL=C.UTF-8",
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
		"-E", "GOCACHE=/tmp/go-cache",
		"-E", "LANG=C.UTF-8",
		"-E", "LC_ALL=C.UTF-8",
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
	if res.Status != "accepted" {
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

	allowed := []string{"PATH", "HOME", "GOCACHE", "LANG", "LC_ALL"}
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
		if r.Status != "accepted" {
			t.Errorf("parallel result[%d].Status = %q, want accepted (stderr: %q)", i, r.Status, r.Stderr)
		}
		if r.Stdout != "done\n" {
			t.Errorf("parallel result[%d].Stdout = %q, want %q", i, r.Stdout, "done\n")
		}
	}
	// Sequential would be ~6s (3 × 2s). Parallel with 2 slots should be < 5s.
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
		if r.Status != "accepted" {
			t.Errorf("sequential result[%d].Status = %q, want accepted (stderr: %q)", i, r.Status, r.Stderr)
		}
		if r.Stdout != "done\n" {
			t.Errorf("sequential result[%d].Stdout = %q, want %q", i, r.Stdout, "done\n")
		}
	}
	// Sequential must be ≥ 4s (3 × 2s).
	if seqElapsed < 4*time.Second {
		t.Errorf("sequential elapsed %v, want ≥ 4s", seqElapsed)
	}
}
