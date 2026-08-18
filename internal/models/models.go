// Package models defines the shared data types for the goboxd API.
// These types are used by the HTTP handlers, the runner, and the test
// fixtures. The JSON serialization tags match the public API contract.
package models

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

// BuildResult represents the outcome of the build phase.
type BuildResult struct {
	Status     string `json:"status"` // ok, failed, internal_error
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int    `json:"duration_ms"`
	CpuTimeMs  int    `json:"cpu_time_ms"`
}

// TestResult represents the outcome of a single test.
// ExitCode and TerminationSignal are always present (no omitempty):
// (0, 0) reads for a clean user exit 0 and for no process. The Status
// field distinguishes the two cases.
type TestResult struct {
	Status            string `json:"status"`
	Stdout            string `json:"stdout"`
	Stderr            string `json:"stderr"`
	DurationMs        int    `json:"duration_ms"`
	CpuTimeMs         int    `json:"cpu_time_ms"`
	MemoryPeakKB      int    `json:"memory_peak_kb"`
	ExitCode          int    `json:"exit_code"`
	TerminationSignal int    `json:"termination_signal"`
}

// RunResponse is the outgoing payload for POST /run (HTTP 200)
type RunResponse struct {
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
