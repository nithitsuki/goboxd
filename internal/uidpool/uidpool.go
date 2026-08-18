// Package uidpool allocates distinct unprivileged uids for jails.
//
// Every jail runs as its own host-level uid (piston/isolate model) so that a
// sandbox escape yields only that uid's privileges, never root. The pool hands
// out uids from a fixed range [GOBOXD_UID_MIN, GOBOXD_UID_MIN+size). A uid is
// never handed out twice while held; allocation failure surfaces as an
// internal_error so a request never silently shares a uid with another jail.
package uidpool

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
)

// DefaultMinUid is the first pool uid when GOBOXD_UID_MIN is unset.
// 10000 is past common system ranges; the container's own uids start lower.
const DefaultMinUid = 10000

// ConcurrentJobs returns the concurrent-jobs bound used by the API semaphore:
// GOBOXD_MAX_JOBS if set and positive, else runtime.NumCPU().
func ConcurrentJobs() int {
	if e := os.Getenv("GOBOXD_MAX_JOBS"); e != "" {
		if v, err := strconv.Atoi(e); err == nil && v > 0 {
			return v
		}
	}
	return runtime.NumCPU()
}

// UidBudget returns the size of the uid pool: ConcurrentJobs() x (NumCPU + 1).
//
// The admission gate admits up to maxJobs requests. A request holds one uid
// for its template jail for the whole request, plus up to NumCPU uids at once
// for its parallel tests (capped at runtime.NumCPU() in ExecuteRun), so
// worst-case simultaneous demand is maxJobs x (NumCPU + 1). A pool sized to
// that product can never be exhausted while jobs are admitted. The per-request
// +1 (the template uid) is folded into the per-request factor, so an
// overflowing GOBOXD_MAX_JOBS cannot silently drop it: the product saturates
// at MaxInt instead of wrapping negative. Exhaustion stays an internal_error,
// never a silent uid sharing.
func UidBudget() int {
	jobs := ConcurrentJobs()
	if jobs < 1 {
		jobs = 1
	}
	cpus := runtime.NumCPU()
	if cpus < 1 {
		cpus = 1
	}
	per := cpus + 1 // template jail uid + one per parallel test
	maxInt := int(^uint(0) >> 1)
	if jobs > maxInt/per {
		return maxInt // saturate: maxJobs x (NumCPU + 1) cannot be represented
	}
	return jobs * per
}

// minUid reads GOBOXD_UID_MIN, falling back to DefaultMinUid.
func minUid() int {
	if e := os.Getenv("GOBOXD_UID_MIN"); e != "" {
		if v, err := strconv.Atoi(e); err == nil && v > 0 {
			return v
		}
	}
	return DefaultMinUid
}

// Pool hands out distinct uids from a fixed range. Safe for concurrent use.
type Pool struct {
	mu    sync.Mutex
	min   int
	size  int
	inUse map[int]bool
	next  int
}

// New creates a pool of size n uids starting at GOBOXD_UID_MIN.
// A non-positive n degrades to 1 so the pool is never empty.
func New(n int) *Pool {
	if n < 1 {
		n = 1
	}
	return &Pool{
		min:   minUid(),
		size:  n,
		inUse: make(map[int]bool, n),
		next:  0,
	}
}

// Alloc returns an unused uid, or an error when every uid in the pool is held.
func (p *Pool) Alloc() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := 0; i < p.size; i++ {
		uid := p.min + ((p.next + i) % p.size)
		if !p.inUse[uid] {
			p.inUse[uid] = true
			p.next = (p.next + i + 1) % p.size
			return uid, nil
		}
	}
	return 0, fmt.Errorf("uid pool exhausted (size %d)", p.size)
}

// Release returns a held uid to the pool. Releasing a uid this pool never
// handed out (or an already-free uid) is a safe no-op.
func (p *Pool) Release(uid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if uid < p.min || uid >= p.min+p.size {
		return
	}
	delete(p.inUse, uid)
}

// Size returns the number of uids the pool can hand out.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.size
}
