// Package cgroupv2 enforces per-jail memory and pids limits via cgroup v2 when
// a writable cgroup2 hierarchy is available, and reports per-jail peak memory.
//
// Security contract: limits are NEVER unenforced. When cgroup v2 is inactive
// (probe failure, Docker Desktop, GOBOXD_CGROUPV2=off), the runner falls back
// to nsjail rlimit flags, which are always present. A cgroup setup failure
// degrades to the rlimit path for that request; it never fails the request.
//
// Layout (probe creates the delegation point):
//
//	<root>/goboxd/                 -- probe creates this, enables memory+pids
//	<root>/goboxd/<jailID>/        -- per-jail dir (NewJail), memory+pids enabled
//	<root>/goboxd/<jailID>/NSJAIL.<pid> -- nsjail's leaf for each exec
//
// nsjail only moves the child into its NSJAIL.<pid> leaf when it receives its
// own cgroup limits (--cgroup_mem_max/--cgroup_pids_max), so the runner passes
// them per exec. The leaf is removed by nsjail when the exec exits; our
// per-jail dir's memory.peak includes the leaf's usage (descendants) and
// survives the removal, which is why PeakKB is read after the run.
package cgroupv2

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// cgroup2Magic is the CGROUP2_SUPER_MAGIC filesystem type.
const cgroup2Magic = 0x63677270

// baseName is the delegation directory created under the cgroup2 root.
const baseName = "goboxd"

var (
	defaultOnce sync.Once
	defaultMgr  *Manager
)

// Default returns the process-wide manager, probing once on first use.
// main() and /info both consult this so the runner and the API agree on
// whether cgroup v2 is active.
func Default() *Manager {
	defaultOnce.Do(func() {
		defaultMgr = NewManager("/sys/fs/cgroup")
		if defaultMgr.Active() {
			log.Printf("[cgroupv2] active, mount=%s", defaultMgr.Root())
		} else {
			log.Printf("[cgroupv2] inactive, using rlimit fallback")
		}
	})
	return defaultMgr
}

// Manager probes and owns a cgroup2 hierarchy. A manager over an unusable
// root is inactive but never errors; callers fall back to rlimits.
type Manager struct {
	mu     sync.Mutex
	root   string // cgroup2 mount root, e.g. /sys/fs/cgroup
	base   string // <root>/goboxd
	active bool
}

// NewManager probes root and returns a manager. GOBOXD_CGROUPV2=off forces an
// inactive manager. Probe failures never return an error: they leave the
// manager inactive (rlimit fallback), with the reason logged.
func NewManager(root string) *Manager {
	m := &Manager{
		root: root,
		base: filepath.Join(root, baseName),
	}
	if os.Getenv("GOBOXD_CGROUPV2") == "off" {
		log.Printf("[cgroupv2] disabled via GOBOXD_CGROUPV2=off")
		return m
	}
	if err := m.probe(); err != nil {
		log.Printf("[cgroupv2] probe failed, using rlimit fallback: %v", err)
		return m
	}
	m.active = true
	m.Sweep() // remove jail dirs left by crashed runs
	return m
}

// probe verifies root is a writable cgroup2 fs and creates the goboxd
// delegation point with the memory and pids controllers enabled.
func (m *Manager) probe() error {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(m.root, &fs); err != nil {
		return fmt.Errorf("statfs %s: %w", m.root, err)
	}
	if fs.Type != cgroup2Magic {
		return fmt.Errorf("%s is not a cgroup2 filesystem (type %#x)", m.root, fs.Type)
	}
	if err := os.MkdirAll(m.base, 0700); err != nil {
		return fmt.Errorf("creating %s: %w", m.base, err)
	}
	// Enable the controllers for children of the delegation point. This is
	// what makes per-jail dirs usable; requires the controllers to be active
	// in root's subtree (the host's cgroup delegation).
	if err := os.WriteFile(filepath.Join(m.base, "cgroup.subtree_control"), []byte("+memory +pids"), 0644); err != nil {
		return fmt.Errorf("enabling memory+pids in %s: %w", m.base, err)
	}
	// Writability is NOT enforcement: Docker containers commonly expose a
	// cgroupfs where every write succeeds but the memory controller is never
	// charged (the container's scope has no controllers delegated from the
	// host). Only trust the hierarchy after observing real charging.
	if err := m.verifyEnforcement(); err != nil {
		return fmt.Errorf("enforcement check: %w", err)
	}
	return nil
}

// verifyEnforcement proves the memory controller actually charges memory by
// running a ~16MB hog in a probe cgroup and checking memory.peak moved. A
// working controller shows peak >= 8MB; an inert one shows 0.
func (m *Manager) verifyEnforcement() error {
	probeDir := filepath.Join(m.base, "probe")
	if err := os.Mkdir(probeDir, 0700); err != nil {
		return fmt.Errorf("creating probe dir: %w", err)
	}
	defer func() {
		_ = removeJailDir(filepath.Join(probeDir, "leaf"))
		_ = removeJailDir(probeDir)
	}()
	if err := os.WriteFile(filepath.Join(probeDir, "cgroup.subtree_control"), []byte("+memory +pids"), 0644); err != nil {
		return fmt.Errorf("enabling probe controllers: %w", err)
	}
	leaf := filepath.Join(probeDir, "leaf")
	if err := os.Mkdir(leaf, 0700); err != nil {
		return fmt.Errorf("creating probe leaf: %w", err)
	}
	// 512MB limit: high enough that the 16MB hog is never killed; we only
	// need to observe that usage is charged to the leaf. Swap disabled so
	// the accounting is unambiguous.
	if err := os.WriteFile(filepath.Join(leaf, "memory.max"), []byte("536870912"), 0644); err != nil {
		return fmt.Errorf("writing probe memory.max: %w", err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "memory.swap.max"), []byte("0"), 0644); err != nil {
		return fmt.Errorf("writing probe memory.swap.max: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/exe")
	cmd.Env = append(os.Environ(), "GOBOXD_CGROUP_PROBE_HOG=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("probe hog stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting probe hog: %w", err)
	}
	// Move the hog into the leaf, THEN release it to allocate: memory is only
	// charged to the cgroup the process is in at allocation time, so the
	// trigger must strictly follow the cgroup.procs write. The hog blocks on
	// stdin until this byte arrives.
	if err := os.WriteFile(filepath.Join(leaf, "cgroup.procs"), []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("moving hog into probe leaf: %w", err)
	}
	if _, err := stdin.Write([]byte("go")); err != nil {
		// The child may have exited (e.g. the test binary, which never runs the
		// hog): a closed pipe here still lets the peak check below fail the
		// probe, which is the correct verdict for an inert controller.
		_ = cmd.Process.Kill()
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("probe hog failed: %w", err)
	}
	b, err := os.ReadFile(filepath.Join(leaf, "memory.peak"))
	if err != nil {
		return fmt.Errorf("reading probe memory.peak: %w", err)
	}
	peak, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return fmt.Errorf("parsing probe memory.peak %q: %w", string(b), err)
	}
	if peak < 8*1024*1024 {
		return fmt.Errorf("memory controller inert: hog touched 16MB but peak is %d bytes", peak)
	}
	return nil
}

// ProbeHog waits for a byte on stdin, allocates and touches ~16MB of memory,
// holds it for two seconds, then exits. main() re-execs this binary with
// GOBOXD_CGROUP_PROBE_HOG=1 so the enforcement probe can move the child into a
// probe cgroup BEFORE releasing it: memory is charged to the cgroup the
// process is in at allocation time, so the stdin handshake is what makes the
// probe deterministic (a hog that allocates before the cgroup.procs write
// would show a false "inert" verdict).
func ProbeHog() {
	var one [1]byte
	if _, err := os.Stdin.Read(one[:]); err != nil {
		return
	}
	buf := make([]byte, 16*1024*1024)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1
	}
	time.Sleep(2 * time.Second)
}

// Active reports whether cgroup v2 limits and peak reporting are in use.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// Root returns the cgroup2 mount root (for /info and tests).
func (m *Manager) Root() string { return m.root }

// NewJail creates the per-jail cgroup directory and enables its controllers.
func (m *Manager) NewJail(id string) (*Jail, error) {
	p := filepath.Join(m.base, id)
	if err := os.Mkdir(p, 0700); err != nil {
		return nil, fmt.Errorf("creating jail cgroup %s: %w", p, err)
	}
	// Enable memory+pids for the jail's children (nsjail's NSJAIL.<pid>
	// leaves). Requires the controllers active in the jail dir, which the
	// probe enabled in base's subtree_control.
	if err := os.WriteFile(filepath.Join(p, "cgroup.subtree_control"), []byte("+memory +pids"), 0644); err != nil {
		return nil, fmt.Errorf("enabling controllers in %s: %w", p, err)
	}
	return &Jail{manager: m, path: p}, nil
}

// Sweep removes leftover jail cgroup dirs (crashed runs). Called at probe
// time, when no jails can be active yet.
func (m *Manager) Sweep() {
	entries, err := os.ReadDir(m.base)
	if err != nil {
		return
	}
	for _, e := range entries {
		// Only jail dirs: kernel pseudo-files (cgroup.controllers, memory.*,
		// ...) are files, not directories, and must not be touched.
		if !e.IsDir() {
			continue
		}
		if err := removeJailDir(filepath.Join(m.base, e.Name())); err != nil && !os.IsNotExist(err) {
			log.Printf("[cgroupv2] sweep: removing %s: %v", e.Name(), err)
		}
	}
}

// Jail is one request's cgroup directory.
type Jail struct {
	manager *Manager
	path    string
}

// Path returns the jail cgroup directory (passed as nsjail's --cgroupv2_mount).
func (j *Jail) Path() string { return j.path }

// removeJailDir removes a jail cgroup dir. On a real cgroup2 filesystem the
// kernel removes pseudo-files (cgroup.subtree_control, memory.peak) on rmdir;
// on a regular filesystem (tests) we remove them ourselves first.
func removeJailDir(path string) error {
	_ = os.Remove(filepath.Join(path, "cgroup.subtree_control"))
	_ = os.Remove(filepath.Join(path, "memory.peak"))
	return os.Remove(path)
}

// Teardown removes the jail cgroup directory, retrying briefly on EBUSY
// (nsjail may still be tearing down its leaf). Non-fatal on failure: the
// startup Sweep cleans up leftovers.
func (j *Jail) Teardown() {
	for i := 0; i < 5; i++ {
		err := removeJailDir(j.path)
		if err == nil || os.IsNotExist(err) {
			return
		}
		if !errors.Is(err, syscall.EBUSY) {
			log.Printf("[cgroupv2] teardown %s: %v", j.path, err)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("[cgroupv2] teardown %s: still busy after retries", j.path)
}

// PeakKB reads the jail's memory.peak (bytes, includes descendants' usage)
// and returns kilobytes. 0 when unavailable.
func (j *Jail) PeakKB() int {
	b, err := os.ReadFile(filepath.Join(j.path, "memory.peak"))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return int(n / 1024)
}

// ResetPeak resets memory.peak so the next exec's peak is measured in
// isolation (per-test peaks, not cumulative across build + tests).
func (j *Jail) ResetPeak() error {
	if err := os.WriteFile(filepath.Join(j.path, "memory.peak"), []byte("0"), 0644); err != nil {
		return fmt.Errorf("resetting memory.peak: %w", err)
	}
	return nil
}

// OOMKills returns the total oom_kill count across nsjail's leaf cgroups
// under this jail dir. nsjail names leaves NSJAIL.<clone child pid>, which
// differs from the parent process pid Go sees, so we scan the directory
// instead of guessing. A missing dir or unreadable leaf reads as 0.
func (j *Jail) OOMKills() (uint64, error) {
	entries, err := os.ReadDir(j.path)
	if err != nil {
		return 0, nil // jail dir gone: no leaves to read
	}
	var total uint64
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "NSJAIL.") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(j.path, e.Name(), "memory.events"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "oom_kill" {
				n, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					return 0, fmt.Errorf("parsing oom_kill: %w", err)
				}
				total += n
			}
		}
	}
	return total, nil
}
