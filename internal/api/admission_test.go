package api

import (
	"context"
	"testing"
	"time"
)

// waitForGate polls a gate condition with a hard deadline so a broken wakeup
// fails the test instead of hanging it.
func waitForGate(t *testing.T, g *admissionGate, timeout time.Duration, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (inFlight=%d queued=%d)", desc, g.snapInFlight(), g.snapQueued())
		}
		time.Sleep(time.Millisecond)
	}
}

func (g *admissionGate) snapInFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inFlight
}

func (g *admissionGate) snapQueued() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.queued
}

// TestAdmissionAdmitQueueReject locks the core state machine with N=1 M=1:
// the first acquire is admitted, the second queues, the third is rejected
// with errQueueFull, and release hands the slot to the queued waiter.
func TestAdmissionAdmitQueueReject(t *testing.T) {
	g := newAdmissionGate(1, 1)

	if err := g.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v, want nil", err)
	}

	done := make(chan error, 1)
	waitCtx, waitCancel := context.WithCancel(context.Background())
	t.Cleanup(waitCancel)
	go func() { done <- g.acquire(waitCtx) }()
	waitForGate(t, g, 2*time.Second, func() bool { return g.snapQueued() == 1 }, "second acquire to queue")

	if err := g.acquire(context.Background()); err != errQueueFull {
		t.Fatalf("third acquire: %v, want errQueueFull", err)
	}

	g.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued acquire: %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued acquire never woke after release")
	}

	g.release()
	if n := g.snapInFlight(); n != 0 {
		t.Errorf("inFlight after final release = %d, want 0", n)
	}
	if q := g.snapQueued(); q != 0 {
		t.Errorf("queued after final release = %d, want 0", q)
	}
}

// TestAdmissionReleaseWakesQueued proves the broadcast wakeup: a queued
// waiter must be admitted the moment the slot is released (no missed wakeup,
// no polling).
func TestAdmissionReleaseWakesQueued(t *testing.T) {
	g := newAdmissionGate(1, 1)

	if err := g.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v, want nil", err)
	}

	done := make(chan error, 1)
	waitCtx, waitCancel := context.WithCancel(context.Background())
	t.Cleanup(waitCancel)
	go func() { done <- g.acquire(waitCtx) }()
	waitForGate(t, g, 2*time.Second, func() bool { return g.snapQueued() == 1 }, "waiter to queue")

	g.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued acquire: %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued acquire was not woken by release")
	}
}

// TestAdmissionCancelWhileQueued proves the disconnect path: cancelling the
// context of a queued waiter returns ctx.Err(), decrements the queue counter,
// and frees the ticket so the next acquire can queue again.
func TestAdmissionCancelWhileQueued(t *testing.T) {
	g := newAdmissionGate(1, 1)

	if err := g.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- g.acquire(ctx) }()
	waitForGate(t, g, 2*time.Second, func() bool { return g.snapQueued() == 1 }, "waiter to queue")

	cancel()
	select {
	case err := <-done:
		if err != ctx.Err() {
			t.Fatalf("cancelled acquire = %v, want %v", err, ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled acquire never returned")
	}

	// The cancelled waiter's ticket must be gone: with the slot still held,
	// a fresh acquire must be able to queue (M=1), and a fourth must reject.
	if q := g.snapQueued(); q != 0 {
		t.Fatalf("queued after cancel = %d, want 0 (ticket not freed)", q)
	}
	requeued := make(chan error, 1)
	requeueCtx, requeueCancel := context.WithCancel(context.Background())
	t.Cleanup(requeueCancel)
	go func() { requeued <- g.acquire(requeueCtx) }()
	waitForGate(t, g, 2*time.Second, func() bool { return g.snapQueued() == 1 }, "replacement waiter to queue")

	if err := g.acquire(context.Background()); err != errQueueFull {
		t.Fatalf("acquire while queue full after cancel = %v, want errQueueFull", err)
	}

	g.release()
	select {
	case err := <-requeued:
		if err != nil {
			t.Fatalf("replacement queued acquire: %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement waiter never woke after release")
	}
}

// TestAdmissionNoQueueRejects locks the M=0 behavior: with the in-flight
// slot taken, the next acquire is rejected immediately instead of queueing.
func TestAdmissionNoQueueRejects(t *testing.T) {
	g := newAdmissionGate(1, 0)

	if err := g.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v, want nil", err)
	}
	if err := g.acquire(context.Background()); err != errQueueFull {
		t.Fatalf("second acquire with M=0 = %v, want errQueueFull", err)
	}
	if q := g.snapQueued(); q != 0 {
		t.Errorf("queued with M=0 = %d, want 0", q)
	}
}

// TestAdmissionMultipleSlots admits up to N immediately without touching the
// queue when there is spare capacity.
func TestAdmissionMultipleSlots(t *testing.T) {
	g := newAdmissionGate(2, 1)

	if err := g.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v, want nil", err)
	}
	if err := g.acquire(context.Background()); err != nil {
		t.Fatalf("second acquire: %v, want nil", err)
	}
	if q := g.snapQueued(); q != 0 {
		t.Errorf("queued with spare capacity = %d, want 0", q)
	}

	done := make(chan error, 1)
	waitCtx, waitCancel := context.WithCancel(context.Background())
	t.Cleanup(waitCancel)
	go func() { done <- g.acquire(waitCtx) }()
	waitForGate(t, g, 2*time.Second, func() bool { return g.snapQueued() == 1 }, "third acquire to queue")

	g.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued acquire: %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued acquire never woke after release")
	}
	g.release()
	g.release()
}

// TestAdmissionWakeupWhileFull proves a waiter that wakes into a still-full
// gate re-queues on the freshly minted channel instead of spinning or losing
// the wakeup.
func TestAdmissionWakeupWhileFull(t *testing.T) {
	g := newAdmissionGate(1, 2)

	if err := g.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v, want nil", err)
	}

	doneA := make(chan error, 1)
	doneB := make(chan error, 1)
	ctxA, cancelA := context.WithCancel(context.Background())
	t.Cleanup(cancelA)
	ctxB, cancelB := context.WithCancel(context.Background())
	t.Cleanup(cancelB)
	go func() { doneA <- g.acquire(ctxA) }()
	waitForGate(t, g, 2*time.Second, func() bool { return g.snapQueued() == 1 }, "waiter A to queue")
	go func() { doneB <- g.acquire(ctxB) }()
	waitForGate(t, g, 2*time.Second, func() bool { return g.snapQueued() == 2 }, "waiter B to queue")

	// One release: exactly one waiter is admitted, the other stays queued.
	g.release()
	var admitted chan error
	select {
	case err := <-doneA:
		if err != nil {
			t.Fatalf("waiter A: %v, want nil", err)
		}
		admitted = doneB
	case err := <-doneB:
		if err != nil {
			t.Fatalf("waiter B: %v, want nil", err)
		}
		admitted = doneA
	case <-time.After(2 * time.Second):
		t.Fatal("no waiter was admitted after release")
	}
	waitForGate(t, g, 2*time.Second, func() bool { return g.snapQueued() == 1 }, "remaining waiter to re-queue")

	// Second release must wake the remaining waiter (it re-queued on the new
	// channel; if it kept the closed one it would spin, if it missed the
	// minted channel it would hang).
	g.release()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("remaining waiter: %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remaining waiter never woke after second release")
	}
	g.release()
}
