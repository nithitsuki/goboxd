package integration

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nithitsuki/goboxd/internal/models"
)

// The shutdown tests spawn their own server: killing the shared harness
// server would break the rest of the suite. The harness builds the binary
// at bin/goboxd-test in TestMain, so these tests reuse it.

// requireRootAndNsjail skips when the sandbox cannot run on this host.
func requireRootAndNsjail(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found, skipping shutdown test")
	}
}

// freePort reserves an ephemeral port for the spawned server.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving free port: %v", err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing port probe: %v", err)
	}
	return port
}

// buildServerEnv returns the parent environment with the override keys
// replaced (duplicate keys would shadow nothing: getenv finds the first).
func buildServerEnv(overrides map[string]string) []string {
	skip := make(map[string]bool, len(overrides))
	for k := range overrides {
		skip[k] = true
	}
	var env []string
	for _, e := range os.Environ() {
		if k := strings.SplitN(e, "=", 2)[0]; skip[k] {
			continue
		}
		env = append(env, e)
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

// spawnedServer is a dedicated goboxd process for one shutdown test.
type spawnedServer struct {
	cmd  *exec.Cmd
	url  string
	port string
}

// requirePy3 skips when the spawned server does not advertise py3. The
// shutdown tests hardcode py3 runs. A host whose GOBOXD_LANGS excludes py3
// must skip instead of fail.
func requirePy3(t *testing.T, url string) {
	t.Helper()
	var info struct {
		Languages []struct {
			ID string `json:"id"`
		} `json:"languages"`
	}
	resp, err := http.Get(url + "/info")
	if err != nil {
		t.Fatalf("GET /info: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decoding /info: %v", err)
	}
	for _, l := range info.Languages {
		if l.ID == "py3" {
			return
		}
	}
	t.Skip("spawned server does not advertise py3 (GOBOXD_LANGS may exclude it)")
}

// spawnShutdownServer starts the harness-built binary on a free port with
// the given env overrides and waits for /healthz.
func spawnShutdownServer(t *testing.T, overrides map[string]string) *spawnedServer {
	t.Helper()
	if os.Getenv("API_URL") != "" {
		t.Skip("shutdown tests spawn their own server; API_URL mode unsupported")
	}

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	binary := filepath.Join(dir, "..", "..", "bin", "goboxd-test")
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("server binary not built: %v (TestMain builds bin/goboxd-test)", err)
	}

	port := freePort(t)
	cmd := exec.Command(binary)
	// The registry path (config/languages.yml) is relative to the repo
	// root, so the server must start there (the test binary's cwd is the
	// package dir).
	cmd.Dir = filepath.Join(dir, "..", "..")
	envOverrides := map[string]string{"PORT": port}
	for k, v := range overrides {
		envOverrides[k] = v
	}
	cmd.Env = buildServerEnv(envOverrides)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting goboxd: %v", err)
	}
	s := &spawnedServer{cmd: cmd, url: "http://localhost:" + port, port: port}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(s.url + "/healthz")
		if err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			requirePy3(t, s.url)
			return s
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("spawned server at %s never became healthy", s.url)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// signalAndWait sends SIGTERM and waits for a clean exit (code 0). Returns
// the time from the signal to the exit.
func (s *spawnedServer) signalAndWait(t *testing.T, maxWait time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}
	exitCh := make(chan error, 1)
	go func() { exitCh <- s.cmd.Wait() }()
	select {
	case err := <-exitCh:
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("server exited with error: %v (after %s of SIGTERM)", err, elapsed)
		}
		return elapsed
	case <-time.After(maxWait):
		_ = s.cmd.Process.Kill()
		t.Fatalf("server did not exit within %s of SIGTERM", maxWait)
		return 0
	}
}

// waitInFlight polls /metrics until at least n runs are in flight.
func waitInFlight(t *testing.T, url string, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var m struct {
			InFlight int `json:"in_flight"`
		}
		resp, err := http.Get(url + "/metrics")
		if err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&m)
			_ = resp.Body.Close()
			if m.InFlight >= n {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("in_flight never reached %d within %s", n, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// serverCgroup reports whether cgroup v2 is active on the spawned server and
// its mount root (the jail dirs live under <root>/goboxd).
func serverCgroup(t *testing.T, url string) (bool, string) {
	t.Helper()
	var info struct {
		Cgroupv2 struct {
			Active bool   `json:"active"`
			Mount  string `json:"mount"`
		} `json:"cgroupv2"`
	}
	resp, err := http.Get(url + "/info")
	if err != nil {
		t.Fatalf("GET /info: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decoding /info: %v", err)
	}
	return info.Cgroupv2.Active, info.Cgroupv2.Mount
}

// TestShutdownSecondSignal verifies the force path: a second SIGTERM during
// the drain skips the rest of the drain deadline. The server closes
// connections at once, the P0-1 path kills the jail, and the process exits 0
// well before the 10s drain deadline with no leftovers.
func TestShutdownSecondSignal(t *testing.T) {
	requireRootAndNsjail(t)

	s := spawnShutdownServer(t, map[string]string{"GOBOXD_SHUTDOWN_TIMEOUT": "10"})
	cgActive, cgMount := serverCgroup(t, s.url)

	wall := 9 // py3 registry run max (config/languages.yml)
	req := models.RunRequest{
		Language: "py3",
		Source:   "while True:\n    pass\n",
		Run: &models.StageConfig{
			Limits: &models.Limits{WallTimeS: &wall},
		},
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: ""},
		},
	}
	client := &http.Client{Timeout: 30 * time.Second}
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		b, _ := json.Marshal(req)
		resp, err := client.Post(s.url+"/run", "application/json", bytes.NewBuffer(b))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	waitInFlight(t, s.url, 1, 15*time.Second)

	start := time.Now()
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending first SIGTERM: %v", err)
	}
	// Let the graceful path start (admission stops, drain begins), then
	// force it with a second signal.
	time.Sleep(300 * time.Millisecond)
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending second SIGTERM: %v", err)
	}

	exitCh := make(chan error, 1)
	go func() { exitCh <- s.cmd.Wait() }()
	select {
	case err := <-exitCh:
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("server exited with error: %v", err)
		}
		if elapsed > 8*time.Second {
			t.Errorf("second-signal shutdown took %s, want well under the 10s drain deadline", elapsed)
		}
	case <-time.After(20 * time.Second):
		_ = s.cmd.Process.Kill()
		t.Fatal("server did not exit after the second signal")
	}

	select {
	case <-clientDone:
	case <-time.After(15 * time.Second):
		t.Fatal("client POST never returned after forced shutdown")
	}

	assertNoJailLeftovers(t, cgMount, cgActive)
}

// assertNoJailLeftovers fails the test on any surviving jail dir, cgroup
// jail dir, or nsjail process.
func assertNoJailLeftovers(t *testing.T, cgroupRoot string, cgroupActive bool) {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "goboxd-jail-") {
			t.Errorf("leftover jail dir in temp dir: %s", e.Name())
		}
	}
	if out, _ := exec.Command("pgrep", "-x", "nsjail").CombinedOutput(); len(bytes.TrimSpace(out)) > 0 {
		t.Errorf("nsjail processes still running after shutdown:\n%s", out)
	}
	if cgroupActive {
		base := filepath.Join(cgroupRoot, "goboxd")
		if ents, err := os.ReadDir(base); err == nil {
			for _, e := range ents {
				if e.IsDir() {
					t.Errorf("leftover cgroup jail dir: %s", filepath.Join(base, e.Name()))
				}
			}
		}
	}
}

// busyRunRequest builds a py3 busy loop that prints done after ~1s. The
// wall limit is explicit so the test never depends on the YAML default.
func busyRunRequest(wall int) models.RunRequest {
	return models.RunRequest{
		Language: "py3",
		Source:   "import time\nend = time.time() + 1.0\nwhile time.time() < end:\n    pass\nprint('done')\n",
		Run: &models.StageConfig{
			Limits: &models.Limits{WallTimeS: &wall},
		},
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: "done\n"},
		},
	}
}

// TestShutdownDrainsInFlight sends SIGTERM while a run is in flight and
// asserts the graceful path: the run completes with its result (200), the
// server exits 0, and no jail dirs, cgroup dirs, or nsjail processes remain.
func TestShutdownDrainsInFlight(t *testing.T) {
	requireRootAndNsjail(t)

	s := spawnShutdownServer(t, map[string]string{"GOBOXD_SHUTDOWN_TIMEOUT": "10"})
	cgActive, cgMount := serverCgroup(t, s.url)

	wall := 2 // under the py3 registry run max of 9
	req := busyRunRequest(wall)
	client := &http.Client{Timeout: 30 * time.Second}
	runCh := make(chan *http.Response, 1)
	runErrCh := make(chan error, 1)
	go func() {
		b, err := json.Marshal(req)
		if err != nil {
			runErrCh <- err
			return
		}
		resp, err := client.Post(s.url+"/run", "application/json", bytes.NewBuffer(b))
		if err != nil {
			runErrCh <- err
			return
		}
		runCh <- resp
	}()

	waitInFlight(t, s.url, 1, 15*time.Second)

	elapsed := s.signalAndWait(t, 20*time.Second)
	if elapsed > 10*time.Second {
		t.Errorf("graceful drain took %s, want well under the 10s drain deadline", elapsed)
	}

	// The in-flight run must have finished with its result, not been cut.
	select {
	case resp := <-runCh:
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("in-flight run got %d, want 200 (drain must finish it)", resp.StatusCode)
		}
		var res models.RunResponse
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			t.Fatalf("decoding run response: %v", err)
		}
		if res.Status != "accepted" {
			t.Errorf("run status = %q, want accepted", res.Status)
		}
	case err := <-runErrCh:
		t.Fatalf("in-flight run failed during drain: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight run never completed during drain")
	}

	assertNoJailLeftovers(t, cgMount, cgActive)
}

// TestShutdownForceClose verifies the deadline path: an infinite loop with
// the py3 registry-max wall of 9s outlives the 2s drain deadline, so the
// server force-closes connections, the P0-1 path kills the jail, and the
// process exits 0 shortly after the deadline with no leftovers.
func TestShutdownForceClose(t *testing.T) {
	requireRootAndNsjail(t)

	s := spawnShutdownServer(t, map[string]string{"GOBOXD_SHUTDOWN_TIMEOUT": "2"})
	cgActive, cgMount := serverCgroup(t, s.url)

	wall := 9 // py3 registry run max (config/languages.yml)
	req := models.RunRequest{
		Language: "py3",
		Source:   "while True:\n    pass\n",
		Run: &models.StageConfig{
			Limits: &models.Limits{WallTimeS: &wall},
		},
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: ""},
		},
	}
	client := &http.Client{Timeout: 30 * time.Second}
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		b, _ := json.Marshal(req)
		resp, err := client.Post(s.url+"/run", "application/json", bytes.NewBuffer(b))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	waitInFlight(t, s.url, 1, 15*time.Second)

	elapsed := s.signalAndWait(t, 20*time.Second)
	// The drain deadline is 2s; the kill, drain poll, and sweep add ~1-3s.
	// The broken force-close path would instead wait out the whole 9s run.
	if elapsed > 6*time.Second {
		t.Errorf("force-close drain took %s, want ~2s deadline + kill margin", elapsed)
	}
	if elapsed < 2*time.Second {
		t.Errorf("server exited %s after SIGTERM, want at least the 2s drain deadline", elapsed)
	}

	select {
	case <-clientDone:
	case <-time.After(15 * time.Second):
		t.Fatal("client POST never returned after force-close")
	}

	assertNoJailLeftovers(t, cgMount, cgActive)
}
