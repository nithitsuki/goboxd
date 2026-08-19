package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// captureLog redirects the process-wide log output to a buffer for the
// duration of the test. No test in this package runs in parallel, so swapping
// the global logger is safe.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(orig)
	})
	return &buf
}

// TestLoggingNoBodyBuffering locks C3: the access log must not buffer the
// response body. The client still receives the entire stream, and the
// recorder must have no field capable of retaining a copy (a bytes.Buffer or
// a []byte accumulator). The reflection check fails if a buffer is
// reintroduced, which the old code would have passed silently.
func TestLoggingNoBodyBuffering(t *testing.T) {
	const want = 2 * 1024 * 1024

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		payload := bytes.Repeat([]byte("A"), want)
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("write: %v", err)
		}
	})

	// The client receives the entire 2MB stream (streaming is preserved).
	w := httptest.NewRecorder()
	LoggingMiddleware(inner).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/run", nil))
	if w.Body.Len() != want {
		t.Fatalf("client body length = %d, want %d", w.Body.Len(), want)
	}

	// The bodyRecorder must have no field that can retain the body. This is a
	// structural contract: reintroducing `body bytes.Buffer` or a []byte
	// accumulator fails this test deterministically (the old code would have
	// passed a behavioral check, since it still forwarded the body).
	typ := reflect.TypeOf(bodyRecorder{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type == reflect.TypeOf(bytes.Buffer{}) {
			t.Errorf("bodyRecorder has a bytes.Buffer field %q — response body may be retained", f.Name)
		}
		if f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Uint8 {
			t.Errorf("bodyRecorder has a []byte field %q — response body may be retained", f.Name)
		}
	}
}

// TestLoggingRunStatusHeader locks the C3 contract: LoggingMiddleware reads
// run_status from the X-Run-Status response header (not from the body) on
// POST /run, for 200s and 503s alike. No header set means the field is
// omitted from the log line.
func TestLoggingRunStatusHeader(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		runStatus string
	}{
		{name: "200 accepted", status: http.StatusOK, runStatus: "accepted"},
		{name: "503 queue_full", status: http.StatusServiceUnavailable, runStatus: "queue_full"},
		{name: "no header omits run_status", status: http.StatusOK, runStatus: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.runStatus != "" {
					w.Header().Set("X-Run-Status", tc.runStatus)
				}
				w.WriteHeader(tc.status)
			})

			w := httptest.NewRecorder()
			LoggingMiddleware(inner).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/run", nil))

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}

			line := buf.String()
			if tc.runStatus == "" {
				if strings.Contains(line, "run_status") {
					t.Errorf("log line must omit run_status when no header is set\nlog: %s", line)
				}
				return
			}
			want := `"run_status":"` + tc.runStatus + `"`
			if !strings.Contains(line, want) {
				t.Errorf("log line missing %s\nlog: %s", want, line)
			}
		})
	}
}

// TestLoggingRun503QueueFull locks the C3 contract end to end on the real 503
// /run path: HandleRun must set X-Run-Status on the wire and the access log
// must carry run_status for the rejection. A zero-capacity gate rejects the
// request before any execution, so no nsjail is needed.
func TestLoggingRun503QueueFull(t *testing.T) {
	orig := gate
	gate = newAdmissionGate(0, 0)
	defer func() { gate = orig }()

	buf := captureLog(t)

	body := `{"language":"py3","source":"print(1)","tests":[{"stdin":"","expected_stdout":"1\n"}]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	LoggingMiddleware(http.HandlerFunc(HandleRun)).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got := w.Header().Get("X-Run-Status"); got != "queue_full" {
		t.Errorf("X-Run-Status header = %q, want queue_full", got)
	}
	line := buf.String()
	if !strings.Contains(line, `"run_status":"queue_full"`) {
		t.Errorf("log line missing run_status queue_full\nlog: %s", line)
	}
}

// TestLoggingRun503ShuttingDown locks the C3 contract on the real 503
// shutting_down /run path: HandleRun must set X-Run-Status and the log must
// carry it. A stopped gate rejects before execution, so no nsjail is needed.
func TestLoggingRun503ShuttingDown(t *testing.T) {
	orig := gate
	g := newAdmissionGate(1, 0)
	g.Stop()
	gate = g
	defer func() { gate = orig }()

	buf := captureLog(t)

	body := `{"language":"py3","source":"print(1)","tests":[{"stdin":"","expected_stdout":"1\n"}]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	LoggingMiddleware(http.HandlerFunc(HandleRun)).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got := w.Header().Get("X-Run-Status"); got != "shutting_down" {
		t.Errorf("X-Run-Status header = %q, want shutting_down", got)
	}
	line := buf.String()
	if !strings.Contains(line, `"run_status":"shutting_down"`) {
		t.Errorf("log line missing run_status shutting_down\nlog: %s", line)
	}
}

// TestLoggingRun200CarriesRunStatus locks the C3 contract on the real 200
// /run path: whenever HandleRun computes a top-level status it must set the
// X-Run-Status header, and the log must carry it. As root this executes a
// real py3 run (accepted); as non-root the jail materialization fails
// (internal_error). Either way the header is set and the log carries a
// non-empty run_status.
func TestLoggingRun200CarriesRunStatus(t *testing.T) {
	buf := captureLog(t)

	body := `{"language":"py3","source":"print(1)","tests":[{"stdin":"","expected_stdout":"1\n"}]}`
	req := httptest.NewRequest(http.MethodPost, "/run", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	LoggingMiddleware(http.HandlerFunc(HandleRun)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := w.Header().Get("X-Run-Status")
	if got == "" {
		t.Error("X-Run-Status header empty on a 200 /run response")
	}
	line := buf.String()
	if !strings.Contains(line, `"run_status":"`+got+`"`) {
		t.Errorf("log line missing run_status %s\nlog: %s", got, line)
	}
}
