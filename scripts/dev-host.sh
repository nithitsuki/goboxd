#!/usr/bin/env bash
# Run goboxd natively on the host for local development (no docker).
#
# Advertises only the languages actually installed on this host. The e2e
# fixture suite skips anything unadvertised automatically (see fixture_test.go).
# Requires root: nsjail needs namespaces, and multi-uid needs chown.
#
# cgroup v2 activates automatically when the host cgroup hierarchy is writable
# (it is, as root, on systemd hosts) and falls back to rlimit enforcement
# otherwise. GOBOXD_EXCLUDE_LANGS keeps the VM runtimes that cannot fit the
# strict RLIMIT_AS guard out of the registry.
set -euo pipefail
cd "$(dirname "$0")/.."

GOBOXD_LANGS="${GOBOXD_LANGS:-py3,c,cpp,bash,js,rust,go,lua,perl,r,py2}"
GOBOXD_EXCLUDE_LANGS="${GOBOXD_EXCLUDE_LANGS:-}"
# Unset = full host resources: GOBOXD_MAX_JOBS defaults to runtime.NumCPU()
# (the uid pool sizes to it) and no per-jail CPU cap is applied.
GOBOXD_MAX_JOBS="${GOBOXD_MAX_JOBS:-}"
GOBOXD_MAX_CPUS="${GOBOXD_MAX_CPUS:-}"
PORT="${PORT:-8080}"

echo "goboxd host dev server: langs=$GOBOXD_LANGS exclude=$GOBOXD_EXCLUDE_LANGS port=$PORT"
exec sudo env \
  GOBOXD_LANGS="$GOBOXD_LANGS" \
  GOBOXD_EXCLUDE_LANGS="$GOBOXD_EXCLUDE_LANGS" \
  GOBOXD_MAX_JOBS="$GOBOXD_MAX_JOBS" \
  GOBOXD_MAX_CPUS="$GOBOXD_MAX_CPUS" \
  PORT="$PORT" \
  ./goboxd
