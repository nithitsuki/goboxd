# Language Registry

Languages are configured via `config/languages.yml`, loaded at startup by
`internal/config/config.go`. Adding a language requires no Go code change —
just a YAML entry and, if the runtime/compiler isn't already in the Docker
image, an `apt-get install` line in `scripts/install.sh`.

## Currently registered

| id | name | type | compiler | runtime |
|---|---|---|---|---|
| py3 | Python 3 | interpreted | — | `/usr/bin/python3` |
| c | C | compiled | `/usr/bin/gcc` | `./{{artifact}}` |
| cpp | C++ | compiled | `/usr/bin/g++` | `./{{artifact}}` |
| java | Java | compiled | `/usr/bin/javac` | `/usr/bin/java` |
| bash | Bash | interpreted | — | `/usr/bin/bash` |
| js | JavaScript (Node) | interpreted | — | `/usr/bin/node` |
| verilog | Verilog | compiled | `/usr/bin/iverilog` | `/usr/bin/vvp` |
| rust | Rust | compiled | `/usr/bin/rustc` | `./{{artifact}}` |
| go | Go | compiled | `/usr/bin/go` | `./{{artifact}}` |
| haskell | Haskell | compiled | `/usr/bin/ghc` | `./{{artifact}}` |
| ocaml | OCaml | compiled | `/usr/bin/ocamlopt` | `./{{artifact}}` |
| r | R | interpreted | — | `/usr/bin/Rscript` |
| d | D (GDC) | compiled | `/usr/bin/gdc` | `./{{artifact}}` |
| lua | Lua (LuaJIT) | interpreted | — | `/usr/bin/luajit` |
| perl | Perl | interpreted | — | `/usr/bin/perl` |

## Adding a new language

1. Add a block to `config/languages.yml`.
2. Install the compiler/runtime in `scripts/install.sh` (if not already present).
3. Create test cases under `tests/testcases/{id}/` — at minimum a
   `positive-basic/` case with `input.json` and `want.json`.
4. Rebuild the Docker image.

### YAML reference

```yaml
- id: rust                      # unique language identifier
  name: Rust                    # human-readable name
  source_filename: main.rs      # filename for the uploaded source (used inside the jail)
  artifact: main                # (compiled only) output binary name
  build:                        # (compiled only) omit for interpreted languages
    cmd: /usr/bin/rustc         # compiler binary
    args: ["{{flags}}", "-o", "{{artifact}}", "{{source}}"]
    limits:                     # build-stage resource limits
      wall_time_s: 10
      memory_kb: 1048576
      max_processes: 100
    flag_allowlist:              # permitted compiler flags (optional)
      - "-O*"
      - "--edition*"
  run:
    cmd: ./{{artifact}}         # runtime binary (or script path for interpreted)
    args: []                    # extra arguments
    limits:                     # run-stage resource limits
      wall_time_s: 5
      memory_kb: 524288
      max_processes: 64
```

## Template variables

These placeholders are expanded in `cmd` and `args` at request time:

| Variable | Description |
|---|---|
| `{{source}}` | `source_filename` from the config (overridable per-request) |
| `{{artifact}}` | `artifact` from the config (overridable per-request) |
| `{{flags}}` | User-supplied flags from the request body (filtered through `flag_allowlist`) |

## Flag allow-lists

Compiled languages can optionally restrict which compiler flags the caller is
allowed to pass. Flags are matched exactly (`-O2`) or by prefix (`-std=*`
matches `-std=c99`, `-std=c17`, etc.). Requests with disallowed flags get a
400 `invalid_flags` response.

| Language | Allowed flags |
|---|---|
| C/C++ | `-O0`, `-O1`, `-O2`, `-O3`, `-Wall`, `-Wextra`, `-std=*` |
| Rust | `-O*` |
| Go | `-ldflags*`, `-tags*` |
| Others | none restricted |

## Resource limits

Each language defines default limits for build and run stages. These can be
overridden per-request via the `build.limits` and `run.limits` fields. The
limits are enforced by nsjail's rlimit and time_limit mechanisms:

| Limit | Description |
|---|---|
| `wall_time_s` | Wall-clock time in seconds (nsjail `--time_limit`) |
| `memory_kb` | Address space limit in KB (`--rlimit_as`) |
| `max_processes` | Maximum number of processes (`--rlimit_nproc`) |
