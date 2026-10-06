package services

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Installer one-liners, as the README gives them. installCommand is shown to
// the user to copy; installArgv is what the app runs.
const (
	installURLWindows = "https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.ps1"
	installURLUnix    = "https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.sh"
)

// FFCInfo says whether the ffc binary is installed and where.
type FFCInfo struct {
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
	// Error is set when the binary was found but did not answer
	// "ffc --version".
	Error string `json:"error,omitempty"`
}

// installCommand is the command the user can copy to install ffc by hand.
func installCommand(goos string) string {
	if goos == "windows" {
		return `powershell -NoProfile -ExecutionPolicy Bypass -Command "irm ` + installURLWindows + ` | iex"`
	}
	return "curl -fsSL " + installURLUnix + " | sh"
}

// installArgv is the installer as the app runs it. On Windows the progress
// bar of Invoke-WebRequest is turned off: PowerShell 5.1 slows a download
// down badly while drawing it, and nobody sees it here.
func installArgv(goos string) []string {
	if goos == "windows" {
		return []string{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command",
			"$ProgressPreference = 'SilentlyContinue'; irm " + installURLWindows + " | iex"}
	}
	return []string{"sh", "-c", "curl -fsSL " + installURLUnix + " | sh"}
}

// FFCLocator finds the ffc binary: on PATH first, then where the install
// scripts put it (a GUI app on macOS does not get the shell's PATH, and a
// fresh install on Windows changes the user PATH only for new processes).
type FFCLocator struct {
	goos     string
	home     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	isFile   func(string) bool
	version  func(path string) (string, error)

	mu     sync.Mutex
	cached *FFCInfo
}

func newFFCLocator(goos, home string) *FFCLocator {
	return &FFCLocator{
		goos:     goos,
		home:     home,
		getenv:   os.Getenv,
		lookPath: exec.LookPath,
		isFile:   isRegularFile,
		version:  ffcVersion,
	}
}

func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// candidates are the install scripts' locations, in the order they choose
// them: install.ps1 uses %LOCALAPPDATA%\Programs\ffc, install.sh
// /usr/local/bin when writable, else ~/.local/bin.
func (l *FFCLocator) candidates() []string {
	if l.goos == "windows" {
		var out []string
		if d := l.getenv("LOCALAPPDATA"); d != "" {
			out = append(out, filepath.Join(d, "Programs", "ffc", "ffc.exe"))
		}
		if l.home != "" {
			out = append(out, filepath.Join(l.home, "AppData", "Local", "Programs", "ffc", "ffc.exe"))
		}
		return out
	}
	out := []string{"/usr/local/bin/ffc"}
	if l.home != "" {
		out = append(out, filepath.Join(l.home, ".local", "bin", "ffc"))
	}
	return out
}

// find looks for the binary without running it.
func (l *FFCLocator) find() (string, bool) {
	if p, err := l.lookPath("ffc"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return p, true
	}
	for _, c := range l.candidates() {
		if l.isFile(c) {
			return c, true
		}
	}
	return "", false
}

// Info returns the cached detection, detecting on first use.
func (l *FFCLocator) Info() FFCInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cached == nil {
		info := l.detect()
		l.cached = &info
	}
	return *l.cached
}

// Refresh detects again (after an install, or when the user asks).
func (l *FFCLocator) Refresh() FFCInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	info := l.detect()
	l.cached = &info
	return info
}

func (l *FFCLocator) detect() FFCInfo {
	p, ok := l.find()
	if !ok {
		return FFCInfo{}
	}
	info := FFCInfo{Found: true, Path: p}
	v, err := l.version(p)
	if err != nil {
		info.Error = err.Error()
	}
	info.Version = v
	return info
}

var versionPattern = regexp.MustCompile(`ffc version (\S+)`)

// ffcVersion runs "ffc --version" (at most 10 s) and returns the version
// ("v1.10.0", or "dev" for a source build).
func ffcVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("ffc --version failed: " + err.Error())
	}
	return parseFFCVersion(string(out)), nil
}

func parseFFCVersion(out string) string {
	if m := versionPattern.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return strings.TrimSpace(out)
}

// runInstaller runs argv and calls line for every line it prints (stdout and
// stderr), until it exits or ctx ends.
func runInstaller(ctx context.Context, argv []string, line func(string)) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	hideWindow(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			if l := strings.TrimRight(sc.Text(), "\r"); strings.TrimSpace(l) != "" {
				line(l)
			}
		}
		// Keep draining after an over-long line so the child never blocks.
		_, _ = io.Copy(io.Discard, pr)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}
