package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMCPLockAndInstanceCheckedRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := os.MkdirAll(mcpStateDir(), 0o700); err != nil {
		t.Fatal(err)
	}

	unlock, err := acquireMCPLock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireMCPLock(); err == nil {
		t.Fatal("second lock must fail while the first is held")
	}
	unlock()
	unlock2, err := acquireMCPLock()
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	unlock2()

	// A stale lock is taken over.
	lock := filepath.Join(mcpStateDir(), "mcp.lock")
	os.WriteFile(lock, []byte("1"), 0o600)
	old := time.Now().Add(-2 * mcpLockStale)
	os.Chtimes(lock, old, old)
	unlock3, err := acquireMCPLock()
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	unlock3()

	if err := writeMCPState(mcpState{PID: 1, Instance: "A"}); err != nil {
		t.Fatal(err)
	}
	removeMCPStateIf("B")
	if s, _ := readMCPState(); s == nil {
		t.Fatal("state of another instance must survive")
	}
	removeMCPStateIf("A")
	if s, _ := readMCPState(); s != nil {
		t.Fatal("own state must be removed")
	}
}
