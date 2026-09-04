// Package seccomp embeds the jail seccomp policy and materializes it to a
// temp file so it can be passed to nsjail's --seccomp_policy flag, and merges
// per-language additions into a combined inline policy for --seccomp_string.
//
// nsjail compiles the policy with kafel at jail startup and applies the
// resulting seccomp-bpf filter to the jailed process. The policy is a
// deny-list with DEFAULT ALLOW, so normal code execution is unaffected.
//
// ADDITIVE-MERGE (P2-12): a per-language `seccomp:` directive only ADDS deny
// syscalls on top of the embedded global deny-list. CombinedWith builds a
// policy that always contains the full global deny-list plus the extras, so a
// language profile can never weaken the global policy.
package seccomp

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

//go:embed seccomp.policy
var policy []byte

var (
	once    sync.Once
	path    string
	pathErr error
)

// PolicyPath writes the embedded policy to a stable temp file on first call
// and returns its path. Subsequent calls return the same path. nsjail
// requires a file path (--seccomp_policy FILE), not inline bytes.
func PolicyPath() (string, error) {
	once.Do(func() {
		f, err := os.CreateTemp("", "goboxd-seccomp-*.policy")
		if err != nil {
			pathErr = fmt.Errorf("creating seccomp policy temp file: %w", err)
			return
		}
		path = f.Name()
		if _, err := f.Write(policy); err != nil {
			_ = f.Close()
			pathErr = fmt.Errorf("writing seccomp policy: %w", err)
			return
		}
		if err := f.Close(); err != nil {
			pathErr = fmt.Errorf("closing seccomp policy: %w", err)
			return
		}
	})
	return path, pathErr
}

// ParseSyscallNames splits a per-language `seccomp:` directive (whitespace- or
// comma-separated) into individual syscall names. Names are not validated
// here; CombinedWith rejects unknown ones (M3).
func ParseSyscallNames(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

// syscallNumRe matches kafel's numeric syscall form (e.g. SYSCALL[166],
// the umount2 workaround: kafel's amd64 table has no such name). It is the
// only non-name shape CombinedWith accepts.
var syscallNumRe = regexp.MustCompile(`^SYSCALL\[\d+\]$`)

// validSyscall reports whether kafel accepts the extra on amd64: either a
// name from its table (validSyscalls, generated from the vendored kafel) or
// the numeric SYSCALL[n] form.
func validSyscall(name string) bool {
	return validSyscalls[name] || syscallNumRe.MatchString(name)
}

// CombinedWith returns a kafel policy equivalent to the embedded global policy
// (the same POLICY name and the same `USE <name> DEFAULT <action>` tail) but
// with extraSyscalls appended to the DENY block. It is purely additive:
// every entry in the global deny-list is always present, so a per-language
// profile can never weaken the global policy. Extras already present in the
// global list are skipped (dedup). The extras are raw kafel syscall names or
// expressions (e.g. "chmod" or "SYSCALL[166]").
func CombinedWith(extraSyscalls []string) ([]byte, error) {
	// M3: reject unknown names up front. kafel would fail to compile the
	// policy at jail startup and nsjail would exit 255 — byte-identical to a
	// missing binary or a user program exiting 255, i.e. a misleading
	// build_failed/runtime_error. Failing here returns through nsjailArgs as
	// Infra, which both interpreters map to internal_error: an operator
	// config error reads as one.
	for _, e := range extraSyscalls {
		e = strings.TrimSpace(e)
		if e == "" || validSyscall(e) {
			continue
		}
		return nil, fmt.Errorf("seccomp: unknown syscall %q (not in kafel amd64 table; numeric SYSCALL[n] form is accepted)", e)
	}
	names, header, name, defAction, err := parseGlobalPolicy()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		seen[n] = true
	}
	for _, e := range extraSyscalls {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		names = append(names, e)
		seen[e] = true
	}

	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "POLICY %s {\n\tDENY {\n", name)
	for i, n := range names {
		b.WriteString("\t\t" + n)
		if i < len(names)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("\t}\n}\n\n")
	fmt.Fprintf(&b, "USE %s DEFAULT %s\n", name, defAction)
	return []byte(b.String()), nil
}

// parseGlobalPolicy extracts the deny-list entry names, the header comment,
// the policy name, and the default action from the EMBEDDED policy.
func parseGlobalPolicy() (names []string, header, name, defAction string, err error) {
	return parseGlobalPolicyBytes(policy)
}

// parseGlobalPolicyBytes parses any kafel policy of the same shape as the
// embedded global policy. Splitting on top-level commas is safe here: the
// deny-list has no nested braces (only SYSCALL[166] square brackets).
func parseGlobalPolicyBytes(pol []byte) (names []string, header, name, defAction string, err error) {
	s := string(pol)
	polIdx := strings.Index(s, "POLICY ")
	if polIdx < 0 {
		return nil, "", "", "", fmt.Errorf("global seccomp policy: missing POLICY declaration")
	}
	header = s[:polIdx]

	// POLICY <name> { — grab the policy name.
	afterPol := s[polIdx+len("POLICY "):]
	nameEnd := strings.IndexAny(afterPol, " \t{")
	if nameEnd < 0 {
		return nil, "", "", "", fmt.Errorf("global seccomp policy: malformed POLICY declaration")
	}
	name = strings.TrimSpace(afterPol[:nameEnd])

	blockIdx := strings.Index(s[polIdx:], "DENY {")
	if blockIdx < 0 {
		return nil, "", "", "", fmt.Errorf("global seccomp policy: missing DENY block")
	}
	bodyStart := polIdx + blockIdx + len("DENY {")
	bodyEndRel := strings.Index(s[bodyStart:], "}")
	if bodyEndRel < 0 {
		return nil, "", "", "", fmt.Errorf("global seccomp policy: DENY block not closed")
	}
	body := s[bodyStart : bodyStart+bodyEndRel]

	// Strip kafel // comments, then split the comma-separated entry list.
	var clean []string
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		clean = append(clean, line)
	}
	joined := strings.Join(clean, "\n")
	for _, part := range strings.Split(joined, ",") {
		if n := strings.TrimSpace(part); n != "" {
			names = append(names, n)
		}
	}

	// Derive the default action from the USE <name> DEFAULT <action> tail so a
	// future tightening (e.g. DEFAULT KILL) is never silently weakened.
	if ui := strings.LastIndex(s, "USE "+name+" DEFAULT "); ui >= 0 {
		defAction = strings.TrimSpace(s[ui+len("USE "+name+" DEFAULT "):])
	}
	if defAction == "" {
		return nil, "", "", "", fmt.Errorf("global seccomp policy: missing USE %s DEFAULT <action> tail", name)
	}
	return names, header, name, defAction, nil
}
