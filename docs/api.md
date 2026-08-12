# API Reference

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness check |
| `GET` | `/readyz` | Readiness probe |
| `GET` | `/info` | Service metadata |
| `POST` | `/run` | Execute untrusted code |
| `GET` | `/testcases` | List all test fixtures |
| `GET` | `/testcases/{lang}/{name}` | Get a specific test fixture |
| `GET` | `/playground` | Web UI (if embedded) |

---

## `POST /run`

POST /run executes untrusted code inside an nsjail sandbox. The server
returns HTTP 200 after the run completes. The result of the user code does
not change this. The server returns HTTP 400 for validation errors. It
returns HTTP 500 for infrastructure failures.

### Request

```json
{
  "language": "py3",
  "source": "print(\"hello\")",
  "source_filename": "solution.py",
  "artifact_filename": "",
  "build": {
    "limits": { "wall_time_s": 5, "memory_kb": 1048576, "max_processes": 100 },
    "flags": ["-O2"]
  },
  "run": {
    "limits": { "wall_time_s": 3, "memory_kb": 524288, "max_processes": 64 },
    "flags": []
  },
  "tests": [
    { "stdin": "1\n", "expected_stdout": "hello" }
  ]
}
```

### Fields

| Field | Required | Description |
|---|---|---|
| `language` | yes | Language ID from the registry (see [languages.md](languages.md)) |
| `source` | yes | UTF-8 source code, max 256 KiB |
| `source_filename` | no | Override for the source file name (default from language config). Single path component only — no `/`, `\`, leading `.`, or `..`. Max 64 chars. |
| `artifact_filename` | no | Override for the compiled artifact name (default from language config). Same constraints as `source_filename`. |
| `build` | no | Build-stage configuration (limits + flags). Missing fields fall back to language defaults. Ignored for interpreted languages. |
| `run` | no | Run-stage configuration (limits + flags). Missing fields fall back to language defaults. |
| `tests` | yes | Array of test cases, min 1, max 50. |

#### Test case fields

| Field | Description |
|---|---|
| `stdin` | Input to pipe to the program's stdin. Max 64 KiB. |
| `expected_stdout` | The expected stdout output. Max 64 KiB. An empty string means that the service accepts any output. |

### Status vocabulary

| Status | Description |
|---|---|
| `accepted` | Build passed, all tests matched expected output |
| `build_failed` | Compilation failed (all tests return `not_executed`) |
| `internal_error` | Server-side infrastructure failure (nsjail, filesystem, or similar) |
| `runtime_error` | User code exited with non-zero status (crash, error) |
| `time_exceeded` | Wall-clock time limit exceeded |
| `memory_exceeded` | Memory limit exceeded (SIGSEGV/SIGABRT) |
| `wrong_output` | Program output did not match `expected_stdout` |
| `output_whitespace_mismatch` | Output matches `expected_stdout` after trimming whitespace |
| `not_executed` | The service skips the test because the build failed |

### Response

```json
{
  "status": "accepted",
  "build": {
    "status": "ok",
    "stdout": "",
    "stderr": "",
    "duration_ms": 412
  },
  "tests": [
    {
      "status": "accepted",
      "stdout": "hello",
      "stderr": "",
      "duration_ms": 38,
      "memory_peak_kb": 8192
    }
  ]
}
```

### Build result statuses

| Status | Meaning |
|---|---|
| `ok` | Compilation succeeded |
| `failed` | Compilation failed (compiler returned non-zero) |
| `internal_error` | Infrastructure error (nsjail failure, disk error) |

### Top-level status computation

The server computes the top-level status with this procedure:

1. If build.status is `internal_error`, the top-level status is `internal_error`.
2. If build.status is not `ok`, the top-level status is `build_failed`.
3. If any test has `internal_error`, the top-level status is `internal_error`.
4. The first test that is not `accepted` sets the top-level status.
5. If all tests are `accepted`, the top-level status is `accepted`.

### Error responses (400)

```json
{ "error": { "code": "invalid_filename", "message": "filename must be a single path component without traversal characters" } }
```

| Error code | Description |
|---|---|
| `invalid_request` | Payload exceeds 256 KiB or invalid JSON |
| `missing_language` | `language` field is empty |
| `unknown_language` | Language ID not found in registry |
| `missing_source` | `source` field is empty |
| `missing_tests` | `tests` array is empty |
| `too_many_tests` | More than 50 test cases |
| `test_too_large` | stdin or expected_stdout exceeds 64 KiB |
| `invalid_filename` | Filename contains path traversal chars |
| `invalid_flags` | Compiler flag not in allow-list |

Internal errors (500) return the same shape with code `internal_error`.

---

## `GET /healthz`

GET /healthz is a simple liveness check. It always returns HTTP 200.

```json
{"status":"ok"}
```

---

## `GET /readyz`

GET /readyz is a readiness probe. It returns HTTP 200 when nsjail and all
language runtimes are operational. It returns HTTP 503 with the failure
details for each component when any component is degraded.

```json
{
  "status": "degraded",
  "nsjail": { "ok": true, "version": "3.4" },
  "languages": {
    "py3": { "ok": true, "version": "Python 3.11.2" },
    "c": { "ok": true, "version": "gcc 14" },
    "rust": { "ok": false, "error": "rustc not found at /usr/bin/rustc" }
  }
}
```

The server probes each language by running `<compiler/runtime> --version`.
If that fails, it falls back to `exec.LookPath` to confirm the binary exists.

---

## `GET /info`

GET /info returns service metadata and runtime statistics. It always returns
HTTP 200.

```json
{
  "build_info": {
    "version": "0.1.0",
    "commit": "abc1234",
    "go_version": "go1.26.3"
  },
  "nsjail": {
    "path": "/usr/bin/nsjail",
    "version": "3.4"
  },
  "languages": [
    {
      "id": "py3",
      "name": "Python 3",
      "version": "Python 3.11.2",
      "default_run_limits": {
        "wall_time_s": 9,
        "memory_kb": 102400,
        "max_processes": 100
      }
    }
  ],
  "limits": {
    "max_source_bytes": 262144,
    "max_tests": 50,
    "max_concurrent_jobs": 16
  },
  "stats": {
    "in_flight_jobs": 0,
    "jobs_total": 42,
    "jobs_failed_internal": 1,
    "last_internal_error_at": "2026-05-28T10:00:00Z",
    "disk_free_bytes_jail_dir": 53687091200
  }
}
```

---

## `GET /playground`

GET /playground serves a browser-based code editor for interactive testing.
It works only when the server embeds the playground web UI. Go `embed.FS`
embeds the UI from `internal/api/playground-dist/`. The server redirects
from `/playground` to `/playground/`.
