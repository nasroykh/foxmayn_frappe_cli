//go:build windows

package cmd

import "os/exec"

// setSysProcAttr is a no-op on Windows.
// To run ffc mcp in the background on Windows, use:
//
//	start /B ffc mcp --port 8765
func setSysProcAttr(_ *exec.Cmd) {}
