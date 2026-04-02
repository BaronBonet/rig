package main

import (
	"fmt"
	"os"

	"agent/internal/adapters/handler/cli"
)

func main() {
	if err := cli.NewRootCommand(cli.Dependencies{}).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
