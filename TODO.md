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
8. [x] **Per-UID build caches** — shipped 2026-08-18. Each uid gets a persistent cache dir at /var/cache/goboxd/uid-<uid>/ with Go GOCACHE and ccache. Cache dirs bind-mounted into the jail at /app/.gocache and /app/.ccache (writable by the jailed uid). Verified: TestBuildCacheFirstRun, TestBuildCacheSecondRun (cache populated with 258 entries), TestBuildCacheIsolation. Cache-poisoning tradeoff documented in docs/security.md.
9. [x] **Configurable output caps** — shipped 2026-08-18. Per-request max_output_bytes field (int, 0=64 KiB default). Hard ceiling via GOBOXD_MAX_OUTPUT_BYTES env (default 1 MB). readCapped takes a per-request limit. Verified: TestReadCapped, TestOutputCapCustom, handler validation, lint clean.
10. [x] **Upward limit overrides within per-language ceilings** — shipped 2026-08-18. New per-stage `ceiling:` block in the registry YAML. Clients can raise limits above the defaults up to the measured-safe ceiling; above-ceiling values get 400 limit_exceeded. Absent ceiling = defaults act as ceiling (backward compatible). Ceilings set for go/rust/java/csharp builds + elixir run. /info exposes ceiling_run_limits. Verified: validation table (between/above/absent), config parse tests, fixtures green for touched langs, lint clean.
11. [x] **Registry hot-reload (SIGHUP)** — shipped 2026-08-18. config.DefaultRegistry became an atomic snapshot (config.Registry() accessor). SIGHUP reloads the YAML registry atomically; a failed parse keeps the old registry. /info, /readyz, /run pick up new languages on the next request. Verified: reload/error tests, concurrent-read race test (-race clean), TestShutdown unaffected, shuffle-safe test restores.

## P2 — Hardening depth (securely)

12. [x] **Per-language seccomp profiles** — shipped 2026-08-20 (additive-merge). New optional per-language `seccomp:` YAML field lists extra deny syscalls (comma/space-separated). seccomp.CombinedWith merges extras into the global deny-list, ALWAYS carrying every global entry (never-weaker invariant, derived policy name/action). A profile is applied via --seccomp_string; languages without one keep --seccomp_policy byte-identical. Mechanism demonstrated by a denied-syscall test (chmod → SIGSYS); ptrace already globally denied so no production profile currently adds value. Verified: exact-set global-coverage test, runtime chmod denial, empty-case byte-identical, graded by dropping a global entry and removing the wiring. M3 follow-up tracked: a bad syscall name surfaces as nsjail failing to start; classifying it as internal_error is a one-line follow-up.
13. [x] **Mask /proc and /etc/hosts** — shipped 2026-08-20. /proc is mounted per-jail (fresh pid ns, no host processes) via --disable_proc + manual mount; /proc/sys is masked with a tmpfs (no kernel config); /etc/hosts is masked to localhost-only (no container hostname). Residual: /proc/mounts leaks the host mount table (nsjail bind API cannot overlay the symlink target); /proc/self/environ shows the allowlisted jail env; proc is mounted rw (nsjail's `-m` forces rw; ro proc via --proc_path would shadow the /proc/sys mask). Bounded: jailed uid has no caps, mount/unshare are seccomp-denied, /proc/sys is masked; only cosmetic /proc/self writes (e.g. comm) are possible. Verified: TestJailProcAndHostsMasked (localhost-only hosts, /proc/sys empty count 0), graded by removing each mask. Decision: Option A (rw proc + masked /proc/sys). /proc must stay mounted: Go/Java/Python/Node read /proc/self/* and /proc/meminfo/cpuinfo at runtime (--disable_proc alone breaks them). Exploration (Option C): resolve the rw-vs-masked /proc/sys conflict by having server-side code mount proc read-only and overlay /proc/sys AFTER nsjail's own mount pass (needs custom mount wiring or an nsjail feature) — open question.
14. [x] **Leak/soak tests** — shipped 2026-09-02. TestSoakNoLeaks snapshots jail dirs (the /tmp-growth signal), cgroup leaves, the server's open fds (/proc/<pid>/fd), and its goroutines (env-gated pprof at /debug/pprof, mounted only when GOBOXD_PPROF=1) before/after N runs, and fails on any growth or leftover. N defaults to 200 fast iterations; GOBOXD_SOAK_ITERATIONS lifts it to 1000+ for deep leak runs. FuzzRunParsing feeds malformed and well-formed POST /run payloads (seeds extended with the P1 contract fields: max_parallel, max_output_bytes, ceilings, advisory builders) and fails on any panic or non-JSON response. Verified: TestSoakNoLeaks + FuzzRunParsing in the root+nsjail harness, TestPprofGated pins the 404-off/200-on pprof gate, lint clean. The fd/goroutine checks run against the local harness server (PID + pprof); a remote API_URL target skips those two.
15. [x] **/readyz fixups + probe module (C4)** — shipped 2026-08-19. One probe module (internal/api/probes.go) with a TTL cache serves both /readyz and /info: a warm cache spawns zero subprocesses (kills the 5s watchdog's 30-exec-per-tick tax). Per-language entries keyed independently so a SIGHUP registry swap re-probes on the next request. nsjail version is discovered from the binary (no hardcoded "3.6"). Probe execs bounded by a 5s timeout (a hung runtime can't wedge the cache mutex process-wide). Verified: TestProbeCacheTTL, TestReadyzCacheWarm, TestInfoSharesReadyzCache, TestNsjailVersionDiscovered, TestReadyzWarmCacheNoSpawn, graded by disabling the cache and restoring the hardcoded version. Subsumes arch review C4.
16. [x] **Measurable SLOs + benchmark regression gates** — shipped 2026-09-02. The docs/benchmarks.md SLO table is now enforced code, not prose: the soak loop records per-request latencies and asserts p50 < 50 ms (the documented jail-setup SLO, Python trivial, single client), and BenchmarkRunThroughput (tests/integration/bench_test.go, `make bench`) reports the per-core runs/sec number. CI wiring itself stayed deferred: the sandbox CI job cannot fit the GitHub runner (ci commit ca4acda), so the gates run locally via `make integration` / `make integration-docker` / `make bench`. Verified: soak p50 assertion + fd/goroutine deltas in the harness, benchmark compiles and reports ns/op (runs/sec), lint clean.

## Architecture review — 2026-08-18 (from /tmp/architecture-review-20260818-2119.html)

Source of truth: the review HTML for full diagrams and rationale. All items
are L-tier (architecture): full gates plus a second security/architecture
review pass and a rollback plan per item. Order per the review's top
recommendation: C1 first (its outcome struct forces C2 and C5 along).

C1. [x] **One Jail module — collapse the execution lifecycle** — shipped 2026-08-18. New internal/runner/jail.go: one Jail type owns uid, dir+chown, cgroup leaf, source, artifact, and teardown (sync.Once, idempotent). Sequential reuses one jail; parallel materializes N jails from one build template (seedFrom copies source + artifact). Fixes three live defects: parallel compiled tests now find the artifact, the uid pool is sized to maxJobs x (NumCPU + 1) (template + parallel jails, exhaustion impossible), and cgroup names are unique by construction (filepath.Base(dir)). Verified: TestParallelCompiled, TestJailTeardown, TestUidPoolParallel, TestConcurrentParallelRequests, all graded by breaking each fix; two review passes (correctness + security/architecture) SHIP; rollback = revert (sequential byte-identical).
C2. [x] **One exec primitive under the jail** — shipped 2026-08-18. New internal/runner/exec.go: execJail returns ExecOutcome{Stdout, Stderr, OOMKilled, CPUKilled, CPUTimeUS, ExitCode, TermSignal, Infra, Err}; runBuild and runSingleTest are thin interpreters. isInfraError text-matching gone (typed Infra at pipe/start sites). Verified: exec-sequence byte-identical on reachable paths, fixture corpus parity (zero new failures), TestExecOutcome{InfraTyped,Fields,InfraStartFailure}, graded by breaking the cpu-kill path. Note: missing binary inside the jail reads exit 255 (user-error shape, nsjail propagates); classifying 255 as infra regressed 10 fixtures, so it stays a user error (fail-closed is a one-line option).
C3. [x] **Stop buffering run responses in the access log** — shipped 2026-08-19. bodyRecorder no longer copies the response body (Write passes through; struct is buffer-free by construction, locked by a reflection test). run_status now travels via an additive X-Run-Status response header set by HandleRun on every status path (200 result, 200 internal_error, 503 queue_full, 503 shutting_down); the log reads the header, so 503s now carry run_status too. The ~100MB-per-request memory spike is gone. Verified: no-buffering + real-path header tests (queue_full, shutting_down, 200), graded by restoring the buffer and removing the 503 header.
C5. [x] **Typed result-status vocabulary** — shipped 2026-08-18. ResultStatus (11 constants) and BuildStatus (3 constants) in models; TestResult.Status and BuildResult.Status typed. ResultStatus("") marshals to not_executed (the parallel-path "" hole is closed); computeTopLevelStatus gates non-valid statuses to not_executed. JSON byte-identical for all real statuses (contract tests + fixture corpus pin the wire strings); a status typo now fails to compile. Verified: TestZeroValueStatusNotExecuted, TestComputeTestStatusTyped, grep gate clean, lint clean.

## What the executor must not build

Batch endpoints, callbacks, checkers, submission history, tokens — all of it lives in the bundle, wrapping /run. To make that wrapping trivial, the two P0 items that matter most are #3 (cpu_time) and #4 (raw exit_code/signal). They are the difference between the bundle reimplementing Judge0's status mapping and being able to.

## Language backlog

All backlog languages shipped 2026-09-02: **clojure, cobol, coffeescript,
crystal, dash, dotnet (Mono), elisp, groovy, julia, nasm, nim,
octave, odin, pony, prolog, pwsh, raku, smalltalk, vlang, zig** — each
with a pinned install script (`scripts/lang_install/`), a registry entry
(`config/languages.yml`), fixture cases (`tests/testcases/{id}/`), a Docker
layer, and docs. julia's half-install blocker is resolved by installing the
official 1.11.2 tarball in the image; smalltalk is not in
Debian bookworm (source build, documented in docs/languages.md).
All install scripts were verified end-to-end in a Debian bookworm container
(2026-09-02).

Deferred with blockers (project precedent: deno, juliaup):

- **dragon**: the only live release (aaveshdev/dragon v1.0.7) has a broken
  print implementation — `show`/`showln` emit nothing to stdout, stderr, or a
  PTY (verified 2026-09-02). A language that cannot produce output cannot
  pass fixtures. The old dragon-lang/dragon repo is gone. Re-add when the
  toolchain's stdout works; the install script and pin layout were verified
  (binary + libdragon_lib.so at the tarball root, `dragon --version` works).
- **pure**: pure 0.68 supports only LLVM 2.5–3.5 (pre-MCJIT JIT; upstream
  port issue open since 2015) and bookworm ships LLVM 13+ — the source build
  fails at `llvm/ExecutionEngine/JIT.h` (verified in a bookworm container
  2026-09-02; no bookworm LLVM can build it). Re-add if pure lands a
  modern-LLVM port; the pinned source tarball is
  agraef/pure-lang pure-0.68.
- **freebasic**: the pinned fbc 1.10.1 runtime calls `ioperm` at program
  startup, which the global seccomp deny-list kills with SIGSYS (verified in
  a bookworm container 2026-09-02: exit 159 on build and run). The
  per-language seccomp mechanism is additive-deny only, so ioperm cannot be
  re-allowed without weakening the global policy. Re-add if a future fbc
  drops the ioperm call or a per-language allow-list lands.

Notes:

- One pinned version per language, matching the current YAML registry design. Multi-version (semver, for example python 2.7.18 + 3.12.0) is out of scope for the executor roadmap.
- Reuse the `scripts/lang_install/` pattern. Each language needs an install script, a YAML entry, and fixture tests (tests/testcases/{lang}/)
- The deleted `need-to-port-payloads/` archive contained read-N-print-N*2 fixtures for 34 languages. Regenerate from piston's packages/ tests if needed.
- With every language shipped, the remaining build-efficiency item is the prebuilt golden languages image (Dockerfile.langs built on a schedule, pushed to GHCR with dated tags). Then goboxd builds with zero downloads (decision 2025-08-13).

## Penetration corpus — full 50-language coverage (2026-09-02)

- **510 penetration fixtures across all 50 registry languages** (was ~145
  across 22). Catalog-driven: `scripts/gen-pentests.py` instantiates
  per-language idiomatic payloads (raw x86-64 syscalls for nasm,
  `CALL "SYSTEM"` for cobol, `PipeStream` for smalltalk, `os.process_exec`
  for odin, `std.net` for zig, etc.) with per-language run limits so
  VM/CLR runtimes boot and genuinely *attempt* the attack. Re-gen preserves
  pinned `want.json`; the harvest step flags compile-failed snippets and
  treats ANY non-empty stdout as EXPLOIT/LEAK (0 leaks on the host run).
- **Harvest status:** host-verified clean for the 15 new host-runnable
  languages (crystal, dash, elisp, nim, octave*, odin, pony, prolog,
  coffeescript*, dotnet, vlang, zig, pwsh, nasm, erl). Host-bucket
  exceptions that await a docker-image harvest to pin observed statuses
  (same stance as the pre-existing JVM family): *octave (Arch builds ship
  UCX, whose masked-`/proc/sys` boot-id error leaks onto stdout; bookworm
  octave 7.3 is clean), *coffeescript (host node 24 needs >1GB VA at build;
  pinned node 18 is clean), plus clojure/groovy/julia/cobol/raku/smalltalk
  (container-only runtimes). Until then those fixtures keep the placeholder
  contract (accepted + empty stdout) — a docker `make integration-docker`
  harvest overwrites them with observed statuses.
- **RESOLVED (2026-09-04): nsjail-in-image was silently host-built — real bug,
  now fixed.** The Sep-02 link failure (`__isoc23_strtoimax`, protobuf absl
  ABI) was a stale-object artifact: a container relinked Aug-28 host-built
  `.o` files against bookworm libs. But chasing it exposed the worse latent
  defect: `COPY external/nsjail` carried the worktree's `.o` files and
  `nsjail` binary into the builder, so `make` was a no-op and the image
  shipped a **host-linked nsjail** (protobuf 36) into bookworm (protobuf
  3.21) — dead on arrival (`libprotobuf.so.36: cannot open shared object`).
  Fix: `.dockerignore` excludes all nsjail build artifacts (`nsjail`,
  `*.o`, `*.pb.cc/h`, kafel `*.o/*.a`) and the builder runs
  `make clean && make`. Verified 2026-09-04: pristine-source compile in a
  bookworm container with the exact Dockerfile pins, full `LANGS=py3,c`
  image build, and a live smoke (py3/c execute; /etc/shadow read blocked
  with empty stdout; infinite loop bounded as time_exceeded). Note for a
  future `LANGS=all` build (~33GB disk): the builder now always compiles
  nsjail from source, so the full build is hermetic too.

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
