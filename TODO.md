# goboxd TODO

Items within each phase are ordered by value ÷ effort (highest first).

## Phase 1 — Security Hardening

- [x] **Multi-uid execution** — each jail runs as a distinct unprivileged host uid from `internal/uidpool` (dual nsjail uid map `U:U:1` + `0:0:1`, 0700 jail dirs owned by the jail uid). An escape yields only uid U, never root. Shipped 2026-08-14.
- [x] **Actually load the seccomp policy** — `internal/seccomp/seccomp.policy` is embedded in the binary and passed to every jail via `--seccomp_policy` (2026-08-14). kafel quirks worked around: `//` comments only, and `umount2` referenced as `SYSCALL[166]` (kafel's amd64 table lacks it; nsjail vendors its own maintained kafel fork, and the fork's table still lacks umount2). nsjail submodule bumped 3.4 → 3.6 in the same change.
- [x] **Downward-only per-request limits** — client-requested build/run limits must be positive and at or below the configured YAML maxima; over-limit requests are rejected with HTTP 400 (`limit_exceeded`), non-positive values with `invalid_limit`, and interpreted languages reject build limits. Shipped 2026-08-14.
- [x] Add Slowloris mitigation (ReadHeaderTimeout 10s, ReadTimeout 60s, IdleTimeout 120s via `api.NewServer`; `TestNewServerTimeouts` pins the timeouts AND the listen addr — the addr omission once silently bound :http). Shipped 2026-08-14.
- [x] Add TOCTOU symlink protection (writeSource uses O_EXCL|O_NOFOLLOW; `TestWriteSourceRejectsSymlink` proves a planted symlink fails the open). Shipped 2026-08-14.
- [x] **Full cgroup v2 support** — per-jail cgroup dirs enforce memory.max/pids.max with per-jail memory.peak and OOM-vs-timeout classification (`internal/cgroupv2`). Startup probe proves real charging; any probe failure falls back to the always-present rlimit path (limits never unenforced). Docker Desktop = documented rlimit fallback. Shipped 2026-08-14.
- [ ] **Re-enable excluded VM languages** — the tight RLIMIT_AS guard (now real, after the MB-units fix that made limits ~1024x too loose) cannot host CoreCLR (csharp) and BEAM (elixir): they reserve more virtual address space than any tight cap allows. They are excluded via GOBOXD_EXCLUDE_LANGS. Revisit when cgroup v2 memory.max is the primary enforcement and rlimit_as can be loosened, or ship raised per-language limits. Related: scala/racket/go/js/r limits were raised to their measured virtual needs (see config/languages.yml).

## Phase 2 — Observability & DX

- [x] Add structured request logging with trace IDs — `RequestIDMiddleware` honors/generates X-Request-Id (crypto-random, echoed to the response), and the access log emits one JSON line per request with request_id, method, path, status, duration_ms, run_status. Shipped 2026-08-14.
- [x] Add Swagger/OpenAPI docs endpoint — hand-written embedded OpenAPI 3 spec at GET /openapi.json covering healthz/readyz/info/metrics/dashboard/run schemas. Shipped 2026-08-14.
- [x] Build a live DDOS dashboard — /metrics (in_flight, queue depth, latency histogram, status + error counters) and an embedded /dashboard HTML page polling it every 2s, no external assets. Shipped 2026-08-14.

## Phase 3 — Polish

- [x] Fix memory_peak_kb — superseded by per-jail cgroup `memory.peak` (Hole 14). The global RUSAGE_CHILDREN read remains only as the fallback when cgroup v2 is inactive.
- [ ] Add models package tests
- [ ] Fix /readyz to return full breakdown on success
- [ ] Add per-language smoke probe overrides in YAML
- [ ] Add source_filename_strategy / artifact_filename_strategy for Java
- [ ] Update docs/languages.md with all registered languages

## Phase 4 — Auth

- [ ] Add lightweight optional auth: env-configured API key(s) checked via constant-time compare in one middleware — no framework, no dependency bloat, disabled by default (empty key = open)
- [ ] Document the production recommendation: terminate TLS at a reverse proxy (nginx/caddy), rate limiting there, auth token required for /run (GET endpoints stay open for health checks)
- [ ] Keep the Go codebase lean — auth must not burden the core runner

## Phase 5 — Missing Languages Support

Missing languages:

- **Mainstream**: clojure, cobol, coffeescript, crystal, dash, deno, dotnet, dragon, emacs (elisp), freebasic, gawk, groovy, julia, nasm, nim, octave, ponylang, prolog, pure, pwsh, raku, smalltalk, sqlite3, vlang, zig
- **Misc**: file (arbitrary binary execution), llvm_ir

Notes:
- Multi-version support (semver, e.g. python 2.7.18 + 3.12.0) is out of scope for now — one pinned version per language, matching the current YAML registry design
- Reuse `scripts/lang_install/` pattern; each language needs an install script, YAML entry, and fixture tests (tests/testcases/{lang}/)
- The deleted `need-to-port-payloads/` archive contained read-N-print-N*2 fixtures for 34 languages — regenerate from piston's packages/ tests if needed
- Once every language above ships, replace the per-layer installs with a prebuilt golden languages image (Dockerfile.langs built on a schedule, pushed to GHCR with dated tags); goboxd then builds with zero downloads (decision 2025-08-13)
