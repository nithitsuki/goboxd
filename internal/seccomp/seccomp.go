// Package seccomp embeds the jail seccomp policy and materializes it to a
// temp file so it can be passed to nsjail's --seccomp_policy flag.
//
// nsjail compiles the policy with kafel at jail startup and applies the
// resulting seccomp-bpf filter to the jailed process. The policy is a
// deny-list with DEFAULT ALLOW, so normal code execution is unaffected.
package seccomp

import (
	_ "embed"
	"fmt"
	"os"
	"sync"
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
