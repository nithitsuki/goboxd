package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestWaitForDrainUntil locks the drain-cap contract: the shutdown sweep
// must wait for the in-flight count to reach zero. When the count never
// drains, the function reports false so the caller skips the sweep.
func TestWaitForDrainUntil(t *testing.T) {
	t.Run("already drained", func(t *testing.T) {
		if !waitForDrainUntil(time.Now().Add(time.Second), time.Millisecond, func() int { return 0 }) {
			t.Fatal("zero in-flight must report true (sweep allowed)")
		}
	})

	t.Run("drains after start", func(t *testing.T) {
		var n atomic.Int64
		n.Store(2)
		go func() {
			time.Sleep(50 * time.Millisecond)
			n.Store(0)
		}()
		if !waitForDrainUntil(time.Now().Add(5*time.Second), time.Millisecond, func() int { return int(n.Load()) }) {
			t.Fatal("in-flight that reaches zero must report true (sweep allowed)")
		}
	})

	t.Run("never drains", func(t *testing.T) {
		start := time.Now()
		if waitForDrainUntil(time.Now().Add(100*time.Millisecond), 10*time.Millisecond, func() int { return 1 }) {
			t.Fatal("in-flight stuck above zero must report false (sweep skipped)")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("cap poll took %s, want ~100ms", elapsed)
		}
	})
}
