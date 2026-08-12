# Package Reference

This reference comes from the Go documentation in the source code. It
describes the packages that make up goboxd. Generate the source output with
these commands:

```bash
go doc -all ./internal/api
go doc -all ./internal/config
go doc -all ./internal/models
go doc -all ./internal/runner
go doc -all ./cmd/goboxd
```

The reference has one section for each package. Keep the code examples
exactly as they are. The prose follows Simplified Technical English.

## cmd/goboxd

Command goboxd is an HTTP service. It compiles and runs untrusted code inside
nsjail sandboxes. It accepts source code with POST /run. It compiles the code
when the language needs compilation. It executes the code against test cases.
It returns the results for each test.

The service loads the configuration from config/languages.yml at startup. It
removes orphan jail directories at startup.

## internal/api

Package api implements the HTTP handlers, the routes, the request validation,
and the structured logging for the goboxd service. It registers the routes
with the Go 1.22 method-pattern mux syntax. All responses are JSON. The
handler validates POST /run requests. It then dispatches them to the runner
package for sandboxed execution inside nsjail.

### Functions

```go
func HandleHealthz(w http.ResponseWriter, r *http.Request)
```

HandleHealthz answers GET /healthz. It returns HTTP 200 with `{"status":"ok"}`.

```go
func HandleInfo(w http.ResponseWriter, r *http.Request)
```

HandleInfo answers GET /info. It returns service metadata and runtime
statistics.

```go
func HandlePlayground(w http.ResponseWriter, r *http.Request)
```

HandlePlayground serves the web playground. It prefers the filesystem dist
directory over the embedded fallback. The Vite build creates the dist
directory.

```go
func HandleReadyz(w http.ResponseWriter, r *http.Request)
```

HandleReadyz answers GET /readyz. It returns the readiness state of nsjail and
all language runtimes.

```go
func HandleRun(w http.ResponseWriter, r *http.Request)
```

HandleRun answers POST /run. It validates the request and executes the code.

```go
func HandleTestcasesGet(w http.ResponseWriter, r *http.Request)
```

HandleTestcasesGet returns one testcase. It selects the testcase by language
and name.

```go
func HandleTestcasesList(w http.ResponseWriter, r *http.Request)
```

HandleTestcasesList returns all available testcases as a flat list.

```go
func LoggingMiddleware(next http.Handler) http.Handler
```

LoggingMiddleware wraps an http.Handler. It emits structured JSON request
logs.

```go
func NewRouter() http.Handler
```

NewRouter constructs and wires up the API routes with structured logging. It
registers these endpoints:

- `GET /healthz` — liveness check
- `GET /readyz` — readiness probe, checks nsjail and all languages
- `GET /info` — service metadata and runtime stats
- `POST /run` — execute untrusted code
- `GET /playground` — web UI, only when embedded with embed.FS
- `GET /testcases` — list all testcases
- `GET /testcases/{lang}/{name}` — get a specific testcase

```go
func PlaygroundExists() bool
```

PlaygroundExists returns true when a real playground build is available.

```go
func RecoveryMiddleware(next http.Handler) http.Handler
```

RecoveryMiddleware catches panics in handlers. One bad request does not crash
the server. A crash would reset all in-flight connections.

```go
func GetStats() jobStatsSnapshot
```

GetStats returns a snapshot of the current job stats.

### Types

```go
type TestcaseDetail struct {
	Lang  string          `json:"lang"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Want  json.RawMessage `json:"want"`
}
```

TestcaseDetail is the full testcase. The detail endpoint returns this type.

```go
type TestcaseEntry struct {
	Lang string `json:"lang"`
	Name string `json:"name"`
}
```

TestcaseEntry is one testcase reference. The list endpoint returns this type.

## internal/config

Package config loads the language registry from a YAML configuration file.
Each language has a source filename, an optional build command, a run
command, and resource limits. The service populates the registry once at
startup with LoadRegistry. The registry is read-only after startup.

The service expands template variables in command arguments at request time.
The variables are `{{source}}`, `{{artifact}}`, and `{{flags}}`.

### Variables

```go
var DefaultRegistry = map[string]LanguageConfig{}
```

DefaultRegistry stores the language configuration. The service populates it
at startup from YAML. It has a hardcoded fallback.

```go
var RegistryPath = "config/languages.yml"
```

RegistryPath is the path to the YAML config file. Override it for testing.

### Functions

```go
func LoadRegistry() error
```

LoadRegistry reads the YAML config and populates DefaultRegistry. It returns
an error when the file is missing or invalid.

### Types

```go
type ConfigYAML struct {
	Languages []LanguageYAML `yaml:"languages"`
}
```

ConfigYAML is the top-level YAML structure.

```go
type LanguageConfig struct {
	ID               string
	Name             string
	SourceFilename   string
	ArtifactFilename string
	BuildCmd         []string // pre-expanded build command + args (empty for interpreted)
	RunCmd           []string // pre-expanded run command + args
	DefaultLimits    Limits   // merged limits (build limits for compiled, run for interpreted)
	BuildLimits      Limits   // YAML build limits (for compiled languages)
	RunLimits        Limits   // YAML run limits
	FlagAllowlist    []string
}
```

LanguageConfig holds the fully-resolved execution parameters for a language.
BuildCmd is empty for interpreted languages. DefaultLimits uses the build
limits for compiled languages and the run limits for interpreted languages.

```go
type LanguageYAML struct {
	ID             string    `yaml:"id"`
	Name           string    `yaml:"name"`
	SourceFilename string    `yaml:"source_filename"`
	Artifact       string    `yaml:"artifact,omitempty"`
	Build          *StageCmd `yaml:"build,omitempty"`
	Run            StageCmd  `yaml:"run"`
}
```

LanguageYAML is the per-language structure in the YAML config.

```go
type Limits struct {
	WallTimeS    int `yaml:"wall_time_s"`
	MemoryKB     int `yaml:"memory_kb"`
	MaxProcesses int `yaml:"max_processes"`
}
```

Limits represents resource constraints. It applies to the build or run stage.

```go
type StageCmd struct {
	Cmd           string   `yaml:"cmd"`
	Args          []string `yaml:"args"`
	Limits        Limits   `yaml:"limits"`
	FlagAllowlist []string `yaml:"flag_allowlist,omitempty"`
}
```

StageCmd describes a build or run command with template variables. The
service expands `{{source}}` and `{{artifact}}` for each request. It replaces
`{{flags}}` with the user-supplied flags at request time.

## internal/models

Package models defines the shared data types for the goboxd API. The HTTP
handlers, the runner, and the test fixtures use these types. The JSON
serialization tags match the public API specification.

### Types

```go
type APIError struct {
	Error ErrorDetail `json:"error"`
}
```

APIError represents the standard error format for HTTP 400 responses.

```go
type BuildResult struct {
	Status     string `json:"status"` // ok, failed, internal_error
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int    `json:"duration_ms"`
}
```

BuildResult represents the outcome of the build phase.

```go
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
```

ErrorDetail holds the code and message for an API error.

```go
type Limits struct {
	WallTimeS    *int `json:"wall_time_s,omitempty"`
	MemoryKB     *int `json:"memory_kb,omitempty"`
	MaxProcesses *int `json:"max_processes,omitempty"`
}
```

Limits represents resource constraints for the build or run stage. All fields
are optional pointers. A nil value means "use the language default".

```go
type RunRequest struct {
	Language         string       `json:"language"`
	Source           string       `json:"source"`
	SourceFilename   string       `json:"source_filename,omitempty"`
	ArtifactFilename string       `json:"artifact_filename,omitempty"`
	Build            *StageConfig `json:"build,omitempty"`
	Run              *StageConfig `json:"run,omitempty"`
	Tests            []TestCase   `json:"tests"`
}
```

RunRequest is the incoming payload for POST /run.

```go
type RunResponse struct {
	Status string       `json:"status"`
	Build  BuildResult  `json:"build"`
	Tests  []TestResult `json:"tests"`
}
```

RunResponse is the outgoing payload for POST /run. It returns with HTTP 200.

```go
type StageConfig struct {
	Limits *Limits  `json:"limits,omitempty"`
	Flags  []string `json:"flags,omitempty"`
}
```

StageConfig represents the configuration for the build or run stage.

```go
type TestCase struct {
	Stdin          string `json:"stdin"`
	ExpectedStdout string `json:"expected_stdout"`
}
```

TestCase represents one test case input and its expected output.

```go
type TestResult struct {
	Status       string `json:"status"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
	DurationMs   int    `json:"duration_ms"`
	MemoryPeakKB int    `json:"memory_peak_kb"`
}
```

TestResult represents the outcome of one test.

## internal/runner

Package runner executes untrusted user code inside nsjail sandboxes. Each
request gets a unique temporary jail directory. The service creates the
directory with os.MkdirTemp. It writes the source code to disk inside the
jail. It compiles the code for compiled languages. It then executes the code
against each test case. The service wraps every invocation of the compiler or the user
program in nsjail. This gives namespace isolation, resource limits, and
filesystem containment.

nsjail enforces the resource limits with its `--time_limit` and `--rlimit`
flags. The limits are wall time, memory, and process count. The service caps
output at 64 KiB per stream. This prevents unbounded memory
consumption.

Infrastructure errors differ from user-code errors. An infrastructure error
means that nsjail itself failed. Such errors produce the `internal_error`
status. They do not produce the misleading `build_failed` or `runtime_error`
status.

### Functions

```go
func ExecuteRun(req models.RunRequest, lc config.LanguageConfig) (models.BuildResult, []models.TestResult, error)
```

ExecuteRun runs the full request. It builds the code when the language needs
compilation. It then runs each test case. It returns the build result and the
test results.

```go
func SweepOrphans()
```

SweepOrphans removes jail directories older than 30 minutes. Call it at
startup.
