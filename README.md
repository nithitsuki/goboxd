# goboxd

A Go HTTP service that compiles and runs untrusted code inside
[nsjail](https://github.com/google/nsjail) sandboxes. Accepts source code,
optionally compiles it, executes it against test cases, and returns per-test
results.

## Features

- **15 supported languages** — Python, C, C++, Go, Rust, Java, JavaScript
  (Node), Bash, Haskell, OCaml, Verilog, R, D, Lua (LuaJIT), Perl
- **nsjail isolation** — every execution runs in a dedicated Linux namespace
  with resource limits (wall time, memory, processes, file size)
- **Configurable per-language** — add a language with a YAML entry, no Go code
  change required
- **Bounded concurrency** — channel-based semaphore prevents resource
  exhaustion under burst load
- **Security-first** — path traversal prevention, flag allow-lists, request
  size limits, output capping, automatic jail directory cleanup
- **Fixture-driven tests** — test cases are JSON files, no recompile needed to
  add scenarios
- **Embedded playground** — optional web UI for interactive testing (served
  via `embed.FS`)

## Quick start

```bash
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
| `make integration-docker` | Run integration tests against a running Docker container (`API_URL=http://localhost:8080`) |
| `make load` | Run load benchmarks with [hey](https://github.com/rakyll/hey) |
| `make lint` | Run golangci-lint and govulncheck |
| `make fmt` | Format Go source code |

## Project structure

```
cmd/goboxd/main.go              Entry point
internal/
├── api/                        HTTP handlers, validation, routing, logging
│   ├── handlers.go             POST /run, GET /healthz, GET /readyz, GET /info
│   ├── handlers_test.go        Validation unit tests
│   ├── logging.go              Structured JSON request logging
│   ├── playground.go           Embedded web UI (optional)
│   └── router.go               Route registration
├── config/
│   ├── config.go               YAML language registry loader
│   └── config_test.go          Registry unit tests
├── models/
│   └── models.go               Shared request/response types
└── runner/
    ├── runner.go               nsjail sandbox execution
    └── runner_test.go          Integration tests with real nsjail
tests/
├── integration/                End-to-end and fixture-driven test harness
└── testcases/                  JSON fixture pairs (input.json + want.json) per language
```

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness check |
| `GET` | `/readyz` | Readiness probe (nsjail + all languages) |
| `GET` | `/info` | Service metadata and runtime stats |
| `POST` | `/run` | Execute untrusted code |
| `GET` | `/playground` | Web UI (if embedded) |

See [docs/api.md](docs/api.md) for the full API reference.

## Languages

| id | name | type |
|---|---|---|
| py3 | Python 3 | interpreted |
| c | C | compiled |
| cpp | C++ | compiled |
| java | Java | compiled |
| bash | Bash | interpreted |
| js | JavaScript (Node) | interpreted |
| verilog | Verilog | compiled |
| rust | Rust | compiled |
| go | Go | compiled |
| haskell | Haskell | compiled |
| ocaml | OCaml | compiled |
| r | R | interpreted |
| d | D (GDC) | compiled |
| lua | Lua (LuaJIT) | interpreted |
| perl | Perl | interpreted |

See [docs/languages.md](docs/languages.md) for configuration details and
instructions for adding new languages.

## Documentation

- [Architecture](docs/architecture.md) — request flow, package layout, design decisions
- [API Reference](docs/api.md) — endpoint details, request/response formats, errors
- [Security](docs/security.md) — threat model, closed vulnerabilities, mitigation details
- [Languages](docs/languages.md) — registry, template variables, resource limits
- [Benchmarks](docs/benchmarks.md) — performance numbers under load

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
# Against a local binary (builds + runs it automatically)
make integration

# Against a running Docker container
make integration-docker
```

Test cases are defined as JSON fixtures in `tests/testcases/{lang}/{name}/`:

```
tests/testcases/
├── py3/
│   ├── positive-basic/    # input.json + want.json
│   ├── runtime-error/
│   └── ...
├── go/
│   ├── positive-basic/
│   ├── build-failure/
│   └── ...
└── ...
```
