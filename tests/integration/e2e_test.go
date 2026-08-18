package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/nithitsuki/goboxd/internal/models"
)

func sendRun(t *testing.T, req models.RunRequest) models.RunResponse {
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(getAPIURL()+"/run", "application/json", bytes.NewBuffer(b))
	if err != nil {
		t.Fatalf("POST /run failed: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("closing response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		var errData models.APIError
		if err := json.NewDecoder(resp.Body).Decode(&errData); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, errData.Error.Message)
	}

	var res models.RunResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return res
}

func TestE2E_Python3(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		stdin          string
		expectedStdout string
		expectedStatus models.ResultStatus
	}{
		{
			name:           "positive-basic",
			source:         "print(\"Hello from Python 3!\")",
			stdin:          "",
			expectedStdout: "Hello from Python 3!\n",
			expectedStatus: models.ResultAccepted,
		},
		{
			name:           "positive-advanced",
			source:         "print(\"Python 3 advanced test\")\nimport sys\ndata = list(range(100))\nprint(f\"Processed {len(data)} items\")",
			stdin:          "",
			expectedStdout: "Python 3 advanced test\nProcessed 100 items\n",
			expectedStatus: models.ResultAccepted,
		},
		{
			name:           "positive-io",
			source:         "n = int(input())\nfor i in range(n):\n    print(f\"Line {i+1}\")",
			stdin:          "3\n",
			expectedStdout: "Line 1\nLine 2\nLine 3\n",
			expectedStatus: models.ResultAccepted,
		},
		{
			name:           "memorylimit-high",
			source:         "data = [1, 2, 3, 4, 5] * 1000\nprint(\"Memory test completed\")",
			stdin:          "",
			expectedStdout: "Memory test completed\n",
			expectedStatus: models.ResultAccepted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := models.RunRequest{
				Language: "py3",
				Source:   tt.source,
				Tests: []models.TestCase{
					{
						Stdin:          tt.stdin,
						ExpectedStdout: tt.expectedStdout,
					},
				},
			}

			res := sendRun(t, req)

			if len(res.Tests) != 1 {
				t.Fatalf("expected 1 test result, got %d", len(res.Tests))
			}

			testRes := res.Tests[0]
			if testRes.Status != tt.expectedStatus {
				t.Errorf("expected status %s, got %s. Stderr: %q", tt.expectedStatus, testRes.Status, testRes.Stderr)
			}
			if testRes.Stdout != tt.expectedStdout {
				t.Errorf("expected stdout %q, got %q", tt.expectedStdout, testRes.Stdout)
			}

			if strings.Contains(testRes.Stderr, "UID/EUID") {
				t.Errorf("expected nsjail warnings to be silenced, but got leakage in stderr: %q", testRes.Stderr)
			}
		})
	}
}

// TestE2E_SeccompMountBlocked verifies the seccomp policy is actually loaded in
// every jail: mount() must be denied (SECCOMP_RET_KILL -> SIGSYS -> runtime_error).
// Without the policy the jailed process either mounts successfully or fails with
// EPERM and exits normally, so this test fails until --seccomp_policy is wired.
func TestE2E_SeccompMountBlocked(t *testing.T) {
	source := `#include <sys/mount.h>
#include <stdio.h>

int main(void) {
    if (mount(NULL, "/tmp", "tmpfs", 0, NULL) == -1) {
        perror("mount");
        return 0;
    }
    printf("mount succeeded\n");
    return 0;
}`
	req := models.RunRequest{
		Language: "c",
		Source:   source,
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: ""},
		},
	}

	res := sendRun(t, req)

	if res.Build.Status != models.BuildOk {
		t.Fatalf("build failed: status=%s stderr=%q", res.Build.Status, res.Build.Stderr)
	}
	if len(res.Tests) != 1 {
		t.Fatalf("expected 1 test result, got %d", len(res.Tests))
	}

	got := res.Tests[0]
	if got.Status != models.ResultRuntimeError {
		t.Errorf("expected runtime_error (SIGSYS from seccomp), got %q (stdout=%q stderr=%q)",
			got.Status, got.Stdout, got.Stderr)
	}
}

// TestE2E_MultiUID verifies every jail runs as its own unprivileged uid:
// getuid() inside the jail must be non-zero, and two concurrent jails must get
// two different uids. Before multi-uid, every jail ran as uid 0, so this test
// is red until the uid pool is wired into the runner.
func TestE2E_MultiUID(t *testing.T) {
	source := `#include <stdio.h>
#include <unistd.h>

int main(void) {
    printf("%d", (int)getuid());
    return 0;
}`
	run := func() (int, error) {
		req := models.RunRequest{
			Language: "c",
			Source:   source,
			Tests: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
		}
		res := sendRun(t, req)
		if res.Build.Status != models.BuildOk {
			return 0, fmt.Errorf("build failed: status=%s stderr=%q", res.Build.Status, res.Build.Stderr)
		}
		if len(res.Tests) != 1 {
			return 0, fmt.Errorf("expected 1 test result, got %d", len(res.Tests))
		}
		out := strings.TrimSpace(res.Tests[0].Stdout)
		if out == "" {
			return 0, fmt.Errorf("empty stdout, status=%s stderr=%q", res.Tests[0].Status, res.Tests[0].Stderr)
		}
		var uid int
		if _, err := fmt.Sscanf(out, "%d", &uid); err != nil {
			return 0, fmt.Errorf("stdout %q is not an integer uid: %v", out, err)
		}
		return uid, nil
	}

	t.Run("non-zero uid", func(t *testing.T) {
		uid, err := run()
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if uid == 0 {
			t.Errorf("jail ran as uid 0; expected an unprivileged non-zero uid")
		}
	})

	t.Run("distinct uids under concurrency", func(t *testing.T) {
		const n = 2
		uids := make([]int, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				uids[i], errs[i] = run()
			}(i)
		}
		wg.Wait()
		for i := 0; i < n; i++ {
			if errs[i] != nil {
				t.Fatalf("concurrent run %d: %v", i, errs[i])
			}
		}
		if uids[0] == uids[1] {
			t.Errorf("two concurrent jails shared uid %d", uids[0])
		}
	})
}

// TestE2E_CgroupMemory verifies per-jail memory.peak reporting when cgroup v2
// is active: a program that touches ~30MB (under the 64MB limit) must run to
// completion and report a nonzero per-jail peak from the cgroup, not the
// global RUSAGE_CHILDREN fallback. When cgroup v2 is inactive the server
// falls back to rlimits and this test skips (documented behavior; the rlimit
// path is covered by the fixture suite).
//
// The cgroup OOM-kill path (memory_exceeded) is not deterministically
// triggerable: the RLIMIT_AS guard is TIGHT (equal to the memory limit), so an
// over-limit allocation fails at mmap time before the cgroup can OOM. The
// memory_exceeded classification is covered by the unit test
// TestComputeTestStatusOOMKilled.
func TestE2E_CgroupMemory(t *testing.T) {
	var info map[string]interface{}
	{
		resp, err := http.Get(getAPIURL() + "/info")
		if err != nil {
			t.Fatalf("GET /info: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			t.Fatalf("decoding /info: %v", err)
		}
	}
	cg, ok := info["cgroupv2"].(map[string]interface{})
	if !ok {
		t.Fatal("server /info is missing the cgroupv2 field; expected {active bool, mount string}")
	}
	active, _ := cg["active"].(bool)
	if !active {
		t.Skip("cgroup v2 inactive on server (rlimit fallback); skipping cgroup enforcement test")
	}

	// Touch 30MB with a 64MB limit: comfortably inside the tight RLIMIT_AS
	// guard, so the program runs; the per-jail cgroup must still report the
	// resident peak.
	limitKB := 65536
	source := `#include <stdlib.h>
#include <string.h>
#include <stdio.h>

int main(void) {
    size_t sz = 30UL * 1024 * 1024;
    char *p = malloc(sz);
    if (!p) { printf("malloc failed\n"); return 0; }
    memset(p, 1, sz);
    printf("allocated\n");
    return 0;
}`
	req := models.RunRequest{
		Language: "c",
		Source:   source,
		Run: &models.StageConfig{
			Limits: &models.Limits{MemoryKB: &limitKB},
		},
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: ""},
		},
	}
	res := sendRun(t, req)
	if res.Build.Status != models.BuildOk {
		t.Fatalf("build failed: status=%s stderr=%q", res.Build.Status, res.Build.Stderr)
	}
	if len(res.Tests) != 1 {
		t.Fatalf("expected 1 test result, got %d", len(res.Tests))
	}
	got := res.Tests[0]
	if got.Status != models.ResultAccepted {
		t.Errorf("expected accepted (in-limit run), got %q (stdout=%q stderr=%q)",
			got.Status, got.Stdout, got.Stderr)
	}
	if got.MemoryPeakKB <= 0 {
		t.Errorf("expected MemoryPeakKB > 0 from per-jail cgroup, got %d", got.MemoryPeakKB)
	}
}

// TestE2E_CgroupFallbackEnforced verifies the "limits are NEVER unenforced"
// contract on the rlimit path: when cgroup v2 is inactive, the TIGHT RLIMIT_AS
// guard (equal to the memory limit, 64MB here) must reject a grossly over-limit
// allocation. 12GB exceeds the guard, so mmap fails and malloc returns NULL.
// Without any guard the program would print "malloc ok".
func TestE2E_CgroupFallbackEnforced(t *testing.T) {
	var info map[string]interface{}
	{
		resp, err := http.Get(getAPIURL() + "/info")
		if err != nil {
			t.Fatalf("GET /info: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			t.Fatalf("decoding /info: %v", err)
		}
	}
	if cg, ok := info["cgroupv2"].(map[string]interface{}); ok {
		if active, _ := cg["active"].(bool); active {
			t.Skip("cgroup v2 active; rlimit fallback not in use")
		}
	}

	limitKB := 65536 // 64MB limit; the 12GB allocation must fail the 64MB guard
	source := `#include <stdlib.h>
#include <stdio.h>

int main(void) {
    size_t sz = 12UL * 1024 * 1024 * 1024;
    char *p = malloc(sz);
    if (!p) { printf("malloc failed\n"); return 0; }
    printf("malloc ok\n");
    return 0;
}`
	req := models.RunRequest{
		Language: "c",
		Source:   source,
		Run: &models.StageConfig{
			Limits: &models.Limits{MemoryKB: &limitKB},
		},
		Tests: []models.TestCase{
			{Stdin: "", ExpectedStdout: ""},
		},
	}
	res := sendRun(t, req)
	if res.Build.Status != models.BuildOk {
		t.Fatalf("build failed: status=%s stderr=%q", res.Build.Status, res.Build.Stderr)
	}
	if len(res.Tests) != 1 {
		t.Fatalf("expected 1 test result, got %d", len(res.Tests))
	}
	got := res.Tests[0]
	if got.Status == models.ResultMemoryExceeded {
		t.Error("memory_exceeded without cgroup v2 active: status classification depends on cgroup polling")
	}
	if !strings.Contains(got.Stdout, "malloc failed") {
		t.Errorf("expected rlimit to reject the over-limit malloc (stdout=%q stderr=%q), got status=%q",
			got.Stdout, got.Stderr, got.Status)
	}
}
