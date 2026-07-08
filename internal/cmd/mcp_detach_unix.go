//go:build !windows

package cmd

import (
	"os"
	"os/exec"
	"syscall"
)

// setSysProcAttr configures the child process to start a new session,
// detaching it from the terminal's process group so it survives terminal closure.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// terminateProcess asks the process to shut down gracefully (SIGTERM), so the
// HTTP server can drain and exit cleanly (M16).
func terminateProcess(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}
