package uidpool

import (
	"runtime"
	"sync"
	"testing"
)

// minUid is the default pool start when GOBOXD_UID_MIN is unset.
const defaultMinUid = 10000

func TestAllocDistinct(t *testing.T) {
	p := New(4)
	seen := map[int]bool{}
	for i := 0; i < 4; i++ {
		uid, err := p.Alloc()
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		if seen[uid] {
			t.Fatalf("uid %d allocated twice", uid)
		}
		seen[uid] = true
		if uid < defaultMinUid {
			t.Errorf("uid %d below pool start %d", uid, defaultMinUid)
		}
	}
}

func TestNoReuseWhileHeld(t *testing.T) {
	p := New(2)
	a, err := p.Alloc()
	if err != nil {
		t.Fatalf("alloc a: %v", err)
	}
	b, err := p.Alloc()
	if err != nil {
		t.Fatalf("alloc b: %v", err)
	}
	p.Release(a)
	c, err := p.Alloc()
	if err != nil {
		t.Fatalf("alloc c: %v", err)
	}
	if c != a {
		t.Errorf("expected released uid %d to be reused, got %d", a, c)
	}
	// b is still held: the pool (size 2) must be exhausted now.
	if _, err := p.Alloc(); err == nil {
		t.Errorf("expected exhaustion while uid %d still held", b)
	}
}

func TestExhaustion(t *testing.T) {
	p := New(2)
	if _, err := p.Alloc(); err != nil {
		t.Fatalf("alloc 1: %v", err)
	}
	if _, err := p.Alloc(); err != nil {
		t.Fatalf("alloc 2: %v", err)
	}
	if _, err := p.Alloc(); err == nil {
		t.Fatal("expected error on third alloc, got nil")
	}
}

func TestConcurrentAllocNoDuplicates(t *testing.T) {
	p := New(8)
	const n = 8
	start := make(chan struct{})
	uids := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			uids[i], errs[i] = p.Alloc()
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[int]bool{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if seen[uids[i]] {
			t.Errorf("uid %d allocated to multiple goroutines", uids[i])
		}
		seen[uids[i]] = true
	}
	for i := 0; i < n; i++ {
		p.Release(uids[i])
	}
}

func TestPoolSizeFromEnv(t *testing.T) {
	t.Setenv("GOBOXD_MAX_JOBS", "5")
	if got := ConcurrentJobs(); got != 5 {
		t.Errorf("ConcurrentJobs with GOBOXD_MAX_JOBS=5: got %d, want 5", got)
	}
	t.Setenv("GOBOXD_MAX_JOBS", "")
	if got := ConcurrentJobs(); got != runtime.NumCPU() {
		t.Errorf("ConcurrentJobs default: got %d, want NumCPU %d", got, runtime.NumCPU())
	}
}

func TestUidMinFromEnv(t *testing.T) {
	t.Setenv("GOBOXD_UID_MIN", "20000")
	p := New(2)
	a, err := p.Alloc()
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	b, err := p.Alloc()
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if a != 20000 || b != 20001 {
		t.Errorf("expected uids 20000,20001 from GOBOXD_UID_MIN=20000, got %d,%d", a, b)
	}
}

func TestReleaseUnheldIsSafe(t *testing.T) {
	p := New(2)
	// Releasing a uid this pool never handed out must not panic or corrupt state.
	p.Release(12345)
	// Pool must still work afterwards.
	if _, err := p.Alloc(); err != nil {
		t.Fatalf("alloc after bogus release: %v", err)
	}
}

func TestPoolSizeAtLeastOne(t *testing.T) {
	p := New(0)
	a, err := p.Alloc()
	if err != nil {
		t.Fatalf("pool sized 0 must degrade to 1: %v", err)
	}
	if a < defaultMinUid {
		t.Errorf("unexpected uid %d", a)
	}
}

// TestUidBudget locks the pool sizing contract: maxJobs x (NumCPU + 1) (each
// request holds one uid for its template jail plus up to NumCPU uids for its
// parallel tests, and up to maxJobs requests are in flight), and an
// overflowing GOBOXD_MAX_JOBS saturates at MaxInt (keeping the +1 factor)
// instead of wrapping negative.
func TestUidBudget(t *testing.T) {
	t.Setenv("GOBOXD_MAX_JOBS", "4")
	if got, want := UidBudget(), 4*(runtime.NumCPU()+1); got != want {
		t.Errorf("UidBudget with GOBOXD_MAX_JOBS=4 = %d, want %d", got, want)
	}
	t.Setenv("GOBOXD_MAX_JOBS", "")
	if got, want := UidBudget(), runtime.NumCPU()*(runtime.NumCPU()+1); got != want {
		t.Errorf("UidBudget default = %d, want %d", got, want)
	}
	t.Setenv("GOBOXD_MAX_JOBS", "9223372036854775807")
	if got, want := UidBudget(), int(^uint(0)>>1); got != want {
		t.Errorf("UidBudget overflow = %d, want saturated MaxInt %d", got, want)
	}
}
