# goboxd TODO

Items within each phase are ordered by value ÷ effort (highest first).

## Phase 1 — Security Hardening

- [ ] **Multi-uid execution** — run each job as a different unprivileged uid inside the jail (piston's isolate model). The single most valuable sandbox property goboxd lacks: today every jail runs as the container user, so one escape is a container-wide compromise.
- [x] **Actually load the seccomp policy** — `internal/seccomp/seccomp.policy` is embedded in the binary and passed to every jail via `--seccomp_policy` (2026-08-14). kafel quirks worked around: `//` comments only, and `umount2` referenced as `SYSCALL[166]` (kafel's amd64 table lacks it; nsjail vendors its own maintained kafel fork, and the fork's table still lacks umount2). nsjail submodule bumped 3.4 → 3.6 in the same change.
- [ ] **Downward-only per-request limits** — clients can currently raise build/run limits up to configured maxima; switch to piston's model where per-request limits can only go *down*, never up.
- [ ] Add Slowloris mitigation (ReadHeaderTimeout, etc.)
- [ ] Add TOCTOU symlink protection (O_EXCL | O_NOFOLLOW)
- [ ] **Full cgroup v2 support** — replace the `--rlimit_as` / `--rlimit_nproc` fallbacks with real cgroup v2 memory + pids controllers (memory.peak tracking, OOM kill detection). Screw Docker Desktop — it doesn't expose a writable cgroup hierarchy; drop the workaround and document it as unsupported.

## Phase 2 — Observability & DX

- [ ] Add structured request logging with trace IDs
- [ ] Add Swagger/OpenAPI docs endpoint
- [ ] Build a live DDOS dashboard showing real-time metrics (in_flight, queue depth, latency heatmap, error rate)

## Phase 3 — Polish

- [ ] Fix memory_peak_kb to use per-process (not global RUSAGE_CHILDREN)
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
