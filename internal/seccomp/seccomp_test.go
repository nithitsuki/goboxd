package seccomp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPolicyNoHashComments guards the kafel lexer constraint: # comments are
// not supported, only // and /* */. A # anywhere in the policy would make
// nsjail fail to compile it at jail startup.
func TestPolicyNoHashComments(t *testing.T) {
	for i, line := range strings.Split(string(policy), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			t.Errorf("line %d starts with # (kafel rejects # comments): %q", i+1, line)
		}
	}
}

// TestPolicyDenyEntries checks the deny-list contains the core escape
// primitives and the SYSCALL[166] workaround for kafel's missing umount2.
func TestPolicyDenyEntries(t *testing.T) {
	pol := string(policy)
	for _, want := range []string{
		"mount",
		"ptrace",
		"bpf",
		"SYSCALL[166]",
		"USE goboxd DEFAULT ALLOW",
	} {
		if !strings.Contains(pol, want) {
			t.Errorf("policy missing expected entry %q", want)
		}
	}
}

// TestCombinedWithKeepsGlobalDeny proves the additive-merge contract (P2-12):
// a combined policy must contain EVERY global deny entry plus the extra one.
// This is the regression test for the security review: the old REPLACE
// semantics dropped the global deny-list (a "ptrace only" profile silently
// removed mount, chroot, bpf, etc.). Dropping any global entry fails here.
//
// Exact-set comparison (not substring): "mount" is a substring of "umount" and
// "init_module" of "finit_module", so substring matching has exact blind spots
// for the worst regressions. We re-parse the combined policy's DENY set and
// require it to be the global set plus the extras.
func TestCombinedWithKeepsGlobalDeny(t *testing.T) {
	gNames, _, _, _, err := parseGlobalPolicy()
	if err != nil {
		t.Fatalf("parseGlobalPolicy: %v", err)
	}

	combined, err := CombinedWith([]string{"chmod"})
	if err != nil {
		t.Fatalf("CombinedWith: %v", err)
	}
	cNames, _, _, _, err := parseGlobalPolicyBytes(combined)
	if err != nil {
		t.Fatalf("parsing combined policy: %v", err)
	}

	// The combined DENY set must contain every global entry and the extra.
	want := map[string]bool{}
	for _, n := range gNames {
		want[n] = true
	}
	want["chmod"] = true
	for _, n := range cNames {
		if !want[n] {
			t.Errorf("combined policy contains unexpected entry %q", n)
		}
		delete(want, n)
	}
	for missing := range want {
		t.Errorf("combined policy missing global or extra deny entry %q", missing)
	}

	// The DEFAULT ALLOW tail must be preserved.
	pol := string(combined)
	if !strings.Contains(pol, "USE goboxd DEFAULT ALLOW") {
		t.Errorf("combined policy must preserve the DEFAULT ALLOW tail\n%s", pol)
	}
}

// TestCombinedWithDedupsGlobalEntries ensures an extra that is ALREADY in the
// global deny-list is not duplicated (kafel's grammar is comma-separated; a
// stray duplicate would still parse but is pointless and noisy). The point of
// the test is that the extra cannot REMOVE the global entry — it must still be
// present exactly once, additively.
func TestCombinedWithDedupsGlobalEntries(t *testing.T) {
	combined, err := CombinedWith([]string{"ptrace"}) // ptrace already globally denied
	if err != nil {
		t.Fatalf("CombinedWith: %v", err)
	}
	pol := string(combined)
	// ptrace must still be denied (additive guarantee) and not duplicated.
	if strings.Count(pol, "ptrace") != 1 {
		t.Errorf("ptrace appears %d times in combined policy, want exactly 1 (must not be dropped or duplicated)",
			strings.Count(pol, "ptrace"))
	}
}

// TestCombinedWithSingleDeniedSyscall locks the exact kafel shapes the runner
// will pass to --seccomp_string: a valid POLICY named goboxd with a DENY block
// and the USE goboxd DEFAULT ALLOW tail.
func TestCombinedWithSingleDeniedSyscall(t *testing.T) {
	combined, err := CombinedWith([]string{"chmod"})
	if err != nil {
		t.Fatalf("CombinedWith: %v", err)
	}
	pol := string(combined)
	if strings.Contains(pol, "USE py3") {
		t.Error("combined policy must reuse the global policy name `goboxd`, not a per-language name")
	}
	if !strings.Contains(pol, "POLICY goboxd {") {
		t.Error("combined policy must declare POLICY goboxd")
	}
	if !strings.Contains(pol, "\n\tDENY {") {
		t.Error("combined policy must contain a DENY block")
	}
}

// TestParseSyscallNames locks the per-language `seccomp:` directive parsing:
// whitespace- or comma-separated syscall names.
func TestParseSyscallNames(t *testing.T) {
	got := ParseSyscallNames("chmod, chown\nptrace  fchmod")
	want := []string{"chmod", "chown", "ptrace", "fchmod"}
	if len(got) != len(want) {
		t.Fatalf("ParseSyscallNames = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ParseSyscallNames[%d] = %q, want %q (full: %q)", i, got[i], want[i], got)
		}
	}
	if ParseSyscallNames("") != nil {
		t.Error("ParseSyscallNames(\"\") should return nil/empty")
	}
}

// TestPolicyPath verifies the materialized file matches the embedded policy
// and that PolicyPath is stable across calls (written once).
func TestPolicyPath(t *testing.T) {
	p, err := PolicyPath()
	if err != nil {
		t.Fatalf("PolicyPath: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading policy file: %v", err)
	}
	if string(data) != string(policy) {
		t.Errorf("materialized policy does not match embedded policy")
	}
	p2, err := PolicyPath()
	if err != nil {
		t.Fatalf("second PolicyPath: %v", err)
	}
	if p2 != p {
		t.Errorf("PolicyPath not stable: got %q then %q", p, p2)
	}
}

// TestCombinedWithRejectsUnknownSyscall (M3): a per-language extra kafel
// cannot compile must fail HERE, not at jail startup. Without this check
// nsjail exits 255 — byte-identical to a missing binary or a user program
// exiting 255 — and the run misreads as build_failed/runtime_error. The
// error must name the offender so the operator can fix the YAML, and it
// must flow through nsjailArgs as Infra (internal_error), which the
// end-to-end test below locks.
func TestCombinedWithRejectsUnknownSyscall(t *testing.T) {
	for _, bad := range []string{"nosuchsyscall_xyz", "CHMOD", "SYSCALL[]", "SYSCALL[x]", "umount2"} {
		if _, err := CombinedWith([]string{bad}); err == nil {
			t.Errorf("CombinedWith(%q) = nil error, want rejection", bad)
		} else if !strings.Contains(err.Error(), strings.TrimSpace(bad)) {
			t.Errorf("CombinedWith(%q) error %q must name the offender", bad, err)
		}
	}

	// Valid shapes still merge: a table name, the numeric form (the umount2
	// workaround), and empties (defensive: ParseSyscallNames never emits
	// them, but direct callers might).
	for _, tc := range [][]string{
		{"chmod"},
		{"SYSCALL[166]"},
		{"chmod", "SYSCALL[166]"},
		{""},
		{},
	} {
		if _, err := CombinedWith(tc); err != nil {
			t.Errorf("CombinedWith(%q) = %v, want success", tc, err)
		}
	}
}

// TestSyscallTableInSync guards the generated table against drift: the Go
// set must equal the vendored kafel amd64 table exactly, or CombinedWith
// could reject a name kafel accepts (false internal_error) or accept one
// kafel rejects (back to the misleading 255). Skips when the nsjail
// submodule is absent (CI checks out without submodules); re-run
// scripts/gen-seccomp-table.sh after any submodule bump.
func TestSyscallTableInSync(t *testing.T) {
	f := filepath.Join("..", "..", "external", "nsjail", "kafel", "src", "syscalls", "amd64_syscalls.c")
	data, err := os.ReadFile(f)
	if err != nil {
		t.Skipf("vendored kafel table absent (%v); run with submodules for the sync check", err)
	}
	re := regexp.MustCompile(`(?m)^\s+\{"([a-z0-9_]+)",`)
	got := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		got[m[1]] = true
	}
	if len(got) < 300 {
		t.Fatalf("extracted only %d names from kafel table, extraction is broken", len(got))
	}
	for n := range got {
		if !validSyscalls[n] {
			t.Errorf("kafel name %q missing from validSyscalls (re-run scripts/gen-seccomp-table.sh)", n)
		}
	}
	for n := range validSyscalls {
		if !got[n] {
			t.Errorf("validSyscalls has %q absent from kafel table (re-run scripts/gen-seccomp-table.sh)", n)
		}
	}
}
