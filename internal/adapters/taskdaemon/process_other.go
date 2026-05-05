//go:build !unix

package taskdaemon

import (
	"os"
	"os/exec"
)

func configureDetachedProcess(cmd *exec.Cmd, devNull *os.File, output *os.File) {
	cmd.Stdin = devNull
	cmd.Stdout = output
	cmd.Stderr = output
}
