package integration

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

// BenchmarkRunThroughput (TODO #16) measures sustained POST /run throughput
// for the Python trivial payload against the harness server: the per-core
// "N runs/sec" number in docs/benchmarks.md. Run with:
//
//	go test -bench=BenchmarkRunThroughput -benchtime=5s ./tests/integration/
//
// or `make bench`. Requires root + nsjail (same guard as the soak test);
// without them the benchmark skips instead of failing.
func BenchmarkRunThroughput(b *testing.B) {
	if os.Geteuid() != 0 {
		b.Skip("requires root to run nsjail")
	}
	if _, err := exec.LookPath("nsjail"); err != nil {
		b.Skip("nsjail not found, skipping benchmark")
	}

	body := soakBody()
	client := &http.Client{Timeout: 10 * time.Second}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Post(getAPIURL()+"/run", "application/json", bytes.NewReader(body))
		if err != nil {
			b.Fatalf("POST /run: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		if err := resp.Body.Close(); err != nil {
			b.Fatalf("closing response body: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("POST /run returned %d, want 200", resp.StatusCode)
		}
	}
}
