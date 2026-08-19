package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nithitsuki/goboxd/internal/config"
)

// TestProbeCacheTTL locks the cache semantics: a probe fn is called once for
// two run() calls within the TTL (cache hit), and again after the TTL elapses
// (miss). The controllable TTL is what makes the behavior unit-testable
// without waiting on the real 5s default.
func TestProbeCacheTTL(t *testing.T) {
	c := newProbeCache(50 * time.Millisecond)
	calls := 0
	fn := func() *readyProbe {
		calls++
		return &readyProbe{OK: true, Version: "v1"}
	}

	if got := c.run("k", fn); !got.OK {
		t.Fatalf("first run not OK: %+v", got)
	}
	if calls != 1 {
		t.Fatalf("first run called fn %d times, want 1", calls)
	}

	// Within the TTL: cache hit, fn must not re-run.
	if got := c.run("k", fn); !got.OK {
		t.Fatalf("cached run not OK: %+v", got)
	}
	if calls != 1 {
		t.Errorf("within TTL fn called %d times, want 1 (cache hit)", calls)
	}

	// Past the TTL: cache miss, fn runs again.
	time.Sleep(80 * time.Millisecond)
	if got := c.run("k", fn); !got.OK {
		t.Fatalf("expired run not OK: %+v", got)
	}
	if calls != 2 {
		t.Errorf("past TTL fn called %d times, want 2", calls)
	}
}

// TestReadyzCacheWarm locks the module-level warm path: a warm probeReadiness
// (second call within the TTL) must reach the cached per-language probe
// instead of re-running the underlying exec. The langProbe seam counts spawns.
func TestReadyzCacheWarm(t *testing.T) {
	orig := config.Registry()
	config.SetRegistryForTest(map[string]config.LanguageConfig{
		"sh": {ID: "sh", RunCmd: []string{"/bin/sh"}},
	})
	defer func() { config.SetRegistryForTest(orig) }()

	probes.resetForTest()
	calls := 0
	probes.langProbe = func(lc config.LanguageConfig) *readyProbe {
		calls++
		return &readyProbe{OK: true, Version: "warm"}
	}
	defer func() { probes.langProbe = nil }()

	if st := probeReadiness(); len(st.Languages) != 1 {
		t.Fatalf("readiness languages = %d, want 1", len(st.Languages))
	}
	if calls != 1 {
		t.Fatalf("first readiness spawned %d language probes, want 1", calls)
	}

	// Warm second call within the TTL: zero additional spawns.
	if st := probeReadiness(); st.Languages["sh"] == nil {
		t.Fatal("warm readiness missing language breakdown")
	}
	if calls != 1 {
		t.Errorf("warm readiness spawned %d language probes, want 1 (cache warm)", calls)
	}
}

// TestNsjailVersionDiscovered locks nsjail version discovery: the version must
// be derived from the nsjail binary's --help output, not hardcoded "3.6". A
// fake nsjail on PATH whose --help prints a distinctive version proves the
// discovery path.
func TestNsjailVersionDiscovered(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "nsjail")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'nsjail version 9.8.7'\n"), 0o755); err != nil {
		t.Fatalf("writing fake nsjail: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := probeNsjail()
	if !p.OK {
		t.Fatalf("fake nsjail probe should pass: %+v", p)
	}
	if p.Version == "" {
		t.Fatal("nsjail version is empty; must be derived from the binary")
	}
	if p.Version == "3.6" {
		t.Fatal("nsjail version is the old hardcoded sentinel; must be discovered, not hardcoded")
	}
	if p.Version != "9.8.7" {
		t.Errorf("nsjail version = %q, want 9.8.7 (derived from fake --help)", p.Version)
	}
}

// TestReadyzWarmCacheNoSpawn is the handler-level warm test: two /readyz calls
// within one TTL and the second must spawn zero language subprocesses.
func TestReadyzWarmCacheNoSpawn(t *testing.T) {
	orig := config.Registry()
	config.SetRegistryForTest(map[string]config.LanguageConfig{
		"warm": {ID: "warm", RunCmd: []string{"/bin/echo"}},
	})
	defer func() { config.SetRegistryForTest(orig) }()

	probes.resetForTest()
	calls := 0
	probes.langProbe = func(lc config.LanguageConfig) *readyProbe {
		calls++
		return &readyProbe{OK: true, Version: "warm"}
	}
	defer func() { probes.langProbe = nil }()

	do := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		HandleReadyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return w
	}

	do()
	if calls != 1 {
		t.Fatalf("first /readyz spawned %d language probes, want 1", calls)
	}
	do() // warm: within TTL
	if calls != 1 {
		t.Errorf("warm /readyz spawned %d language probes, want 0 additional (cache warm)", calls)
	}
}

// TestInfoSharesReadyzCache locks C4 AC.3: a warm /readyz and a subsequent
// /info within the same TTL share the probe cache, so the second endpoint
// spawns zero additional language probes.
func TestInfoSharesReadyzCache(t *testing.T) {
	orig := config.Registry()
	config.SetRegistryForTest(map[string]config.LanguageConfig{
		"sh": {ID: "sh", RunCmd: []string{"/bin/sh"}},
	})
	defer func() { config.SetRegistryForTest(orig) }()

	probes.resetForTest()
	calls := 0
	probes.langProbe = func(lc config.LanguageConfig) *readyProbe {
		calls++
		return &readyProbe{OK: true, Version: "shared"}
	}
	defer func() { probes.langProbe = nil }()

	// Cold /readyz spawns the one language probe.
	HandleReadyz(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if calls != 1 {
		t.Fatalf("cold /readyz spawned %d language probes, want 1", calls)
	}

	// Warm /info within the TTL must reuse the cache: zero additional spawns.
	HandleInfo(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/info", nil))
	if calls != 1 {
		t.Errorf("/info after warm /readyz spawned %d language probes, want 0 additional (shared cache)", calls)
	}
}
