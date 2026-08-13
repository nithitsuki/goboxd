# Language Registry

You configure languages through `config/languages.yml`. The service loads
the file at startup with `internal/config/config.go`. You do not need to
change Go code to add a language. You add a YAML entry. If the runtime or compiler is not in the Docker
image, you also add an install script in `scripts/lang_install/`.

## Currently registered

| id | name | type | compiler | runtime |
|---|---|---|---|---|
| py3 | Python 3 | interpreted | — | `/usr/bin/python3` |
| py2 | Python 2 | interpreted | — | `/usr/local/bin/python2.7` |
| c | C | compiled | `/usr/bin/gcc` | `./{{artifact}}` |
| cpp | C++ | compiled | `/usr/bin/g++` | `./{{artifact}}` |
| csharp | C# | compiled | `csharp-build` wrapper (Roslyn) | `/usr/local/dotnet/dotnet` |
| dart | Dart | compiled | `/usr/local/dart/bin/dart compile exe` | `./{{artifact}}` |
| elixir | Elixir | interpreted | — | `/usr/bin/elixir` |
| java | Java | compiled | `/usr/bin/javac` | `/usr/bin/java` |
| kotlin | Kotlin | compiled | `/usr/local/kotlinc/bin/kotlinc` | `/usr/bin/java -jar` |
| bash | Bash | interpreted | — | `/usr/bin/bash` |
| js | JavaScript (Node) | interpreted | — | `/usr/bin/node` |
| php | PHP | interpreted | — | `/usr/bin/php` |
| racket | Racket | interpreted | — | `/usr/local/bin/racket` |
| ruby | Ruby | interpreted | — | `/usr/bin/ruby` |
| scala | Scala 3 | compiled | `/usr/local/scala3/bin/scalac` | `/usr/local/scala3/bin/scala` |
| swift | Swift | compiled | `/usr/local/swift/usr/bin/swiftc` | `./{{artifact}}` |
| ts | TypeScript | compiled | `/usr/local/bin/tsc` | `/usr/bin/node` |
| verilog | Verilog | compiled | `/usr/bin/iverilog` | `/usr/bin/vvp` |
| rust | Rust | compiled | `/usr/bin/rustc` | `./{{artifact}}` |
| go | Go | compiled | `/usr/bin/go` | `./{{artifact}}` |
| haskell | Haskell | compiled | `/usr/bin/ghc` | `./{{artifact}}` |
| ocaml | OCaml | compiled | `/usr/bin/ocamlopt` | `./{{artifact}}` |
| r | R | interpreted | — | `/usr/bin/Rscript` |
| d | D (GDC) | compiled | `/usr/bin/gdc` | `./{{artifact}}` |
| lua | Lua (LuaJIT) | interpreted | — | `/usr/bin/luajit` |
| perl | Perl | interpreted | — | `/usr/bin/perl` |
| erl | Erlang | compiled | `/usr/bin/erlc` | `erl -noshell -pa /app -s solution start -s init stop` |
| lisp | Lisp (SBCL) | interpreted | — | `/usr/bin/sbcl --script {{source}}` |
| pascal | Pascal (Free Pascal) | compiled | `/usr/bin/fpc` | `./{{artifact}}` |

## Version notes

The Docker image runs Debian bookworm. Some versions differ from the LeetCode
environments:

- Ruby 3.1 (LeetCode: 3.2), Elixir 1.14 (LeetCode: 1.17), Node.js 18
  (LeetCode: 22), Python 3.11 (LeetCode: 3.14), PHP 8.2 and C++ (GCC 14)
  match, and Go uses the module version from `go.mod`.
- The toolchains that are not in the Debian repos come from their official
  releases: .NET SDK 10 (C# 14), Swift 6.0.3 (Debian 12 build), Scala 3.3.1,
  Kotlin 2.1.10, Dart 3.2.6, TypeScript 5.7.3, and Racket 8.15 (Chez).
  Python 2.7.18 is copied from the `python:2.7-slim` image because Debian
  bookworm does not ship Python 2.

## Adding a new language

1. Add a block to `config/languages.yml`.
2. Create an install script at `scripts/lang_install/<id>.sh`. The script
   installs and verifies the compiler or runtime. See the existing scripts
   for examples. Pin each apt package to an exact version. Use the
   `pkg=VERSION` form. Pin each downloaded toolchain to an exact version.
   `scripts/check-pins.sh` verifies both rules in CI.
3. Create test cases under `tests/testcases/{id}/`. Create at minimum a
   `positive-basic/` case with `input.json` and `want.json`.
4. Rebuild the Docker image with `make build`. The staged Dockerfile rebuilds
   only the new language layer and the layers after it. The previous language
   installations stay in the cache.

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

The server expands these placeholders in `cmd` and `args` at request time:

| Variable | Description |
|---|---|
| `{{source}}` | `source_filename` from the config (overridable per-request) |
| `{{artifact}}` | `artifact` from the config (overridable per-request) |
| `{{flags}}` | User-supplied flags from the request body (filtered through `flag_allowlist`) |

## Flag allow-lists

Compiled languages can restrict which compiler flags the caller can pass.
The service matches flags exactly (`-O2`) or by prefix. For example, `-std=*`
matches `-std=c99` and `-std=c17`. Requests with disallowed flags get a 400
`invalid_flags` response.

| Language | Allowed flags |
|---|---|
| C/C++ | `-O0`, `-O1`, `-O2`, `-O3`, `-Wall`, `-Wextra`, `-std=*` |
| Rust | `-O*` |
| Go | `-ldflags*`, `-tags*` |
| Others | none restricted |

## Resource limits

Each language defines default limits for the build and run stages. You can
override these limits per-request through the `build.limits` and `run.limits`
fields. nsjail enforces the limits with its rlimit and time_limit mechanisms:

| Limit | Description |
|---|---|
| `wall_time_s` | Wall-clock time in seconds (nsjail `--time_limit`) |
| `memory_kb` | Address space limit in KB (`--rlimit_as`) |
| `max_processes` | Maximum number of processes (`--rlimit_nproc`) |
