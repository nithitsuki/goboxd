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

// Version is the semantic version of the service.
var Version = "0.1.0"

// Commit is the short git SHA the binary was built from, or "dev" when the
// build was not stamped.
var Commit = "dev"

// BuildDate is the UTC build timestamp (RFC3339), or empty when not stamped.
var BuildDate = ""
