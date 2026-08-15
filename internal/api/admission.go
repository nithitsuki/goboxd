package api

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

// errQueueFull is returned by acquire when the in-flight slots are all taken
// and the queue is full (or disabled). HandleRun maps it to HTTP 503.
var errQueueFull = errors.New("admission queue full")

// admissionGate bounds concurrency: at most maxJobs requests in flight and
// maxQueued waiting. A queued waiter is woken by a broadcast (the channel is
// closed and replaced under the mutex, so a release can never be missed by a
// waiter that is holding the current channel).
type admissionGate struct {
	mu        sync.Mutex
	inFlight  int
	queued    int
	maxJobs   int
	maxQueued int
	broadcast chan struct{}
}

// newAdmissionGate builds a gate with n in-flight slots and m queue slots.
func newAdmissionGate(n, m int) *admissionGate {
	return &admissionGate{
		maxJobs:   n,
		maxQueued: m,
		broadcast: make(chan struct{}),
	}
}

// acquire admits the caller when inFlight < maxJobs, queues it when the
// queue has room (maxQueued > 0 and queued < maxQueued), and returns
// errQueueFull otherwise. While queued it waits for a slot release (broadcast)
// or the request context to be cancelled; a cancellation frees the queue
// ticket and returns ctx.Err().
func (g *admissionGate) acquire(ctx context.Context) error {
	g.mu.Lock()
	if g.inFlight < g.maxJobs {
		g.inFlight++
		g.mu.Unlock()
		return nil
	}
	if g.maxQueued <= 0 || g.queued >= g.maxQueued {
		g.mu.Unlock()
		return errQueueFull
	}
	g.queued++
	atomic.AddInt64(&metrics.Queued, 1)
	b := g.broadcast
	g.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			g.mu.Lock()
			g.queued--
			atomic.AddInt64(&metrics.Queued, -1)
			g.mu.Unlock()
			return ctx.Err()
		case <-b:
			g.mu.Lock()
			if g.inFlight < g.maxJobs {
				g.queued--
				atomic.AddInt64(&metrics.Queued, -1)
				g.inFlight++
				g.mu.Unlock()
				return nil
			}
			// Another waiter took the freed slot; stay queued and wait on
			// the freshly minted broadcast channel.
			b = g.broadcast
			g.mu.Unlock()
		}
	}
}

// release frees one in-flight slot. If anyone is queued, it closes the
// broadcast channel and mints a replacement under the mutex so every queued
// waiter wakes exactly once.
func (g *admissionGate) release() {
	g.mu.Lock()
	g.inFlight--
	if g.queued > 0 {
		close(g.broadcast)
		g.broadcast = make(chan struct{})
	}
	g.mu.Unlock()
}

// readMaxJobs parses GOBOXD_MAX_JOBS (default runtime.NumCPU()).
func readMaxJobs() int {
	n := runtime.NumCPU()
	if e := os.Getenv("GOBOXD_MAX_JOBS"); e != "" {
		if v, err := strconv.Atoi(e); err == nil && v > 0 {
			n = v
		}
	}
	return n
}

// readMaxQueued parses GOBOXD_MAX_QUEUED. Invalid or negative values fall
// back to readMaxJobs(); an explicit 0 disables the queue.
func readMaxQueued() int {
	e := os.Getenv("GOBOXD_MAX_QUEUED")
	if e == "" {
		return readMaxJobs()
	}
	v, err := strconv.Atoi(e)
	if err != nil || v < 0 {
		return readMaxJobs()
	}
	return v
}

// gate is the process-wide admission gate, built from the environment at
// init. Tests swap the value (never the env); no test in this package runs
// in parallel, which makes the swap safe.
var (
	gate = newAdmissionGate(readMaxJobs(), readMaxQueued())

	// maxJobs/maxQueued mirror the configured limits for /info.
	maxJobs   = gate.maxJobs
	maxQueued = gate.maxQueued
)
