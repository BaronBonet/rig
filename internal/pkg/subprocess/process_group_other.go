//go:build !unix

package subprocess

import (
	"os"
	"os/exec"
)

// Without process groups, shutdown reaches only the process itself.

func startInOwnProcessGroup(*exec.Cmd) {}

func terminateProcessGroup(process *os.Process) error {
	return process.Kill()
}

func killProcessGroup(process *os.Process) error {
	return process.Kill()
}
