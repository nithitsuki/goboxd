package integration

import (
	"flag"
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

// serverPID is the harness-spawned server's process id (0 when the tests
// target a remote server via API_URL). The soak test uses it to count the
// server's open fds across runs; remote targets skip that check.
var serverPID int

// flagLang and flagCase narrow TestFixtures to one language and/or one case:
//
//	go test ./tests/integration/ -lang go -case positive-basic
//
// Without them every advertised-language fixture runs.
var (
	flagLang = flag.String("lang", "", "run fixtures for this language only")
	flagCase = flag.String("case", "", "run this fixture case only (use with -lang)")
)

func TestMain(m *testing.M) {
	flag.Parse()
	if url := os.Getenv("API_URL"); url != "" {
		apiURL = url
		os.Exit(m.Run())
	}

	// Run inside a helper so the defers (binary removal, server kill+wait)
	// execute before the process exits. A bare os.Exit(code) skips them and
	// leaves the harness server alive, which makes go test report
	// "Test I/O incomplete" once it lingers past the exit.
	os.Exit(runWithServer(m))
}

func runWithServer(m *testing.M) int {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	binary := filepath.Join(dir, "..", "..", "bin", "goboxd-test")
	build := exec.Command("go", "build", "-o", binary, "./cmd/goboxd")
	build.Dir = filepath.Join(dir, "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		log.Fatalf("building goboxd binary: %v\n%s", err, out)
	}
	defer func() {
		if err := os.Remove(binary); err != nil && !os.IsNotExist(err) {
			log.Printf("removing binary: %v", err)
		}
	}()

	testPort := "18923"
	apiURL = "http://localhost:" + testPort

	repoRoot := filepath.Join(dir, "..", "..")
	cmd := exec.Command(binary)
	// The registry path (config/languages.yml) is relative to the repo
	// root, so the server must start there (the test binary's cwd is the
	// package dir).
	cmd.Dir = repoRoot
	// GOBOXD_PPROF=1 mounts /debug/pprof on the harness server so the soak
	// test can count server goroutines. The production compose never sets it;
	// the endpoint stays off on the public API (see api.maybeMountPprof).
	cmd.Env = append(os.Environ(), "PORT="+testPort, "GOBOXD_PPROF=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Fatalf("starting goboxd: %v", err)
	}
	serverPID = cmd.Process.Pid
	defer func() {
		if err := cmd.Process.Kill(); err != nil {
			log.Printf("killing server process: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			log.Printf("waiting for server process: %v", err)
		}
	}()

	healthy := false
	for i := 0; i < 10; i++ {
		resp, err := http.Get(apiURL + "/healthz")
		if err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			healthy = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !healthy {
		if err := cmd.Process.Kill(); err != nil {
			log.Printf("killing server after health timeout: %v", err)
		}
		log.Fatalf("server at %s never became healthy", apiURL)
	}

	fmt.Fprintf(os.Stderr, "goboxd integration server ready at %s\n", apiURL)
	return m.Run()
}

func getAPIURL() string {
	return apiURL
}
