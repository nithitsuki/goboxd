package api

import (
	"embed"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed playground-dist
var playgroundFS embed.FS

// playgroundDistPath is set at startup to the runtime path of the dist directory.
// When empty, the embedded fallback is used (minimal placeholder).
var playgroundDistPath string

func init() {
	// Check if playground-dist is available at a known runtime path
	for _, candidate := range []string{
		"internal/api/playground-dist",
		"/app/internal/api/playground-dist",
		filepath.Join(filepath.Dir(os.Args[0]), "playground-dist"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			playgroundDistPath = candidate
			break
		}
	}
}

// HandlePlayground serves the web playground.
// It prefers the filesystem dist directory (built by Vite) over the embedded fallback.
func HandlePlayground(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/playground")
	if filePath == "" || filePath == "/" {
		filePath = "/index.html"
	}

	// Try filesystem first (runtime-built playground)
	if playgroundDistPath != "" {
		fsPath := path.Join(playgroundDistPath, filePath)
		if data, err := os.ReadFile(fsPath); err == nil {
			writePlaygroundFile(w, filePath, data)
			return
		}
	}

	// Fall back to embedded (minimal placeholder)
	embedPath := path.Join("playground-dist", filePath)
	data, err := playgroundFS.ReadFile(embedPath)
	if err != nil {
		// Serve the placeholder index.html for any missing path
		data, err = playgroundFS.ReadFile("playground-dist/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	writePlaygroundFile(w, filePath, data)
}

func writePlaygroundFile(w http.ResponseWriter, filePath string, data []byte) {
	ext := path.Ext(filePath)
	switch ext {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript")
	case ".css":
		w.Header().Set("Content-Type", "text/css")
	case ".json":
		w.Header().Set("Content-Type", "application/json")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".woff2":
		w.Header().Set("Content-Type", "font/woff2")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// PlaygroundExists returns true if a real playground build is available.
func PlaygroundExists() bool {
	if playgroundDistPath != "" {
		return true
	}
	// Check if the embedded build has actual content (not just placeholder stub)
	data, err := playgroundFS.ReadFile("playground-dist/index.html")
	if err != nil {
		return false
	}
	// The stub is tiny (< 200 bytes). A real build is 300+ bytes.
	return len(data) > 200
}
