package services

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

// fakeInstaller installs into a temp folder from a fake release.
func fakeInstaller(t *testing.T, goos string) (*ffcInstaller, string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	i := newFFCInstaller(goos, filepath.Join(dir, "home"))
	i.goarch = "amd64"
	i.getenv = func(k string) string {
		if k == "LOCALAPPDATA" {
			return filepath.Join(dir, "LocalAppData")
		}
		return ""
	}
	i.latest = func(context.Context) (*release.Release, error) {
		return &release.Release{TagName: "v9.9.9", Assets: []release.Asset{
			{Name: release.AssetName("v9.9.9", goos, "amd64"), BrowserDownloadURL: "https://x/archive"},
		}}, nil
	}
	i.download = func(_ context.Context, tg release.Target) ([]byte, error) {
		if tg.DownloadURL != "https://x/archive" || tg.GOOS != goos {
			t.Errorf("target = %+v", tg)
		}
		return []byte("NEW"), nil
	}
	i.addToPath = func(string) (bool, error) { return true, nil }
	i.shellPath = func() (string, error) { return "/usr/bin:/bin", nil }
	var log []string
	return i, dir, &log
}

func TestInstallerWindows(t *testing.T) {
	i, dir, log := fakeInstaller(t, "windows")
	var pathDir string
	i.addToPath = func(d string) (bool, error) { pathDir = d; return true, nil }

	want := filepath.Join(dir, "LocalAppData", "Programs", "ffc", "ffc.exe")
	// An older ffc is already there: it is replaced.
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := i.install(context.Background(), func(l string) { *log = append(*log, l) })
	if err != nil || path != want {
		t.Fatalf("install = %q, %v", path, err)
	}
	if b, _ := os.ReadFile(want); string(b) != "NEW" {
		t.Errorf("binary = %q", b)
	}
	if pathDir != filepath.Dir(want) {
		t.Errorf("PATH dir = %q", pathDir)
	}
	all := strings.Join(*log, "\n")
	for _, s := range []string{"windows/amd64", "v9.9.9", "signature", "Installed ffc v9.9.9 to " + want, "Added "} {
		if !strings.Contains(all, s) {
			t.Errorf("log lacks %q:\n%s", s, all)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(want), ".ffc-install-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}

	// A PATH that cannot be changed is only reported.
	i.addToPath = func(string) (bool, error) { return false, errors.New("access denied") }
	*log = nil
	if _, err := i.install(context.Background(), func(l string) { *log = append(*log, l) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(*log, "\n"), "Could not add") {
		t.Errorf("log = %v", *log)
	}
}

func TestInstallerDarwin(t *testing.T) {
	i, dir, log := fakeInstaller(t, "darwin")
	path, err := i.install(context.Background(), func(l string) { *log = append(*log, l) })
	want := filepath.Join(dir, "home", ".local", "bin", "ffc")
	if err != nil || path != want {
		t.Fatalf("install = %q, %v", path, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(want); err != nil || fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("not executable: %v %v", fi, err)
		}
	}
	if !strings.Contains(strings.Join(*log, "\n"), "is not on your PATH") {
		t.Errorf("no PATH hint: %v", *log)
	}

	// On PATH: no hint. (A Windows temp path has a drive colon, so this half
	// runs only where the PATH separator cannot clash with it.)
	if runtime.GOOS == "windows" {
		return
	}
	i.shellPath = func() (string, error) { return "/usr/bin:" + filepath.Dir(want) + "/", nil }
	*log = nil
	if _, err := i.install(context.Background(), func(l string) { *log = append(*log, l) }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(*log, "\n"), "is not on your PATH") {
		t.Errorf("unexpected PATH hint: %v", *log)
	}
}

func TestInstallerFailsClosed(t *testing.T) {
	i, dir, log := fakeInstaller(t, "darwin")
	bin := filepath.Join(dir, "home", ".local", "bin", "ffc")
	i.download = func(context.Context, release.Target) ([]byte, error) {
		return nil, errors.New("checksum mismatch")
	}
	if _, err := i.install(context.Background(), func(l string) { *log = append(*log, l) }); err == nil {
		t.Fatal("a failed verification must fail the install")
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Errorf("binary written after a failed verification: %v", err)
	}
	if last := (*log)[len(*log)-1]; last != "error: checksum mismatch" {
		t.Errorf("last log line = %q", last)
	}

	// No archive for this platform.
	i, _, log = fakeInstaller(t, "darwin")
	i.goarch = "riscv64"
	if _, err := i.install(context.Background(), func(l string) { *log = append(*log, l) }); err == nil || !strings.Contains(err.Error(), "no asset") {
		t.Errorf("missing platform: %v", err)
	}

	// Cancelled after the download: nothing is written, no error line.
	i, dir, log = fakeInstaller(t, "darwin")
	ctx, cancel := context.WithCancel(context.Background())
	i.download = func(context.Context, release.Target) ([]byte, error) { cancel(); return []byte("NEW"), nil }
	if _, err := i.install(ctx, func(l string) { *log = append(*log, l) }); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "home", ".local", "bin", "ffc")); !os.IsNotExist(err) {
		t.Errorf("binary written after cancel: %v", err)
	}
	if strings.Contains(strings.Join(*log, "\n"), "error:") {
		t.Errorf("cancel logged as an error: %v", *log)
	}
}

func TestPathHas(t *testing.T) {
	if !pathHas(`C:\a;C:\Users\Me\AppData\Local\Programs\ffc\;D:\b`, `c:\users\me\appdata\local\programs\ffc`, true) {
		t.Error("windows entry not matched")
	}
	if pathHas(`C:\Programs\ffc-old`, `C:\Programs\ffc`, true) {
		t.Error("substring matched")
	}
	if !pathHas("/usr/bin:/Users/me/.local/bin/", "/Users/me/.local/bin", false) || pathHas("/usr/bin", "/Users/me/.local/bin", false) {
		t.Error("unix PATH")
	}
}

// TestInstallerRealRelease downloads the real latest release into a scratch
// folder and runs it. It needs the network, so it only runs with
// FFD_REAL_INSTALL=1; it never touches the real install location or PATH.
func TestInstallerRealRelease(t *testing.T) {
	if os.Getenv("FFD_REAL_INSTALL") != "1" {
		t.Skip("set FFD_REAL_INSTALL=1 to install the real latest release into a temp folder")
	}
	dir := t.TempDir()
	i := newFFCInstaller(runtime.GOOS, filepath.Join(dir, "home"))
	i.getenv = func(k string) string {
		if k == "LOCALAPPDATA" {
			return filepath.Join(dir, "LocalAppData")
		}
		return ""
	}
	i.addToPath = func(d string) (bool, error) { t.Logf("would add %s to PATH", d); return false, nil }
	i.shellPath = func() (string, error) { return "", nil }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	path, err := i.install(ctx, func(l string) { t.Log(l) })
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		t.Fatalf("%s --version: %v", path, err)
	}
	t.Logf("%s --version: %s", path, strings.TrimSpace(string(out)))
	if v := parseFFCVersion(string(out)); !strings.HasPrefix(v, "v") && !strings.HasPrefix(v, "1") {
		t.Errorf("version = %q", v)
	}
}

func TestInstallerInPlace(t *testing.T) {
	for _, goos := range []string{"windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			i, dir, log := fakeInstaller(t, goos)
			i.addToPath = func(string) (bool, error) { t.Error("PATH changed for an in-place update"); return false, nil }
			i.shellPath = func() (string, error) { t.Error("shell PATH read for an in-place update"); return "", nil }
			over := filepath.Join(dir, "elsewhere", release.BinaryName(goos))
			if err := os.MkdirAll(filepath.Dir(over), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(over, []byte("OLD"), 0o755); err != nil {
				t.Fatal(err)
			}
			path, err := i.installAt(context.Background(), over, func(l string) { *log = append(*log, l) })
			if err != nil || path != over {
				t.Fatalf("installAt = %q, %v", path, err)
			}
			if b, _ := os.ReadFile(over); string(b) != "NEW" {
				t.Errorf("binary = %q", b)
			}
			def, _ := i.dir()
			if _, err := os.Stat(filepath.Join(def, release.BinaryName(goos))); !os.IsNotExist(err) {
				t.Errorf("a second copy was installed in %s: %v", def, err)
			}
			if !strings.Contains(strings.Join(*log, "\n"), "Updating the ffc at "+over) {
				t.Errorf("log = %v", *log)
			}
		})
	}
}

func TestInstallerInPlaceNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions via chmod need a Unix file system")
	}
	i, dir, log := fakeInstaller(t, "darwin")
	sys := filepath.Join(dir, "system")
	over := filepath.Join(sys, "ffc")
	if err := os.MkdirAll(sys, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(over, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sys, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sys, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	_, err := i.installAt(context.Background(), over, func(l string) { *log = append(*log, l) })
	if err == nil || !strings.Contains(err.Error(), `"ffc update"`) {
		t.Fatalf("installAt = %v", err)
	}
	if b, _ := os.ReadFile(over); string(b) != "OLD" {
		t.Errorf("binary changed: %q", b)
	}
}
