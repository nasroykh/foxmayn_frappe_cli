//go:build windows

package desktopinstall

import (
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// uninstallKey is where the per-user NSIS setup records the install
// (INFO_COMPANYNAME + INFO_PRODUCTNAME, wails_tools.nsh).
const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\FoxmaynFoxmayn Frappe Desktop`

// openLocked opens path for reading while denying every other opener write
// and delete access (FILE_SHARE_READ only): while the handle is open nothing
// can modify, replace or rename the file, so what was hashed is what runs.
func openLocked(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// registryVersion is the DisplayVersion the setup wrote for the current user.
var registryVersion = func() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("DisplayVersion")
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}
