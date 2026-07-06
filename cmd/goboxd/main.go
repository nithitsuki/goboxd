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

	"github.com/thesouldev/goboxd/internal/api"
	"github.com/thesouldev/goboxd/internal/config"
	"github.com/thesouldev/goboxd/internal/runner"
)

func main() {
	// Load language registry from YAML (config/languages.yml)
	if err := config.LoadRegistry(); err != nil {
		log.Fatalf("Loading language registry: %v", err)
	}

	// Sweep orphan jail dirs older than 30 minutes from previous runs.
	// This is a startup safety net (see Security Hole #7).
	runner.SweepOrphans()

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
