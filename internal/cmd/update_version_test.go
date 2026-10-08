package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pinTEnv is updateTEnv plus releasesBase answering /tags/<tag> for the
// tags in have (no assets), 404 for the rest.
func pinTEnv(t *testing.T, current, exe string, have ...string) {
	t.Helper()
	updateTEnv(t, current, "v9.9.9", exe)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := strings.TrimPrefix(r.URL.Path, "/tags/")
		for _, h := range have {
			if h == tag {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"tag_name":%q,"prerelease":%t,"assets":[]}`, tag, strings.Contains(tag, "-"))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	old := releasesBase
	t.Cleanup(func() { releasesBase = old })
	releasesBase = srv.URL
}

func TestUpdateVersion(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	exe := "/usr/local/bin/ffc"

	// A named release, older or a pre-release, goes on to the download (the
	// fake has no assets, so it stops at the asset lookup).
	for _, v := range []string{"1.2.0", "v1.2.0", "v1.4.0-rc1"} {
		pinTEnv(t, "v1.3.0", exe, "v1.2.0", "v1.4.0-rc1")
		r := runFFC(t, cfg, "", "update", "--version", v, "--yes")
		tag := "v" + strings.TrimPrefix(v, "v")
		if r.Code == exitOK || !strings.Contains(r.Stderr, "Release "+tag+" found (you run v1.3.0)") || !strings.Contains(r.Stderr, "no asset found") {
			t.Errorf("--version %s: code %d, stderr %q", v, r.Code, r.Stderr)
		}
	}

	pinTEnv(t, "v1.2.0", exe, "v1.2.0")
	if r := runFFC(t, cfg, "", "update", "--version", "v1.2.0"); r.Code != exitOK || !strings.Contains(r.Stdout+r.Stderr, "Already running v1.2.0") {
		t.Errorf("same version: code %d, stdout %q, stderr %q", r.Code, r.Stdout, r.Stderr)
	}

	pinTEnv(t, "v1.2.0", exe)
	if r := runFFC(t, cfg, "", "update", "--version", "v1.1.0"); r.Code == exitOK || !strings.Contains(r.Stderr, "no ffc release v1.1.0 on GitHub") {
		t.Errorf("missing tag: code %d, stderr %q", r.Code, r.Stderr)
	}

	for _, bad := range []string{"desktop-v0.1.2", "latest", "1.2", "v1.2.0;x"} {
		if r := runFFC(t, cfg, "", "update", "--version", bad); r.Code != exitUsage {
			t.Errorf("--version %q: code %d, stderr %q", bad, r.Code, r.Stderr)
		}
	}

	pinTEnv(t, "v1.3.0", "/opt/homebrew/Caskroom/ffc/1.3.0/ffc", "v1.2.0")
	if r := runFFC(t, cfg, "", "update", "--version", "v1.2.0", "--yes"); r.Code != exitGeneric || !strings.Contains(r.Stderr, "brew upgrade ffc") {
		t.Errorf("managed: code %d, stderr %q", r.Code, r.Stderr)
	}
}

func TestClassifyPin(t *testing.T) {
	for _, c := range []struct {
		current, target string
		want            updateKind
	}{
		{"v1.2.0", "v1.2.0", kindUpToDate},
		{"1.2.0", "v1.2.0", kindUpToDate},
		{"v1.2.0", "v1.3.0", kindUpdate},
		{"v1.2.0-rc1", "v1.2.0", kindUpdate},
		{"v1.2.0", "v1.1.0", kindDowngrade},
		{"v1.2.0", "v1.2.0-rc1", kindDowngrade},
		{"abc1234", "v1.2.0", kindDev},
	} {
		if got := classifyPin(c.current, c.target); got != c.want {
			t.Errorf("classifyPin(%s, %s) = %d, want %d", c.current, c.target, got, c.want)
		}
	}
}

func TestPrevPath(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/local/bin/ffc":      "/usr/local/bin/ffc.prev",
		`C:\Programs\ffc\ffc.exe`: `C:\Programs\ffc\ffc.prev.exe`,
		`C:\Programs\ffc\FFC.EXE`: `C:\Programs\ffc\FFC.prev.EXE`,
	} {
		if got := prevPath(in); got != want {
			t.Errorf("prevPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUpdateRollback replaces a fake binary, then rolls back twice: each
// swap keeps the replaced binary as the previous one.
func TestUpdateRollback(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	dir := t.TempDir()
	exe := filepath.Join(dir, "ffc")
	if err := os.WriteFile(exe, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	updateTEnv(t, "v1.1.0", "v1.1.0", exe)
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	r := runFFC(t, cfg, "", "update", "--rollback", "--yes")
	if r.Code == exitOK || !strings.Contains(r.Stderr, "no previous version to roll back to") {
		t.Errorf("nothing kept: code %d, stderr %q", r.Code, r.Stderr)
	}

	if err := replaceBinary([]byte("new build")); err != nil {
		t.Fatal(err)
	}
	if read(exe) != "new build" || read(prevPath(exe)) != "old build" {
		t.Fatalf("after update: exe %q, prev %q", read(exe), read(prevPath(exe)))
	}

	for i, want := range []string{"old build", "new build"} {
		r = runFFC(t, cfg, "", "update", "--rollback", "--yes")
		if r.Code != exitOK || !strings.Contains(r.Stdout+r.Stderr, "Rolled back to the previous ffc") {
			t.Fatalf("rollback %d: code %d, stdout %q, stderr %q", i+1, r.Code, r.Stdout, r.Stderr)
		}
		if read(exe) != want {
			t.Errorf("rollback %d: exe %q, want %q", i+1, read(exe), want)
		}
	}

	if r = runFFC(t, cfg, "", "update", "--rollback", "--version", "v1.0.0"); r.Code != exitUsage {
		t.Errorf("--rollback --version: code %d", r.Code)
	}

	updateTEnv(t, "v1.1.0", "v1.1.0", `C:\Users\me\scoop\apps\ffc\1.1.0\ffc.exe`)
	if r = runFFC(t, cfg, "", "update", "--rollback", "--yes"); r.Code != exitGeneric || !strings.Contains(r.Stderr, "scoop update ffc") {
		t.Errorf("managed rollback: code %d, stderr %q", r.Code, r.Stderr)
	}
}
