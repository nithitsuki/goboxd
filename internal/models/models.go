// Package models defines the shared data types for the goboxd API.
// These types are used by the HTTP handlers, the runner, and the test
// fixtures. The JSON serialization tags match the public API contract.
package models

import "encoding/json"

// Limits represents resource constraints for build or run stages.
// All fields are optional pointers; nil means "use language default".
type Limits struct {
	WallTimeS    *int `json:"wall_time_s,omitempty"`
	MemoryKB     *int `json:"memory_kb,omitempty"`
	MaxProcesses *int `json:"max_processes,omitempty"`
	CpuTimeS     *int `json:"cpu_time_s,omitempty"`
}

// StageConfig represents the configuration for either the build or run stage.
type StageConfig struct {
	Limits *Limits  `json:"limits,omitempty"`
	Flags  []string `json:"flags,omitempty"`
}

// TestCase represents a single test case input and expected output.
type TestCase struct {
	Stdin          string `json:"stdin"`
	ExpectedStdout string `json:"expected_stdout"`
}

// RunRequest is the incoming payload for POST /run
type RunRequest struct {
	Language         string       `json:"language"`
	Source           string       `json:"source"`
	SourceFilename   string       `json:"source_filename,omitempty"`
	ArtifactFilename string       `json:"artifact_filename,omitempty"`
	Build            *StageConfig `json:"build,omitempty"`
	Run              *StageConfig `json:"run,omitempty"`
	Tests            []TestCase   `json:"tests"`
	MaxParallel      *int         `json:"max_parallel,omitempty"`
	MaxOutputBytes   *int         `json:"max_output_bytes,omitempty"`
}

// ResultStatus is the closed vocabulary of per-test result statuses. The
// zero value ResultStatus("") is not a member: a TestResult whose status was
// never set serializes as "not_executed" (see MarshalJSON) instead of an
// empty string. This set is closed — a typo in a constant name fails to
// compile, and new values require a deliberate review of every consumer.
type ResultStatus string

const (
	ResultAccepted           ResultStatus = "accepted"
	ResultBuildFailed        ResultStatus = "build_failed"
	ResultInternalError      ResultStatus = "internal_error"
	ResultRuntimeError       ResultStatus = "runtime_error"
	ResultTimeExceeded       ResultStatus = "time_exceeded"
	ResultMemoryExceeded     ResultStatus = "memory_exceeded"
	ResultWrongOutput        ResultStatus = "wrong_output"
	ResultWhitespaceMismatch ResultStatus = "output_whitespace_mismatch"
	ResultNotExecuted        ResultStatus = "not_executed"
	ResultCancelled          ResultStatus = "cancelled"
	ResultCPUTimeExceeded    ResultStatus = "cpu_time_exceeded"
)

// Valid reports whether s is a member of the closed ResultStatus vocabulary.
// The zero value ("") is invalid: an unset status must never silently read
// as a real outcome.
func (s ResultStatus) Valid() bool {
	switch s {
	case ResultAccepted, ResultBuildFailed, ResultInternalError, ResultRuntimeError,
		ResultTimeExceeded, ResultMemoryExceeded, ResultWrongOutput,
		ResultWhitespaceMismatch, ResultNotExecuted, ResultCancelled,
		ResultCPUTimeExceeded:
		return true
	default:
		return false
	}
}

// MarshalJSON maps the zero value to "not_executed" so a fresh TestResult
// never serializes with an empty status. Known values serialize byte-
// identically to their plain strings, keeping the wire contract unchanged.
func (s ResultStatus) MarshalJSON() ([]byte, error) {
	if s == "" {
		return []byte(`"not_executed"`), nil
	}
	return json.Marshal(string(s))
}

// BuildStatus is the closed vocabulary of build outcomes. The zero value
// BuildStatus("") is invalid and is deliberately NOT mapped to "ok": a
// forgotten status must not read as success (see BuildStatus.Valid).
type BuildStatus string

const (
	BuildOk            BuildStatus = "ok"
	BuildFailed        BuildStatus = "failed"
	BuildInternalError BuildStatus = "internal_error"
)

// BuildStatus has no MarshalJSON: a zero value would serialize as ""
// next to a build_failed top-level status. That is unreachable today
// (ExecuteRun always sets buildRes.Status; computeTopLevelStatus gates
// on Valid()), so the wire format stays the raw constants.
// Valid reports whether s is a member of the closed BuildStatus vocabulary.
// The zero value ("") is invalid.
func (s BuildStatus) Valid() bool {
	switch s {
	case BuildOk, BuildFailed, BuildInternalError:
		return true
	default:
		return false
	}
}

// BuildResult represents the outcome of the build phase.
type BuildResult struct {
	Status     BuildStatus `json:"status"` // BuildOk, BuildFailed, BuildInternalError
	Stdout     string      `json:"stdout"`
	Stderr     string      `json:"stderr"`
	DurationMs int         `json:"duration_ms"`
	CpuTimeMs  int         `json:"cpu_time_ms"`
}

// TestResult represents the outcome of a single test.
// ExitCode and TerminationSignal are always present (no omitempty):
// (0, 0) reads for a clean user exit 0 and for no process. The Status
// field distinguishes the two cases.
type TestResult struct {
	Status            ResultStatus `json:"status"`
	Stdout            string       `json:"stdout"`
	Stderr            string       `json:"stderr"`
	DurationMs        int          `json:"duration_ms"`
	CpuTimeMs         int          `json:"cpu_time_ms"`
	MemoryPeakKB      int          `json:"memory_peak_kb"`
	ExitCode          int          `json:"exit_code"`
	TerminationSignal int          `json:"termination_signal"`
}

// RunResponse is the outgoing payload for POST /run (HTTP 200)
type RunResponse struct {
	// Status is deliberately a plain string: it is the wire format for the
	// top-level run status, always derived from the typed constants at the
	// handler seam. A typed field here would surface "" at the boundary
	// instead of being caught by computeTopLevelStatus.
	Status string       `json:"status"`
	Build  BuildResult  `json:"build"`
	Tests  []TestResult `json:"tests"`
}

// APIError represents the standard error format for HTTP 400 responses
type APIError struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail holds the code and message for an API error.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
