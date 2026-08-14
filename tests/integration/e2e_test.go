package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/thesouldev/goboxd/internal/models"
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
		expectedStatus string
	}{
		{
			name:           "positive-basic",
			source:         "print(\"Hello from Python 3!\")",
			stdin:          "",
			expectedStdout: "Hello from Python 3!\n",
			expectedStatus: "accepted",
		},
		{
			name:           "positive-advanced",
			source:         "print(\"Python 3 advanced test\")\nimport sys\ndata = list(range(100))\nprint(f\"Processed {len(data)} items\")",
			stdin:          "",
			expectedStdout: "Python 3 advanced test\nProcessed 100 items\n",
			expectedStatus: "accepted",
		},
		{
			name:           "positive-io",
			source:         "n = int(input())\nfor i in range(n):\n    print(f\"Line {i+1}\")",
			stdin:          "3\n",
			expectedStdout: "Line 1\nLine 2\nLine 3\n",
			expectedStatus: "accepted",
		},
		{
			name:           "memorylimit-high",
			source:         "data = [1, 2, 3, 4, 5] * 1000\nprint(\"Memory test completed\")",
			stdin:          "",
			expectedStdout: "Memory test completed\n",
			expectedStatus: "accepted",
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

	if res.Build.Status != "ok" {
		t.Fatalf("build failed: status=%s stderr=%q", res.Build.Status, res.Build.Stderr)
	}
	if len(res.Tests) != 1 {
		t.Fatalf("expected 1 test result, got %d", len(res.Tests))
	}

	got := res.Tests[0]
	if got.Status != "runtime_error" {
		t.Errorf("expected runtime_error (SIGSYS from seccomp), got %q (stdout=%q stderr=%q)",
			got.Status, got.Stdout, got.Stderr)
	}
}
