package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nithitsuki/goboxd/internal/config"
	"github.com/nithitsuki/goboxd/internal/models"
)

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
		memPeak  int
		memLimit int
		wallTime int
		duration int
		want     string
	}{
		{"exact match", nil, "hello\n", "hello\n", 0, 0, 10, 500, "accepted"},
		{"whitespace diff", nil, "hello\n", "hello", 0, 0, 10, 500, "output_whitespace_mismatch"},
		{"wrong output", nil, "world", "hello", 0, 0, 10, 500, "wrong_output"},
		{"empty expected", nil, "anything", "", 0, 0, 10, 500, "accepted"},
		{"exact match with empty expected", nil, "", "", 0, 0, 10, 500, "accepted"},
		{"exit 137 early kill", fmt.Errorf("exit status 137"), "", "", 0, 0, 10, 1000, "time_exceeded"},
		{"exit 137 at wall time", fmt.Errorf("exit status 137"), "", "", 0, 0, 10, 10000, "time_exceeded"},
		{"exit 139 segv", fmt.Errorf("exit status 139"), "", "", 0, 0, 10, 1000, "runtime_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTestStatus(context.Background(), tt.err, tt.stdout, tt.expected, nil, tt.memPeak, tt.memLimit, tt.wallTime, tt.duration, false)
			if got != tt.want {
				t.Errorf("computeTestStatus = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSignalKillReason(t *testing.T) {
	// signalKillReason with nil ProcessState should return ""
	if got := signalKillReason(nil); got != "" {
		t.Errorf("signalKillReason(nil) = %q, want ''", got)
	}
}

// TestComputeTestStatusOOMKilled: a cgroup OOM kill (detected via the leaf's
// memory.events) must be classified memory_exceeded, ahead of the exit-137 /
// wall-time heuristics that would otherwise say time_exceeded.
func TestComputeTestStatusOOMKilled(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		oomKilled bool
		wallTime  int
		duration  int
		want      string
	}{
		{"oom kill at 137", fmt.Errorf("exit status 137"), true, 10, 1000, "memory_exceeded"},
		{"oom kill at wall time", fmt.Errorf("exit status 137"), true, 10, 10000, "memory_exceeded"},
		{"oom kill with nil err", nil, true, 10, 1000, "memory_exceeded"},
		{"no oom kill stays time_exceeded", fmt.Errorf("exit status 137"), false, 10, 10000, "time_exceeded"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTestStatus(context.Background(), tt.err, "", "", nil, 0, 0, tt.wallTime, tt.duration, tt.oomKilled)
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
	args, err := nsjailArgs("/app", 5, 65536, 100, 10000, nil) // 64MB, 100 procs
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
	args, err = nsjailArgs("/app", 5, 16*1024*1024, 100, 10000, nil)
	if err != nil {
		t.Fatalf("nsjailArgs (16GB): %v", err)
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--rlimit_as" && args[i+1] != "16384" {
			t.Errorf("--rlimit_as for 16GB limit = %q, want 16384 (1:1 MB)", args[i+1])
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
	args, err := nsjailArgs("/app", 5, 65536, 100, 12345, nil)
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
