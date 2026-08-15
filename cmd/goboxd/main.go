// Command goboxd is an HTTP service that compiles and runs untrusted code
// inside nsjail sandboxes. It accepts source code via POST /run, optionally
// compiles it, executes it against test cases, and returns per-test results.
//
// Configuration is loaded from config/languages.yml at startup.
// Orphan jail directories are cleaned up on start and again on shutdown.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

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
	srv := api.NewServer(":"+port, mux)

	// SIGTERM and SIGINT start the graceful path: stop admitting, let
	// in-flight jails finish within the drain deadline, then sweep.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Starting goboxd on :%s", port)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		// A real listen/serve failure (e.g. port in use) stays fatal.
		// ErrServerClosed cannot appear here: Shutdown/Close run only on
		// the ctx.Done branch below, and that branch owns the drain and
		// the sweep before this process exits.
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed: %v", err)
		}
		return
	case <-ctx.Done():
	}

	log.Printf("Shutdown signal received: stopping admission, draining in-flight runs")
	api.StartShutdown()
	api.StopAdmission()

	// A second signal during the drain forces the shutdown: the server
	// closes connections at once, jails die with their requests (the
	// disconnect-cancellation path), and the rest of the deadline is
	// skipped. The watcher exits when the drain finishes normally.
	forceDone := make(chan struct{})
	defer close(forceDone)
	secondSignal := make(chan os.Signal, 1)
	signal.Notify(secondSignal, syscall.SIGTERM, os.Interrupt)
	go func() {
		select {
		case sig := <-secondSignal:
			log.Printf("Second signal (%v): forcing shutdown", sig)
			_ = srv.Close()
		case <-forceDone:
		}
	}()

	drainCtx, cancel := context.WithTimeout(context.Background(), readShutdownTimeout())
	defer cancel()
	drained := true
	if err := srv.Shutdown(drainCtx); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			log.Printf("Shutdown forced by a second signal")
		} else {
			log.Printf("Graceful drain timed out: %v; closing connections (jails die with their requests)", err)
			if cerr := srv.Close(); cerr != nil && !errors.Is(cerr, http.ErrServerClosed) {
				log.Printf("Force-close failed: %v", cerr)
			}
		}
		drained = waitForDrain()
	}

	// Sweep only when every run finished. A sweep under a still-running
	// jail would remove its directory out from under nsjail.
	if drained {
		runner.SweepAllJails()
		cgroupv2.Default().Sweep()
	} else {
		log.Printf("shutdown sweep skipped: %d run(s) still in flight after the drain cap; the next startup's 30-minute orphan sweep owns them", api.GetStats().InFlight)
	}
	log.Printf("shutdown complete")
}

// waitForDrain polls the in-flight count until zero so the shutdown sweep
// never races a still-running jail. It reports false when the 5 second cap
// passes with runs still in flight; the caller then skips the sweep. A
// stuck handler cannot hold shutdown forever.
func waitForDrain() bool {
	return waitForDrainUntil(time.Now().Add(5*time.Second), 100*time.Millisecond, func() int {
		return api.GetStats().InFlight
	})
}

// waitForDrainUntil polls inFlight until it reads zero. It reports false
// when the deadline passes first.
func waitForDrainUntil(deadline time.Time, poll time.Duration, inFlight func() int) bool {
	for inFlight() > 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(poll)
	}
	return true
}

// readShutdownTimeout parses GOBOXD_SHUTDOWN_TIMEOUT in seconds (default 10).
// Invalid or non-positive values fall back to the default.
func readShutdownTimeout() time.Duration {
	n := 10
	if e := os.Getenv("GOBOXD_SHUTDOWN_TIMEOUT"); e != "" {
		if v, err := strconv.Atoi(e); err == nil && v > 0 {
			n = v
		}
	}
	return time.Duration(n) * time.Second
}
