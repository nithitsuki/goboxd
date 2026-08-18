// Package models tests lock the public API contract: JSON field names,
// pointer semantics for optional limits, and error shapes. Any change here
// is a breaking API change and must be deliberate.
package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRunRequestJSONContract locks the request field names and the optional
// pointer semantics of the limits.
func TestRunRequestJSONContract(t *testing.T) {
	raw := `{
		"language": "py3",
		"source": "print(1)",
		"source_filename": "main.py",
		"artifact_filename": "main",
		"build": {"limits": {"wall_time_s": 3, "memory_kb": 65536, "cpu_time_s": 20}, "flags": ["-O2"]},
		"run": {"limits": {"max_processes": 8, "cpu_time_s": 2}},
		"tests": [{"stdin": "x", "expected_stdout": "y"}]
	}`
	var req RunRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Language != "py3" || req.Source != "print(1)" {
		t.Errorf("basic fields wrong: %+v", req)
	}
	if req.SourceFilename != "main.py" || req.ArtifactFilename != "main" {
		t.Errorf("filename fields wrong")
	}
	if req.Build == nil || req.Build.Limits == nil || req.Build.Limits.WallTimeS == nil ||
		*req.Build.Limits.WallTimeS != 3 || req.Build.Limits.MemoryKB == nil {
		t.Errorf("build limits pointer semantics wrong: %+v", req.Build)
	}
	if req.Build.Limits.CpuTimeS == nil || *req.Build.Limits.CpuTimeS != 20 {
		t.Errorf("build cpu_time_s pointer semantics wrong: %+v", req.Build.Limits)
	}
	if len(req.Build.Flags) != 1 || req.Build.Flags[0] != "-O2" {
		t.Errorf("build flags wrong")
	}
	if req.Run == nil || req.Run.Limits == nil || req.Run.Limits.MaxProcesses == nil ||
		*req.Run.Limits.MaxProcesses != 8 {
		t.Errorf("run limits pointer semantics wrong: %+v", req.Run)
	}
	if req.Run.Limits.CpuTimeS == nil || *req.Run.Limits.CpuTimeS != 2 {
		t.Errorf("run cpu_time_s pointer semantics wrong: %+v", req.Run.Limits)
	}
	if req.Run.Limits.WallTimeS != nil || req.Run.Limits.MemoryKB != nil {
		t.Errorf("absent limits must stay nil (nil means 'use default')")
	}
	if len(req.Tests) != 1 || req.Tests[0].Stdin != "x" || req.Tests[0].ExpectedStdout != "y" {
		t.Errorf("tests wrong")
	}
}

// TestLimitsOmitEmpty locks the omitempty serialization: an empty limits
// object must not appear in the response (nil fields drop out).
func TestLimitsOmitEmpty(t *testing.T) {
	b, err := json.Marshal(StageConfig{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "limits") || strings.Contains(string(b), "flags") {
		t.Errorf("empty StageConfig should marshal to empty object, got %s", b)
	}
}

// TestRunResponseJSONContract locks the response field names.
func TestRunResponseJSONContract(t *testing.T) {
	resp := RunResponse{
		Status: "accepted",
		Build:  BuildResult{Status: "ok", DurationMs: 12, CpuTimeMs: 300},
		Tests:  []TestResult{{Status: "accepted", MemoryPeakKB: 2048, CpuTimeMs: 25, ExitCode: 139, TerminationSignal: 11}},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round struct {
		Status string       `json:"status"`
		Build  BuildResult  `json:"build"`
		Tests  []TestResult `json:"tests"`
	}
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.Status != "accepted" || round.Build.Status != "ok" ||
		len(round.Tests) != 1 || round.Tests[0].MemoryPeakKB != 2048 {
		t.Errorf("response contract broken: %s", b)
	}
	if round.Build.CpuTimeMs != 300 {
		t.Errorf("build cpu_time_ms not round-tripped: %s", b)
	}
	if round.Tests[0].CpuTimeMs != 25 {
		t.Errorf("test cpu_time_ms not round-tripped: %s", b)
	}
	if round.Tests[0].ExitCode != 139 || round.Tests[0].TerminationSignal != 11 {
		t.Errorf("exit facts not round-tripped: %s", b)
	}
	// cpu_time_ms is always present (it is a result fact, not an optional limit).
	if !strings.Contains(string(b), `"cpu_time_ms"`) {
		t.Errorf("response must contain cpu_time_ms fields: %s", b)
	}
	if !strings.Contains(string(b), `"exit_code"`) || !strings.Contains(string(b), `"termination_signal"`) {
		t.Errorf("response must contain exit_code and termination_signal fields: %s", b)
	}
}

// TestZeroValueStatusNotExecuted locks the zero-value contract: a fresh
// TestResult (status never set) must serialize as "not_executed", never "".
// The empty string is a real unhandled state the parallel path used to emit
// on cancellation; after C5 the zero value must be meaningful.
func TestZeroValueStatusNotExecuted(t *testing.T) {
	b, err := json.Marshal(TestResult{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"status":"not_executed"`) {
		t.Errorf("zero TestResult must read status not_executed, got %s", b)
	}
}

// TestExitFactsAlwaysPresent locks the zero-value contract: exit facts are
// result facts, not optional limits, so a zero TestResult must still carry
// both fields in the marshaled JSON (no omitempty).
func TestExitFactsAlwaysPresent(t *testing.T) {
	b, err := json.Marshal(TestResult{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"exit_code":0`) {
		t.Errorf("zero TestResult must carry exit_code:0, got %s", b)
	}
	if !strings.Contains(string(b), `"termination_signal":0`) {
		t.Errorf("zero TestResult must carry termination_signal:0, got %s", b)
	}
}

// TestAPIErrorShape locks the error envelope: {"error":{"code","message"}}.
func TestAPIErrorShape(t *testing.T) {
	b, err := json.Marshal(APIError{Error: ErrorDetail{Code: "x", Message: "y"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"error":{"code":"x","message":"y"}}` {
		t.Errorf("APIError envelope = %s", b)
	}
}
