//go:build !unix

package statusstream

import (
	"io"
	"os/exec"
)

func configureDetachedProcess(cmd *exec.Cmd, devNull io.ReadWriteCloser) {
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
}
