package cgroupv2

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProbeInactiveOnNonCgroupFS: a regular directory is not a cgroup2
// filesystem, so a manager over it must be inactive (fallback to rlimits).
func TestProbeInactiveOnNonCgroupFS(t *testing.T) {
	m := NewManager(t.TempDir())
	if m.Active() {
		t.Error("manager over a plain directory must be inactive")
	}
}

func TestProbeInactiveWhenRootMissing(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "does-not-exist"))
	if m.Active() {
		t.Error("manager over a missing root must be inactive")
	}
}

func TestProbeInactiveWhenOff(t *testing.T) {
	t.Setenv("GOBOXD_CGROUPV2", "off")
	m := NewManager(t.TempDir())
	if m.Active() {
		t.Error("GOBOXD_CGROUPV2=off must force the rlimit fallback")
	}
}

// White-box: exercise the active path against a fake cgroup dir so the jail
// lifecycle (mkdir/rmdir/peak/events) is unit-tested without a real cgroup2 fs.
func fakeActiveManager(t *testing.T) *Manager {
	t.Helper()
	base := filepath.Join(t.TempDir(), "goboxd")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatalf("creating fake base: %v", err)
	}
	return &Manager{root: t.TempDir(), base: base, active: true}
}

func TestNewJailCreatesDir(t *testing.T) {
	m := fakeActiveManager(t)
	j, err := m.NewJail("jail-1")
	if err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	if !strings.HasSuffix(j.Path(), "/goboxd/jail-1") {
		t.Errorf("unexpected jail path %q", j.Path())
	}
	if fi, err := os.Stat(j.Path()); err != nil || !fi.IsDir() {
		t.Errorf("jail dir not created: %v", err)
	}
}

func TestTeardownRemovesDir(t *testing.T) {
	m := fakeActiveManager(t)
	j, err := m.NewJail("jail-2")
	if err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	j.Teardown()
	if _, err := os.Stat(j.Path()); !os.IsNotExist(err) {
		t.Errorf("jail dir should be removed after Teardown, stat err=%v", err)
	}
}

func TestPeakKBReadsAndResets(t *testing.T) {
	m := fakeActiveManager(t)
	j, err := m.NewJail("jail-3")
	if err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	// Fake kernel files.
	peakPath := filepath.Join(j.Path(), "memory.peak")
	if err := os.WriteFile(peakPath, []byte("5242880\n"), 0644); err != nil {
		t.Fatalf("writing fake memory.peak: %v", err)
	}
	if got := j.PeakKB(); got != 5120 {
		t.Errorf("PeakKB: got %d, want 5120 (5242880 bytes)", got)
	}
	if err := j.ResetPeak(); err != nil {
		t.Fatalf("ResetPeak: %v", err)
	}
	b, _ := os.ReadFile(peakPath)
	if strings.TrimSpace(string(b)) != "0" {
		t.Errorf("ResetPeak should write 0, file contains %q", string(b))
	}
}

func TestPeakKBMissingFileIsZero(t *testing.T) {
	m := fakeActiveManager(t)
	j, err := m.NewJail("jail-4")
	if err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	if got := j.PeakKB(); got != 0 {
		t.Errorf("missing memory.peak should read 0, got %d", got)
	}
}

func TestOOMKillsScansAllLeaves(t *testing.T) {
	m := fakeActiveManager(t)
	j, err := m.NewJail("jail-5")
	if err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	// Two leaves, one with a kill: nsjail names leaves NSJAIL.<child pid>,
	// so we scan instead of guessing the pid.
	for _, leaf := range []string{"NSJAIL.100", "NSJAIL.200"} {
		d := filepath.Join(j.Path(), leaf)
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("mkdir leaf %s: %v", leaf, err)
		}
	}
	events := "low 0\nhigh 0\nmax 0\noom 1\noom_kill 1\noom_group_kill 0\n"
	if err := os.WriteFile(filepath.Join(j.Path(), "NSJAIL.100", "memory.events"), []byte(events), 0644); err != nil {
		t.Fatalf("writing fake memory.events: %v", err)
	}
	n, err := j.OOMKills()
	if err != nil {
		t.Fatalf("OOMKills: %v", err)
	}
	if n != 1 {
		t.Errorf("oom_kill: got %d, want 1", n)
	}
	// Non-NSJAIL entries (our own memory.peak file) are ignored.
	if err := os.WriteFile(filepath.Join(j.Path(), "memory.peak"), []byte("5242880\n"), 0644); err != nil {
		t.Fatalf("writing memory.peak: %v", err)
	}
	if n, err := j.OOMKills(); err != nil || n != 1 {
		t.Errorf("extra files must not count: got n=%d err=%v, want 1,nil", n, err)
	}
	// Missing jail dir -> 0, no error.
	gone, _ := fakeActiveManager(t).NewJail("gone")
	if n, err := gone.OOMKills(); err != nil || n != 0 {
		t.Errorf("missing jail dir: got n=%d err=%v, want 0,nil", n, err)
	}

	// OOMKillsSince classifies per-exec: a baseline taken after an earlier
	// kill must not count that kill against a later exec.
	if since, err := j.OOMKillsSince(0); err != nil || !since {
		t.Errorf("OOMKillsSince(0) with 1 kill: got %v,%v, want true,nil", since, err)
	}
	if since, err := j.OOMKillsSince(1); err != nil || since {
		t.Errorf("OOMKillsSince(1) with 1 kill: got %v,%v, want false,nil (baseline must absorb old kills)", since, err)
	}
}

// TestVerifyCPUInertController locks the cpu verdict seam: a probe leaf whose
// cpu.stat stays at 0 (or is missing) must flip cpuActive off without failing
// the probe, so cpu limits degrade to the rlimit path on inert-cpu hosts.
func TestVerifyCPUInertController(t *testing.T) {
	base := filepath.Join(t.TempDir(), "goboxd")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	leaf := filepath.Join(base, "leaf")
	if err := os.Mkdir(leaf, 0700); err != nil {
		t.Fatalf("mkdir leaf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "cpu.stat"), []byte("usage_usec 0\n"), 0644); err != nil {
		t.Fatalf("writing fake cpu.stat: %v", err)
	}

	// Inert: the hog would have spun ~2s, but nothing was charged.
	m := &Manager{root: t.TempDir(), base: base, active: true, cpuActive: true}
	m.verifyCPU(leaf)
	if m.cpuActive {
		t.Error("verifyCPU must set cpuActive=false when usage_usec stays below the threshold")
	}

	// Charging: usage above the threshold keeps cpu active.
	m2 := &Manager{root: t.TempDir(), base: base, active: true, cpuActive: true}
	if err := os.WriteFile(filepath.Join(leaf, "cpu.stat"), []byte("usage_usec 2500000\n"), 0644); err != nil {
		t.Fatalf("rewriting fake cpu.stat: %v", err)
	}
	m2.verifyCPU(leaf)
	if !m2.cpuActive {
		t.Error("verifyCPU must keep cpuActive=true when usage_usec passes the threshold")
	}

	// Missing cpu.stat (no cpu delegation at all) is the inert verdict too.
	m3 := &Manager{root: t.TempDir(), base: base, active: true, cpuActive: true}
	_ = os.Remove(filepath.Join(leaf, "cpu.stat"))
	m3.verifyCPU(leaf)
	if m3.cpuActive {
		t.Error("verifyCPU must set cpuActive=false when cpu.stat is missing")
	}
}

func TestVerifyEnforcementDetectsInertController(t *testing.T) {
	// When the probe re-execs the test binary as its hog, the child runs this
	// suite; the env flag marks it and it exits immediately (no recursion).
	if os.Getenv("GOBOXD_CGROUP_PROBE_HOG") == "1" {
		return
	}
	// A fake dir never charges memory: the enforcement probe must fail,
	// keeping the manager inactive instead of trusting a writable-but-inert
	// cgroupfs (the Docker-container failure mode).
	m := &Manager{root: t.TempDir(), base: filepath.Join(t.TempDir(), "goboxd"), active: true}
	if err := os.MkdirAll(m.base, 0700); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := m.verifyEnforcement(); err == nil {
		t.Error("verifyEnforcement must fail on a fake (inert) cgroupfs")
	}
}

func TestSweepRemovesStaleJails(t *testing.T) {
	m := fakeActiveManager(t)
	if _, err := m.NewJail("stale-1"); err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	m.Sweep()
	entries, err := os.ReadDir(m.base)
	if err != nil {
		t.Fatalf("readdir base: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Sweep should remove stale jail dirs, found %d", len(entries))
	}
}

func TestEnvOffForcesInactiveEvenWithActiveManager(t *testing.T) {
	t.Setenv("GOBOXD_CGROUPV2", "off")
	m := NewManager("/sys/fs/cgroup")
	if m.Active() {
		t.Error("GOBOXD_CGROUPV2=off must disable cgroup v2")
	}
}

// TestNewJailCPUController: a jail on a cpu-active manager enables +cpu in
// its subtree_control (usage polling and the cpu kill); a manager without
// the cpu delegation enables memory+pids only and the jail reports it.
func TestNewJailCPUController(t *testing.T) {
	m := fakeActiveManager(t)
	m.cpuActive = true
	j, err := m.NewJail("cpu-jail")
	if err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	if !j.CPUActive() {
		t.Error("jail over a cpu-active manager must report CPUActive")
	}
	b, err := os.ReadFile(filepath.Join(j.Path(), "cgroup.subtree_control"))
	if err != nil {
		t.Fatalf("reading subtree_control: %v", err)
	}
	if !strings.Contains(string(b), "+cpu") {
		t.Errorf("subtree_control = %q, want +cpu enabled", string(b))
	}

	m2 := fakeActiveManager(t)
	j2, err := m2.NewJail("nocpu-jail")
	if err != nil {
		t.Fatalf("NewJail (no cpu): %v", err)
	}
	if j2.CPUActive() {
		t.Error("jail over a manager without cpu delegation must not report CPUActive")
	}
	b2, err := os.ReadFile(filepath.Join(j2.Path(), "cgroup.subtree_control"))
	if err != nil {
		t.Fatalf("reading subtree_control: %v", err)
	}
	if strings.Contains(string(b2), "+cpu") {
		t.Errorf("subtree_control = %q, want no +cpu", string(b2))
	}
}

// TestCPUUsageReadsUsageUsec: CPUUsageUS parses usage_usec from cpu.stat.
func TestCPUUsageReadsUsageUsec(t *testing.T) {
	m := fakeActiveManager(t)
	j, err := m.NewJail("cpu-stat-jail")
	if err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	if _, err := j.CPUUsageUS(); err == nil {
		t.Error("missing cpu.stat must be an error")
	}
	stat := "usage_usec 12345678\nuser_usec 100\nsystem_usec 200\n"
	if err := os.WriteFile(filepath.Join(j.Path(), "cpu.stat"), []byte(stat), 0644); err != nil {
		t.Fatalf("writing fake cpu.stat: %v", err)
	}
	us, err := j.CPUUsageUS()
	if err != nil {
		t.Fatalf("CPUUsageUS: %v", err)
	}
	if us != 12345678 {
		t.Errorf("CPUUsageUS = %d, want 12345678", us)
	}
}

// TestResetCPUWrite: ResetCPU attempts the reset write (the kernel may
// reject it: real hosts without cpu.stat reset support return EINVAL and the
// caller falls back to delta measurement).
func TestResetCPUWrite(t *testing.T) {
	m := fakeActiveManager(t)
	j, err := m.NewJail("cpu-reset-jail")
	if err != nil {
		t.Fatalf("NewJail: %v", err)
	}
	if err := os.WriteFile(filepath.Join(j.Path(), "cpu.stat"), []byte("usage_usec 5\n"), 0644); err != nil {
		t.Fatalf("writing fake cpu.stat: %v", err)
	}
	_ = j.ResetCPU() // regular fs accepts the write; real cgroupfs may EINVAL
	b, _ := os.ReadFile(filepath.Join(j.Path(), "cpu.stat"))
	if strings.TrimSpace(string(b)) != "0" {
		t.Errorf("ResetCPU should zero the usage counter, file contains %q", string(b))
	}
}
