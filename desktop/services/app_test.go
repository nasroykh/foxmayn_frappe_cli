package services

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// fakeLocator is a locator with a fake PATH and file system: onPath is
// what LookPath finds ("" = nothing), files the paths that exist.
func fakeLocator(goos, home, onPath string, env map[string]string, files ...string) *FFCLocator {
	l := newFFCLocator(goos, home)
	l.lookPath = func(name string) (string, error) {
		if name == "ffc" && onPath != "" {
			return onPath, nil
		}
		return "", exec.ErrNotFound
	}
	l.getenv = func(k string) string { return env[k] }
	l.isFile = func(p string) bool {
		for _, f := range files {
			if f == p {
				return true
			}
		}
		return false
	}
	l.version = func(string) (string, error) { return "v1.2.3", nil }
	return l
}

func TestFFCDetection(t *testing.T) {
	abs, _ := filepath.Abs(filepath.Join("bin", "ffc"))
	if info := fakeLocator("darwin", "/Users/me", abs, nil).Info(); !info.Found || info.Path != abs || info.Version != "v1.2.3" {
		t.Errorf("on PATH = %+v", info)
	}

	// Not on PATH: the install scripts' locations, in their order.
	win := filepath.Join(`C:\Users\me\AppData\Local`, "Programs", "ffc", "ffc.exe")
	l := fakeLocator("windows", `C:\Users\me`, "", map[string]string{"LOCALAPPDATA": `C:\Users\me\AppData\Local`}, win)
	if info := l.Info(); !info.Found || info.Path != win {
		t.Errorf("windows install dir = %+v", info)
	}
	mac := fakeLocator("darwin", "/Users/me", "", nil, filepath.Join("/Users/me", ".local", "bin", "ffc"))
	if info := mac.Info(); !info.Found || info.Path != filepath.Join("/Users/me", ".local", "bin", "ffc") {
		t.Errorf("~/.local/bin = %+v", info)
	}
	if c := mac.candidates(); c[0] != "/usr/local/bin/ffc" {
		t.Errorf("candidates = %q", c)
	}

	none := fakeLocator("darwin", "/Users/me", "", nil)
	if info := none.Info(); info.Found || info.Path != "" {
		t.Errorf("missing = %+v", info)
	}
	// Info is cached; Refresh looks again.
	none.isFile = func(p string) bool { return p == "/usr/local/bin/ffc" }
	if none.Info().Found {
		t.Error("Info did not cache")
	}
	if info := none.Refresh(); !info.Found {
		t.Errorf("Refresh = %+v", info)
	}

	broken := fakeLocator("darwin", "/Users/me", abs, nil)
	broken.version = func(string) (string, error) { return "", errors.New("exit status 1") }
	if info := broken.Info(); !info.Found || info.Error == "" {
		t.Errorf("broken binary = %+v", info)
	}
}

func TestParseFFCVersion(t *testing.T) {
	for in, want := range map[string]string{
		"ffc version 1.10.0 (b75ef8c, 2026-10-06T09:07:10Z)\n": "1.10.0",
		"ffc version dev (none, unknown)\n":                    "dev",
		"something else\n":                                     "something else",
	} {
		if got := parseFFCVersion(in); got != want {
			t.Errorf("parseFFCVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstallCommands(t *testing.T) {
	pinned := "/foxmayn_frappe_cli/" + installScriptTag + "/"
	if c := installCommand("windows"); !strings.Contains(c, pinned+"install.ps1 | iex") || !strings.HasPrefix(c, "powershell -NoProfile -ExecutionPolicy Bypass") {
		t.Errorf("windows command = %q", c)
	}
	if c := installCommand("darwin"); !strings.Contains(c, pinned+"install.sh | sh") || strings.Contains(c, "/main/") {
		t.Errorf("unix command = %q", c)
	}
}

func TestInstallFFC(t *testing.T) {
	h := &fakeHost{}
	loc := fakeLocator("darwin", "/Users/me", "", nil)
	s := NewAppService(h, "/Users/me/.config/ffc/config.yaml", loc)
	s.goos = "darwin"
	s.install = func(ctx context.Context, log func(string)) (string, error) {
		log("Downloading ffc...")
		log("Installed ffc v9.9.9 to /usr/local/bin/ffc")
		loc.isFile = func(p string) bool { return p == "/usr/local/bin/ffc" }
		return "/usr/local/bin/ffc", nil
	}
	info, err := s.InstallFFC(context.Background())
	if err != nil || !info.Found || info.Path != "/usr/local/bin/ffc" {
		t.Fatalf("InstallFFC = %+v, %v", info, err)
	}
	if lines := h.named(EventInstallerLog); len(lines) != 2 || lines[1].(InstallerLine).Line != "Installed ffc v9.9.9 to /usr/local/bin/ffc" {
		t.Errorf("log events = %v", lines)
	}

	// A failing install, one that leaves no binary behind, and a cancelled one.
	loc.isFile = func(string) bool { return false }
	s.install = func(context.Context, func(string)) (string, error) { return "", errors.New("checksum mismatch") }
	if _, err := s.InstallFFC(context.Background()); code(err) != CodeFailed {
		t.Errorf("failing install: %v", err)
	}
	s.install = func(context.Context, func(string)) (string, error) { return "/usr/local/bin/ffc", nil }
	if _, err := s.InstallFFC(context.Background()); code(err) != CodeFailed {
		t.Errorf("install without a binary: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.install = func(ctx context.Context, _ func(string)) (string, error) { cancel(); return "", ctx.Err() }
	if _, err := s.InstallFFC(ctx); code(err) != CodeCancelled {
		t.Errorf("cancelled install: %v", err)
	}
}

func TestOpenWebsiteAndFolders(t *testing.T) {
	h := &fakeHost{}
	dir := t.TempDir()
	s := NewAppService(h, filepath.Join(dir, "config.yaml"), fakeLocator("darwin", dir, "", nil))
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "ms-settings:", "https://"} {
		if err := s.OpenWebsite(bad); code(err) != CodeInvalid {
			t.Errorf("OpenWebsite(%q) = %v", bad, err)
		}
	}
	if err := s.OpenWebsite("https://github.com/nasroykh/foxmayn_frappe_cli"); err != nil || len(h.opened) != 1 {
		t.Errorf("OpenWebsite = %v, opened %v", err, h.opened)
	}
	if err := s.OpenConfigFolder(); err != nil || len(h.files) != 1 || h.files[0] != dir {
		t.Errorf("OpenConfigFolder = %v, %v", err, h.files)
	}
	if err := s.OpenFFCFolder(); code(err) != CodeFFCMissing {
		t.Errorf("OpenFFCFolder without ffc = %v", err)
	}
	missing := NewAppService(h, filepath.Join(dir, "nope", "config.yaml"), fakeLocator("darwin", dir, "", nil))
	if err := missing.OpenConfigFolder(); code(err) != CodeNotFound {
		t.Errorf("missing folder = %v", err)
	}
}

func utf16le(s string, bom bool) []byte {
	var b []byte
	if bom {
		b = append(b, 0xff, 0xfe)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

func TestParseWSLList(t *testing.T) {
	for _, in := range [][]byte{utf16le("Ubuntu-24.04\r\nDebian\r\n", false), utf16le("Ubuntu-24.04\r\nDebian\r\n", true), []byte("Ubuntu-24.04\nDebian\n")} {
		if got := parseWSLList(in); strings.Join(got, ",") != "Ubuntu-24.04,Debian" {
			t.Errorf("parseWSLList(%q) = %q", in, got)
		}
	}
	if got := parseWSLList(nil); len(got) != 0 {
		t.Errorf("empty = %q", got)
	}
}

type dirEntry struct{ name string }

func (d dirEntry) Name() string               { return d.name }
func (d dirEntry) IsDir() bool                { return true }
func (d dirEntry) Type() os.FileMode          { return os.ModeDir }
func (d dirEntry) Info() (os.FileInfo, error) { return nil, errors.New("n/a") }

func TestWSLDetect(t *testing.T) {
	d := newWSLDetector("windows")
	d.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "wsl.exe" || strings.Join(args, " ") != "--list --running --quiet" {
			t.Errorf("ran %s %q", name, args)
		}
		return utf16le("Ubuntu\r\nAlpine\r\n", false), nil
	}
	d.readDir = func(p string) ([]os.DirEntry, error) {
		if p == `\\wsl.localhost\Ubuntu\home` {
			return []os.DirEntry{dirEntry{"nas"}}, nil
		}
		return nil, os.ErrNotExist
	}
	want := `\\wsl.localhost\Ubuntu\home\nas\.config\ffc\config.yaml`
	d.isFile = func(p string) bool { return p == want }
	info := d.detect()
	if !info.Detected || strings.Join(info.Distros, ",") != "Ubuntu" || len(info.Paths) != 1 || info.Paths[0] != want {
		t.Errorf("detect = %+v", info)
	}

	// No WSL, or not Windows: nothing, and nothing run on macOS.
	d.run = func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound }
	if info := d.detect(); info.Detected || info.Distros == nil {
		t.Errorf("no wsl = %+v", info)
	}
	mac := newWSLDetector("darwin")
	mac.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Error("ran wsl.exe on macOS")
		return nil, nil
	}
	if info := mac.detect(); info.Detected {
		t.Errorf("macOS = %+v", info)
	}
}

func TestConfigPathFromEnv(t *testing.T) {
	def := func() (string, error) { return "/home/u/.config/ffc/config.yaml", nil }
	got, err := configPathFrom(func(string) string { return "" }, def)
	if err != nil || got != "/home/u/.config/ffc/config.yaml" {
		t.Errorf("no env: %q, %v", got, err)
	}
	got, err = configPathFrom(func(k string) string {
		if k == "FFC_CONFIG" {
			return "work.yaml"
		}
		return ""
	}, def)
	if err != nil || !filepath.IsAbs(got) || filepath.Base(got) != "work.yaml" {
		t.Errorf("FFC_CONFIG: %q, %v", got, err)
	}
}

func TestSetWindowTheme(t *testing.T) {
	h := &fakeHost{}
	dir := t.TempDir()
	s := NewAppService(h, filepath.Join(dir, "config.yaml"), fakeLocator("windows", dir, "", nil))
	s.SetWindowTheme(true)
	s.SetWindowTheme(false)
	if len(h.themes) != 2 || !h.themes[0] || h.themes[1] {
		t.Errorf("themes = %v, want [true false]", h.themes)
	}
	if WindowBackground(true) == WindowBackground(false) {
		t.Error("dark and light window backgrounds are the same")
	}
}
