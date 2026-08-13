# goboxd TODO

## Phase 1 — Bug Fixes ✅ (done)

- [x] Fix stale import in e2e_test.go (boxd/models → internal/models)
- [x] Fix run limits bug — compiled languages use build limits instead of run limits for test execution phase
- [x] Fix build defaults — when request omits build.limits, code uses hardcoded values instead of YAML-configured limits
- [x] Fix internal_error returning 500 — per the API contract, internal_error is a response status (HTTP 200), not a 500

## Phase 2 — Language Coverage ✅ (done)

- [x] Add Java to YAML registry + install script + fixtures
- [x] Add Bash to YAML registry + install script + fixtures
- [x] Add JavaScript (Node) to YAML registry + install script + fixtures
- [x] Add Verilog to YAML registry + install script + fixtures
- [x] Add 11 LeetCode languages (py2, csharp, ruby, swift, scala, kotlin, php, ts, racket, elixir, dart)
- [x] Add pascal + opt-in language builds with caching, zero-network jails

## Phase 3 — Interactive Frontend ✅ (done)

- [x] Build playground website (React + Monaco Editor + Bun) for interactive code execution
- [x] Language dropdown, code presets, file upload, real-time output display
- [x] Serve embedded via Go's //go:embed from internal/api/playground-dist/

## Phase 4 — Security Hardening

- [ ] **Multi-uid execution** — run each job as a different unprivileged uid inside the jail (piston's isolate model). The single most valuable sandbox property goboxd lacks: today every jail runs as the container user, so one escape is a container-wide compromise.
- [ ] **Actually load the seccomp policy** — `scripts/seccomp.policy` exists (kafel, denies mount/ptrace/kernel-module ops) but no Go code references it and the nsjail invocation never passes `--seccomp_policy`. Either wire it up or delete the Hole 11 claim from docs/security.md — right now it's a false audit statement. (TODO note from earlier: nsjail 3.4 kafel parser limits may require upgrading nsjail or a different seccomp approach.)
- [ ] **Downward-only per-request limits** — clients can currently raise build/run limits up to configured maxima; switch to piston's model where per-request limits can only go *down*, never up.
- [ ] **Full cgroup v2 support** — replace the `--rlimit_as` / `--rlimit_nproc` fallbacks with real cgroup v2 memory + pids controllers (memory.peak tracking, OOM kill detection). Screw Docker Desktop — it doesn't expose a writable cgroup hierarchy; drop the workaround and document it as unsupported.
- [ ] Add Slowloris mitigation (ReadHeaderTimeout, etc.)
- [ ] Add TOCTOU symlink protection (O_EXCL | O_NOFOLLOW)

## Phase 5 — Observability & DX

- [ ] Add Swagger/OpenAPI docs endpoint
- [ ] Build a live DDOS dashboard showing real-time metrics (in_flight, queue depth, latency heatmap, error rate)
- [ ] Add structured request logging with trace IDs

## Phase 6 — Infrastructure

- [ ] Design orchestrator that spins up goboxd containers on demand
- [ ] Add load balancer layer distributing requests across containers
- [ ] Add horizontal auto-scaling based on queue depth / in_flight jobs
- [ ] Health check integration with orchestrator

## Phase 7 — Polish

- [ ] Fix memory_peak_kb to use per-process (not global RUSAGE_CHILDREN)
- [ ] Add per-language smoke probe overrides in YAML
- [ ] Add source_filename_strategy / artifact_filename_strategy for Java
- [ ] Fix /readyz to return full breakdown on success
- [ ] Add models package tests
- [ ] Update docs/languages.md with all registered languages

## Phase 8 — Auth

- [ ] Add lightweight optional auth: env-configured API key(s) checked via constant-time compare in one middleware — no framework, no dependency bloat, disabled by default (empty key = open)
- [ ] Document the production recommendation: terminate TLS at a reverse proxy (nginx/caddy), rate limiting there, auth token required for /run (GET endpoints stay open for health checks)
- [ ] Keep the Go codebase lean — auth must not burden the core runner

## Phase 9 — Piston Language Parity

Goal: support all 76 languages from piston's registry (currently 26 of 76).

Missing 50 languages:

- **Esolangs**: befunge93, bqn, brachylog, brainfuck, cjam, cow, emojicode, forte, forth, golfscript, husk, japt, jelly, lolcode, MATL, osabie, paradoc, pyth, retina, rockstar, samarium, vyxal, yeethon
- **Mainstream**: clojure, cobol, coffeescript, crystal, dash, deno, dotnet, dragon, emacs (elisp), freebasic, gawk, groovy, julia, nasm, nim, octave, ponylang, prolog, pure, pwsh, raku, smalltalk, sqlite3, vlang, zig
- **Misc**: file (arbitrary binary execution), llvm_ir

Notes:
- Multi-version support (semver, e.g. python 2.7.18 + 3.12.0) is out of scope for now — one pinned version per language, matching the current YAML registry design
- Reuse `scripts/lang_install/` pattern; each language needs an install script, YAML entry, and fixture tests (tests/testcases/{lang}/)
- The deleted `need-to-port-payloads/` archive contained read-N-print-N*2 fixtures for 34 languages — regenerate from piston's packages/ tests if needed
