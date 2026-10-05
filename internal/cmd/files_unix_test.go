//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// saveAtomic honours the umask: a restrictive one keeps a public download
// private, where a fixed 0644 would have opened it.
func TestSaveAtomicUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	dir := t.TempDir()
	for perm, want := range map[os.FileMode]os.FileMode{0o666: 0o600, 0o600: 0o600} {
		dst := filepath.Join(dir, perm.String())
		if _, err := saveAtomic(strings.NewReader("x"), dst, false, perm); err != nil {
			t.Fatal(err)
		}
		if fi, _ := os.Stat(dst); fi.Mode().Perm() != want {
			t.Errorf("perm %v under umask 077: saved %v, want %v", perm, fi.Mode().Perm(), want)
		}
	}
	syscall.Umask(0o022)
	dst := filepath.Join(dir, "public")
	if _, err := saveAtomic(strings.NewReader("x"), dst, false, 0o666); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o644 {
		t.Errorf("umask 022: saved %v, want 0644", fi.Mode().Perm())
	}
}
