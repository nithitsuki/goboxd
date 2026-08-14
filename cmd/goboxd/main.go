// Command goboxd is an HTTP service that compiles and runs untrusted code
// inside nsjail sandboxes. It accepts source code via POST /run, optionally
// compiles it, executes it against test cases, and returns per-test results.
//
// Configuration is loaded from config/languages.yml at startup.
// Orphan jail directories are cleaned up on start.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/nithitsuki/goboxd/internal/api"
	"github.com/nithitsuki/goboxd/internal/cgroupv2"
	"github.com/nithitsuki/goboxd/internal/config"
	"github.com/nithitsuki/goboxd/internal/runner"
)

func main() {
	// Enforcement probe support: the cgroupv2 package re-execs this binary
	// with GOBOXD_CGROUP_PROBE_HOG set to verify the memory controller really
	// charges memory before goboxd trusts the cgroup hierarchy.
	if os.Getenv("GOBOXD_CGROUP_PROBE_HOG") == "1" {
		cgroupv2.ProbeHog()
		return
	}

	// Load language registry from YAML (config/languages.yml)
	if err := config.LoadRegistry(); err != nil {
		log.Fatalf("Loading language registry: %v", err)
	}

	// Sweep orphan jail dirs older than 30 minutes from previous runs.
	// This is a startup safety net (see Security Hole #7).
	runner.SweepOrphans()

	// Probe cgroup v2 availability (logs active/inactive; the runner falls
	// back to rlimits when inactive). Must run before the server accepts
	// requests so /info and the runner agree on the state.
	cgroupv2.Default()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := api.NewRouter()

	log.Printf("Starting goboxd on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
