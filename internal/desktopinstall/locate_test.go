package desktopinstall

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// swapLocate replaces the file system and environment seams for one test.
func swapLocate(t *testing.T, env map[string]string, writable func(string) bool) {
	t.Helper()
	oe, oev, ow, ol, oa, orv := getenv, evalSymlinks, dirWritable, lstat, applicationsDir, registryVersion
	t.Cleanup(func() {
		getenv, evalSymlinks, dirWritable, lstat, applicationsDir, registryVersion = oe, oev, ow, ol, oa, orv
	})
	getenv = func(k string) string { return env[k] }
	evalSymlinks = func(p string) (string, error) { return p, nil }
	if writable == nil {
		writable = func(string) bool { return true }
	}
	dirWritable = writable
}

const macExe = "/Applications/Foxmayn Frappe Desktop.app/Contents/MacOS/foxmayn-frappe-desktop"

func TestLocateSelfDarwin(t *testing.T) {
	swapLocate(t, nil, nil)
	cases := []struct {
		name, exe string
		want      error
		bundle    string
	}{
		{"applications", macExe, nil, "/Applications/Foxmayn Frappe Desktop.app"},
		{"user applications", "/Users/me/Applications/Foxmayn Frappe Desktop.app/Contents/MacOS/foxmayn-frappe-desktop", nil, "/Users/me/Applications/Foxmayn Frappe Desktop.app"},
		{"anywhere else", "/opt/tools/Foxmayn Frappe Desktop.app/Contents/MacOS/foxmayn-frappe-desktop", nil, "/opt/tools/Foxmayn Frappe Desktop.app"},
		{"disk image", "/Volumes/Foxmayn Frappe Desktop/Foxmayn Frappe Desktop.app/Contents/MacOS/foxmayn-frappe-desktop", ErrNotInstalledCopy, ""},
		{"translocated", "/private/var/folders/ab/T/AppTranslocation/1234/d/Foxmayn Frappe Desktop.app/Contents/MacOS/foxmayn-frappe-desktop", ErrNotInstalledCopy, ""},
		{"wrong bundle name", "/Applications/Other.app/Contents/MacOS/foxmayn-frappe-desktop", ErrNotInstalledCopy, ""},
		{"wrong executable", "/Applications/Foxmayn Frappe Desktop.app/Contents/MacOS/ffc", ErrNotInstalledCopy, ""},
		{"bare binary", "/tmp/foxmayn-frappe-desktop", ErrNotInstalledCopy, ""},
		{"not under Contents/MacOS", "/Applications/Foxmayn Frappe Desktop.app/foxmayn-frappe-desktop", ErrNotInstalledCopy, ""},
	}
	for _, c := range cases {
		got, err := LocateSelf("darwin", c.exe)
		if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
			continue
		}
		if c.want == nil && (got.Path != c.bundle || !got.Installed || got.GOOS != "darwin") {
			t.Errorf("%s: target = %+v", c.name, got)
		}
	}

	// Unwritable parent, then unwritable bundle.
	dirWritable = func(d string) bool { return d != "/Applications" }
	if _, err := LocateSelf("darwin", macExe); !errors.Is(err, ErrNotWritable) {
		t.Errorf("unwritable parent: %v", err)
	}
	dirWritable = func(d string) bool { return !strings.HasSuffix(d, ".app") }
	if _, err := LocateSelf("darwin", macExe); !errors.Is(err, ErrNotWritable) {
		t.Errorf("unwritable bundle: %v", err)
	}
	// A symlink to a disk image resolves there and is refused.
	dirWritable = func(string) bool { return true }
	evalSymlinks = func(p string) (string, error) {
		return "/Volumes/X/Foxmayn Frappe Desktop.app/Contents/MacOS/foxmayn-frappe-desktop", nil
	}
	if _, err := LocateSelf("darwin", macExe); !errors.Is(err, ErrNotInstalledCopy) {
		t.Errorf("symlink into /Volumes: %v", err)
	}
	evalSymlinks = func(p string) (string, error) { return "", fs.ErrNotExist }
	if _, err := LocateSelf("darwin", macExe); !errors.Is(err, ErrNotInstalledCopy) {
		t.Errorf("unresolvable: %v", err)
	}
}

func TestLocateSelfWindows(t *testing.T) {
	swapLocate(t, map[string]string{"LOCALAPPDATA": `C:\Users\Me\AppData\Local`}, nil)
	const good = `C:\Users\Me\AppData\Local\Programs\Foxmayn Frappe Desktop\foxmayn-frappe-desktop.exe`
	for _, exe := range []string{
		good,
		strings.ToUpper(good),
		`c:/users/me/appdata/local/programs/foxmayn frappe desktop/foxmayn-frappe-desktop.exe`,
	} {
		if got, err := LocateSelf("windows", exe); err != nil || got.GOOS != "windows" || !got.Installed || got.Path != exe {
			t.Errorf("%s: %+v %v", exe, got, err)
		}
	}
	for _, exe := range []string{
		`C:\Program Files\Foxmayn Frappe Desktop\foxmayn-frappe-desktop.exe`,
		`C:\Users\Me\AppData\Local\Programs\Foxmayn Frappe Desktop\other.exe`,
		`C:\Users\Me\Downloads\foxmayn-frappe-desktop.exe`,
		`C:\Users\Other\AppData\Local\Programs\Foxmayn Frappe Desktop\foxmayn-frappe-desktop.exe`,
	} {
		if _, err := LocateSelf("windows", exe); !errors.Is(err, ErrNotInstalledCopy) {
			t.Errorf("%s: %v", exe, err)
		}
	}
	dirWritable = func(string) bool { return false }
	if _, err := LocateSelf("windows", good); !errors.Is(err, ErrNotWritable) {
		t.Errorf("unwritable: %v", err)
	}
	getenv = func(string) string { return "" }
	if _, err := LocateSelf("windows", good); !errors.Is(err, ErrNotInstalledCopy) {
		t.Errorf("no LOCALAPPDATA: %v", err)
	}
	if _, err := LocateSelf("linux", good); !errors.Is(err, ErrUnsupported) {
		t.Errorf("linux: %v", err)
	}
}

func TestEligible(t *testing.T) {
	swapLocate(t, nil, nil)
	cur, _ := ParseVersion("0.9.0")
	dev, _ := ParseVersion("0.0.0-dev")
	if err := Eligible("darwin", cur, macExe); err != nil {
		t.Errorf("installed: %v", err)
	}
	if err := Eligible("darwin", dev, macExe); !errors.Is(err, ErrNotInstalledCopy) {
		t.Errorf("dev build: %v", err)
	}
	if err := Eligible("darwin", cur, "/tmp/x"); !errors.Is(err, ErrNotInstalledCopy) {
		t.Errorf("elsewhere: %v", err)
	}
	if err := Eligible("linux", cur, macExe); !errors.Is(err, ErrUnsupported) {
		t.Errorf("linux: %v", err)
	}
}

func TestLocateInstalledDarwin(t *testing.T) {
	swapLocate(t, nil, nil)
	apps, home := t.TempDir(), t.TempDir()
	applicationsDir = apps
	in := New("darwin")
	var plutilArgs []string
	in.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		plutilArgs = append([]string{name}, args...)
		return []byte("0.9.0\n"), nil
	}
	bundle := func(dir string) string {
		p := filepath.Join(dir, bundleApp)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Nothing installed: /Applications when writable, else ~/Applications.
	got, err := in.LocateInstalled(home, os.Getenv)
	if err != nil || got.Installed || got.Path != filepath.Join(apps, bundleApp) || got.Version != "" {
		t.Errorf("none, writable: %+v %v", got, err)
	}
	dirWritable = func(d string) bool { return d != apps }
	if got, _ = in.LocateInstalled(home, os.Getenv); got.Installed || got.Path != filepath.Join(home, "Applications", bundleApp) {
		t.Errorf("none, /Applications unwritable: %+v", got)
	}
	dirWritable = func(string) bool { return true }

	// Only in ~/Applications.
	userApp := bundle(filepath.Join(home, "Applications"))
	got, err = in.LocateInstalled(home, os.Getenv)
	if err != nil || !got.Installed || got.Path != userApp || got.Version != "0.9.0" {
		t.Errorf("user copy: %+v %v", got, err)
	}
	want := []string{toolPlutil, "-extract", "CFBundleShortVersionString", "raw", "-o", "-", filepath.Join(userApp, "Contents", "Info.plist")}
	if strings.Join(plutilArgs, "|") != strings.Join(want, "|") {
		t.Errorf("plutil argv = %q", plutilArgs)
	}

	// Both: /Applications wins.
	sysApp := bundle(apps)
	if got, _ = in.LocateInstalled(home, os.Getenv); got.Path != sysApp {
		t.Errorf("both: %+v", got)
	}

	// Installed but not writable.
	dirWritable = func(d string) bool { return d != apps }
	if _, err := in.LocateInstalled(home, os.Getenv); !errors.Is(err, ErrNotWritable) {
		t.Errorf("unwritable install: %v", err)
	}
	dirWritable = func(string) bool { return true }

	// A version that cannot be read does not make the app "not installed".
	in.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no plutil") }
	if got, err = in.LocateInstalled(home, os.Getenv); err != nil || !got.Installed || got.Version != "" {
		t.Errorf("unreadable version: %+v %v", got, err)
	}
}

func TestLocateInstalledWindows(t *testing.T) {
	swapLocate(t, nil, nil)
	in := New("windows")
	lad := `C:\Users\Me\AppData\Local`
	env := func(k string) string {
		if k == "LOCALAPPDATA" {
			return lad
		}
		return ""
	}
	exe := lad + `\Programs\Foxmayn Frappe Desktop\foxmayn-frappe-desktop.exe`

	got, err := in.LocateInstalled("", env)
	if err != nil || got.Installed || got.Path != exe || got.GOOS != "windows" {
		t.Errorf("not installed: %+v %v", got, err)
	}

	self, _ := os.Executable()
	fi, _ := os.Lstat(self)
	lstat = func(p string) (fs.FileInfo, error) {
		if p == exe {
			return fi, nil
		}
		return nil, fs.ErrNotExist
	}
	registryVersion = func() (string, bool) { return "0.9.0", true }
	got, err = in.LocateInstalled("", env)
	if err != nil || !got.Installed || got.Version != "0.9.0" || got.Path != exe {
		t.Errorf("installed: %+v %v", got, err)
	}
	if _, err := in.LocateInstalled("", func(string) string { return "" }); err == nil {
		t.Error("no LOCALAPPDATA accepted")
	}
	if _, err := New("linux").LocateInstalled("", env); !errors.Is(err, ErrUnsupported) {
		t.Errorf("linux: %v", err)
	}
}
