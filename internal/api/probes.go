package api

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nithitsuki/goboxd/internal/config"
)

// This file is the probe module: it owns every readiness/version probe and a
// shared TTL cache so /readyz and /info serve warm probes with zero subprocess
// spawns. The cache can go stale by at most one TTL; readiness remains a
// genuine health signal (a stale entry re-probes on the next miss).

// probeCache is a mutex-protected time-to-live cache of readyProbe values.
type probeCache struct {
	mu      sync.Mutex
	entries map[string]cachedProbe
	ttl     time.Duration
}

// cachedProbe is one cache entry: the computed probe and when it was stored.
type cachedProbe struct {
	at time.Time
	p  *readyProbe
}

func newProbeCache(ttl time.Duration) *probeCache {
	return &probeCache{entries: make(map[string]cachedProbe), ttl: ttl}
}

// run returns the cached probe for key if it is younger than the TTL,
// otherwise it calls fn, stores the result, and returns it. A stale or absent
// entry re-runs fn; within a TTL a warm cache does zero fn calls (and thus
// zero subprocess spawns, since the real probes only exec inside fn).
func (c *probeCache) run(key string, fn func() *readyProbe) *readyProbe {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && time.Since(e.at) < c.ttl {
		return e.p
	}
	p := fn()
	c.entries[key] = cachedProbe{at: time.Now(), p: p}
	return p
}

// reset clears every cached entry. Tests use it to get a cold cache without
// waiting out the TTL; production code never needs it.
func (c *probeCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cachedProbe)
}

// readProbeTTL returns the probe TTL from GOBOXD_PROBE_TTL (seconds, default
// 5). A zero, negative, or non-numeric value falls back to the default.
func readProbeTTL() time.Duration {
	const def = 5 * time.Second
	if raw := os.Getenv("GOBOXD_PROBE_TTL"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return def
}

// probeState is the shared, cached probe state read by both /readyz and
// /info. Both endpoints resolve each language and nsjail through cache.run,
// so a warm cache means a second call does not re-exec anything.
type probeState struct {
	cache *probeCache
	// langProbe computes a language's readyProbe from its config. It is a
	// field rather than a hardcoded function so tests can swap it to count
	// subprocess spawns or inject a deterministic result. nil selects the
	// real probeLangExec.
	langProbe func(lc config.LanguageConfig) *readyProbe
}

// probes is the package-level shared instance.
var probes = &probeState{
	cache: newProbeCache(readProbeTTL()),
}

// resetForTest clears the shared cache and restores the real language probe.
// Tests call it to get a deterministic cold cache.
func (s *probeState) resetForTest() {
	s.cache.reset()
	s.langProbe = nil
}

// nsjail returns the cached nsjail probe. keyed "nsjail", shared by /readyz
// and /info.
func (s *probeState) nsjail() *readyProbe {
	return s.cache.run("nsjail", probeNsjail)
}

// languageProbe returns the cached readyProbe for a language, computing it
// (via s.langProbe) only when the cache is cold or stale. Each language is
// cached independently under its id, so a language added by a SIGHUP reload
// is probed on its first request after the registry swap.
func (s *probeState) languageProbe(lc config.LanguageConfig) *readyProbe {
	fn := s.langProbe
	if fn == nil {
		fn = probeLangExec
	}
	return s.cache.run("lang:"+lc.ID, func() *readyProbe {
		return fn(lc)
	})
}

// probeReadiness probes nsjail and every configured language through the
// shared cache and returns the /readyz state. Shutdown skips probing entirely
// (unchanged); a warm cache does zero subprocess spawns.
func probeReadiness() readyState {
	state := readyState{
		AllOK:     true,
		Status:    "ok",
		Languages: make(map[string]*readyProbe),
	}
	if shuttingDown.Load() {
		// Shutdown in progress: no point probing runtimes. The response
		// keeps the contract (503 + full state shape) without the execs.
		state.AllOK = false
		state.Status = "shutting_down"
		return state
	}
	state.Nsjail = probes.nsjail()
	if !state.Nsjail.OK {
		state.AllOK = false
	}

	reg := config.Registry()
	for lid, lc := range reg {
		p := probes.languageProbe(lc)
		state.Languages[lid] = p
		if !p.OK {
			state.AllOK = false
		}
	}

	if !state.AllOK {
		state.Status = "degraded"
	}
	return state
}

// probeLangExec computes a language's readyProbe by running its smoke command
// (if declared) or its build/run binary with --version. This is the seeding
// work the TTL cache avoids repeating on warm requests.
func probeLangExec(lc config.LanguageConfig) *readyProbe {
	if len(lc.SmokeCmd) > 0 {
		// Explicit smoke command from the YAML (languages whose build/run
		// binary cannot answer --version).
		return probeExecArgs(lc.SmokeCmd[0], lc.SmokeCmd[1:]...)
	}
	probeCmd := lc.RunCmd[0]
	if len(lc.BuildCmd) > 0 {
		probeCmd = lc.BuildCmd[0]
	}
	return probeExec(probeCmd, "--version")
}

// nsjailVersionRe matches a version token reported by nsjail --help. Real
// nsjail --help does not print its own version, so this matches only when the
// binary (or a wrapper) does. The pattern is anchored on "nsjail" (and
// optionally "version") so we never mistake a kernel-version mention (e.g.
// "kernel version 4.14") for nsjail's own version.
var nsjailVersionRe = regexp.MustCompile(`(?i)nsjail(?:version)?[^0-9]{0,24}v?([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)

// nsjailUnknownVersion is the stable fallback reported when nsjail --help
// prints no version and GOBOXD_NSJAIL_VERSION is unset. It is intentionally
// not the previously hardcoded "3.6", which could silently drift from the
// version shipped in the image.
const nsjailUnknownVersion = "unknown"

// probeNsjail checks nsjail via --help (nsjail does not support --version)
// and discovers its version from the help text rather than hardcoding it.
func probeNsjail() *readyProbe {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nsjail", "--help").CombinedOutput()
	if err != nil {
		return &readyProbe{
			OK:    false,
			Error: fmt.Sprintf("nsjail not found or failed: %v", err),
		}
	}
	return &readyProbe{
		OK:      true,
		Version: nsjailVersionFromHelp(string(out)),
	}
}

// nsjailVersionFromHelp derives an nsjail version string from its --help
// output, falling back to GOBOXD_NSJAIL_VERSION and then a stable placeholder.
func nsjailVersionFromHelp(help string) string {
	if m := nsjailVersionRe.FindStringSubmatch(help); m != nil {
		return m[1]
	}
	if v := os.Getenv("GOBOXD_NSJAIL_VERSION"); v != "" {
		return strings.TrimSpace(v)
	}
	return nsjailUnknownVersion
}

func probeExec(binary, arg string) *readyProbe {
	return probeExecArgs(binary, arg)
}

func probeExecArgs(binary string, args ...string) *readyProbe {
	// Bound the probe: a hung runtime binary must not wedge the cache
	// mutex and stall every /readyz and /info process-wide.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, args...).Output()
	if err == nil {
		return &readyProbe{
			OK:      true,
			Version: strings.TrimSpace(string(out)),
		}
	}
	// The command failed, try just confirming the binary exists
	if path, lookupErr := exec.LookPath(binary); lookupErr == nil {
		return &readyProbe{
			OK:      true,
			Version: path,
		}
	}
	return &readyProbe{
		OK:    false,
		Error: fmt.Sprintf("%s not found: %v", binary, err),
	}
}
