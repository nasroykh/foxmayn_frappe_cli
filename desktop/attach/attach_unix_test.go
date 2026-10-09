//go:build unix

package attach

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO is refused by Stat before Open, so nothing blocks on a writer.
func TestReadFileRefusesFIFO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pipe.txt")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadFile(p); done <- err }()
	select {
	case err := <-done:
		refusal(t, err, "is not a regular file")
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFile blocked on a FIFO")
	}
}
