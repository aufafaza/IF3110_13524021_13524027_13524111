package main

import (
	"log"
	"net/http"
	"os"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web"
)

func main() {
	addr := ":" + getenv("PORT", "8080")
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, web.New()))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
