//go:build !unix

package taskdaemon

import (
	"io"
	"os/exec"
)

func configureDetachedProcess(cmd *exec.Cmd, devNull io.ReadWriteCloser) {
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
}
