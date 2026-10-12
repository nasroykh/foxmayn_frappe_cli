package desktopinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	bundleApp  = "Foxmayn Frappe Desktop.app"
	bundleID   = "com.foxmayn.frappedesktop"
	bundleExec = "foxmayn-frappe-desktop"
	// winRel is the per-user install location below %LOCALAPPDATA%
	// (project.nsi), with backslashes: it is matched on any host.
	winRel = `Programs\Foxmayn Frappe Desktop\foxmayn-frappe-desktop.exe`
)

// Target is an installed copy of the app (or where one would go). On macOS
// Path is the .app bundle, on Windows the exe. Version is what the system
// says is installed ("" when unknown).
type Target struct {
	GOOS      string
	Path      string
	Installed bool
	Version   string
}

// Seams for tests: the resolved file system and environment by default.
var (
	getenv       = os.Getenv
	evalSymlinks = filepath.EvalSymlinks
	dirWritable  = isDirWritable
	lstat        = os.Lstat
	// applicationsDir is the system-wide macOS application folder.
	applicationsDir = "/Applications"
)

// isDirWritable reports whether this user can create (and so rename and
// remove) entries in dir.
func isDirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".ffd-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// LocateSelf checks that exe (the running app) is an installed copy that
// Apply can replace and returns it. It reads only the local file system.
// macOS: <dir>/Foxmayn Frappe Desktop.app/Contents/MacOS/foxmayn-frappe-desktop
// outside /Volumes and App Translocation, with the bundle and its folder
// writable. Windows: the per-user setup's exe below %LOCALAPPDATA%.
func LocateSelf(goos, exe string) (Target, error) {
	if !supported(goos) {
		return Target{}, ErrUnsupported
	}
	resolved, err := evalSymlinks(exe)
	if err != nil {
		return Target{}, fmt.Errorf("%w: resolving %s: %w", ErrNotInstalledCopy, exe, err)
	}
	if goos == "darwin" {
		return locateSelfDarwin(resolved)
	}
	return locateSelfWindows(resolved)
}

func locateSelfDarwin(resolved string) (Target, error) {
	p := filepath.ToSlash(resolved)
	if strings.HasPrefix(p, "/Volumes/") || strings.Contains(p, "/AppTranslocation/") {
		return Target{}, fmt.Errorf("%w: it runs from a disk image or a quarantined location (%s)", ErrNotInstalledCopy, p)
	}
	const tail = "/Contents/MacOS/" + bundleExec
	bundle, ok := strings.CutSuffix(p, tail)
	if !ok || path.Base(bundle) != bundleApp {
		return Target{}, fmt.Errorf("%w: %s is not inside %s", ErrNotInstalledCopy, p, bundleApp)
	}
	if !dirWritable(path.Dir(bundle)) {
		return Target{}, fmt.Errorf("%w: %s", ErrNotWritable, path.Dir(bundle))
	}
	if !dirWritable(bundle) {
		return Target{}, fmt.Errorf("%w: %s", ErrNotWritable, bundle)
	}
	return Target{GOOS: "darwin", Path: bundle, Installed: true}, nil
}

// winKey normalises a Windows path for comparison: one separator, no case.
func winKey(p string) string {
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`))
}

func winExe(localAppData string) string {
	return strings.TrimRight(localAppData, `\/`) + `\` + winRel
}

func locateSelfWindows(resolved string) (Target, error) {
	lad := getenv("LOCALAPPDATA")
	if lad == "" {
		return Target{}, fmt.Errorf("%w: LOCALAPPDATA is not set", ErrNotInstalledCopy)
	}
	want := winExe(lad)
	if r, err := evalSymlinks(want); err == nil {
		want = r
	}
	if winKey(resolved) != winKey(want) {
		return Target{}, fmt.Errorf("%w: %s is not %s", ErrNotInstalledCopy, resolved, want)
	}
	if !dirWritable(filepath.Dir(resolved)) {
		return Target{}, fmt.Errorf("%w: %s", ErrNotWritable, filepath.Dir(resolved))
	}
	return Target{GOOS: "windows", Path: resolved, Installed: true}, nil
}

// Eligible reports, with no network, whether an app at version cur running
// from exe could replace itself: nil, or an error wrapping ErrUnsupported,
// ErrNotInstalledCopy (also for a development build) or ErrNotWritable.
func Eligible(goos string, cur Version, exe string) error {
	if !supported(goos) {
		return ErrUnsupported
	}
	if cur.IsDevBuild() {
		return fmt.Errorf("%w: this is a development build", ErrNotInstalledCopy)
	}
	_, err := LocateSelf(goos, exe)
	return err
}

// LocateInstalled finds the app a CLI user has installed. Windows: the
// per-user exe and the version the setup registered. macOS: the bundle in
// /Applications, else in ~/Applications, and its version from Info.plist.
// When nothing is installed the Target says where an install would go
// (Installed false): /Applications if writable, else ~/Applications.
func (in *Installer) LocateInstalled(home string, getenv func(string) string) (Target, error) {
	switch in.GOOS {
	case "windows":
		lad := getenv("LOCALAPPDATA")
		if lad == "" {
			return Target{}, errors.New("LOCALAPPDATA is not set")
		}
		t := Target{GOOS: "windows", Path: winExe(lad)}
		if fi, err := lstat(t.Path); err == nil && fi.Mode().IsRegular() {
			t.Installed = true
			t.Version, _ = registryVersion()
		}
		return t, nil
	case "darwin":
		return in.locateInstalledDarwin(home)
	}
	return Target{}, ErrUnsupported
}

func (in *Installer) locateInstalledDarwin(home string) (Target, error) {
	var dirs []string
	dirs = append(dirs, applicationsDir)
	if home != "" {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	for _, d := range dirs {
		app := filepath.Join(d, bundleApp)
		fi, err := lstat(app)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			return Target{}, fmt.Errorf("%w: %s is not a plain app bundle", ErrNotInstalledCopy, app)
		}
		if !dirWritable(d) {
			return Target{}, fmt.Errorf("%w: %s", ErrNotWritable, d)
		}
		if !dirWritable(app) {
			return Target{}, fmt.Errorf("%w: %s", ErrNotWritable, app)
		}
		return Target{GOOS: "darwin", Path: app, Installed: true, Version: in.bundleVersion(app)}, nil
	}
	dest := applicationsDir
	if !dirWritable(dest) && home != "" {
		dest = filepath.Join(home, "Applications")
	}
	return Target{GOOS: "darwin", Path: filepath.Join(dest, bundleApp)}, nil
}

// plistValue reads one Info.plist key of an app bundle with plutil.
func (in *Installer) plistValue(ctx context.Context, app, key string) (string, error) {
	out, err := in.run()(ctx, toolPlutil, "-extract", key, "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (in *Installer) bundleVersion(app string) string {
	v, err := in.plistValue(context.Background(), app, "CFBundleShortVersionString")
	if err != nil {
		in.log("reading the installed version failed: " + err.Error())
		return ""
	}
	return v
}

func (in *Installer) run() RunFunc {
	if in.Run != nil {
		return in.Run
	}
	return ExecRun
}
