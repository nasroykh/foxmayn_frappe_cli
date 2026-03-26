//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr configures the child process to start a new session,
// detaching it from the terminal's process group so it survives terminal closure.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
