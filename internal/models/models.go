package models

// Limits represents the resource constraints for build or run stages.
type Limits struct {
	WallTimeS    *int `json:"wall_time_s,omitempty"`
	MemoryKB     *int `json:"memory_kb,omitempty"`
	MaxProcesses *int `json:"max_processes,omitempty"`
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
}

// BuildResult represents the outcome of the build phase.
type BuildResult struct {
	Status     string `json:"status"` // ok, failed, internal_error
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int    `json:"duration_ms"`
}

// TestResult represents the outcome of a single test.
type TestResult struct {
	Status       string `json:"status"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
	DurationMs   int    `json:"duration_ms"`
	MemoryPeakKB int    `json:"memory_peak_kb"`
}

// RunResponse is the outgoing payload for POST /run (HTTP 200)
type RunResponse struct {
	Status string       `json:"status"`
	Build  *BuildResult `json:"build,omitempty"`
	Tests  []TestResult `json:"tests"`
}

// APIError represents the standard error format for HTTP 400 responses
type APIError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
