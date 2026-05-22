package main

import (
	"log"
	"net/http"
	"os"

	"github.com/thesouldev/goboxd/internal/api"
)

func main() {
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
