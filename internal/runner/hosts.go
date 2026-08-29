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

// maskedResolvContent is the jail /etc/resolv.conf. It contains NO nameserver,
// so DNS resolution inside the jail fails and cannot be used as an
// exfiltration/C2 channel (a DNS-tunneling read from the host's resolver was
// the open finding). A comment-only file keeps the file a valid empty config
// (runtimes that read it don't trip) while providing no resolver.
const maskedResolvContent = "# goboxd jail: no DNS (nameservers removed to block exfiltration)\n"

// maskedNsswitchContent is the jail /etc/nsswitch.conf. The host config lists
// `resolve` / `mdns` / `dns` under `hosts:`, which route hostname lookups to
// system services/resolvers regardless of /etc/resolv.conf. The jail copy
// rewrites `hosts:` to use ONLY `files` (the masked /etc/hosts -> localhost
// only), so there is no NSS resolver to leak a DNS-tunneling channel. passwd /
// group / shadow stay on `files` so runtimes still resolve users locally.
const maskedNsswitchContent = `passwd: files
group: files
shadow: files
gshadow: files

hosts: files
networks: files
protocols: files
services: files
ethers: files
rpc: files
`

var (
	hostsOnce    sync.Once
	hostsPath    string
	hostsPathErr error

	// resolvOnce materializes the masked resolv.conf once per process.
	resolvOnce    sync.Once
	resolvPath    string
	resolvPathErr error

	// nsswitchOnce materializes the masked nsswitch.conf once per process.
	nsswitchOnce    sync.Once
	nsswitchPath    string
	nsswitchPathErr error
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

// maskedResolvPath writes a nameserver-free resolv.conf once and returns its
// path. It is bind-mounted into every jail at /etc/resolv.conf (after the broad
// -B /etc bind) so jailed code has no resolver to query, closing the DNS
// exfiltration channel. The file is world-readable and carries no data.
func maskedResolvPath() (string, error) {
	resolvOnce.Do(func() {
		f, err := os.CreateTemp("", "goboxd-resolv-*.masked")
		if err != nil {
			resolvPathErr = fmt.Errorf("creating masked resolv temp file: %w", err)
			return
		}
		resolvPath = f.Name()
		if _, err := f.WriteString(maskedResolvContent); err != nil {
			_ = f.Close()
			resolvPathErr = fmt.Errorf("writing masked resolv temp file: %w", err)
			return
		}
		if err := f.Close(); err != nil {
			resolvPathErr = fmt.Errorf("closing masked resolv temp file: %w", err)
			return
		}
		if err := os.Chmod(resolvPath, 0o644); err != nil {
			resolvPathErr = fmt.Errorf("chmod masked resolv temp file: %w", err)
			return
		}
	})
	return resolvPath, resolvPathErr
}

// maskedNsswitchPath writes a sanitized nsswitch.conf once (hosts: files only)
// and returns its path, bind-mounted into every jail at /etc/nsswitch.conf so
// hostname lookups cannot route to a system resolver (closing the DNS channel).
func maskedNsswitchPath() (string, error) {
	nsswitchOnce.Do(func() {
		f, err := os.CreateTemp("", "goboxd-nsswitch-*.masked")
		if err != nil {
			nsswitchPathErr = fmt.Errorf("creating masked nsswitch temp file: %w", err)
			return
		}
		nsswitchPath = f.Name()
		if _, err := f.WriteString(maskedNsswitchContent); err != nil {
			_ = f.Close()
			nsswitchPathErr = fmt.Errorf("writing masked nsswitch temp file: %w", err)
			return
		}
		if err := f.Close(); err != nil {
			nsswitchPathErr = fmt.Errorf("closing masked nsswitch temp file: %w", err)
			return
		}
		if err := os.Chmod(nsswitchPath, 0o644); err != nil {
			nsswitchPathErr = fmt.Errorf("chmod masked nsswitch temp file: %w", err)
			return
		}
	})
	return nsswitchPath, nsswitchPathErr
}
