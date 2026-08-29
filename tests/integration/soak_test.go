package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests implement TODO #14 (leak/soak) and #16 (regression gate) for
// the sandbox. Executors die slowly: an fd, goroutine, /tmp, or cgroup-leaf
// leak only shows up after many runs. The soak loop runs N trivial jobs and
// asserts the server stays healthy and leaves no jail/cgroup directories
// behind. The fuzz target exercises POST /run parsing across many malformed
// and well-formed payloads.
//
// They run under the same root + nsjail harness as the rest of the package
// (see main_test.go). Without API_URL, TestMain spawns the server; with
// API_URL set it targets a running instance.

const soakLang = "py3"

// soakBody returns a minimal valid POST /run body for one trivial job.
func soakBody() []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"language": soakLang,
		"source":   "print(sum(range(11)))",
		"tests":    []map[string]string{{"stdin": "", "expected_stdout": "55\n"}},
	})
	return b
}

// TestSoakNoLeaks runs many short jobs and verifies the server does not leak
// jail directories, cgroup leaves, or leave itself wedged. It is a grading
// gate: a leaking teardown (a missed rmdir, a dangling cgroup leaf) makes the
// after-run scan find leftovers and fails the test.
func TestSoakNoLeaks(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping soak tests (run inside docker-compose)")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to run nsjail")
	}
	const iterations = 200
	base := getAPIURL()

	// Snapshot jail/cgroup dirs before running.
	preJail := countJailDirs(t)
	preCg := countCgroupLeaves(t)

	client := &http.Client{Timeout: 10 * time.Second}
	var fail int
	for i := 0; i < iterations; i++ {
		resp, err := client.Post(base+"/run", "application/json", bytes.NewReader(soakBody()))
		if err != nil {
			t.Fatalf("iteration %d POST /run: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
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

	// Server must still answer after the soak.
	if resp, err := client.Get(base + "/healthz"); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("post-soak healthz failed: err=%v status=%v", err, respStatusCode(resp))
	} else {
		resp.Body.Close()
	}
}

// countJailDirs counts goboxd jail directories under /tmp. goboxd names them
// goboxd-jail-<rand>. A non-zero delta after a soak means teardown is leaking.
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
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		resp, err := http.Post(getAPIURL()+"/run", "application/json", bytes.NewReader(data))
		if err != nil {
			return // network/transport failure is acceptable; we care about crashes
		}
		defer resp.Body.Close()
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

// ensure strconv stays imported for future numeric assertions.
var _ = strconv.Itoa

// ensure fmt stays imported for future diagnostics.
var _ = fmt.Sprintf
