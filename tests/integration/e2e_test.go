package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thesouldev/goboxd/internal/models"
)

func getAPIURL() string {
	url := os.Getenv("API_URL")
	if url == "" {
		url = "http://localhost:8080"
	}
	return url
}

func waitForHealthy(t *testing.T, count int) {
	for i := 0; i < count; i++ {
		resp, err := http.Get(getAPIURL() + "/healthz")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("API never became healthy at %s", getAPIURL())
}

func sendRun(t *testing.T, req models.RunRequest) models.RunResponse {
	b, _ := json.Marshal(req)
	resp, err := http.Post(getAPIURL()+"/run", "application/json", bytes.NewBuffer(b))
	if err != nil {
		t.Fatalf("POST /run failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errData models.APIError
		json.NewDecoder(resp.Body).Decode(&errData)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, errData.Error.Message)
	}

	var res models.RunResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return res
}

func TestE2E_Python3(t *testing.T) {
	waitForHealthy(t, 5)

	tests := []struct {
		name           string
		source         string
		stdin          string
		expectedStdout string
		expectedStatus string // the test result status
	}{
		{
			name:           "positive-basic",
			source:         "print(\"Hello from Python 3!\")",
			stdin:          "",
			expectedStdout: "Hello from Python 3!\n",
			expectedStatus: "ok",
		},
		{
			name:           "positive-advanced",
			source:         "print(\"Python 3 advanced test\")\nimport sys\ndata = list(range(100))\nprint(f\"Processed {len(data)} items\")",
			stdin:          "",
			expectedStdout: "Python 3 advanced test\nProcessed 100 items\n",
			expectedStatus: "ok",
		},
		{
			name:           "positive-io",
			source:         "n = int(input())\nfor i in range(n):\n    print(f\"Line {i+1}\")",
			stdin:          "3\n",
			expectedStdout: "Line 1\nLine 2\nLine 3\n",
			expectedStatus: "ok",
		},
		{
			name:           "memorylimit-high",
			source:         "data = [1, 2, 3, 4, 5] * 1000\nprint(\"Memory test completed\")",
			stdin:          "",
			expectedStdout: "Memory test completed\n",
			expectedStatus: "ok",
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
			
			// Silenced nsjail leakage check
			if strings.Contains(testRes.Stderr, "UID/EUID=0") {
				t.Errorf("expected nsjail warnings to be silenced, but got leakage in stderr: %q", testRes.Stderr)
			}
		})
	}
}
