package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	sqliterepo "agent/internal/adapters/repository/sqlite"
	"agent/internal/infrastructure"
)

func main() {
	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}

	listenAddr := flag.String("listen", "127.0.0.1:4123", "loopback listen address")
	dbPath := flag.String("db-path", cfg.SQLite.Path, "SQLite state path")
	flag.Parse()

	repo, err := sqliterepo.NewRepository(sqliterepo.Config{Path: *dbPath})
	if err != nil {
		log.Fatal(err)
	}
	if err := repo.IsAvailable(context.Background()); err != nil {
		log.Fatal(err)
	}

	srv := newServer(repo, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", srv.handleHook)

	log.Printf("hook-collector listening on http://%s/hook", *listenAddr)
	log.Fatal(http.ListenAndServe(*listenAddr, mux))
}
