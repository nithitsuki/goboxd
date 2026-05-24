# Architecture

## Request flow

```
POST /run
  │
  ▼
net/http mux ────► internal/api/handler.go
  │                 │
  │                 ├─ MaxBytesReader (256 KiB cap)
  │                 ├─ JSON decode (strict, no unknown fields)
  │                 ├─ Validate: language, source, tests, filenames, flags
  │                 ├─ Language lookup from registry
  │                 └─ Runner.ExecuteRun()
  │
  ▼
internal/runner/runner.go
  │                 │
  │                 ├─ os.MkdirTemp (unique jail dir)
  │                 ├─ defer os.RemoveAll (cleanup on any exit)
  │                 ├─ Write source file into jail dir
  │                 ├─ Build step (if language has BuildCmd):
  │                 │   └─ execInJail: nsjail + gcc/g++/javac
  │                 ├─ Test loop:
  │                 │   └─ runSingleTest: nsjail + runtime
  │                 │       ├─ io.LimitReader (64 KiB cap)
  │                 │       └─ signalKillReason (timeout vs memory)
  │                 └─ Return BuildResult + []TestResult
  │
  ▼
Back in handler
  ├─ computeTopLevelStatus (accepted / build_failed / etc.)
  ├─ Stats tracking (atomic counters)
  └─ JSON response
```

## Package layout

```
cmd/goboxd/main.go        — entry point, orphan sweep on startup
internal/
├── api/
│   ├── handlers.go       — HTTP handlers, validation, stats
│   ├── handlers_test.go  — unit tests
│   └── router.go         — route wiring
├── config/
│   └── config.go         — LanguageConfig struct, DefaultRegistry
├── models/
│   └── models.go         — RunRequest, RunResponse, shared types
└── runner/
    ├── runner.go         — nsjail execution, build/test loops
    └── runner_test.go    — integration tests with nsjail
tests/
├── integration/
│   ├── main_test.go      — TestMain, server auto-start
│   ├── fixture.go        — fixture loader
│   ├── fixture_test.go   — fixture runner
│   └── e2e_test.go       — hardcoded e2e tests
└── testcases/            — input.json/want.json pairs per language
```

## Key decisions

### Standard library routing
Used `net/http` with Go 1.22+ method routing. No external framework. The API has 4 endpoints, the standard library is sufficient and keeps the dependency surface minimal.

### Fixture-driven tests
Test cases are JSON files in `tests/testcases/{lang}/{name}/`. The go test runner discovers and runs them dynamically. Adding a language test case means creating a directory with `input.json` and `want.json`. No recompile needed.

### Security-first architecture
All request validation happens at the HTTP layer before any execution. The runner is stateless per request. Cleanup is deferred at the point of creation to prevent leaks on any code path.

### Status vocabulary
The spec defines a strict vocabulary for build, test, and top-level statuses. `computeTestStatus` checks in order: timeout, runtime error, exact match, whitespace-only diff, wrong output. `computeTopLevelStatus` sets build_failed and marks all tests not_executed per spec rules.
