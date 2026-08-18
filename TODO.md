# goboxd TODO

The mission: be the best code execution engine. Run lots of code fast and safe. goboxd is a pure executor, not a judge. Its job is a contract of trust: enforce the requested limits exactly, return a precise and complete result, and get out of the way (fast, no state).

The bundled package (the judge layer) wraps /run and owns everything that needs state. The tiers below are ordered by value ÷ effort. Items within a tier have the same order.

## P0 — Fix the contract (stateless, mostly bug fixes)

1. [x] **Client-disconnect cancellation** — shipped 2026-08-15. Request ctx threaded through ExecuteRun/runBuild/runSingleTest/execInJail; WithTimeout derives from it; cancelled runs free uid/jail/cgroup/slot immediately (nsjail PDEATHSIG cascade). New "cancelled" status recorded in metrics; handler skips the response write. Verified: TestExecuteRunContextCancel (60s wall, 1s cancel → ~4s kill, uid reusable, no jail dirs) + TestHandleRunContextCancel, both graded by breaking the code; sudo runner suite green, golangci-lint clean.
2. [x] **Bounded admission with 503** — shipped 2026-08-15. Blocking semaphore replaced by an admission gate: N=GOBOXD_MAX_JOBS in-flight + M=GOBOXD_MAX_QUEUED queued (default M=N, M=0 disables the queue). Overflow gets 503 + Retry-After: 1 + queue_full; a queued request frees its ticket on disconnect. Verified: 6 deterministic gate unit tests (wakeup, cancel-frees-ticket, M=0), 503 + queued-cancel handler tests, race + sudo suites green, graded by breaking broadcast-close and queued--.
3. [x] **CPU time via the cgroup cpu controller** — shipped 2026-08-15. +cpu in subtree_control with a charging probe (inert cpu degrades to the rlimit path, never unenforced), per-exec cpu.stat baselines, a merged 50ms OOM+CPU poller that kills CPU-exceeded jails before wall time, cpu_time_s request limit (downward-only), cpu_time_ms in results, cpu_time_exceeded status, and the durationMs >= (wallTime-1)*1000 heuristic is deleted. --rlimit_cpu arm is exactly-one-authority per exec (kernel SIGKILLs at soft==hard). Verified: sudo runner tests (cpu kill ~2.1s vs 9s wall, cpu reporting), full integration suite 52.8s, inert-cpu simulation grading, golangci-lint clean.
4. [x] **Enrich the response contract now** — shipped 2026-08-15. exit_code and termination_signal land on every test result alongside the computed status (cpu_time_ms landed with P0-3). Raw facts: user exit codes propagate, signal deaths read 128+signal, goboxd kills read -1+signal. The bundle can map to Judge0's vocabulary from facts, not from goboxd's interpretation. Verified: exitFacts table (13 cases incl. boundaries), sudo tests pinning all six shapes, live curl (3,0) and (139,11), nsjail propagation confirmed in vendored subproc.cc.
5. [x] **Graceful shutdown + drain** — shipped 2026-08-15. SIGTERM/SIGINT stop admission, drain in-flight jails within GOBOXD_SHUTDOWN_TIMEOUT (default 10s), force-close on deadline (the P0-1 path kills the jails), sweep only after the drain, exit 0. A second signal forces the shutdown at once. readyz reports 503 shutting_down during the drain. Verified: 3 integration shutdown tests (drain, force-close, second-signal), full suite 64.7s, zero leftovers, graded by breaking gate.Stop and the force-close.
6. [x] **Environment allowlist into the jail** — shipped 2026-08-15. jailEnv() builds exactly PATH (copied from the server env, fallback to the hardcode), HOME=/tmp, GOCACHE=/tmp/go-cache, LANG/LC_ALL=C.UTF-8. nsjail clears the child env by default; the contract test pins the exact key set and the absence of planted secrets (GOBOXD_*, proxy, AWS_*) so an nsjail behavior change cannot leak the server env. Verified: sudo contract test, graded by injecting -E SECRET_TEST=1.

## P1 — Throughput (runs lots of code, fast)

7. [x] **Parallel tests within a request (opt-in, bounded)** — shipped 2026-08-18. New max_parallel field on RunRequest (int, 0=sequential). Each parallel test gets its own jail dir, uid, and cgroup leaf. Semaphore bounds concurrency by CPU count. Results returned in test order. Verified: TestExecuteRunParallel (parallel 1s vs sequential 2s), all runner suites green, lint clean, G4 review SHIP.
8. [ ] **Per-UID build caches** — the uid pool already gives per-uid identity. Give each UID a persistent cache dir (GOCACHE, ccache) reused across runs on that UID. Repeated C++/Rust/Go builds become ~10× faster after first use. Security caveat to work through explicitly: a shared writable cache is a cross-submission contamination vector. Per-UID isolation contains it, but cache poisoning via compiler bugs is a real (if unlikely) risk. Document the tradeoff before shipping.
9. [ ] **Configurable output caps** — 64 KiB (maxOutputBytes, internal/runner/runner.go:58) is a hardcoded constant. Make it per-request with a hard ceiling. Platforms will need both more (LeetCode-style large outputs) and less (their own limits).
10. [ ] **Upward limit overrides within per-language ceilings** — downward-only (validateStageLimits, internal/api/handlers.go:545) is a good security stance, but the platform is the authority here. Add a per-language limit_ceiling in the registry so clients can raise limits up to the measured-safe ceiling. This is what makes the bundle able to sell "per-submission resource tuning" (a Judge0 feature).
11. [ ] **Registry hot-reload (SIGHUP)** — currently loaded once (internal/config/config.go:78). The bundle should not have to restart the executor to add or disable a language mid-contest.

## P2 — Hardening depth (securely)

12. [ ] **Per-language seccomp profiles** — the deny-list is global (seccomp.policy). nsjail supports --seccomp_string per invocation. Python does not need ptrace. Add optional per-language profiles in the registry. Genuine differentiator — Judge0/isolate has no seccomp at all.
13. [ ] **Mask /proc and /etc/hosts** — --proc_path /proc exposes /proc/sys, mounts, and host kernel details to every jail. The container's /etc/hosts leaks hostname. Synthetic minimal /proc is effort, but reading the current exposure in docs/security.md and deciding is mandatory before public deployment.
14. [ ] **Leak/soak tests** — fd, goroutine, /tmp growth, and cgroup-leaf leaks over N×1000 runs, in CI. Executors die slowly. This is the test that catches it. The fixture corpus is the moat — extend it (add a fuzz target for POST /run parsing while you are at it).
15. [ ] **/readyz fixups** — cache per-language probes with a TTL (a watchdog polling /readyz every 5s currently spawns 30 subprocesses each time) and stop hardcoding nsjail version "3.6" (internal/api/handlers.go:266).
16. [ ] **Measurable SLOs + benchmark regression in CI** — publish per-language baselines (docs/benchmarks.md exists. Add go test -bench regression gates) with targets like "jail setup p50 < X ms", "N runs/min/core", "0 leaks over 24h soak". "Fast" needs numbers you can defend.

## What the executor must not build

Batch endpoints, callbacks, checkers, submission history, tokens — all of it lives in the bundle, wrapping /run. To make that wrapping trivial, the two P0 items that matter most are #3 (cpu_time) and #4 (raw exit_code/signal). They are the difference between the bundle reimplementing Judge0's status mapping and being able to.

## Language backlog

Missing languages:

- **Mainstream**: clojure, cobol, coffeescript, crystal, dash, dotnet, dragon, emacs (elisp), freebasic, groovy, julia, nasm, nim, octave, odin, ponylang, prolog, pure, pwsh, raku, smalltalk, vlang, zig
- **julia**: the host has only a juliaup launcher with no installed channel (half-install).

Notes:

- One pinned version per language, matching the current YAML registry design. Multi-version (semver, for example python 2.7.18 + 3.12.0) is out of scope for the executor roadmap.
- Reuse the `scripts/lang_install/` pattern. Each language needs an install script, a YAML entry, and fixture tests (tests/testcases/{lang}/)
- The deleted `need-to-port-payloads/` archive contained read-N-print-N*2 fixtures for 34 languages. Regenerate from piston's packages/ tests if needed.
- Once every language above ships, replace the per-layer installs with a prebuilt golden languages image (Dockerfile.langs built on a schedule, pushed to GHCR with dated tags). Then goboxd builds with zero downloads (decision 2025-08-13).

## Shipped — prior phases

### Phase 1 — Security hardening (all shipped 2026-08-14)

- [x] **Multi-uid execution** — each jail runs as a distinct unprivileged host uid from `internal/uidpool` (dual nsjail uid map `U:U:1` + `0:0:1`, 0700 jail dirs owned by the jail uid). An escape yields only uid U, never root.
- [x] **Actually load the seccomp policy** — `internal/seccomp/seccomp.policy` is embedded in the binary and passed to every jail via `--seccomp_policy`. Workarounds for kafel quirks: `//` comments only, and `umount2` referenced as `SYSCALL[166]` (kafel's amd64 table lacks it. nsjail vendors its own maintained kafel fork, and the fork's table still lacks umount2). nsjail submodule bumped 3.4 → 3.6 in the same change.
- [x] **Downward-only per-request limits** — client-requested build/run limits must be positive and at or below the configured YAML maxima. The server rejects over-limit requests with HTTP 400 (`limit_exceeded`), non-positive values with `invalid_limit`, and interpreted languages reject build limits.
- [x] **Slowloris mitigation** — ReadHeaderTimeout 10s, ReadTimeout 60s, IdleTimeout 120s via `api.NewServer`. `TestNewServerTimeouts` pins the timeouts AND the listen addr (the addr omission once silently bound :http).
- [x] **TOCTOU symlink protection** — writeSource uses O_EXCL|O_NOFOLLOW. `TestWriteSourceRejectsSymlink` proves a planted symlink fails the open.
- [x] **Full cgroup v2 support** — per-jail cgroup dirs enforce memory.max/pids.max with per-jail memory.peak and OOM-vs-timeout classification (`internal/cgroupv2`). Startup probe proves real charging. Any probe failure falls back to the always-present rlimit path (limits never unenforced). Docker Desktop = documented rlimit fallback.
- [x] **Re-enable excluded VM languages** — csharp (CoreCLR) and elixir (BEAM) now run with raised per-language limits measured in the image: csharp 4GB (GC init needs it), elixir 8GB (super carrier + VM reservation) with +S 2:2 scheduler pin. Exclusions removed from docker-compose.yml and scripts/dev-host.sh. All 29 languages advertised and green in docker. Note: the elixir 8GB virtual cap also becomes the cgroup resident cap where cgroup v2 is active - revisit if that is ever a problem.

### Phase 2 — Observability & DX (all shipped 2026-08-14)

- [x] **Structured request logging with trace IDs** — `RequestIDMiddleware` honors/generates X-Request-Id (crypto-random, echoed to the response), and the access log emits one JSON line per request with request_id, method, path, status, duration_ms, run_status.
- [x] **Swagger/OpenAPI docs endpoint** — hand-written embedded OpenAPI 3 spec at GET /openapi.json covering healthz/readyz/info/metrics/dashboard/run schemas.
- [x] **Live DDOS dashboard** — /metrics (in_flight, queue depth, latency histogram, status + error counters) and an embedded /dashboard HTML page polling it every 2s, no external assets.

### Phase 3 — Polish (all shipped 2026-08-14)

- [x] **Fix memory_peak_kb** — superseded by per-jail cgroup `memory.peak` (Hole 14). The global RUSAGE_CHILDREN read remains only as the fallback when cgroup v2 is inactive.
- [x] **models package tests** — JSON contract tests for RunRequest/StageConfig/RunResponse/APIError (field names, pointer semantics, omitempty, error envelope).
- [x] **Fix /readyz breakdown on success** — /readyz always returns status + nsjail + per-language probes (200 vs 503 by health).
- [x] **Per-language smoke probe overrides in YAML** — optional smoke_cmd/smoke_args per language for runtimes whose build/run binary cannot answer --version (csharp uses dotnet --version).
- [x] **source_filename_strategy for Java** — java pins source_filename_strategy: fixed so the server ignores the client's filename and Main.java always matches the public class.
- [x] **docs/languages.md with all registered languages** — verified: the table matches the 29-language registry at that time (gawk added later, 2026-08-14).

### Phase 5 — Languages (shipped pieces)

- [x] **gawk** — YAML + install script + fixtures, host-verified (2026-08-14). The rest of the backlog lives in the Language backlog section above.
