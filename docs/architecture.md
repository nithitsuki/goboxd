# Architecture

## Overview

goboxd is a sandboxed code execution engine. It accepts source code via HTTP.
It optionally compiles the code inside an nsjail container. It then runs the
code against test cases. It returns the results for each test. Every
execution runs in a dedicated nsjail namespace. nsjail enforces the
resource limits through rlimit flags.

## Request flow

```
POST /run
  │
  ▼
net/http mux (Go 1.22+ method routing)
  │
  ├─► LoggingMiddleware           (structured JSON request log)
  │
  ▼
internal/api/handlers.go
  │
  ├─ MaxBytesReader               (256 KiB request cap — Security Hole #4)
  ├─ JSON decode (strict, DisallowUnknownFields)
  ├─ Validate:
  │   ├─ language present & known
  │   ├─ source non-empty
  │   ├─ tests ≥ 1 and ≤ 50
  │   ├─ per-field size caps (64 KiB stdin/expected_stdout)
  │   ├─ source_filename / artifact_filename path-safety (Security Hole #1)
  │   └─ compiler flags match allow-list (Security Hole #3)
  ├─ acquireSlot()                (bounded concurrency semaphore)
  │
  ▼
internal/runner/runner.go
  │
  ├─ os.MkdirTemp                 (unique jail dir, Security Hole #5)
  ├─ defer os.RemoveAll           (cleanup on any exit path, Security Hole #7)
  ├─ Write source file to jail dir
  │
  ├─ Build step (if language has BuildCmd):
  │   └─ execInJail():
  │       ├─ Build nsjail args: -Mo, --chroot /, --proc_path /proc,
  │       │   --bindmount, rlimits, -B mounts for /usr, /lib, /etc, and more
  │       ├─ exec.CommandContext with Go deadline
  │       ├─ Concurrent stdout/stderr read with io.LimitReader (64 KiB cap)
  │       └─ cmd.Wait() → classify error as "ok" / "failed" / "internal_error"
  │
  ├─ Test loop:
  │   └─ runSingleTest():
  │       ├─ Same nsjail setup but with per-language run limits
  │       ├─ Stdin piped from test case
  │       ├─ Concurrent stdout/stderr goroutines
  │       ├─ cmd.Wait() → check for nsjail infra errors → computeTestStatus()
  │       └─ memory_peak_kb via getrusage(RUSAGE_CHILDREN)
  │
  └─ Return (BuildResult, []TestResult, error)
  │
  ▼
Back in handler
  ├─ releaseSlot()
  ├─ computeTopLevelStatus()      (accepted / build_failed / internal_error / and more)
  ├─ If build_failed or internal_error → mark all tests "not_executed"
  ├─ Stats tracking (atomic counters for in_flight, total, failed_internal)
  └─ JSON response (always HTTP 200 unless infrastructure failure → 500)
```

## Package layout

```
cmd/goboxd/main.go              Entry point, loads config, starts HTTP server
internal/
├── api/
│   ├── handlers.go             HTTP handlers, validation, stats, /info /readyz /run
│   ├── handlers_test.go        Unit tests for validation, filename checks, flag allow-lists
│   ├── logging.go              Structured JSON request logging middleware
│   ├── playground.go           Embedded web playground (optional, via embed.FS)
│   └── router.go               Route wiring with Go 1.22 method patterns
├── config/
│   ├── config.go               YAML loading, LanguageConfig struct, template expansion
│   └── config_test.go          Registry tests: expected languages, limits, commands
├── models/
│   └── models.go               RunRequest, RunResponse, TestCase, shared types
└── runner/
    ├── runner.go               nsjail execution, build/test loops, output capping
    └── runner_test.go          Integration tests with real nsjail (skip if not found)
tests/
├── integration/
│   ├── main_test.go            TestMain, builds binary, starts server, waits for health
│   ├── e2e_test.go             Hardcoded Python 3 end-to-end tests
│   ├── fixture.go              JSON fixture loader (input.json / want.json)
│   └── fixture_test.go         Dynamic fixture runner for all testcases
└── testcases/                  Per-language, per-scenario JSON fixtures
    ├── py3/
    │   ├── positive-basic/     input.json + want.json
    │   ├── runtime-error/
    │   └── ...
    ├── go/
    ├── c/
    └── ... (one directory per language)
```

## Key design decisions

### Standard library routing
Go 1.22 added native method routing to `net/http.ServeMux`. Examples are
`"GET /path"` and `"POST /run"`. This handles the small API surface without
any third-party framework. It keeps the binary small and the dependencies
minimal.

### Bounded concurrency semaphore
A channel-based semaphore limits concurrent executions to
`runtime.NumCPU()`. You can override this with the `GOBOXD_MAX_JOBS`
environment variable. Requests queue when the semaphore is full. The host is
not overwhelmed. The semaphore initializes lazily on the first request.

```
acquireSlot() → <-jobSem     (reads a token, blocks if empty)
releaseSlot() → jobSem <- _  (returns the token)
```

### Fixture-driven test suite
Test cases are JSON files in `tests/testcases/{lang}/{name}/`. Each directory
contains `input.json` (the API request) and `want.json` (expected response).
The fixture runner discovers and executes them dynamically. To add a test
case, create a directory and two JSON files. You do not need to
recompile the code.

### Security-first architecture
- All request validation happens at the HTTP layer before any execution
  begins.
- The runner is stateless per-request. It creates a temp directory, uses it,
  and cleans it up with `defer os.RemoveAll` immediately after creation.
- Cleanup runs on every code path, which includes panic, error, and success.
- The startup orphan sweep removes any jail dirs left from previous runs.
  The sweep removes dirs older than 30 minutes.
- The runner distinguishes nsjail infrastructure errors from user-code
errors.
  Pipe and start failures produce `internal_error` status. They do not
  produce misleading `runtime_error` or `build_failed`.

### nsjail isolation parameters
Each execution uses:

- `-Mo` — standalone-once mode (clone + execve)
- `--chroot /` — use host filesystem as jail root
- `--proc_path /proc` — mount procfs inside jail
- `--bindmount src:dst:rw` — bind the app directory
- `-B /usr -B /lib -B /bin -B /etc -B /dev -B /var/lib` — read-write bind
  mounts for language runtimes and shared libraries
- `--time_limit`, `--rlimit_as`, `--rlimit_nproc`, `--rlimit_fsize`,
  `--rlimit_nofile` — resource limits applied per build/run stage

### Status vocabulary
The API uses a strict status vocabulary:

| Status | Meaning |
|---|---|
| `accepted` | All checks passed |
| `build_failed` | Compilation failed (all tests → `not_executed`) |
| `internal_error` | Server-side failure (nsjail, filesystem, or similar) |
| `runtime_error` | User code crashed or exited non-zero |
| `time_exceeded` | Wall-clock or CPU limit hit |
| `memory_exceeded` | Memory limit hit (SIGSEGV/SIGABRT) |
| `wrong_output` | Output did not match expected |
| `output_whitespace_mismatch` | Output matches after trimming whitespace |
| `not_executed` | Test skipped because build failed |
