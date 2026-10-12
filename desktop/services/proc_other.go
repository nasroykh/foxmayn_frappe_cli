//go:build !windows

package services

import (
	"os/exec"
	"syscall"
)

func hideWindow(*exec.Cmd) {}

// ProcessRunning reports whether the process pid is still running.
func ProcessRunning(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
