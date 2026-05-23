package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesouldev/goboxd/internal/models"
)

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
			body:         `{"language":"rust","source":"fn main() {}","tests":[{"stdin":"","expected_stdout":""}]}`,
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
