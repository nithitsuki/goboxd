package api

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed playground-dist/index.html playground-dist/assets/*
var playgroundFS embed.FS

// HandlePlayground serves the embedded web playground for interactive testing.
// It rewrites requests to serve files from the embedded playground/ directory.
func HandlePlayground(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/playground")
	if filePath == "" || filePath == "/" {
		filePath = "/index.html"
	}
	filePath = path.Join("playground-dist", filePath)

	data, err := playgroundFS.ReadFile(filePath)
	if err != nil {
		data, err = playgroundFS.ReadFile("playground-dist/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		filePath = "playground-dist/index.html"
	}

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
	if _, err := w.Write(data); err != nil {
		return
	}
}

// PlaygroundExists returns true if the playground is built and embeddable.
func PlaygroundExists() bool {
	_, err := playgroundFS.ReadFile("playground-dist/index.html")
	return err == nil
}
