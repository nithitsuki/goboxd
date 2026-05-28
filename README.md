# goboxd

A Go HTTP service that compiles and runs untrusted code inside nsjail sandboxes. Accepts source code, compiles it (if needed), executes it against test cases, and returns per-test results.

I chose the standard net/http package for our framework because go 1.22 added native routing that handles our strict api perfectly. dodging third-party frameworks keeps our binary small and predictable under heavy concurrent load.

## Languages

| id | language | type |
|---|---|---|
| py3 | Python 3 | interpreted |
| c | C | compiled |
| cpp | C++ | compiled |

Adding a language requires no Go code change — just a YAML config entry and an install script.

## Quick start

```bash
make build      # Build the Docker container
make run        # Start the server
```

```bash
curl -s http://localhost:8080/run \
  -H "Content-Type: application/json" \
  -d '{"language":"py3","source":"print(42)","tests":[{"stdin":"","expected_stdout":"42\n"}]}'
```

## Commands

- `make build` — build Docker container
- `make run` — start server
- `make test` — run unit tests
- `make integration` — run integration tests against live server
- `make load` — run load benchmarks
- `make lint` — run golangci-lint and govulncheck

## Docs

See the `docs/` folder for architecture, API specification, security documentation, and benchmarks.
