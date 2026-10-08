package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWritesCompletionsAndManPages(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "manpages", "ffc-removed.1")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{dir, "v1.2.3", "1767225600"}); err != nil { // 2026-01-01
		t.Fatal(err)
	}

	for _, name := range []string{"ffc.bash", "ffc.zsh", "ffc.fish", "ffc.ps1"} {
		b, err := os.ReadFile(filepath.Join(dir, "completions", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "__complete") {
			t.Errorf("%s does not call ffc __complete", name)
		}
	}
	page, err := os.ReadFile(filepath.Join(dir, "manpages", "ffc.1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`.TH "FFC" "1" "Jan 2026" "ffc 1.2.3"`, "Auto generated"} {
		if got := strings.Contains(string(page), want); got != (want != "Auto generated") {
			t.Errorf("ffc.1 contains %q = %v", want, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "manpages", "ffc-get-doc.1")); err != nil {
		t.Errorf("no page for a subcommand: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale page kept: %v", err)
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {t.TempDir(), "1.0.0", "0"}, {t.TempDir(), "1.0.0", "x"}} {
		if err := run(args); err == nil {
			t.Errorf("run(%q): want an error", args)
		}
	}
}
