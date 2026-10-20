package main

import (
	"log"
	"net/http"
	"os"

	"tubes1/internal/server"
)

func main() {
	addr := ":" + getenv("PORT", "8080")
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, server.New()))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
