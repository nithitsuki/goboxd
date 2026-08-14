package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	// Skip fixtures for languages the server does not advertise (e.g. removed
	// via GOBOXD_EXCLUDE_LANGS). Posting them would fail with a 400 and bury
	// real regressions under noise.
	advertised, err := advertisedLanguages(baseURL)
	if err != nil {
		t.Fatalf("fetching advertised languages: %v", err)
	}

	for _, f := range fixtures {
		if !advertised[f.Lang] {
			t.Run(f.Name, func(t *testing.T) {
				t.Skipf("language %q not advertised by the server (registry filtered)", f.Lang)
			})
			continue
		}
		if *flagLang != "" && f.Lang != *flagLang {
			continue
		}
		if *flagCase != "" && filepath.Base(f.Name) != *flagCase {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			runFixture(t, baseURL, f)
		})
	}
}

// advertisedLanguages returns the set of language ids the server serves,
// from GET /info.
func advertisedLanguages(baseURL string) (map[string]bool, error) {
	resp, err := http.Get(baseURL + "/info")
	if err != nil {
		return nil, fmt.Errorf("GET /info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var info struct {
		Languages []struct {
			ID string `json:"id"`
		} `json:"languages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding /info: %w", err)
	}
	set := make(map[string]bool, len(info.Languages))
	for _, l := range info.Languages {
		set[l.ID] = true
	}
	return set, nil
}

// TestFixtureNamesUnique guards the subtest naming scheme. Every fixture name
// must be "<lang>/<name>" and unique, so a failing subtest maps directly to a
// language without relying on Go's opaque #N collision suffixes.
func TestFixtureNamesUnique(t *testing.T) {
	fixtures, err := discoverFixtures(filepath.Join("..", "testcases"))
	if err != nil {
		t.Fatalf("discovering fixtures: %v", err)
	}

	seen := make(map[string]bool, len(fixtures))
	for _, f := range fixtures {
		if !strings.Contains(f.Name, "/") {
			t.Errorf("fixture name %q has no language prefix", f.Name)
		}
		if seen[f.Name] {
			t.Errorf("duplicate fixture name %q", f.Name)
		}
		seen[f.Name] = true
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
