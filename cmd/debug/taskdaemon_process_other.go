//go:build !unix

package main

import (
	"os"
	"os/exec"
)

func configureDetachedProcess(cmd *exec.Cmd, devNull *os.File) {
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
}
