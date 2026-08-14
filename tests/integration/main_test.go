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

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "PORT="+testPort)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Fatalf("starting goboxd: %v", err)
	}
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
	code := m.Run()
	os.Exit(code)
}

func getAPIURL() string {
	return apiURL
}
