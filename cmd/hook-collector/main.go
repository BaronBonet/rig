package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:4123", "loopback listen address")
	logPath := flag.String("log-file", ".agent/observability/codex-hooks.jsonl", "JSONL output path")
	flag.Parse()

	srv := newServer(*logPath, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", srv.handleHook)

	log.Printf("hook-collector listening on http://%s/hook", *listenAddr)
	log.Fatal(http.ListenAndServe(*listenAddr, mux))
}
