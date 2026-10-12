//go:build windows

package desktopinstall

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpenLockedRefusesWriters(t *testing.T) {
	p := filepath.Join(t.TempDir(), "setup.exe")
	if err := os.WriteFile(p, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openLocked(p)
	if err != nil {
		t.Fatal(err)
	}
	if w, err := os.OpenFile(p, os.O_WRONLY, 0); err == nil {
		w.Close()
		t.Error("a second writer was allowed")
	}
	if err := os.Remove(p); err == nil {
		t.Error("the locked file could be deleted")
	}
	if err := os.Rename(p, p+".moved"); err == nil {
		t.Error("the locked file could be renamed")
	}
	if r, err := os.Open(p); err != nil {
		t.Errorf("a reader was refused: %v", err)
	} else {
		r.Close()
	}
	f.Close()
	if err := os.Remove(p); err != nil {
		t.Errorf("not released after Close: %v", err)
	}
}

func TestDownloadWritesNoZoneIdentifier(t *testing.T) {
	f, in := newFake(t, "windows")
	p := planFor(t, f, in, []byte("setup"))
	path, err := in.Download(context.Background(), p, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Mark of the Web is an NTFS stream; Go never writes one.
	if z, err := os.Open(path + ":Zone.Identifier"); err == nil {
		z.Close()
		t.Error("the download carries a Zone.Identifier stream")
	}
}

// The setup is started while Apply holds it locked, and nothing can write to
// it meanwhile: a real process is launched from the locked file.
func TestApplyStartsTheSetupWhileLocked(t *testing.T) {
	cmdExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	payload, err := os.ReadFile(cmdExe)
	if err != nil {
		t.Skipf("no cmd.exe: %v", err)
	}
	f, in := newFake(t, "windows")
	p := planFor(t, f, in, payload)
	path, err := in.Download(context.Background(), p, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tg := Target{GOOS: "windows", Path: filepath.Join(t.TempDir(), "app", "foxmayn-frappe-desktop.exe")}
	ran := false
	in.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if w, err := os.OpenFile(name, os.O_WRONLY, 0); err == nil {
			w.Close()
			t.Error("the setup file was writable while the install ran")
		}
		// cmd.exe stands in for the setup; /S is replaced so it exits at once.
		out, err := exec.CommandContext(ctx, name, "/c", "exit", "0").CombinedOutput()
		if err != nil {
			return out, err
		}
		ran = true
		if err := os.MkdirAll(filepath.Dir(tg.Path), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(tg.Path, []byte("installed"), 0o755)
	}
	if err := in.Apply(context.Background(), tg, path, p.Version); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("the setup never ran")
	}
	// After Apply the lock is gone.
	if err := os.Remove(path); err != nil {
		t.Errorf("setup file still locked: %v", err)
	}
}
