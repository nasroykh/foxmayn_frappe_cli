package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
)

// updateTEnv serves a release list whose newest ffc release is latest (with
// no assets), runs as version current, and makes the running binary exe.
func updateTEnv(t *testing.T, current, latest, exe string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"tag_name":"desktop-v9.0.0"},{"tag_name":%q,"assets":[]}]`, latest)
	}))
	t.Cleanup(srv.Close)
	oldURL, oldExe, oldVersion := releasesURL, executablePath, version.Version
	t.Cleanup(func() { releasesURL, executablePath, version.Version = oldURL, oldExe, oldVersion })
	releasesURL = srv.URL
	executablePath = func() (string, error) { return exe, nil }
	version.Version = current
}

func TestUpdateManagedInstall(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml") // update reads no config
	brew := "/opt/homebrew/Caskroom/ffc/1.0.0/ffc"

	updateTEnv(t, "v1.0.0", "v1.1.0", brew)
	r := runFFC(t, cfg, "", "update", "--yes")
	if r.Code != exitGeneric || !strings.Contains(r.Stderr, "installed with Homebrew; update it with: brew upgrade ffc") {
		t.Errorf("managed update: code %d, stderr %q", r.Code, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "v1.0.0 → v1.1.0") {
		t.Errorf("the available version is not shown: %q", r.Stderr)
	}

	r = runFFC(t, cfg, "", "update", "--check")
	if r.Code != exitOK || strings.Contains(r.Stderr, "brew upgrade") {
		t.Errorf("--check: code %d, stderr %q", r.Code, r.Stderr)
	}

	updateTEnv(t, "v1.1.0", "v1.1.0", brew)
	if r = runFFC(t, cfg, "", "update"); r.Code != exitOK || !strings.Contains(r.Stdout+r.Stderr, "Already up to date") {
		t.Errorf("up to date: code %d, stdout %q, stderr %q", r.Code, r.Stdout, r.Stderr)
	}

	// An install-script copy goes on to the download (the fake release has
	// no assets, so it stops at the asset lookup).
	updateTEnv(t, "v1.0.0", "v1.1.0", "/usr/local/bin/ffc")
	r = runFFC(t, cfg, "", "update", "--yes")
	if r.Code == exitOK || !strings.Contains(r.Stderr, "no asset found") {
		t.Errorf("unmanaged update: code %d, stderr %q", r.Code, r.Stderr)
	}
}

func TestUpdateCommand(t *testing.T) {
	updateTEnv(t, "v1.0.0", "v1.0.0", `C:\Users\me\scoop\apps\ffc\1.0.0\ffc.exe`)
	if got := updateCommand(); got != "scoop update ffc" {
		t.Errorf("scoop: %q", got)
	}
	executablePath = func() (string, error) { return "", fmt.Errorf("no path") }
	if got := updateCommand(); got != "ffc update" {
		t.Errorf("unknown path: %q", got)
	}
}
