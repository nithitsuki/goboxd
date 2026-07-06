package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtures(t *testing.T) {
	baseURL := getAPIURL()

	fixtures, err := discoverFixtures(filepath.Join("..", "testcases"))
	if err != nil {
		t.Fatalf("discovering fixtures: %v", err)
	}

	if len(fixtures) == 0 {
		t.Fatal("no test fixtures found in tests/testcases/")
	}

	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			runFixture(t, baseURL, f)
		})
	}
}

func runFixture(t *testing.T, baseURL string, f Fixture) {
	// Build the request body as raw JSON so we preserve exactly what's in the fixture
	body, err := json.Marshal(f.Input)
	if err != nil {
		t.Fatalf("marshaling input: %v", err)
	}

	resp, err := http.Post(baseURL+"/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /run: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("closing response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		var errBody map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}
		t.Fatalf("expected 200, got %d. body: %v", resp.StatusCode, errBody)
	}

	var got struct {
		Status string          `json:"status"`
		Build  json.RawMessage `json:"build"`
		Tests  json.RawMessage `json:"tests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	// Assert top-level status
	if got.Status != f.Want.Status {
		t.Errorf("top-level status: want %q, got %q", f.Want.Status, got.Status)
	}

	// Assert build status
	var gotBuild struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(got.Build, &gotBuild); err != nil {
		t.Fatalf("decoding build: %v", err)
	}
	if gotBuild.Status != f.Want.Build.Status {
		t.Errorf("build.status: want %q, got %q", f.Want.Build.Status, gotBuild.Status)
	}

	// Assert test results
	var gotTests []struct {
		Status string `json:"status"`
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal(got.Tests, &gotTests); err != nil {
		t.Fatalf("decoding tests: %v", err)
	}

	if len(gotTests) != len(f.Want.Tests) {
		t.Fatalf("tests count: want %d, got %d", len(f.Want.Tests), len(gotTests))
	}

	for i, want := range f.Want.Tests {
		got := gotTests[i]
		if got.Status != want.Status {
			t.Errorf("tests[%d].status: want %q, got %q", i, want.Status, got.Status)
		}
		if want.Stdout != "" && got.Stdout != want.Stdout {
			t.Errorf("tests[%d].stdout: want %q, got %q", i, want.Stdout, got.Stdout)
		}
		if want.Stderr != "" && !strings.Contains(got.Stderr, want.Stderr) {
			t.Errorf("tests[%d].stderr: want substr %q, got %q", i, want.Stderr, got.Stderr)
		}

		// nsjail warning leakage check
		if strings.Contains(got.Stderr, "UID/EUID") {
			t.Errorf("tests[%d].stderr contains nsjail warning: %q", i, got.Stderr)
		}
	}
}
