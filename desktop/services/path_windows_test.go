//go:build windows

package services

import (
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// The PATH edit runs against a scratch key under HKCU\Software, never the
// real Environment key.
func TestAddPathEntry(t *testing.T) {
	key := fmt.Sprintf(`Software\ffd-test-%d`, time.Now().UnixNano())
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.ALL_ACCESS)
	if err != nil {
		t.Skipf("cannot create a scratch registry key: %v", err)
	}
	k.Close()
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, key) })

	dir := `C:\Users\Me\AppData\Local\Programs\ffc`
	// No Path value yet.
	if added, err := addPathEntry(registry.CURRENT_USER, key, dir); err != nil || !added {
		t.Fatalf("first add = %v, %v", added, err)
	}
	// Already there, also with another case and a trailing backslash.
	if added, err := addPathEntry(registry.CURRENT_USER, key, `c:\users\me\appdata\local\programs\ffc\`); err != nil || added {
		t.Fatalf("second add = %v, %v", added, err)
	}

	// An existing value keeps its %VAR% entries unexpanded and its type.
	k, _ = registry.OpenKey(registry.CURRENT_USER, key, registry.SET_VALUE|registry.QUERY_VALUE)
	defer k.Close()
	if err := k.SetExpandStringValue("Path", `%USERPROFILE%\bin;;D:\tools`); err != nil {
		t.Fatal(err)
	}
	if added, err := addPathEntry(registry.CURRENT_USER, key, dir); err != nil || !added {
		t.Fatalf("add to existing = %v, %v", added, err)
	}
	got, typ, err := k.GetStringValue("Path")
	if err != nil || typ != registry.EXPAND_SZ || got != `%USERPROFILE%\bin;D:\tools;`+dir {
		t.Errorf("Path = %q (type %d), %v", got, typ, err)
	}
}
