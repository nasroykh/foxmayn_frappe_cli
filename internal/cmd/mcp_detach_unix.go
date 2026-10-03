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

// isProcessRunning is a cheap liveness check (signal 0). It cannot tell whether
// the PID still belongs to our server after PID reuse — mcpHealth does that.
// EPERM means the process exists but is not ours, which counts as alive.
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
