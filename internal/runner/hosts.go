// Masked hosts file (P2-13): the jail's /etc/hosts must never leak the host
// or container hostname, so we bind-mount a minimal localhost-only file over
// it. A containerized deployment's /etc/hosts carries the container ID and a
// randomized hostname; exposing it to untrusted code fingerprints the host.
// The minimal file only names localhost (IPv4 and IPv6), which is all a
// network-isolated jail needs (the jail has no loopback and no network).
package runner

import (
	"fmt"
	"os"
	"sync"
)

// maskedHostsContent is the localhost-only /etc/hosts payload mounted into
// every jail. It is deliberately free of any host or container identity:
// IPv4 and IPv6 loopback names map to "localhost" only.
const maskedHostsContent = "127.0.0.1 localhost\n::1 localhost\n"

var (
	hostsOnce    sync.Once
	hostsPath    string
	hostsPathErr error
)

// maskedHostsPath writes the minimal hosts payload to a stable temp file on
// first call and returns its path. Later calls return the same path, so every
// concurrent jail bind-mounts the identical file (nsjail --bindmount_ro
// source, one materialization, no per-request file churn). The file is
// world-readable; only its content is sensitive-adjacent, and it carries no
// identifying data.
//
// The file is intentionally left on disk for the process lifetime (never
// removed), matching the seccomp PolicyPath temp file: it is a few bytes,
// identity-free, and a stable per-process path shared by every jail. Cleaning
// it would reintroduce per-request materialization churn for no security
// benefit (its content carries no host identity).
func maskedHostsPath() (string, error) {
	hostsOnce.Do(func() {
		f, err := os.CreateTemp("", "goboxd-hosts-*.masked")
		if err != nil {
			hostsPathErr = fmt.Errorf("creating masked hosts temp file: %w", err)
			return
		}
		hostsPath = f.Name()
		if _, err := f.WriteString(maskedHostsContent); err != nil {
			_ = f.Close()
			hostsPathErr = fmt.Errorf("writing masked hosts temp file: %w", err)
			return
		}
		if err := f.Close(); err != nil {
			hostsPathErr = fmt.Errorf("closing masked hosts temp file: %w", err)
			return
		}
		// os.CreateTemp writes with mode 0600; the jailed unprivileged uid must
		// be able to read the bind-mounted file, so make it world-readable. The
		// content is deliberately identity-free (localhost only), so 0644 leaks
		// nothing.
		if err := os.Chmod(hostsPath, 0o644); err != nil {
			hostsPathErr = fmt.Errorf("chmod masked hosts temp file: %w", err)
			return
		}
	})
	return hostsPath, hostsPathErr
}
