package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	sqliterepo "agent/internal/adapters/repository/sqlite"
)

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:4123", "loopback listen address")
	dbPath := flag.String("db-path", resolveSQLitePath(), "SQLite state path")
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

func resolveSQLitePath() string {
	if path := os.Getenv("AGENT_SQLITE_PATH"); path != "" {
		return path
	}
	return defaultSQLitePath()
}

func defaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/state.db"
	}

	return filepath.Join(home, ".local", "share", "agent", "state.db")
}
