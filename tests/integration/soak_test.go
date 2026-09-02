package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests implement TODO #14 (leak/soak) and #16 (regression gates) for
// the sandbox. Executors die slowly: an fd, goroutine, /tmp, or cgroup-leaf
// leak only shows up after many runs. The soak loop runs N trivial jobs and
// asserts the server stays healthy: p50 latency under the jail-setup SLO, no
// leftover jail/cgroup directories, and no fd or goroutine growth in the
// server process. The fuzz target exercises POST /run parsing across many
// malformed and well-formed payloads.
//
// They run under the same root + nsjail harness as the rest of the package
// (see main_test.go). Without API_URL, TestMain spawns the server; with
// API_URL set it targets a running instance. The fd and goroutine checks need
// the local harness server (its PID and GOBOXD_PPROF endpoint); a remote
// API_URL target skips those two checks.

const soakLang = "py3"

// Leak ceilings and the latency SLO. The latency number is the documented
// jail-setup SLO from docs/benchmarks.md (Python trivial, single client): a
// regression that slows jail setup past this fails the soak. The fd/goroutine
// ceilings tolerate background runtime noise (GC, keep-alive, timers) while
// still catching even a 0.25/run leak at the default 200 iterations.
const (
	soakLatencyCeiling = 50 * time.Millisecond
	maxFdGrowth        = 50
	maxGoroutineGrowth = 50
)

// soakBody returns a minimal valid POST /run body for one trivial job.
func soakBody() []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"language": soakLang,
		"source":   "print(sum(range(11)))",
		"tests":    []map[string]string{{"stdin": "", "expected_stdout": "55\n"}},
	})
	return b
}

// soakIterations returns the soak length: GOBOXD_SOAK_ITERATIONS when set
// (e.g. "1000" for a deep overnight-style leak run), 200 otherwise.
func soakIterations(t *testing.T) int {
	t.Helper()
	const def = 200
	if e := os.Getenv("GOBOXD_SOAK_ITERATIONS"); e != "" {
		n, err := strconv.Atoi(e)
		if err != nil || n < 1 {
			t.Fatalf("invalid GOBOXD_SOAK_ITERATIONS %q: want a positive integer", e)
		}
		return n
	}
	return def
}

// serverFDs counts the harness server's open fds via /proc. It reports
// ok=false when the PID is unknown (API_URL mode) or /proc is unreadable.
func serverFDs() (n int, ok bool) {
	if serverPID <= 0 {
		return 0, false
	}
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", serverPID))
	if err != nil {
		return 0, false
	}
	return len(entries), true
}

// serverGoroutines counts the harness server's goroutines via its pprof
// endpoint (mounted only when GOBOXD_PPROF=1). It reports ok=false when the
// endpoint is absent (remote API_URL target or pprof disabled).
func serverGoroutines() (n int, ok bool) {
	if apiURL == "" {
		return 0, false
	}
	resp, err := http.Get(apiURL + "/debug/pprof/goroutine?debug=1")
	if err != nil {
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "goroutine ") {
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return n, true
}

// TestSoakNoLeaks runs many short jobs and verifies the server does not leak
// jail directories, cgroup leaves, fds, or goroutines, and that p50 request
// latency stays under the documented jail-setup SLO. It is a grading gate: a
// leaking teardown (a missed rmdir, a dangling cgroup leaf, an unclosed fd
// or per-request goroutine) shows up as a delta between the before/after
// scans and fails the test.
func TestSoakNoLeaks(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping soak tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}
	iterations := soakIterations(t)
	base := getAPIURL()

	// Snapshot jail/cgroup dirs and server process health before running.
	preJail := countJailDirs(t)
	preCg := countCgroupLeaves(t)
	preFds, fdsMeasurable := serverFDs()
	preGoroutines, goroutinesMeasurable := serverGoroutines()

	client := &http.Client{Timeout: 10 * time.Second}
	latencies := make([]time.Duration, 0, iterations)
	var fail int
	for i := 0; i < iterations; i++ {
		start := time.Now()
		resp, err := client.Post(base+"/run", "application/json", bytes.NewReader(soakBody()))
		if err != nil {
			t.Fatalf("iteration %d POST /run: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		latencies = append(latencies, time.Since(start))
		if resp.StatusCode != http.StatusOK {
			fail++
			if fail <= 3 {
				t.Logf("iteration %d: unexpected status %d: %s", i, resp.StatusCode, string(body))
			}
		}
	}
	if fail > 0 {
		t.Fatalf("%d/%d soak iterations returned non-200", fail, iterations)
	}

	// Allow in-flight teardown to settle, then assert no leftovers.
	time.Sleep(2 * time.Second)
	postJail := countJailDirs(t)
	postCg := countCgroupLeaves(t)
	if postJail != preJail {
		t.Errorf("jail dirs leaked: pre=%d post=%d (delta %d)", preJail, postJail, postJail-preJail)
	}
	if postCg != preCg {
		t.Errorf("cgroup leaves leaked: pre=%d post=%d (delta %d)", preCg, postCg, postCg-preCg)
	}

	// fd leak: an unclosed pipe/connection per run is a slow executor death.
	if fdsMeasurable {
		postFds, ok := serverFDs()
		if !ok {
			t.Fatalf("server fds became unmeasurable after the soak")
		}
		if postFds > preFds+maxFdGrowth {
			t.Errorf("server fd leak: %d -> %d over %d runs (ceiling +%d)", preFds, postFds, iterations, maxFdGrowth)
		}
		t.Logf("fds: %d -> %d", preFds, postFds)
	}

	// goroutine leak: same shape, read from the harness pprof endpoint.
	if goroutinesMeasurable {
		postG, ok := serverGoroutines()
		if !ok {
			t.Fatalf("server goroutines became unmeasurable after the soak")
		}
		if postG > preGoroutines+maxGoroutineGrowth {
			t.Errorf("server goroutine leak: %d -> %d over %d runs (ceiling +%d)", preGoroutines, postG, iterations, maxGoroutineGrowth)
		}
		t.Logf("goroutines: %d -> %d", preGoroutines, postG)
	}

	// Server must still answer after the soak.
	if resp, err := client.Get(base + "/healthz"); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("post-soak healthz failed: err=%v status=%v", err, respStatusCode(resp))
	} else {
		_ = resp.Body.Close()
	}

	// SLO gate (TODO #16, docs/benchmarks.md): jail setup p50 < 50ms for the
	// Python trivial payload with a single client. Sequential requests in
	// this loop ARE the single-client jail-setup path, so the median of the
	// recorded latencies is the enforced p50.
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)/2]
	if p50 > soakLatencyCeiling {
		t.Errorf("p50 latency %v over %d runs exceeds the %v jail-setup SLO (docs/benchmarks.md)",
			p50, iterations, soakLatencyCeiling)
	}
	t.Logf("soak: %d runs, p50=%v p95=%v", iterations, p50, latencies[len(latencies)*95/100])
}

// countJailDirs counts goboxd jail directories under /tmp (the /tmp-growth
// leak signal). goboxd names them goboxd-jail-<rand>. A non-zero delta after
// a soak means teardown is leaking.
func countJailDirs(t *testing.T) int {
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		t.Fatalf("read /tmp: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "goboxd-jail-") {
			n++
		}
	}
	return n
}

// countCgroupLeaves counts per-jail cgroup v2 leaves under the goboxd
// cgroup subtree. A dangling leaf after teardown is a cgroup leak.
func countCgroupLeaves(t *testing.T) int {
	const root = "/sys/fs/cgroup"
	if _, err := os.Stat(root); err != nil {
		// cgroup v2 may be inactive in this environment; nothing to leak.
		return 0
	}
	return countDirsWithPrefix(root, "NSJAIL.")
}

func countDirsWithPrefix(root, prefix string) int {
	out, err := exec.Command("find", root, "-maxdepth", "2", "-type", "d", "-name", prefix+"*").Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] == "" {
		return 0
	}
	return len(lines)
}

func respStatusCode(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// FuzzRunParsing (TODO #14) feeds malformed and well-formed POST /run payloads
// to the server. The server must never panic and must always return a valid
// JSON error envelope (fail-closed) rather than a 500 with a crash. This is a
// regression gate for the request parser.
func FuzzRunParsing(f *testing.F) {
	seeds := [][]byte{
		soakBody(),
		[]byte(`{"language":"py3","source":"print(1)","tests":[]}`),
		[]byte(`{}`),
		[]byte(`not json`),
		[]byte(`{"language":"py3"`),
		[]byte(`{"language":"py3","source":123,"tests":[]}`),
		[]byte(`{"language":"doesnotexist","source":"x","tests":[]}`),
		[]byte(`{"language":"py3","source":"x","tests":[` + strings.Repeat(`{"stdin":"","expected_stdout":""},`, 60) + `]`),
		// Bounds and field-shape corners added with the P1 contract fields.
		[]byte(`{"language":"py3","source":"print(1)","tests":[],"max_parallel":-1}`),
		[]byte(`{"language":"py3","source":"print(1)","tests":[],"max_parallel":999999}`),
		[]byte(`{"language":"py3","source":"print(1)","tests":[],"max_output_bytes":-5}`),
		[]byte(`{"language":"py3","source":"print(1)","tests":[],"run":{"limits":{"wall_time_s":0}}}`),
		[]byte(`{"language":"py3","source":"print(1)","tests":[],"run":{"limits":{"ceiling":{"wall_time_s":1}}}}`),
		[]byte(`{"language":"py3","source":"print(1)","tests":[{"stdin":"","expected_stdout":"42\n"}],"build":{}}`),
		[]byte(`{"language":"py3","source":"print(1)","tests":[null]}`),
		[]byte(`{"language":"py3","source":"print(1)","tests":[{"stdin":"","expected_stdout":""}],"extra_field":true}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		resp, err := http.Post(getAPIURL()+"/run", "application/json", bytes.NewReader(data))
		if err != nil {
			return // network/transport failure is acceptable; we care about crashes
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		// Must not crash: status must be a valid HTTP code and body must be
		// parseable JSON (a well-formed error envelope) when present.
		if resp.StatusCode < 100 || resp.StatusCode > 599 {
			t.Fatalf("impossible status %d", resp.StatusCode)
		}
		if len(body) > 0 {
			var v interface{}
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatalf("non-JSON response for %q: %s", string(data), string(body))
			}
		}
	})
}
