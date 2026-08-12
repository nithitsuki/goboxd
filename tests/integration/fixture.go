package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Fixture represents a single test case loaded from input.json and want.json.
type Fixture struct {
	Lang  string
	Name  string
	Input InputFixture `json:"input"`
	Want  WantFixture  `json:"want"`
}

// InputFixture is the POST /run request we send.
type InputFixture struct {
	Language         string          `json:"language"`
	Source           string          `json:"source"`
	SourceFilename   string          `json:"source_filename,omitempty"`
	ArtifactFilename string          `json:"artifact_filename,omitempty"`
	Build            json.RawMessage `json:"build,omitempty"`
	Run              json.RawMessage `json:"run,omitempty"`
	Tests            []InputTest     `json:"tests"`
}

// InputTest describes one test case input.
type InputTest struct {
	Stdin          string `json:"stdin"`
	ExpectedStdout string `json:"expected_stdout"`
}

// WantFixture describes the fields we assert on in the response.
type WantFixture struct {
	Status string     `json:"status"`
	Build  WantBuild  `json:"build"`
	Tests  []WantTest `json:"tests"`
}

// WantBuild describes expected build result fields.
type WantBuild struct {
	Status string `json:"status"`
}

// WantTest describes expected test result fields.
type WantTest struct {
	Status string `json:"status"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

// loadFixture reads input.json and want.json from a test case directory.
func loadFixture(dir string) (Fixture, error) {
	var f Fixture
	f.Name = filepath.Base(dir)

	inputPath := filepath.Join(dir, "input.json")
	wantPath := filepath.Join(dir, "want.json")

	if err := readJSON(inputPath, &f.Input); err != nil {
		return f, fmt.Errorf("loading input from %s: %w", inputPath, err)
	}
	if err := readJSON(wantPath, &f.Want); err != nil {
		return f, fmt.Errorf("loading want from %s: %w", wantPath, err)
	}

	return f, nil
}

// discoverFixtures walks tests/testcases/*/*/ and loads all fixtures.
func discoverFixtures(root string) ([]Fixture, error) {
	var fixtures []Fixture

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading testcases root %s: %w", root, err)
	}

	for _, langDir := range entries {
		if !langDir.IsDir() {
			continue
		}
		langPath := filepath.Join(root, langDir.Name())
		tests, err := os.ReadDir(langPath)
		if err != nil {
			return nil, fmt.Errorf("reading language dir %s: %w", langPath, err)
		}
		skipPenetration := os.Getenv("SKIP_PENETRATION") != ""
		for _, tc := range tests {
			if !tc.IsDir() {
				continue
			}
			if skipPenetration && strings.HasPrefix(tc.Name(), "penetration-") {
				continue
			}
			tcPath := filepath.Join(langPath, tc.Name())
			f, err := loadFixture(tcPath)
			if err != nil {
				return nil, fmt.Errorf("loading fixture %s: %w", tcPath, err)
			}
			// Qualify the name with the language so subtest names are unique.
			// Go appends opaque #N suffixes to colliding subtest names, which
			// makes failures hard to map back to a language.
			f.Lang = langDir.Name()
			f.Name = langDir.Name() + "/" + f.Name
			fixtures = append(fixtures, f)
		}
	}

	return fixtures, nil
}

func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}
