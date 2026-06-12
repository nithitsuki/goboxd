package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const testcasesDir = "/app/testcases"

// TestcaseEntry is a single testcase reference returned by the list endpoint.
type TestcaseEntry struct {
	Lang string `json:"lang"`
	Name string `json:"name"`
}

// TestcaseDetail is the full testcase returned by the detail endpoint.
type TestcaseDetail struct {
	Lang  string          `json:"lang"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Want  json.RawMessage `json:"want"`
}

// HandleTestcasesList returns all available testcases as a flat list.
func HandleTestcasesList(w http.ResponseWriter, r *http.Request) {
	entries, err := discoverTestcases()
	if err != nil {
		log.Printf("testcases discovery: %v", err)
		writeInternalError(w, "failed to list testcases")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		log.Printf("failed to encode testcases: %v", err)
	}
}

// HandleTestcasesGet returns a single testcase by lang and name.
func HandleTestcasesGet(w http.ResponseWriter, r *http.Request) {
	lang := r.PathValue("lang")
	name := r.PathValue("name")

	if lang == "" || name == "" {
		writeError(w, "invalid_request", "lang and name are required")
		return
	}

	// Security: prevent path traversal
	if strings.Contains(lang, "..") || strings.Contains(name, "..") ||
		strings.Contains(lang, "/") || strings.Contains(name, "/") {
		writeError(w, "invalid_request", "invalid lang or name")
		return
	}

	inputPath := filepath.Join(testcasesDir, lang, name, "input.json")
	wantPath := filepath.Join(testcasesDir, lang, name, "want.json")

	inputData, err := os.ReadFile(inputPath)
	if err != nil {
		writeError(w, "not_found", fmt.Sprintf("testcase %s/%s not found", lang, name))
		return
	}

	wantData, err := os.ReadFile(wantPath)
	if err != nil {
		writeError(w, "not_found", fmt.Sprintf("testcase %s/%s want.json not found", lang, name))
		return
	}

	detail := TestcaseDetail{
		Lang:  lang,
		Name:  name,
		Input: json.RawMessage(inputData),
		Want:  json.RawMessage(wantData),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(detail); err != nil {
		log.Printf("failed to encode testcase: %v", err)
	}
}

// discoverTestcases walks the testcases directory and returns all entries.
func discoverTestcases() ([]TestcaseEntry, error) {
	var entries []TestcaseEntry

	langDirs, err := os.ReadDir(testcasesDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", testcasesDir, err)
	}

	for _, langDir := range langDirs {
		if !langDir.IsDir() {
			continue
		}
		lang := langDir.Name()
		langPath := filepath.Join(testcasesDir, lang)
		testDirs, err := os.ReadDir(langPath)
		if err != nil {
			continue
		}
		for _, td := range testDirs {
			if !td.IsDir() {
				continue
			}
			// Verify it has input.json and want.json
			inputPath := filepath.Join(langPath, td.Name(), "input.json")
			wantPath := filepath.Join(langPath, td.Name(), "want.json")
			if _, err := os.Stat(inputPath); err != nil {
				continue
			}
			if _, err := os.Stat(wantPath); err != nil {
				continue
			}
			entries = append(entries, TestcaseEntry{
				Lang: lang,
				Name: td.Name(),
			})
		}
	}

	return entries, nil
}
