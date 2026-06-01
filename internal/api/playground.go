package api

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed playground/index.html playground/assets/*
var playgroundFS embed.FS

func HandlePlayground(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/playground")
	if filePath == "" || filePath == "/" {
		filePath = "/index.html"
	}
	filePath = path.Join("playground", filePath)

	data, err := playgroundFS.ReadFile(filePath)
	if err != nil {
		data, err = playgroundFS.ReadFile("playground/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		filePath = "playground/index.html"
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
	_, err := playgroundFS.ReadFile("playground/index.html")
	return err == nil
}
