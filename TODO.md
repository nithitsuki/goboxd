# goboxd TODO

## Phase 1 — Bug Fixes ✅ (done)

- [x] Fix stale import in e2e_test.go (boxd/models → internal/models)
- [x] Fix run limits bug — compiled languages use build limits instead of run limits for test execution phase
- [x] Fix build defaults — when request omits build.limits, code uses hardcoded values instead of YAML-configured limits
- [x] Fix internal_error returning 500 — spec says internal_error should be 200 with status in body, not 500

## Phase 2 — Language Coverage ✅ (done)

- [x] Add Java to YAML registry + install script + fixtures
- [x] Add Bash to YAML registry + install script + fixtures
- [x] Add JavaScript (Node) to YAML registry + install script + fixtures
- [x] Add Verilog to YAML registry + install script + fixtures

## Phase 3 — Interactive Frontend

- [ ] Build playground website (React + Monaco Editor + Bun) for interactive code execution
- [ ] Language dropdown, code presets, file upload, real-time output display
- [ ] Serve embedded via Go's //go:embed from boxd/playground/

## Phase 4 — Security Hardening

- [ ] Add seccomp Kafel policy blocking dangerous syscalls (ptrace, bpf, mount, etc.)
- [ ] Add cgroup v2 memory tracking with per-jail memory.peak polling
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
