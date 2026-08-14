package seccomp

import (
	"os"
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
