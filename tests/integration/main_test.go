package integration

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

var apiURL string

func TestMain(m *testing.M) {
	// If API_URL is already set, use it (manual docker workflow)
	if url := os.Getenv("API_URL"); url != "" {
		apiURL = url
		os.Exit(m.Run())
	}

	// Otherwise, start the server ourselves
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	// Build the binary
	binary := filepath.Join(dir, "..", "..", "bin", "goboxd-test")
	build := exec.Command("go", "build", "-o", binary, "./cmd/goboxd")
	build.Dir = filepath.Join(dir, "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		log.Fatalf("building goboxd binary: %v\n%s", err, out)
	}
	defer os.Remove(binary)

	// Pick a random port
	port := "0"
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "PORT="+port)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Fatalf("starting goboxd: %v", err)
	}

	// Find which port it picked by checking what it prints or scanning
	// Since we set PORT=0, Go will pick a random port. We need to know it.
	// Simplest approach: give it a fixed port and rely on it being free.
	// Let's retry with an explicit port.
	cmd.Process.Kill()
	cmd.Wait()

	// Use a fixed port for testing
	testPort := "18923"
	apiURL = "http://localhost:" + testPort

	cmd = exec.Command(binary)
	cmd.Env = append(os.Environ(), "PORT="+testPort)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Fatalf("starting goboxd: %v", err)
	}
	defer cmd.Process.Kill()

	// Wait for /healthz
	healthy := false
	for i := 0; i < 10; i++ {
		resp, err := http.Get(apiURL + "/healthz")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			healthy = true
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !healthy {
		cmd.Process.Kill()
		log.Fatalf("server at %s never became healthy", apiURL)
	}

	fmt.Fprintf(os.Stderr, "goboxd integration server ready at %s\n", apiURL)
	code := m.Run()
	cmd.Process.Kill()
	os.Exit(code)
}

func getAPIURL() string {
	return apiURL
}
