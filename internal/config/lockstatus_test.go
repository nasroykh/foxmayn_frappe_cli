package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if l := LockStatus(path); l.Exists || l.Stale || l.Path != path+".lock" {
		t.Fatalf("no lock: %+v", l)
	}
	lock := path + ".lock"
	if err := os.WriteFile(lock, []byte("1-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l := LockStatus(path); !l.Exists || l.Stale || l.Age > time.Minute {
		t.Errorf("fresh lock: %+v", l)
	}
	old := time.Now().Add(-lockStale - time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if l := LockStatus(path); !l.Exists || !l.Stale {
		t.Errorf("old lock: %+v", l)
	}
	// Looking does not break it.
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("LockStatus removed the lock: %v", err)
	}

	// A symlinked config has its lock next to the target, as Edit takes it.
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	link := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(target, []byte("x: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	// Resolving also expands Windows 8.3 short names, so compare resolved paths.
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if l := LockStatus(link); l.Path != real+".lock" {
		t.Errorf("symlink: lock path %s, want %s", l.Path, real+".lock")
	}
}
