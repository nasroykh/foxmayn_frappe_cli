//go:build windows

package cmd

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// setSysProcAttr starts the child in its own process group with no console, so
// closing the terminal or pressing Ctrl+C there does not kill the server (D9).
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}

// terminateProcess stops the process. Windows does not support SIGTERM via
// os.Process.Signal, so we Kill it; the HTTP server cannot drain gracefully
// there, but the process is reliably stopped (M16).
func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}

// isProcessRunning reports whether pid refers to a live process: the handle
// opens and its exit code is STILL_ACTIVE. Access-denied means it exists (D8).
func isProcessRunning(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true // cannot tell; never claim dead without proof
	}
	return code == 259 // STILL_ACTIVE
}
