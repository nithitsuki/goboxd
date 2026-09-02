package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPprofGated pins the GOBOXD_PPROF behavior on the router: /debug/pprof
// must be 404 by default (pprof stack dumps never ride on the public API)
// and mounted only when the env flag is set (the integration soak test uses
// it to count server goroutines across runs).
func TestPprofGated(t *testing.T) {
	t.Setenv("GOBOXD_PPROF", "")

	t.Run("off by default", func(t *testing.T) {
		router := NewRouter()
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("/debug/pprof/ without GOBOXD_PPROF = %d, want 404", rec.Code)
		}
	})

	t.Run("on with GOBOXD_PPROF=1", func(t *testing.T) {
		t.Setenv("GOBOXD_PPROF", "1")
		router := NewRouter()
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine?debug=1", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/debug/pprof/goroutine with GOBOXD_PPROF=1 = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "goroutine ") {
			t.Fatal("pprof goroutine dump missing \"goroutine \" lines")
		}
	})
}
