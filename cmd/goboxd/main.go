package main

import (
	"log"
	"net/http"
	"os"

	"github.com/thesouldev/goboxd/internal/api"
	"github.com/thesouldev/goboxd/internal/config"
	"github.com/thesouldev/goboxd/internal/runner"
)

func main() {
	// Load language registry from YAML
	if err := config.LoadRegistry(); err != nil {
		log.Fatalf("Loading language registry: %v", err)
	}

	// Security hole #7: sweep orphan jail dirs on startup
	runner.SweepOrphans()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := api.NewRouter()

	log.Printf("Starting goboxd on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
