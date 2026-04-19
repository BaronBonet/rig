//go:build unix

package taskdaemon

import (
	"io"
	"os/exec"
	"syscall"
)

func configureDetachedProcess(cmd *exec.Cmd, devNull io.ReadWriteCloser) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
}
