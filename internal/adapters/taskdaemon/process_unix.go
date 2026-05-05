//go:build unix

package taskdaemon

import (
	"os"
	"os/exec"
	"syscall"
)

func configureDetachedProcess(cmd *exec.Cmd, devNull *os.File, output *os.File) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = devNull
	cmd.Stdout = output
	cmd.Stderr = output
}
