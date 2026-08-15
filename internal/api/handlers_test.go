package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nithitsuki/goboxd/internal/config"
	"github.com/nithitsuki/goboxd/internal/models"
)

// TestMain lets the cgroup probe's re-exec work under go test (mirrors
// internal/runner/runner_test.go). The real goboxd binary runs
// cgroupv2.ProbeHog when GOBOXD_CGROUP_PROBE_HOG=1, but /proc/self/exe of
// this package's tests is the test binary. Mirror the hog here: block on
// stdin until the probe moves this process into the leaf cgroup, then touch
// 16MB, spin ~2s of CPU (the probe's cpu check reads the leaf's cpu.stat
// usage_usec), and exit 0. Without this the probe's child runs the whole
// test suite, which re-probes recursively and fails.
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

func TestHandleHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	HandleHealthz(w, req)

	res := w.Result()
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected status OK, got %v", res.StatusCode)
	}

	contentType := res.Header.Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected content type application/json, got %v", contentType)
	}
}

func TestHandleInfo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()

	HandleInfo(w, req)

	res := w.Result()
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	if _, ok := body["build_info"]; !ok {
		t.Error("missing build_info")
	}
	if _, ok := body["languages"]; !ok {
		t.Error("missing languages")
	}
	// cgroupv2 state must be exposed so clients (and the e2e suite) can tell
	// whether cgroup limits are enforced or the rlimit fallback is in use.
	cg, ok := body["cgroupv2"].(map[string]interface{})
	if !ok {
		t.Error("missing cgroupv2 field")
	} else {
		if _, ok := cg["active"].(bool); !ok {
			t.Error("cgroupv2.active must be a bool")
		}
	}
}

func TestHandleRunValidation(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		expectedCode int
		errorCode    string
	}{
		{
			name:         "missing language",
			body:         `{"source":"print(1)","tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "missing_language",
		},
		{
			name:         "missing source",
			body:         `{"language":"py3","tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "missing_source",
		},
		{
			name:         "missing tests",
			body:         `{"language":"py3","source":"print(1)","tests":[]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "missing_tests",
		},
		{
			name:         "unknown language",
			body:         `{"language":"nonexistent","source":"main = putStrLn \"hi\"","tests":[{"stdin":"","expected_stdout":""}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "unknown_language",
		},
		{
			name:         "path traversal in filename",
			body:         `{"language":"py3","source":"print(1)","source_filename":"../etc/passwd","tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "invalid_filename",
		},
		{
			name:         "oversized json payload",
			body:         `{"garbage": "` + strings.Repeat("A", 300*1024) + `"}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "invalid_request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()
			HandleRun(w, req)

			res := w.Result()
			defer func() {
				_ = res.Body.Close()
			}()

			if res.StatusCode != tt.expectedCode {
				t.Errorf("expected status %d, got %d", tt.expectedCode, res.StatusCode)
			}

			if tt.errorCode != "" {
				var apiErr models.APIError
				if err := json.NewDecoder(res.Body).Decode(&apiErr); err == nil {
					if apiErr.Error.Code != tt.errorCode {
						t.Errorf("expected error code %s, got %s", tt.errorCode, apiErr.Error.Code)
					}
				}
			}
		})
	}
}

func TestHandleRunExecution(t *testing.T) {
	// only run this if nsjail is available
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found, skipping execution test")
	}

	body := `{"language":"py3","source":"print(1)","tests":[{"stdin":"","expected_stdout":"1\n"}]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	HandleRun(w, req)

	res := w.Result()
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var resp models.RunResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status == "" {
		t.Error("response status should not be empty")
	}
}

// TestHandleRunContextCancel proves the client-disconnect path: a request
// whose context is cancelled mid-run gets no response written (the client is
// gone), and the run is still recorded as "cancelled" in the live metrics.
func TestHandleRunContextCancel(t *testing.T) {
	// only run this if nsjail is available
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found, skipping execution test")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	// Warm up the one-time sandbox setup (cgroup probe ~3s on first run) so
	// the cancel timer below lands during the busy loop, not during setup.
	warmBody := `{"language":"py3","source":"print('warm')","tests":[{"stdin":"","expected_stdout":"warm\n"}]}`
	warmReq := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(warmBody))
	HandleRun(httptest.NewRecorder(), warmReq)

	statuses := Snapshot()["status_counts"].(map[string]int64)
	before := statuses["cancelled"]

	body := `{"language":"py3","source":"while True:\n    pass","tests":[{"stdin":"","expected_stdout":""}]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)
	time.AfterFunc(500*time.Millisecond, cancel)

	w := httptest.NewRecorder()
	HandleRun(w, req)

	if w.Body.Len() != 0 {
		t.Errorf("cancelled request received a response body: %q", w.Body.String())
	}
	if w.Header().Get("Content-Type") != "" {
		t.Errorf("cancelled request received response headers: %v", w.Header())
	}

	statuses = Snapshot()["status_counts"].(map[string]int64)
	if after := statuses["cancelled"]; after != before+1 {
		t.Errorf("status_counts[cancelled] = %d, want %d", after, before+1)
	}
}

// TestHandleRunQueueFull locks the bounded-admission contract end to end:
// with a gate of N=1 M=0, one busy-loop run holds the only slot and a second
// request is rejected with 503, Retry-After: 1, body code queue_full, and a
// queue_full entry in the live metrics.
func TestHandleRunQueueFull(t *testing.T) {
	// only run this if nsjail is available
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found, skipping execution test")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	// Warm up the one-time sandbox setup (cgroup probe ~3s on first run).
	warmBody := `{"language":"py3","source":"print('warm')","tests":[{"stdin":"","expected_stdout":"warm\n"}]}`
	HandleRun(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(warmBody)))

	orig := gate
	gate = newAdmissionGate(1, 0)

	statuses := Snapshot()["status_counts"].(map[string]int64)
	before := statuses["queue_full"]

	// The holder's wall_time_s matches the py3 registry run max (9s in
	// config/languages.yml). Setting it explicitly keeps the holder alive
	// for the full registry window even if the YAML default changes.
	body := `{"language":"py3","source":"while True:\n    pass","run":{"limits":{"wall_time_s":9}},"tests":[{"stdin":"","expected_stdout":""}]}`
	ctx1, cancel1 := context.WithCancel(context.Background())
	firstDone := make(chan struct{})
	// Restore the package gate only after the holder goroutine has fully
	// exited. Its deferred release reads the package gate variable, so a
	// failure path must never swap the gate back while the holder still
	// owns a slot in the swapped gate. Waiting here (instead of deferring
	// the swap) also covers t.Fatalf exits: the cleanup runs after the
	// test's defers and cannot corrupt the real gate.
	t.Cleanup(func() {
		cancel1()
		select {
		case <-firstDone:
		case <-time.After(10 * time.Second):
			t.Errorf("holder goroutine did not exit during gate cleanup")
		}
		gate = orig
	})
	go func() {
		defer close(firstDone)
		req1 := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
		HandleRun(httptest.NewRecorder(), req1.WithContext(ctx1))
	}()
	waitForGate(t, gate, 10*time.Second, func() bool { return gate.snapInFlight() == 1 }, "first run to hold the slot")

	req2 := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
	w2 := httptest.NewRecorder()
	HandleRun(w2, req2)

	res2 := w2.Result()
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", res2.StatusCode)
	}
	if got := res2.Header.Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want \"1\"", got)
	}
	var apiErr models.APIError
	if err := json.NewDecoder(res2.Body).Decode(&apiErr); err != nil {
		t.Fatalf("failed to decode error body: %v", err)
	}
	if apiErr.Error.Code != "queue_full" {
		t.Errorf("error code = %q, want queue_full", apiErr.Error.Code)
	}
	if apiErr.Error.Message == "" {
		t.Error("queue_full message is empty")
	}

	statuses = Snapshot()["status_counts"].(map[string]int64)
	if after := statuses["queue_full"]; after != before+1 {
		t.Errorf("status_counts[queue_full] = %d, want %d", after, before+1)
	}

	// Tear down: cancel the busy loop so the suite does not wait out its
	// wall-time limit.
	cancel1()
	select {
	case <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("first run did not finish after context cancel")
	}
}

// TestHandleRunQueuedCancel locks the disconnect-while-queued path: with a
// gate of N=1 M=1, a second request waits in the queue; cancelling its
// context frees the ticket, writes nothing to the response, and records
// "cancelled" in the live metrics.
func TestHandleRunQueuedCancel(t *testing.T) {
	// only run this if nsjail is available
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found, skipping execution test")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}

	// Warm up the one-time sandbox setup (cgroup probe ~3s on first run).
	warmBody := `{"language":"py3","source":"print('warm')","tests":[{"stdin":"","expected_stdout":"warm\n"}]}`
	HandleRun(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(warmBody)))

	orig := gate
	gate = newAdmissionGate(1, 1)

	statuses := Snapshot()["status_counts"].(map[string]int64)
	before := statuses["cancelled"]

	// The holder's wall_time_s matches the py3 registry run max (9s in
	// config/languages.yml). Setting it explicitly keeps the holder alive
	// for the full registry window even if the YAML default changes.
	body := `{"language":"py3","source":"while True:\n    pass","run":{"limits":{"wall_time_s":9}},"tests":[{"stdin":"","expected_stdout":""}]}`
	ctx1, cancel1 := context.WithCancel(context.Background())
	firstDone := make(chan struct{})
	ctx2, cancel2 := context.WithCancel(context.Background())
	secondDone := make(chan struct{})
	// Restore the package gate only after both goroutines have fully
	// exited. The holder's deferred release reads the package gate variable,
	// so a failure path must never swap the gate back while the holder still
	// owns a slot in the swapped gate. Waiting here (instead of deferring
	// the swap) also covers t.Fatalf exits: the cleanup runs after the
	// test's defers and cannot corrupt the real gate.
	t.Cleanup(func() {
		cancel1()
		cancel2()
		select {
		case <-firstDone:
		case <-time.After(10 * time.Second):
			t.Errorf("holder goroutine did not exit during gate cleanup")
		}
		select {
		case <-secondDone:
		case <-time.After(10 * time.Second):
			t.Errorf("queued goroutine did not exit during gate cleanup")
		}
		gate = orig
	})
	go func() {
		defer close(firstDone)
		req1 := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
		HandleRun(httptest.NewRecorder(), req1.WithContext(ctx1))
	}()
	waitForGate(t, gate, 10*time.Second, func() bool { return gate.snapInFlight() == 1 }, "first run to hold the slot")

	w2 := httptest.NewRecorder()
	go func() {
		defer close(secondDone)
		req2 := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
		HandleRun(w2, req2.WithContext(ctx2))
	}()
	waitForGate(t, gate, 10*time.Second, func() bool { return gate.snapQueued() == 1 }, "second run to queue")

	cancel2()
	select {
	case <-secondDone:
	case <-time.After(10 * time.Second):
		t.Fatal("queued request did not finish after context cancel")
	}

	if w2.Body.Len() != 0 {
		t.Errorf("cancelled queued request received a response body: %q", w2.Body.String())
	}
	if w2.Header().Get("Content-Type") != "" {
		t.Errorf("cancelled queued request received response headers: %v", w2.Header())
	}

	statuses = Snapshot()["status_counts"].(map[string]int64)
	if after := statuses["cancelled"]; after != before+1 {
		t.Errorf("status_counts[cancelled] = %d, want %d", after, before+1)
	}

	// Tear down: cancel the busy loop holding the slot.
	cancel1()
	select {
	case <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("first run did not finish after context cancel")
	}
}

func TestComputeTopLevelStatus(t *testing.T) {
	tests := []struct {
		name     string
		build    models.BuildResult
		tests    []models.TestResult
		expected string
	}{
		{
			name:     "all ok",
			build:    models.BuildResult{Status: "ok"},
			tests:    []models.TestResult{{Status: "accepted"}},
			expected: "accepted",
		},
		{
			name:     "build failed",
			build:    models.BuildResult{Status: "failed"},
			tests:    []models.TestResult{{Status: "accepted"}},
			expected: "build_failed",
		},
		{
			name:     "internal error on build",
			build:    models.BuildResult{Status: "internal_error"},
			tests:    []models.TestResult{{Status: "accepted"}},
			expected: "internal_error",
		},
		{
			name:     "first non-accepted test",
			build:    models.BuildResult{Status: "ok"},
			tests:    []models.TestResult{{Status: "accepted"}, {Status: "wrong_output"}, {Status: "time_exceeded"}},
			expected: "wrong_output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeTopLevelStatus(tt.build, tt.tests)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestIsValidFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty is ok", "", true},
		{"simple name", "solution.py", true},
		{"with dots", "test.file.go", true},
		{"path traversal forward slash", "../etc/passwd", false},
		{"path traversal backslash", "..\\etc\\passwd", false},
		{"leading dot", ".hidden", false},
		{"double dot", "foo..bar", false},
		{"too long", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidFilename(tt.input)
			if got != tt.want {
				t.Errorf("isValidFilename(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateFlags(t *testing.T) {
	allowlist := []string{"-O0", "-O1", "-O2", "-O3", "-Wall", "-Wextra", "-std=*"}

	tests := []struct {
		name  string
		flags []string
		want  bool
	}{
		{"empty flags", []string{}, true},
		{"exact match", []string{"-O2"}, true},
		{"prefix match", []string{"-std=c99"}, true},
		{"another prefix", []string{"-std=c17"}, true},
		{"multiple allowed", []string{"-O2", "-Wall", "-std=c99"}, true},
		{"injected flag", []string{"-fplugin=evil.so"}, false},
		{"response file", []string{"@payload"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := validateFlags(tt.flags, allowlist)
			if got != tt.want {
				t.Errorf("validateFlags(%v) = %v, want %v", tt.flags, got, tt.want)
			}
		})
	}
}

func init() {
	// Try to load YAML config from project root
	cfgPath := config.RegistryPath
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(cfgPath); err == nil {
			config.RegistryPath = cfgPath
			if err := config.LoadRegistry(); err == nil {
				return
			}
		}
		cfgPath = "../" + cfgPath
	}
}

func TestSecurityHole2NoShellCommands(t *testing.T) {
	// Security hole #2: verify we never invoke shell interpreters.
	// This test scans ALL Go source files in the project for exec.Command
	// calls and rejects any that reference a shell binary.
	shells := []string{"sh", "bash", "dash", "ash", "zsh", "csh", "tcsh", "ksh", "fish"}
	errs := 0

	// Walk all .go files in the project (excluding test files themselves)
	files, _ := filepath.Glob("../../internal/**/*.go")
	files = append(files, "../../cmd/goboxd/main.go")

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src := string(data)
		for _, shell := range shells {
			pattern := fmt.Sprintf(`exec.Command(%q`, shell)
			if strings.Contains(src, pattern) {
				t.Errorf("%s uses shell %q via exec.Command (security hole #2)", f, shell)
				errs++
			}
		}
	}

	// Verify we use proper filesystem APIs
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src := string(data)
		if strings.Contains(src, "os.MkdirTemp") || strings.Contains(src, "os.RemoveAll") {
			t.Logf("%s uses Go filesystem APIs", f)
		}
	}

	if errs > 0 {
		t.Fatalf("Security hole #2: OPEN — found %d shell invocations", errs)
	}
	t.Log("Security hole #2: CLOSED — no shell interpreters invoked")
}

// TestHandleRunLimitValidation guards the downward-only limit contract:
// client-requested build/run limits must never exceed the configured YAML
// maxima (HTTP 400 limit_exceeded), must be positive (400 invalid_limit),
// and interpreted languages reject build limits (no build stage).
func TestHandleRunLimitValidation(t *testing.T) {
	c := config.DefaultRegistry["c"]
	py := config.DefaultRegistry["py3"]
	if c.BuildLimits.MemoryKB == 0 || py.RunLimits.MemoryKB == 0 || py.RunLimits.CpuTimeS == 0 {
		t.Fatal("test requires c build limits and py3 run limits (incl. cpu_time_s) in the registry")
	}

	tests := []struct {
		name         string
		body         string
		expectedCode int
		errorCode    string
	}{
		{
			name: "build memory above max",
			body: fmt.Sprintf(`{"language":"c","source":"int main(){return 0;}","build":{"limits":{"memory_kb":%d}},"tests":[{"stdin":"","expected_stdout":""}]}`,
				c.BuildLimits.MemoryKB+1),
			expectedCode: http.StatusBadRequest,
			errorCode:    "limit_exceeded",
		},
		{
			name: "run memory above max",
			body: fmt.Sprintf(`{"language":"py3","source":"print(1)","run":{"limits":{"memory_kb":%d}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
				py.RunLimits.MemoryKB+1),
			expectedCode: http.StatusBadRequest,
			errorCode:    "limit_exceeded",
		},
		{
			name: "run wall time above max",
			body: fmt.Sprintf(`{"language":"py3","source":"print(1)","run":{"limits":{"wall_time_s":%d}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
				py.RunLimits.WallTimeS+1),
			expectedCode: http.StatusBadRequest,
			errorCode:    "limit_exceeded",
		},
		{
			name:         "zero wall time",
			body:         `{"language":"py3","source":"print(1)","run":{"limits":{"wall_time_s":0}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "invalid_limit",
		},
		{
			name:         "negative processes",
			body:         `{"language":"py3","source":"print(1)","run":{"limits":{"max_processes":-1}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "invalid_limit",
		},
		{
			name:         "build limits on interpreted language",
			body:         `{"language":"py3","source":"print(1)","build":{"limits":{"memory_kb":1000}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "invalid_limit",
		},
		{
			name: "equal to max is accepted",
			body: fmt.Sprintf(`{"language":"py3","source":"print(1)","run":{"limits":{"memory_kb":%d}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
				py.RunLimits.MemoryKB),
			expectedCode: http.StatusOK,
		},
		{
			name: "below max is accepted",
			body: fmt.Sprintf(`{"language":"py3","source":"print(1)","run":{"limits":{"memory_kb":%d}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
				py.RunLimits.MemoryKB-1),
			expectedCode: http.StatusOK,
		},
		{
			name: "run cpu above max",
			body: fmt.Sprintf(`{"language":"py3","source":"print(1)","run":{"limits":{"cpu_time_s":%d}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
				py.RunLimits.CpuTimeS+1),
			expectedCode: http.StatusBadRequest,
			errorCode:    "limit_exceeded",
		},
		{
			name:         "zero cpu time",
			body:         `{"language":"py3","source":"print(1)","run":{"limits":{"cpu_time_s":0}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "invalid_limit",
		},
		{
			name: "cpu below max is accepted",
			body: fmt.Sprintf(`{"language":"py3","source":"print(1)","run":{"limits":{"cpu_time_s":%d}},"tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
				py.RunLimits.CpuTimeS-1),
			expectedCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()
			HandleRun(w, req)

			res := w.Result()
			defer func() { _ = res.Body.Close() }()

			if res.StatusCode != tt.expectedCode {
				t.Errorf("expected status %d, got %d", tt.expectedCode, res.StatusCode)
			}
			if tt.errorCode != "" {
				var apiErr models.APIError
				if err := json.NewDecoder(res.Body).Decode(&apiErr); err == nil {
					if apiErr.Error.Code != tt.errorCode {
						t.Errorf("expected error code %s, got %s", tt.errorCode, apiErr.Error.Code)
					}
				}
			}
		})
	}
}

// TestValidateStageLimitsCPU locks the cpu_time_s contract: positive,
// downward-only against the YAML max, and a zero YAML max (a language with
// no cpu cap configured) rejects any client cpu limit with invalid_limit.
func TestValidateStageLimitsCPU(t *testing.T) {
	max := config.Limits{WallTimeS: 9, MemoryKB: 102400, MaxProcesses: 100, CpuTimeS: 11}

	ptr := func(n int) *int { return &n }
	cases := []struct {
		name     string
		stageMax config.Limits
		limit    *int
		wantCode string
	}{
		{"below max ok", max, ptr(5), ""},
		{"equal max ok", max, ptr(11), ""},
		{"above max rejected", max, ptr(12), "limit_exceeded"},
		{"zero rejected", max, ptr(0), "invalid_limit"},
		{"negative rejected", max, ptr(-1), "invalid_limit"},
		{"no cap in registry rejected", config.Limits{CpuTimeS: 0}, ptr(2), "invalid_limit"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			stage := &models.StageConfig{Limits: &models.Limits{CpuTimeS: tt.limit}}
			if validateStageLimits(w, stage, tt.stageMax, true, "run") == (tt.wantCode != "") {
				t.Errorf("validateStageLimits accepted=%v, want error %q", tt.wantCode == "", tt.wantCode)
			}
			if tt.wantCode != "" {
				var apiErr models.APIError
				if err := json.NewDecoder(w.Body).Decode(&apiErr); err != nil || apiErr.Error.Code != tt.wantCode {
					t.Errorf("error code = %+v, want %q", apiErr.Error, tt.wantCode)
				}
				if tt.stageMax.CpuTimeS == 0 && tt.limit != nil && *tt.limit > 0 &&
					!strings.Contains(apiErr.Error.Message, "no cpu limit configured") {
					t.Errorf("no-cap message = %q, want it to say the language has no cpu limit configured", apiErr.Error.Message)
				}
			}
		})
	}
}

// TestNewServerTimeouts guards the Slowloris mitigation: the HTTP server must
// bound header read time and total read time, and reap idle connections.
// Without these, a client that opens connections and drips bytes can hold
// goroutines and file descriptors indefinitely. It also pins the listen
// address: NewServer must bind the addr it is given (a missing Addr silently
// defaults to :http, which cost a full debugging session).
func TestNewServerTimeouts(t *testing.T) {
	srv := NewServer(":8080", http.NewServeMux())
	if srv.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080 (empty Addr defaults to :http)", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want > 0 (Slowloris mitigation)", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout = %v, want > 0", srv.ReadTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want > 0", srv.IdleTimeout)
	}
}

// TestRequestIDMiddleware guards the trace-ID contract: a client-supplied
// X-Request-Id is honored and echoed, a missing one is generated (unique
// across requests), and the id lands in the response header.
func TestRequestIDMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFrom(r) == "" {
			t.Error("request id missing from context")
		}
		w.WriteHeader(http.StatusOK)
	})

	// Client-supplied id is honored.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-Id", "trace-abc-123")
	w := httptest.NewRecorder()
	RequestIDMiddleware(inner).ServeHTTP(w, req)
	if got := w.Header().Get("X-Request-Id"); got != "trace-abc-123" {
		t.Errorf("echoed id = %q, want trace-abc-123", got)
	}

	// Generated ids are non-empty and unique.
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		w := httptest.NewRecorder()
		RequestIDMiddleware(inner).ServeHTTP(w, req)
		got := w.Header().Get("X-Request-Id")
		if got == "" {
			t.Fatal("generated request id is empty")
		}
		if seen[got] {
			t.Errorf("generated id %q repeated", got)
		}
		seen[got] = true
	}
}

// TestHandleOpenAPI guards the machine-readable API contract: /openapi.json
// must serve a valid OpenAPI 3 document that covers the public endpoints and
// the /run request/response schemas.
func TestHandleOpenAPI(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	w := httptest.NewRecorder()
	HandleOpenAPI(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var doc struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		t.Fatalf("decoding spec: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		t.Errorf("openapi = %q, want 3.x", doc.OpenAPI)
	}
	for _, p := range []string{"/healthz", "/readyz", "/info", "/run", "/openapi.json"} {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("spec missing path %s", p)
		}
	}
}

// TestHandleMetrics guards the live-metrics contract: /metrics returns a JSON
// snapshot with the dashboard fields, and a completed run moves the counters.
func TestHandleMetrics(t *testing.T) {
	// One run so the counters move (works with or without a live sandbox:
	// a failed sandbox setup is still a counted run).
	body := `{"language":"py3","source":"print(1)","tests":[{"stdin":"","expected_stdout":"1\n"}]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	HandleRun(w, req)

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w = httptest.NewRecorder()
	HandleMetrics(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	var m map[string]json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		t.Fatalf("decoding metrics: %v", err)
	}
	for _, field := range []string{"in_flight", "queue_depth", "total_runs", "error_count", "status_counts", "latency_histogram_ms"} {
		if _, ok := m[field]; !ok {
			t.Errorf("metrics missing field %s", field)
		}
	}

	var total int
	_ = json.Unmarshal(m["total_runs"], &total)
	if total < 1 {
		t.Errorf("total_runs = %d, want >= 1 after one run", total)
	}
	var counts map[string]int
	_ = json.Unmarshal(m["status_counts"], &counts)
	if len(counts) == 0 {
		t.Error("status_counts empty after one run")
	}
}

// TestHandleDashboard guards the embedded dashboard page: HTML that polls the
// metrics endpoint (no external assets).
func TestHandleDashboard(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	HandleDashboard(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "/metrics") {
		t.Error("dashboard page does not reference /metrics")
	}
}

// TestReadyzFullBreakdownOnSuccess locks the readiness contract: the success
// response must include the full per-component breakdown (nsjail + per
// language), not just {"status":"ok"}. Operators need the breakdown to see
// which components are healthy without a failure.
func TestReadyzFullBreakdownOnSuccess(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not installed: the success contract needs the nsjail probe to pass")
	}
	// Pin the registry to one reachable binary so the probe succeeds
	// deterministically in any environment (the real registry includes
	// languages that may not be installed on the test host).
	orig := config.DefaultRegistry
	config.DefaultRegistry = map[string]config.LanguageConfig{
		"sh": {ID: "sh", RunCmd: []string{"/bin/sh"}},
	}
	defer func() { config.DefaultRegistry = orig }()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	HandleReadyz(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()

	var body struct {
		Status    string                 `json:"status"`
		Nsjail    *readyProbe            `json:"nsjail"`
		Languages map[string]*readyProbe `json:"languages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decoding readyz: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
	if body.Nsjail == nil {
		t.Error("success response missing nsjail breakdown")
	}
	if len(body.Languages) == 0 {
		t.Error("success response missing languages breakdown")
	}
}

// TestProbeReadinessSmokeOverride locks the smoke-probe behavior: a language
// with SmokeCmd set is probed with that command, not its run/build binary.
func TestProbeReadinessSmokeOverride(t *testing.T) {
	orig := config.DefaultRegistry
	config.DefaultRegistry = map[string]config.LanguageConfig{
		"smoked": {ID: "smoked", RunCmd: []string{"/bin/false"}, SmokeCmd: []string{"/bin/echo", "smoke-version-42"}},
		"plain":  {ID: "plain", RunCmd: []string{"/bin/echo"}},
	}
	defer func() { config.DefaultRegistry = orig }()

	state := probeReadiness()
	smoked := state.Languages["smoked"]
	if smoked == nil || !smoked.OK {
		t.Fatalf("smoked probe not OK: %+v", smoked)
	}
	if smoked.Version != "smoke-version-42" {
		t.Errorf("smoked version = %q, want smoke-version-42 (SmokeCmd must be used)", smoked.Version)
	}
	if p := state.Languages["plain"]; p == nil || !p.OK {
		t.Errorf("plain probe should still work: %+v", p)
	}
}

// TestClassifyAcquireErr locks the shutdown-vs-cancel branch order: a dead
// client context beats errShuttingDown, so a queued request whose client
// disconnected at the same moment Stop closed the broadcast records
// "cancelled" (not an error), while a live client records "shutting_down".
func TestClassifyAcquireErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status, isErr := classifyAcquireErr(ctx, errShuttingDown)
	if status != "cancelled" || isErr {
		t.Errorf("dead ctx + shutting down = (%q, %v), want (cancelled, false)", status, isErr)
	}

	status, isErr = classifyAcquireErr(context.Background(), errShuttingDown)
	if status != "shutting_down" || !isErr {
		t.Errorf("live ctx + shutting down = (%q, %v), want (shutting_down, true)", status, isErr)
	}

	status, isErr = classifyAcquireErr(context.Background(), context.Canceled)
	if status != "cancelled" || isErr {
		t.Errorf("live ctx + cancel = (%q, %v), want (cancelled, false)", status, isErr)
	}
}

// TestHandleRunShuttingDown locks the shutdown mapping: once the gate is
// stopped, POST /run answers 503 with Retry-After: 1, body code
// shutting_down, and a shutting_down entry in the live metrics. The request
// never reaches execution, so this test needs no nsjail.
func TestHandleRunShuttingDown(t *testing.T) {
	orig := gate
	g := newAdmissionGate(1, 0)
	g.Stop()
	gate = g
	defer func() { gate = orig }()

	statuses := Snapshot()["status_counts"].(map[string]int64)
	before := statuses["shutting_down"]

	body := `{"language":"py3","source":"print('hi')","tests":[{"stdin":"","expected_stdout":"hi\n"}]}`
	w := httptest.NewRecorder()
	HandleRun(w, httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body)))

	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", res.StatusCode)
	}
	if got := res.Header.Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want \"1\"", got)
	}
	var apiErr models.APIError
	if err := json.NewDecoder(res.Body).Decode(&apiErr); err != nil {
		t.Fatalf("failed to decode error body: %v", err)
	}
	if apiErr.Error.Code != "shutting_down" {
		t.Errorf("error code = %q, want shutting_down", apiErr.Error.Code)
	}
	if apiErr.Error.Message == "" {
		t.Error("shutting_down message is empty")
	}

	statuses = Snapshot()["status_counts"].(map[string]int64)
	if after := statuses["shutting_down"]; after != before+1 {
		t.Errorf("status_counts[shutting_down] = %d, want %d", after, before+1)
	}
}

// TestReadyzShuttingDown locks the readiness flip: after StartShutdown,
// readyz returns 503 with status shutting_down and healthz stays 200.
func TestReadyzShuttingDown(t *testing.T) {
	StartShutdown()
	t.Cleanup(func() { shuttingDown.Store(false) })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	HandleReadyz(w, req)

	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz status = %d, want 503", res.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decoding readyz: %v", err)
	}
	if body.Status != "shutting_down" {
		t.Errorf("readyz body status = %q, want shutting_down", body.Status)
	}

	hw := httptest.NewRecorder()
	HandleHealthz(hw, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if hw.Code != http.StatusOK {
		t.Errorf("healthz during shutdown = %d, want 200", hw.Code)
	}
}
