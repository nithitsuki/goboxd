# Gaps & Improvement Plan

Based on competitive analysis against top 3 teams (Alpha ~90%, silverex ~90%, sudo ~83%).

## Priority 1 — Code Quality (fix now)

- [ ] **memory_peak_kb always returns 0** — Top teams poll `/sys/fs/cgroup/NSJAIL.*/memory.peak` or parse nsjail logs. This is a visible gap in every test result.
- [ ] **Negative fixture tests missing** — All 20+ fixture tests are happy-path "accepted". Need fixtures for: build_failed, wrong_output, time_exceeded, memory_exceeded, runtime_error, not_executed.
- [ ] **cpp/extraargs fixture uses wrong flag prefix** — `--std=c++17` (double dash) vs allowlist `-std=*` (single dash). Would fail validation.
- [ ] **Config loading tests missing** — Required by spec. Config is hardcoded Go stubs, no YAML loading.

## Priority 2 — Infrastructure (add now)

- [ ] **Govulncheck** — Add to Makefile lint target for vulnerability scanning.
- [ ] **Expand README** — Mention supported languages, add curl example, one-paragraph overview.
- [ ] **Restructure per conventions** — boxd/ instead of internal/, config/ for YAML, templates/ for templates.

## Priority 3 — Feature Gaps (Stage 2/3)

- [ ] **YAML language registry** — Replace hardcoded DefaultRegistry with languages.yml loading.
- [ ] **Bonus languages** — Rust, Go, Kotlin, Ruby, Lua, OCaml, Swift, Zig. 10% judging weight each.
- [ ] **Seccomp/Kafel policy** — Alpha and sudo both deny 28+ syscalls via nsjail seccomp.
- [ ] **Memory cgroup tracking** — Poll memory.peak for real `memory_peak_kb` values.
- [ ] **Structured request logs** — Bonus item. JSON line per request with id, language, durations, status.
- [ ] **Swagger/Playground UI** — Alpha has embedded Swagger at /docs/ and playground.
- [ ] **Output hang prevention** — Drain pipe to io.Discard after truncation to prevent blocking.

## Competitive Advantages We Already Have

- Clean commit history (14 commits vs single squash from competitors)
- All 7 security holes closed + documented with file:line
- Fixture-based test architecture (zero Go code for new test cases)
- golangci-lint clean (0 issues)
- nsjail as git submodule pinned to 3.4
- Concurrency semaphore with GOBOXD_MAX_JOBS env config
- Real /readyz probing (many competitors have stub)
- Real /info with live stats (disk free, git commit, job counters)
- Benchmark baseline with hey at 8 concurrency levels
