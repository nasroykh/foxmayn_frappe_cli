//go:build !windows

package desktopinstall

import "os"

// openLocked opens path for reading. Only Windows can deny other writers.
func openLocked(path string) (*os.File, error) { return os.Open(path) }

// registryVersion is the DisplayVersion the NSIS setup wrote; only Windows
// has one.
var registryVersion = func() (string, bool) { return "", false }
