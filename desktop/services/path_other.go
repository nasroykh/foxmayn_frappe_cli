//go:build !windows

package services

// addUserPath changes nothing outside Windows: the macOS installer only
// tells the user when ~/.local/bin is not on the login shell's PATH.
func addUserPath(string) (bool, error) { return false, nil }
