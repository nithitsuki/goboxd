# goboxd TODO

## Phase 1 — Bug Fixes (before next judging)

- [x] Fix stale import in e2e_test.go (boxd/models → internal/models)
- [ ] Fix run limits bug — compiled languages use build limits instead of run limits for test execution phase. LanguageConfig.DefaultLimits only stores one set
- [ ] Fix build defaults — when request omits build.limits, code uses hardcoded values (30s wall time) instead of YAML-configured build limits
- [ ] Fix internal_error returning 500 — spec says internal_error should be 200 with status in body, not 500

## Phase 2 — Language Coverage

- [ ] Add Java to YAML registry + install script + fixtures
- [ ] Add Bash to YAML registry + install script + fixtures
- [ ] Add JavaScript (Node) to YAML registry + install script + fixtures
- [ ] Add Verilog to YAML registry + install script + fixtures

## Phase 3 — Security Hardening

- [ ] Add seccomp Kafel policy blocking dangerous syscalls (ptrace, bpf, mount, etc.)
- [ ] Add cgroup v2 memory tracking with per-jail memory.peak polling
- [ ] Add Slowloris mitigation (ReadHeaderTimeout, etc.)
- [ ] Add TOCTOU symlink protection (O_EXCL | O_NOFOLLOW)

## Phase 4 — Observability & DX

- [ ] Add Swagger/OpenAPI docs endpoint
- [ ] Build a playground website (SPA embedded via //go:embed) for testing goboxd interactively
- [ ] Build a live DDOS dashboard showing real-time metrics (in_flight, queue depth, latency heatmap, error rate)
- [ ] Add structured request logging with trace IDs

## Phase 5 — Infrastructure

- [ ] Design orchestrator that spins up goboxd containers on demand
- [ ] Add load balancer layer distributing requests across containers
- [ ] Add horizontal auto-scaling based on queue depth / in_flight jobs
- [ ] Health check integration with orchestrator

## Phase 6 — Polish

- [ ] Fix memory_peak_kb to use per-process (not global RUSAGE_CHILDREN)
- [ ] Add per-language smoke probe overrides in YAML
- [ ] Add source_filename_strategy / artifact_filename_strategy for Java
- [ ] Fix /readyz to return full breakdown on success
- [ ] Add models package tests
- [ ] Update docs/languages.md with all registered languages
