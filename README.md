# goboxd

A hardened Go HTTP service that compiles and runs untrusted code inside
[nsjail](https://github.com/google/nsjail) sandboxes. Accepts source code,
optionally compiles it, executes it against test cases, and returns per-test
results. Includes a comprehensive **penetration test suite** that probes every
sandbox boundary, and a **load-testing framework** to measure throughput and
breaking points.

## Features

- **17 supported languages** — Python, C, C++, Go, Rust, Java, JavaScript
  (Node), Bash, Haskell, OCaml, Verilog, R, D, Lua (LuaJIT), Perl,
  **Erlang**, **Lisp (SBCL)**
- **nsjail isolation** — every execution runs in a dedicated Linux namespace
  with resource limits (wall time, memory, processes, file size, open files)
- **Configurable per-language** — add a language with a YAML entry, no Go code
  change required
- **Bounded concurrency** — channel-based semaphore prevents resource
  exhaustion under burst load (configurable via `GOBOXD_MAX_JOBS`)
- **Security-first** — path traversal prevention, flag allow-lists, request
  size limits (256 KiB), output capping (64 KiB), automatic jail directory
  cleanup, panic recovery middleware
- **Penetration test suite** — 52 test cases across all languages probing file
  reads, shell injection, network isolation, write protection, symlink escapes,
  eval injection, and more
- **Ported payload suite** — 62 test cases ported from the Stage 2 challenge
  specification (accepted, build_failed, runtime_error, time_exceeded, wrong_output)
  covering read-N-print-N*2 scenarios. All ported payloads pass across all
  14 compatible languages.
- **Fixture-driven tests** — test cases are JSON files, no recompile needed to
  add scenarios
- **Embedded playground** — web UI for interactive testing served via Go's
  `embed.FS` (Vite + React + Monaco editor)
- **Load-testing framework** — vegeta-based rate ladder with automated CSV
  reporting and matplotlib graphs

## Quick start

```bash
# First-time only: fetch the nsjail (+ kafel) submodules required by the build
git submodule update --init --recursive

# Build and start
make build
make run
```

```bash
# Submit a Python 3 job
curl -s http://localhost:8080/run \
  -H "Content-Type: application/json" \
  -d '{
    "language": "py3",
    "source": "print(42)",
    "tests": [{"stdin": "", "expected_stdout": "42\n"}]
  }' | jq .
```

## Commands

| Command | Description |
|---|---|
| `make build` | Build Docker image |
| `make run` | Start server with `docker compose up` |
| `make test` | Run unit tests (config, models) |
| `make integration` | Run integration tests against a fresh local server |
| `make integration-docker` | Run integration tests against a running Docker container |
| `make integration-safe` | Run integration tests excluding penetration tests |
| `make load` | Run load benchmarks with [hey](https://github.com/rakyll/hey) |
| `make lint` | Run golangci-lint and govulncheck |
| `make fmt` | Format Go source code |

## Load-testing benchmarks

Container: **2 vCPU / 2 GB RAM** (`GOBOXD_MAX_JOBS=4`).
Workload: Java MemoryHog (150 MB heap allocate + touch + 1 s hold).
Client: [vegeta](https://github.com/tsenart/vegeta), 30 s steady-state steps.

| Offered RPS | Success rate | p50 latency | p95 latency | Notes |
|---|---|---|---|---|
| 1 | 100% | 1.4 s | 1.6 s | Idle |
| 2 | 100% | 1.5 s | 1.8 s | Comfortable |
| **3** | **44%** | **10.0 s** | **10.0 s** | **Breaking point** |
| 4 | 0% | — | — | Queue saturation |
| 5+ | 0% | — | — | Fully saturated |

**Breaking point: 3 RPS.** The service degrades gracefully — partial success at
3 RPS, then clean timeouts at higher rates. No server crashes under any load.
The primary bottleneck is memory pressure: each MemoryHog uses ~354 MB peak,
and at 4 concurrent requests the 2 GB container limit is reached.

These are the best results achievable with the default **Debian base image**
and **stock OpenJDK**. The 3 RPS ceiling is set by memory pressure (354 MB per
request × 4 concurrent = 1.4 GB in a 2 GB container).

**Further optimization potential** (not pursued due to challenge constraints):
- **Alpine Linux** base would reduce per-sandbox memory overhead
- **Custom JVM flags** (`-Xmx`, `-Xms`, `-XX:+UseSerialGC`) could shave ~40 MB
  per request
- **Swap space** could absorb spikes at 4+ RPS at the cost of latency
- **GraalVM native-image** would eliminate JVM startup overhead entirely
- **Pre-warmed JVM pool** would skip compilation per request

See `docs/loadtest/` for the full CSV, graphs, and analysis.

## Project structure

```
cmd/goboxd/main.go              Entry point
internal/
├── api/                        HTTP handlers, validation, routing, logging
│   ├── handlers.go             POST /run, GET /healthz, GET /readyz, GET /info
│   ├── handlers_test.go        Validation unit tests
│   ├── logging.go              Structured JSON request logging + panic recovery
│   ├── playground.go           Embedded web UI (optional)
│   ├── testcases.go            Test case listing/serving API
│   └── router.go               Route registration
├── config/
│   ├── config.go               YAML language registry loader
│   └── config_test.go          Registry unit tests
├── models/
│   └── models.go               Shared request/response types
└── runner/
    ├── runner.go               nsjail sandbox execution with cgroup-aware limits
    └── runner_test.go          Integration tests with real nsjail
tests/
├── integration/                End-to-end and fixture-driven test harness
├── testcases/                  450+ JSON fixture pairs across all languages
│   ├── {lang}/{name}/input.json
│   └── {lang}/{name}/want.json
└── penetration/                Security boundary probes
scripts/
├── install.sh                  Full language installation orchestrator
├── lang_install/               Per-language install + verify scripts
├── loadtest.sh                 Vegeta-based rate ladder
└── seccomp.policy              Seccomp-bpf policy for sandbox hardening
docs/
├── loadtest/                   Load-test results, CSV, graphs
└── ...                         Architecture, API, security docs
```

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness check |
| `GET` | `/readyz` | Readiness probe (nsjail + all languages) |
| `GET` | `/info` | Service metadata and runtime stats |
| `POST` | `/run` | Execute untrusted code |
| `GET` | `/playground` | Web UI (if embedded) |
| `GET` | `/testcases` | List available test fixtures |
| `GET` | `/testcases/{lang}/{name}` | Get a specific test fixture |

See [docs/api.md](docs/api.md) for the full API reference.

## Languages

| id | name | type | Since |
|---|---|---|---|
| bash | Bash | interpreted | v1.0 |
| c | C | compiled | v1.0 |
| cpp | C++ | compiled | v1.0 |
| d | D (GDC) | compiled | v1.0 |
| **erl** | **Erlang** | compiled | **v1.1** |
| go | Go | compiled | v1.0 |
| haskell | Haskell | compiled | v1.0 |
| java | Java | compiled | v1.0 |
| js | JavaScript (Node) | interpreted | v1.0 |
| **lisp** | **Lisp (SBCL)** | interpreted | **v1.1** |
| lua | Lua (LuaJIT) | interpreted | v1.0 |
| ocaml | OCaml | compiled | v1.0 |
| perl | Perl | interpreted | v1.0 |
| py3 | Python 3 | interpreted | v1.0 |
| r | R | interpreted | v1.0 |
| rust | Rust | compiled | v1.0 |
| verilog | Verilog | compiled | v1.0 |

See [docs/languages.md](docs/languages.md) for configuration details and
instructions for adding new languages.

## Penetration testing

goboxd includes 52 automated penetration test cases organized by attack vector:

| Vector | Languages | What it probes |
|---|---|---|
| File reads | all 17 | Access to `/etc/passwd`, `/etc/shadow`, `/proc/1/environ` |
| Shell injection | py3, c, cpp, js, java, go, rust, erl, lisp | `system()`, `exec()`, `popen()`, `subprocess` |
| Network isolation | all 17 | Outbound TCP connections (blocked by CLONE_NEWNET) |
| Write protection | all 17 | Attempts to write to `/etc/hosts` (read-only mounts) |
| Eval injection | py3, js | `eval()` and `exec()` of arbitrary code |
| Symlink escapes | py3 | Symlink attacks across mount boundaries |
| Reverse shells | bash | `/dev/tcp` reverse shell attempts |

Run with: `SKIP_PENETRATION=1 make integration-docker` to exclude
penetration tests during normal development.

## Security architecture

- **Namespace isolation** — nsjail uses CLONE_NEWPID, CLONE_NEWNS,
  CLONE_NEWNET, CLONE_NEWUSER, CLONE_NEWIPC, CLONE_NEWUTS, CLONE_NEWCGROUP
- **Resource limits** — wall time, address space (RLIMIT_AS), process count,
  file size, open file descriptors
- **Read-only mounts** — system directories (`/usr`, `/lib`, `/bin`, `/etc`,
  `/dev`) are mounted read-only inside the sandbox
- **Output capping** — stdout/stderr are capped at 64 KiB to prevent
  unbounded memory consumption
- **Seccomp-bpf policy** — blocks dangerous syscalls (mount, ptrace, kernel
  module ops, bpf, ioperm/iopl) via kafel policy
- **Request limits** — 256 KiB max body size, 50 max test cases, 64 KiB per
  field
- **Panic recovery** — middleware catches panics so a single bad request
  doesn't crash the server
- **Path traversal prevention** — filenames are validated against `/`, `\`,
  `..`, leading dots
- **Flag allow-lists** — compiler flags are validated against per-language
  allow-lists

## Documentation

- [Architecture](docs/architecture.md) — request flow, package layout, design decisions
- [API Reference](docs/api.md) — endpoint details, request/response formats, errors
- [Security](docs/security.md) — threat model, closed vulnerabilities, mitigation details
- [Languages](docs/languages.md) — registry, template variables, resource limits
- [Load Tests](docs/loadtest/README.md) — breaking point, latency curves, failure mode analysis

## Author

**Nithilan R**  
Roll No: 24f2100056  
Email: 24f210056@es.study.iitm.ac.in / hi@nithitsuki.com  
LinkedIn: [linkedin.com/in/nithilanr](https://www.linkedin.com/in/nithilanr/)

## Requirements

- Docker Desktop (macOS) or Docker Engine (Linux)
- `make`, `git`

The Docker container requires `--privileged` mode for nsjail to create Linux
namespaces. This is configured in `docker-compose.yml`.

## Testing

Unit tests don't require nsjail:

```bash
make test
```

Integration tests require a running goboxd instance (either local binary or
Docker container):

```bash
# Against a running Docker container (all tests including penetration)
make integration-docker

# Same but skip penetration tests
make integration-safe
```

Test cases are defined as JSON fixtures in `tests/testcases/{lang}/{name}/`:

```
tests/testcases/
├── py3/
│   ├── positive-basic/
│   ├── ported-accepted/
│   ├── penetration-file-read-etc-passwd/
│   └── ...
├── erl/
│   ├── positive-basic/
│   ├── ported-accepted/
│   └── ...
└── ...
```

Currently **450+ test cases** across 17 languages.
