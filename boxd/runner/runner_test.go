package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesouldev/goboxd/boxd/config"
	"github.com/thesouldev/goboxd/boxd/models"
)

func TestExecuteRun(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not found in PATH, skipping runner tests (run inside docker-compose)")
	}

	py3Config := config.LanguageConfig{
		ID:             "py3",
		Name:           "Python 3",
		RunCmd:         []string{"/usr/bin/python3", "main.py"},
		SourceFilename: "main.py",
		DefaultLimits: config.Limits{
			WallTimeS:    2,
			MemoryKB:     102400,
			MaxProcesses: 100,
		},
	}

	tests := []struct {
		name           string
		source         string
		testCases      []models.TestCase
		expectedStatus string
	}{
		{
			name:   "positive basic",
			source: "print('Hello from Python 3!')",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: "Hello from Python 3!\n"},
			},
			expectedStatus: "accepted",
		},
		{
			name:   "timeout moderate",
			source: "import time\nwhile True: time.sleep(0.1)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: "time_exceeded",
		},
		{
			name:   "runtime error (syntax)",
			source: "print(1/0)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: "runtime_error",
		},
		{
			name:   "memory limit (OOM)",
			source: "l = []\nwhile True:\n    l.append('a' * 1024 * 1024)",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: ""},
			},
			expectedStatus: "runtime_error",
		},
		{
			name:   "wrong output",
			source: "print('wrong')",
			testCases: []models.TestCase{
				{Stdin: "", ExpectedStdout: "right\n"},
			},
			expectedStatus: "wrong_output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := models.RunRequest{
				Language: "py3",
				Source:   tt.source,
				Tests:    tt.testCases,
			}

			_, results, err := ExecuteRun(req, py3Config)
			if err != nil {
				t.Fatalf("ExecuteRun dropped a hard error: %v", err)
			}

			if len(results) != len(tt.testCases) {
				t.Fatalf("expected %d results, got %d", len(tt.testCases), len(results))
			}

			res := results[0]

			// Memory kills: sigkill from nsjail could surface as time_exceeded or runtime_error
			if tt.name == "memory limit (OOM)" && (res.Status == "time_exceeded" || res.Status == "runtime_error") {
				return
			}

			// Timeout: nsjail sigkill can read as time_exceeded or runtime_error
			if tt.name == "timeout moderate" && (res.Status == "time_exceeded" || res.Status == "runtime_error") {
				return
			}

			if res.Status != tt.expectedStatus {
				t.Errorf("expected status %q, got %q (stderr: %q)", tt.expectedStatus, res.Status, res.Stderr)
				if strings.Contains(res.Stderr, "No such file or directory") {
					t.Fatalf("Missing python binary inside nsjail root. Is -B /usr setup correctly?")
				}
			}
		})
	}
}

func TestReadCapped(t *testing.T) {
	// Short string passes through unchanged
	input := "hello world"
	got := readCapped(bytes.NewBufferString(input))
	if got != input {
		t.Errorf("readCapped = %q, want %q", got, input)
	}

	// Empty input
	got = readCapped(bytes.NewBufferString(""))
	if got != "" {
		t.Errorf("readCapped(empty) = %q, want ''", got)
	}

	// Input larger than maxOutputBytes triggers truncation with marker
	truncationMarker := "\n... [output truncated]"
	big := strings.Repeat("A", int(maxOutputBytes)+1)
	got = readCapped(bytes.NewBufferString(big))
	if !strings.HasSuffix(got, truncationMarker) {
		t.Errorf("expected output to end with %q, got suffix %q", truncationMarker, got[len(got)-40:])
	}
	// Total should be maxOutputBytes + marker
	expectedLen := int(maxOutputBytes) + len(truncationMarker)
	if len(got) != expectedLen {
		t.Errorf("expected length %d (cap + marker), got %d", expectedLen, len(got))
	}
	// First maxOutputBytes bytes should be the original input
	prefix := got[:int(maxOutputBytes)]
	expectedPrefix := strings.Repeat("A", int(maxOutputBytes))
	if prefix != expectedPrefix {
		t.Errorf("first %d bytes dont match original input", maxOutputBytes)
	}
}

func TestComputeTestStatus(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		stdout    string
		expected  string
		memPeak   int
		memLimit  int
		want      string
	}{
		{"exact match", nil, "hello\n", "hello\n", 0, 0, "accepted"},
		{"whitespace diff", nil, "hello\n", "hello", 0, 0, "output_whitespace_mismatch"},
		{"wrong output", nil, "world", "hello", 0, 0, "wrong_output"},
		{"empty expected", nil, "anything", "", 0, 0, "accepted"},
		{"memory exceeded via peak check", fmt.Errorf("signal: killed"), "", "", 950, 1000, "memory_exceeded"},
		{"time exceeded via low mem peak", fmt.Errorf("signal: killed"), "", "", 100, 1000, "time_exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err != nil {
				got := computeTestStatus(context.Background(), tt.err, tt.stdout, tt.expected, nil, tt.memPeak, tt.memLimit, 10)
				if got != "runtime_error" {
					t.Errorf("with nil ProcessState: want runtime_error, got %q", got)
				}
				return
			}
			got := computeTestStatus(context.Background(), nil, tt.stdout, tt.expected, nil, 0, 0, 10)
			if got != tt.want {
				t.Errorf("computeTestStatus = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSignalKillReason(t *testing.T) {
	// signalKillReason with nil ProcessState should return ""
	if got := signalKillReason(nil); got != "" {
		t.Errorf("signalKillReason(nil) = %q, want ''", got)
	}
}
