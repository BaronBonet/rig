package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	logPath := flag.String("log-file", ".agent/observability/codex-hooks.jsonl", "JSONL input path")
	flag.Parse()

	summary, err := renderSummary(*logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stdout, summary)
}
