package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestHandleRunValidation(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		expectedCode int
		errorCode    string
	}{
		{
			name:         "valid minimal request",
			body:         `{"language":"py3","source":"print(1)","tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusOK,
			errorCode:    "",
		},
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
			name:         "path traversal in filename",
			body:         `{"language":"py3","source":"print(1)","source_filename":"../etc/passwd","tests":[{"stdin":"","expected_stdout":"1\n"}]}`,
			expectedCode: http.StatusBadRequest,
			errorCode:    "invalid_filename",
		},
		{
			name:         "oversized garbage json payload",
			body:         `{"garbage": "` + strings.Repeat("A", 300*1024) + `"}`, // 300KiB string
			expectedCode: http.StatusBadRequest,
			errorCode:    "invalid_request", // Should hit maxBytes limit
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
