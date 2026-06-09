# Security

## Threat model

goboxd executes untrusted user source code. The attacker controls the source
code content, filenames, compiler flags, test inputs, and (within limits)
resource allowances. The goal is to prevent the attacker from:

- Reading or writing files outside the jail directory
- Consuming excessive server resources (CPU, memory, disk, processes)
- Breaking out of the nsjail namespace isolation
- Influencing other concurrent or future executions

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
| 8 | nsjail error misclassification | `isInfraError` catches nsjail exit code 255 and pipe/start failures, separating infrastructure errors from user-code errors in both build and test paths | `internal/runner/runner.go` |
| 9 | Unbounded concurrency | Channel-based semaphore limits concurrent executions to `runtime.NumCPU()` (or `GOBOXD_MAX_JOBS`), preventing resource exhaustion under burst load | `internal/api/handlers.go` |

## What each fix does

### Hole 1 — Filename validation
All source and artifact filenames from the client are checked before use. They
must be a single path component (no `/` or `\`), no leading `.`, no `..`, and
max 64 characters. This prevents writing outside the jail directory.

### Hole 2 — No shell commands
The reference implementation used `os.system()` with string formatting. goboxd
uses Go's filesystem APIs directly (`os.MkdirTemp`, `os.RemoveAll`,
`os.WriteFile`). Every path operation uses `filepath.Join` to prevent path
injection. A project-wide scan in `TestSecurityHole2NoShellCommands` verifies
no shell binary is invoked via `exec.Command`.

### Hole 3 — Flag allow-list
Each compiled language has a list of permitted compiler flags. Flags can be
allowed exactly (`-O2`) or by prefix (`-std=*`). The allow-list is checked at
the HTTP layer before any execution, returning 400 for disallowed flags. This
prevents flag injection attacks (e.g., `-fplugin=evil.so`, `@payload`).

### Hole 4 — Request limits
- Request body: 256 KiB via `http.MaxBytesReader`
- Test count: max 50
- Per-field stdin/expected_stdout: max 64 KiB each
- Child stdout/stderr: capped at 64 KiB per stream with truncation marker
- File writes inside jail: limited via `--rlimit_fsize 100` (100 MB)

### Hole 5 — Unique directories
`os.MkdirTemp` creates directories with random suffixes. No collision can
occur, unlike the reference's 30k-range UID retry loop that would eventually
collide under load.

### Hole 6 — Output capping
Child stdout and stderr are read through `io.LimitReader`. If output exceeds
64 KiB, it is truncated and `... [output truncated]` is appended so the caller
knows. This prevents unbounded memory consumption from chatty processes.

### Hole 7 — Cleanup on every path
`defer os.RemoveAll(jailDir)` runs immediately after directory creation,
ensuring cleanup on panic, error, or success. On startup, `SweepOrphans`
removes any leftover jail dirs older than 30 minutes.

### Hole 8 — nsjail error classification
nsjail infrastructure failures (exit code 255, pipe creation failure, process
start failure) are detected by `isInfraError` in both `runBuild` and
`runSingleTest`. These produce `internal_error` status rather than being
misclassified as `build_failed` or `runtime_error`.

### Hole 9 — Concurrency limiting
A channel-based semaphore (`jobSem`) limits concurrent executions to prevent
resource exhaustion. The capacity defaults to `runtime.NumCPU()` and can be
overridden via the `GOBOXD_MAX_JOBS` environment variable. Requests queue when
the limit is reached.
