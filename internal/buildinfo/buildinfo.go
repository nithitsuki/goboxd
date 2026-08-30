// Package buildinfo carries build-time metadata surfaced by GET /info.
//
// The values are injected at link time via -ldflags -X (see the Dockerfile and
// scripts/build.sh). The defaults keep a plain `go build` working: such a
// binary reports Commit "dev" and HandleInfo falls back to the Go toolchain's
// VCS stamping when it is available.
//
// Docker builds cannot stamp vcs.revision (the build context has no .git), so
// the ldflags path is the only way a containerized binary reports its commit.
package buildinfo

import (
	"strconv"
	"strings"
)

// Version is the semantic version of the service. Bump it when a deployable
// feature lands; the frontend and operators compare against it with
// AtLeast("...", "minimum") instead of trusting a raw commit hash.
var Version = "0.2.0"

// Commit is the short git SHA the binary was built from, or "dev" when the
// build was not stamped.
var Commit = "dev"

// BuildDate is the UTC build timestamp (RFC3339), or empty when not stamped.
var BuildDate = ""

// AtLeast reports whether the version `current` is numerically at least the
// version `minimum`, using an x.y.z comparison. Prerelease/build suffixes are
// ignored, so "0.2.0-beta.1" counts as "0.2.0". Meant for a lightweight
// "minimum versioning" floor, not a full semver resolution.
func AtLeast(current, minimum string) bool {
	a := parts(current)
	b := parts(minimum)
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

// parts splits an x.y.z version string into its numeric components, treating
// missing or non-numeric segments as zero.
func parts(v string) [3]int {
	var out [3]int
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i] // drop "-..." / "+..." metadata
	}
	for i, s := range strings.SplitN(v, ".", 3) {
		if i >= len(out) {
			break
		}
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 {
			out[i] = n
		}
	}
	return out
}
