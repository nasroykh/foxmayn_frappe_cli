//go:build windows

package cmd

import (
	"os"
	"os/exec"
)

// setSysProcAttr is a no-op on Windows.
// To run ffc mcp in the background on Windows, use:
//
//	start /B ffc mcp --port 8765
func setSysProcAttr(_ *exec.Cmd) {}

// terminateProcess stops the process. Windows does not support SIGTERM via
// os.Process.Signal, so we Kill it; the HTTP server cannot drain gracefully
// there, but the process is reliably stopped (M16).
func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}
