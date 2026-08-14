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
		"build": {"limits": {"wall_time_s": 3, "memory_kb": 65536}, "flags": ["-O2"]},
		"run": {"limits": {"max_processes": 8}},
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
	if len(req.Build.Flags) != 1 || req.Build.Flags[0] != "-O2" {
		t.Errorf("build flags wrong")
	}
	if req.Run == nil || req.Run.Limits == nil || req.Run.Limits.MaxProcesses == nil ||
		*req.Run.Limits.MaxProcesses != 8 {
		t.Errorf("run limits pointer semantics wrong: %+v", req.Run)
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
		Build:  BuildResult{Status: "ok", DurationMs: 12},
		Tests:  []TestResult{{Status: "accepted", MemoryPeakKB: 2048}},
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
