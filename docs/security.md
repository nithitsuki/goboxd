# Security

## Threat model

goboxd executes untrusted user source code. The attacker controls the source
code content, filenames, compiler flags, test inputs, and resource
allowances. The allowances are within the configured limits.

The goal is to prevent the attacker from:

- Access to files outside the jail directory
- Excessive use of server resources (CPU, memory, disk, processes)
- Escape from the nsjail namespace isolation
- Influence on other concurrent or future executions

## Holes closed

| # | Hole | Fix | Location |
|---|---|---|---|
| 1 | Path traversal via filename | `isValidFilename` rejects `/`, `\`, leading `.`, `..`, max 64 chars | `internal/api/handlers.go` |
| 2 | Shell-style directory commands | Use `os.MkdirTemp` and `os.RemoveAll`, no shell exec. Unit test `TestSecurityHole2NoShellCommands` verifies zero shell invocations project-wide. | `internal/runner/runner.go`, `internal/api/handlers_test.go` |
| 3 | Compiler-flag injection | Per-language allow-list with exact and prefix (`*`) matching | `internal/api/handlers.go` |
| 4 | No request size limits | `http.MaxBytesReader` (256 KiB), test count cap (50), per-field cap (64 KiB), output capped at 64 KiB | `internal/api/handlers.go`, `internal/runner/runner.go` |
| 5 | UID collisions under load | `os.MkdirTemp` guarantees unique directory names | `internal/runner/runner.go` |
| 6 | Unbounded child output | `io.LimitReader` caps stdout/stderr at 64 KiB, `readCapped` adds truncation marker | `internal/runner/runner.go` |
| 7 | Stale jail directories | `defer os.RemoveAll` after every jail dir creation + startup orphan sweep (30 min) | `internal/runner/runner.go`, `cmd/goboxd/main.go` |
| 8 | nsjail error misclassification | `isInfraError` detects pipe and start failures. It separates infrastructure errors from user-code errors in both build and test paths. | `internal/runner/runner.go` |
| 9 | Unbounded concurrency | Channel-based semaphore limits concurrent executions to `runtime.NumCPU()` (or `GOBOXD_MAX_JOBS`), preventing resource exhaustion under burst load | `internal/api/handlers.go` |
| 10 | Server crash on handler panic | `RecoveryMiddleware` catches panics in all handlers, logs stack trace, returns 500. One bad request cannot crash the server. | `internal/api/logging.go` |
| 11 | Sandbox escape via dangerous syscalls | The policy file exists at `scripts/seccomp.policy`. The service does not load it. nsjail 3.4 kafel parser limitations block it. This is planned work (see TODO.md Phase 4). | `scripts/seccomp.policy` |

## What each fix does

### Hole 1 — Filename validation
The server checks all source and artifact filenames from the client before
use. Each name must be a single path component. It must not contain `/` or
`\`. It must not start with `.`. It must not contain `..`. The maximum length
is 64 characters. This prevents file writes outside the jail directory.

### Hole 2 — No shell commands
Naive implementations shell out via `os.system()` with string formatting.
goboxd uses the Go filesystem APIs directly. The APIs are `os.MkdirTemp`,
`os.RemoveAll`, and `os.WriteFile`. Every path operation uses
`filepath.Join` to prevent path injection. A project-wide scan in
`TestSecurityHole2NoShellCommands` verifies that the service does not invoke
a shell binary through `exec.Command`.

### Hole 3 — Flag allow-list
Each compiled language has a list of permitted compiler flags. Flags can be
allowed exactly (`-O2`) or by prefix (`-std=*`). The HTTP layer checks the
allow-list before any execution. It returns HTTP 400 for disallowed flags.
This prevents flag injection attacks, for example `-fplugin=evil.so` and
`@payload`.

### Hole 4 — Request limits
- Request body: 256 KiB via `http.MaxBytesReader`
- Test count: max 50
- Per-field stdin/expected_stdout: max 64 KiB each
- Child stdout/stderr: capped at 64 KiB per stream with truncation marker
- File writes inside jail: limited via `--rlimit_fsize 100` (100 MB)

### Hole 5 — Unique directories
`os.MkdirTemp` creates directories with random suffixes. No collision can
occur. A fixed-range UID retry loop could
collide under load.

### Hole 6 — Output capping
The server reads child stdout and stderr through `io.LimitReader`. If output
exceeds 64 KiB, the server truncates it. The server appends
`... [output truncated]` so the caller knows. This prevents unbounded memory
consumption from chatty processes.

### Hole 7 — Cleanup on every path
`defer os.RemoveAll(jailDir)` runs immediately after directory creation. It
ensures cleanup on panic, error, or success. On startup, `SweepOrphans`
removes any leftover jail dirs older than 30 minutes.

### Hole 8 — nsjail error classification
`isInfraError` detects infrastructure failures. A failure is an
infrastructure failure when nsjail cannot create a pipe or cannot start the
process. The check runs in both `runBuild` and `runSingleTest`. These
failures produce `internal_error` status. They are not misclassified as
`build_failed` or `runtime_error`.

Exit codes do not mean infrastructure failure. nsjail propagates the exit
code of the user program. The code 255 comes from the user program, not from
nsjail.

### Hole 9 — Concurrency limiting
A channel-based semaphore (`jobSem`) limits concurrent executions. This
prevents resource exhaustion. The capacity defaults to `runtime.NumCPU()`.
You can override it with the `GOBOXD_MAX_JOBS` environment variable.
Requests queue when the semaphore is full.

### Hole 11 — Seccomp policy is not loaded
The policy file `scripts/seccomp.policy` is a prepared artifact. The service
does not load it. nsjail 3.4 kafel parser limitations block it. The parser
accepts a maximum of 9 syscalls per rule. Multi-rule policies fail. This is
planned work. It requires an nsjail upgrade or a different seccomp approach.
See TODO.md Phase 4.
