# goboxd

goboxd is a hardened Go HTTP service. It compiles and runs untrusted code
inside [nsjail](https://github.com/google/nsjail) sandboxes. It accepts
source code, optionally compiles it, executes it against test cases, and
returns the results for each test. It includes a penetration test suite that
probes every sandbox boundary. It also includes a load-testing framework that
measures throughput and breaking points.

## Features

- **28 supported languages** — Python 2, Python 3, C, C++, C#, Go, Rust, Java,
  Kotlin, Scala, Swift, JavaScript (Node), TypeScript, Bash, Ruby, PHP, Elixir,
  Haskell, OCaml, Verilog, R, D, Lua (LuaJIT), Perl, Erlang, Lisp (SBCL),
  Racket, Dart
- **nsjail isolation** — every execution runs in a dedicated Linux namespace
  with resource limits (wall time, memory, processes, file size, open files)
- **Configurable per-language** — add a language with a YAML entry. You do
  not need to change Go code.
- **Bounded concurrency** — a channel-based semaphore prevents resource
  exhaustion under burst load. Configure it with `GOBOXD_MAX_JOBS`.
- **Security-first** — per-jail unprivileged uids (multi-uid), seccomp
  deny-list, cgroup v2 memory/pids limits with rlimit fallback, path
  traversal prevention, flag allow-lists, request size limits (256 KiB),
  output capping (64 KiB), automatic jail directory cleanup, panic recovery
  middleware
- **Environment controls** — `GOBOXD_UID_MIN` (first jail uid),
  `GOBOXD_CGROUPV2` (auto/on/off), `GOBOXD_EXCLUDE_LANGS` (registry filter,
  default excludes csharp and elixir). See docs/security.md.
- **Penetration test suite** — 138 test cases across 21 languages. They probe
  file reads, shell injection, network isolation, write protection, symlink
  escapes, eval injection, and more.
- **Regression payload suite** — 73 test cases covering accepted, build_failed,
  runtime_error, time_exceeded, and wrong_output across read-N-print-N*2
  scenarios. All payloads pass across all 25 compatible languages.
- **Fixture-driven tests** — test cases are JSON files. You do not need to
  recompile the code to add scenarios.
- **Embedded playground** — a web UI for interactive testing. Go's `embed.FS`
  serves it. It uses Vite, React, and the Monaco editor.
- **Load-testing framework** — a vegeta-based rate ladder with automated CSV
  reporting and matplotlib graphs

## Quick start

```bash
# First-time only: fetch the nsjail (+ kafel) submodules required by the build
git submodule update --init --recursive

# Build and start (installs all 29 languages)
make build
make run

# Or build a smaller image with only the languages you need (faster build,
# smaller image; the server advertises only these languages)
make build LANGS=py3,c,swift
LANGS=py3,c,swift make run
```

**Build caching:** The base images use pinned digests. The apt packages use
pinned versions. `scripts/check-pins.sh` verifies both rules. The build
downloads each package and toolchain once per builder cache store. A fresh
builder or `docker builder prune` starts over. Rebuilding a language layer
does not re-download the compilers. For example, `make build LANGS=py3,c`
takes less than one minute after the first full build. Build-time internet access is used only for package, toolchain, and Go module downloads. The running container has no
outbound network (see Security architecture). A full rebuild with no
cache needs about 33 GB of free disk space. If the build stops with 'no
space left on device', run `docker builder prune` and retry.

Builds use the `goboxd-builder` builder with the `docker-container` driver.
`scripts/build.sh` passes the builder explicitly, so the build uses it even
if the CLI default differs. The builder keeps the downloaded packages
between builds. The image removes the `docker-clean` hook, so cached `.deb`
files survive. The first build with a fresh builder downloads once.
`--no-cache` resets the package cache by design.

To bump a pinned version, change the version in the install script, then
rebuild the image.

Submit a Python 3 job:

```bash
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
| `make build` | Build Docker image. Set `LANGS=py3,c` for a subset (comma-separated ids) |
| `make run` | Start server with `docker compose up` |
| `make test` | Run unit tests (config, models) |
| `make integration` | Run integration tests against a fresh local server |
| `make integration-docker` | Run integration tests against a running Docker container |
| `make integration-safe` | Run integration tests excluding penetration tests |
| `make load` | Run load benchmarks with [hey](https://github.com/rakyll/hey) |
| `make load-save` | Run load benchmarks and save the results |
| `make lint` | Run golangci-lint and govulncheck |
| `make vulncheck` | Run govulncheck |
| `make fmt` | Format Go source code |

## Load-testing benchmarks

Container: **2 vCPU / 2 GB RAM** (`GOBOXD_MAX_JOBS=4`).
Workload: Java MemoryHog (150 MB heap allocate + touch + 1 s hold).
Client: [vegeta](https://github.com/tsenart/vegeta), 30 s steady-state steps.

| Offered RPS | Success rate | p50 latency | p95 latency | Notes |
|---|---|---|---|---|
| 1 | 100% | 1.4 s | 1.6 s | Idle |
| 2 | 100% | 1.8 s | 2.2 s | Comfortable |
| **3** | **36%** | **10.0 s** | **10.0 s** | **Breaking point** |
| 4 | 0% | — | — | Queue saturation |
| 5+ | 0% | — | — | Fully saturated |

**Breaking point: 3 RPS.** The service degrades gracefully. It has partial
success at 3 RPS. It then returns clean timeouts at higher rates. No server
crashes occur under any load. The primary bottleneck is memory pressure. Each
MemoryHog uses about 354 MB peak. At 4 concurrent requests, the service
reaches the 2 GB container limit.

These are the best results achievable with the default **Debian base image**
and **stock OpenJDK**. The 3 RPS ceiling is set by memory pressure. Each
request uses 354 MB. Four concurrent requests use 1.4 GB in a 2 GB container.

**Further optimization potential** (not pursued in the current scope):

- **Alpine Linux** base would reduce per-sandbox memory overhead
- **Custom JVM flags** (`-Xmx`, `-Xms`, `-XX:+UseSerialGC`) could shave about
  40 MB per request
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
    ├── runner.go               nsjail sandbox execution with rlimit-based limits
    └── runner_test.go          Integration tests with real nsjail
tests/
├── integration/                End-to-end and fixture-driven test harness
├── testcases/                  180 JSON fixture pairs across all languages
│   ├── {lang}/{name}/input.json
│   └── {lang}/{name}/want.json
└── penetration/                Security boundary probes
scripts/
├── install.sh                  Full language installation orchestrator
├── lang_install/               Per-language install + verify scripts
├── loadtest.sh                 Vegeta-based rate ladder
├── dev-host.sh                 Run goboxd natively on the host (no docker)
docs/
├── loadtest/                   Load-test results, CSV, graphs
├── reference.md                Package reference from godoc
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
| **csharp** | **C# (.NET 10)** | **compiled** | **v1.2** |
| d | D (GDC) | compiled | v1.0 |
| **dart** | **Dart** | **compiled** | **v1.2** |
| **elixir** | **Elixir** | **interpreted** | **v1.2** |
| **erl** | **Erlang** | compiled | **v1.1** |
| go | Go | compiled | v1.0 |
| haskell | Haskell | compiled | v1.0 |
| java | Java | compiled | v1.0 |
| js | JavaScript (Node) | interpreted | v1.0 |
| **kotlin** | **Kotlin** | **compiled** | **v1.2** |
| **lisp** | **Lisp (SBCL)** | interpreted | **v1.1** |
| lua | Lua (LuaJIT) | interpreted | v1.0 |
| ocaml | OCaml | compiled | v1.0 |
| perl | Perl | interpreted | v1.0 |
| **php** | **PHP** | **interpreted** | **v1.2** |
| **py2** | **Python 2** | **interpreted** | **v1.2** |
| py3 | Python 3 | interpreted | v1.0 |
| r | R | interpreted | v1.0 |
| **racket** | **Racket** | **interpreted** | **v1.2** |
| **ruby** | **Ruby** | **interpreted** | **v1.2** |
| rust | Rust | compiled | v1.0 |
| **scala** | **Scala 3** | **compiled** | **v1.2** |
| **swift** | **Swift** | **compiled** | **v1.2** |
| **ts** | **TypeScript** | **compiled** | **v1.2** |
| verilog | Verilog | compiled | v1.0 |

See [docs/languages.md](docs/languages.md) for configuration details and
instructions for adding new languages.

## Penetration testing

goboxd includes 138 automated penetration test cases organized by attack
vector. The test cases cover 21 languages. The table shows the attack
vectors and the languages they cover:

| Vector | Languages | What it probes |
|---|---|---|
| File reads | bash, c, cpp, csharp, dart, elixir, erl, go, java, js, kotlin, lisp, php, py2, py3, racket, ruby, rust, scala, swift, ts | Access to `/etc/passwd`, `/etc/shadow`, `/proc/1/environ` |
| Shell injection | c, cpp, csharp, dart, elixir, erl, go, java, js, kotlin, lisp, php, py2, py3, racket, ruby, rust, scala, swift, ts | `system()`, `exec()`, `popen()`, `subprocess` |
| Network isolation | bash, c, cpp, csharp, dart, elixir, erl, go, java, js, kotlin, lisp, php, py2, py3, racket, ruby, rust, scala, swift, ts | Outbound TCP connections (blocked by CLONE_NEWNET) |
| Write protection | bash, c, cpp, csharp, dart, elixir, erl, go, java, js, kotlin, lisp, php, py2, py3, racket, ruby, rust, scala, swift, ts | Attempts to write to `/etc/hosts` (read-only mounts) |
| Eval injection | elixir, js, php, py2, py3, racket, ruby, ts | `eval()` and `exec()` of arbitrary code |
| Symlink escapes | php, py2, py3, ruby | Symlink attacks across mount boundaries |
| Reverse shells | bash | `/dev/tcp` reverse shell attempts |

Run with `SKIP_PENETRATION=1 make integration-docker` to exclude penetration
tests during normal development.

## Security architecture

- **Namespace isolation** — nsjail uses CLONE_NEWPID, CLONE_NEWNS,
  CLONE_NEWNET, CLONE_NEWUSER, CLONE_NEWIPC, CLONE_NEWUTS, CLONE_NEWCGROUP
- **Zero-network jail** — each jail runs in its own network namespace with
  no interfaces at all (loopback is not brought up, `--iface_no_lo`).
  Jailed code cannot reach the internet, the container, or even itself via
  localhost
- **Container egress firewall** — the entrypoint (`scripts/entrypoint.sh`)
  drops every new outbound connection from the container (iptables OUTPUT
  policy). The server answers inbound API requests, but nothing inside the
  container can open a connection to the internet — no data exfiltration,
  no RCE persistence channel. The container refuses to start if the
  firewall cannot be applied
- **Resource limits** — wall time, address space (RLIMIT_AS), process count,
  file size, open file descriptors
- **Read-only mounts** — the sandbox mounts system directories (`/usr`,
  `/lib`, `/bin`, `/etc`, `/dev`) read-only
- **Output capping** — the service caps stdout/stderr at 64 KiB to prevent
  unbounded memory consumption
- **Seccomp-bpf policy** — every jail loads a deny-list policy
  (`internal/seccomp/seccomp.policy`, embedded in the binary) via
  `--seccomp_policy`, blocking mount, ptrace, bpf, and other escape
  primitives with SECCOMP_RET_KILL while allowing everything else
- **Request limits** — 256 KiB max body size, 50 max test cases, 64 KiB per
  field
- **Panic recovery** — middleware catches panics so a single bad request
  does not crash the server
- **Path traversal prevention** — the service validates filenames against
  `/`, `\`, `..`, and leading dots
- **Flag allow-lists** — the service validates compiler flags against
  per-language allow-lists

## Documentation

- [Architecture](docs/architecture.md) — request flow, package layout,
  design decisions
- [API Reference](docs/api.md) — endpoint details, request/response formats,
  errors
- [Security](docs/security.md) — threat model, closed vulnerabilities,
  mitigation details
- [Languages](docs/languages.md) — registry, template variables, resource
  limits
- [Package Reference](docs/reference.md) — godoc output for all packages
- [Load Tests](docs/loadtest/README.md) — breaking point, latency curves,
  failure mode analysis

## Author

**Nithilan R**
Email: hi@nithitsuki.com
LinkedIn: [linkedin.com/in/nithilanr](https://www.linkedin.com/in/nithilanr/)
Website: [nithitsuki.com](https://www.nithitsuki.com)

## Requirements

- Docker Desktop (macOS) or Docker Engine (Linux)
- `make`, `git`

The Docker container requires `--privileged` mode for nsjail to create Linux
namespaces. The `docker-compose.yml` file configures this.

## Testing

Unit tests do not require nsjail:

```bash
make test
```

Integration tests require a running goboxd instance. The instance can be a
local binary or a Docker container:

```bash
# Against a running Docker container (all tests including penetration)
make integration-docker

# Same but skip penetration tests
make integration-safe
```

JSON fixtures define the test cases in `tests/testcases/{lang}/{name}/`:

```
tests/testcases/
├── py3/
│   ├── positive-basic/
│   ├── regression-accepted/
│   ├── penetration-file-read-etc-passwd/
│   └── ...
├── erl/
│   ├── positive-basic/
│   ├── regression-accepted/
│   └── ...
└── ...
```

Currently **316 test cases** across 28 languages.
