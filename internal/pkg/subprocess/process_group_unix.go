//go:build unix

package subprocess

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// startInOwnProcessGroup makes the process lead a new process group, so
// signals sent to the group also reach the children it starts.
func startInOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateProcessGroup asks every process in the group to exit.
func terminateProcessGroup(process *os.Process) error {
	return signalProcessGroup(process, syscall.SIGTERM)
}

// killProcessGroup kills every process in the group.
func killProcessGroup(process *os.Process) error {
	return signalProcessGroup(process, syscall.SIGKILL)
}

// signalProcessGroup reports a group with no process left as
// os.ErrProcessDone, which exec.Cmd.Cancel treats as success.
func signalProcessGroup(process *os.Process, signal syscall.Signal) error {
	err := syscall.Kill(-process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
