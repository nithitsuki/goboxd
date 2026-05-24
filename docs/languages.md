# Language Registry

Languages are configured via a YAML file loaded at startup. Adding a language requires no Go code change.

## Currently registered

| id | name | type | runtime/compiler |
|---|---|---|---|
| py3 | Python 3 | interpreted | /usr/bin/python3 |
| c | C | compiled | /usr/bin/gcc |
| cpp | C++ | compiled | /usr/bin/g++ |

## Adding a new language

1. Add a block to the YAML config.
2. Add the compiler/runtime to the Docker image.
3. Create test cases under `tests/testcases/{id}/`.

```yaml
- id: rust
  name: Rust
  source_filename: main.rs
  artifact: main
  build:
    cmd: /usr/bin/rustc
    args: ["{{flags}}", "-o", "{{artifact}}", "{{source}}"]
    limits: { wall_time_s: 10, memory_kb: 1048576, max_processes: 100 }
    flag_allowlist: ["-O*", "--edition*"]
  run:
    cmd: ./{{artifact}}
    limits: { wall_time_s: 5, memory_kb: 524288, max_processes: 64 }
```

## Template variables

The YAML format supports these placeholders in cmd args:
- `{{source}}` — the source filename
- `{{artifact}}` — the artifact filename (compiled languages)
- `{{flags}}` — user-supplied flags from the request (injected before other args)

## Flag allow-lists

Each compiled language has an allow-list of permitted compiler flags. Flags are matched exactly or by prefix (`-std=*` allows `-std=c99`, `-std=c17`, etc.). Requests with disallowed flags get a 400 response.
