package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

// Scoop runs ffc from <root>\apps\ffc\current, a directory junction to the
// version directory. EvalSymlinks fails below it, and the path must still
// resolve to something ManagedBy recognises.
func TestResolveExecutableJunction(t *testing.T) {
	root := t.TempDir()
	apps := filepath.Join(root, "scoop", "apps", "ffc")
	ver := filepath.Join(apps, "1.0.0")
	if err := os.MkdirAll(ver, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ver, "ffc.exe"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(apps, "current")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", current, ver).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v: %s", err, out)
	}

	exe := filepath.Join(current, "ffc.exe")
	got := resolveExecutable(exe)
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("resolveExecutable(%q) = %q: %v", exe, got, err)
	}
	if m := release.ManagedBy(got); m == nil || m.Name != "Scoop" {
		t.Errorf("ManagedBy(%q) = %v, want Scoop", got, m)
	}
}
