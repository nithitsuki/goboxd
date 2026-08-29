package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAuthMiddleware exercises the bearer-token and Origin gate on POST /run
// with a stub backend so the test never touches nsjail. The full chain
// (NewRouter) is covered by the tunnel verification after deployment.
func TestAuthMiddleware(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mk := func() http.Handler { return AuthMiddleware(stub) }
	post := func() *http.Request { return httptest.NewRequest(http.MethodPost, "/run", nil) }

	t.Run("run_requires_valid_token", func(t *testing.T) {
		t.Setenv("GOBOXD_AUTH_TOKEN", "sekrit")
		t.Setenv("GOBOXD_ALLOWED_ORIGINS", "")

		for name, hdr := range map[string]string{
			"no_header":    "",
			"wrong_token":  "Bearer wrong",
			"malformed":    "sekrit",
		} {
			t.Run(name, func(t *testing.T) {
				req := post()
				if hdr != "" {
					req.Header.Set("Authorization", hdr)
				}
				w := httptest.NewRecorder()
				mk().ServeHTTP(w, req)
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
				}
				if !strings.Contains(w.Body.String(), "unauthorized") {
					t.Fatalf("body = %q, want unauthorized error", w.Body.String())
				}
			})
		}
	})

	t.Run("run_accepts_correct_token_server_to_server", func(t *testing.T) {
		t.Setenv("GOBOXD_AUTH_TOKEN", "sekrit")
		t.Setenv("GOBOXD_ALLOWED_ORIGINS", "")
		req := post()
		req.Header.Set("Authorization", "Bearer sekrit")
		w := httptest.NewRecorder()
		mk().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("browser_origin_rejected_when_allowlist_empty", func(t *testing.T) {
		t.Setenv("GOBOXD_AUTH_TOKEN", "sekrit")
		t.Setenv("GOBOXD_ALLOWED_ORIGINS", "")
		req := post()
		req.Header.Set("Authorization", "Bearer sekrit")
		req.Header.Set("Origin", "https://nithitsuki.com")
		w := httptest.NewRecorder()
		mk().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})

	t.Run("allowlist_accepts_listed_origin_rejects_others", func(t *testing.T) {
		t.Setenv("GOBOXD_AUTH_TOKEN", "sekrit")
		t.Setenv("GOBOXD_ALLOWED_ORIGINS", "https://nithitsuki.com, https://code-royale.example")

		req := post()
		req.Header.Set("Authorization", "Bearer sekrit")
		req.Header.Set("Origin", "https://code-royale.example")
		w := httptest.NewRecorder()
		mk().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("listed origin status = %d, want %d", w.Code, http.StatusOK)
		}

		req = post()
		req.Header.Set("Authorization", "Bearer sekrit")
		req.Header.Set("Origin", "https://evil.example")
		w = httptest.NewRecorder()
		mk().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("unlisted origin status = %d, want %d", w.Code, http.StatusForbidden)
		}
	})

	t.Run("read_only_routes_stay_open", func(t *testing.T) {
		t.Setenv("GOBOXD_AUTH_TOKEN", "sekrit")
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		mk().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("unconfigured_token_disables_gate", func(t *testing.T) {
		t.Setenv("GOBOXD_AUTH_TOKEN", "")
		t.Setenv("GOBOXD_ALLOWED_ORIGINS", "")
		w := httptest.NewRecorder()
		mk().ServeHTTP(w, post())
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}