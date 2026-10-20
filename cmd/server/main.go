package main

import (
	"context"
	"log"
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/config"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	pool, err := platformdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if err := platformdb.Migrate(ctx, pool, migrations.Platform()); err != nil {
		log.Fatal(err)
	}

	log.Printf("listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, web.New()))
}
