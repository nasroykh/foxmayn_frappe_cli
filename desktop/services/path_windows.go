//go:build windows

package services

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// addUserPath appends dir to the user PATH (HKCU\Environment) when it is not
// there yet, as install.ps1 does: the raw value is kept unexpanded, entries
// are compared whole, and running programs are told about the change.
func addUserPath(dir string) (bool, error) {
	added, err := addPathEntry(registry.CURRENT_USER, "Environment", dir)
	if added {
		broadcastEnvironmentChange()
	}
	return added, err
}

// addPathEntry appends dir to the Path value of root\key unless it is there.
func addPathEntry(root registry.Key, key, dir string) (bool, error) {
	k, err := registry.OpenKey(root, key, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	raw, _, err := k.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return false, err
	}
	if pathHas(raw, dir, true) {
		return false, nil
	}
	var entries []string
	for _, e := range strings.Split(raw, ";") {
		if e != "" {
			entries = append(entries, e)
		}
	}
	if err := k.SetExpandStringValue("Path", strings.Join(append(entries, dir), ";")); err != nil {
		return false, err
	}
	return true, nil
}

// broadcastEnvironmentChange sends WM_SETTINGCHANGE "Environment" so new
// terminals started from Explorer see the new PATH. Best effort.
func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	_, _, _ = proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
