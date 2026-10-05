package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEditorCommand(t *testing.T) {
	get := func() string {
		t.Helper()
		argv, err := editorCommand()
		if err != nil {
			t.Fatalf("editorCommand: %v", err)
		}
		return strings.Join(argv, "|")
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code --wait")
	if got := get(); got != "code|--wait" {
		t.Errorf("EDITOR split = %q", got)
	}
	t.Setenv("VISUAL", "nvim")
	if got := get(); got != "nvim" {
		t.Errorf("VISUAL = %q", got)
	}
	spaced := filepath.Join(t.TempDir(), "my editor")
	if err := os.WriteFile(spaced, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	// A path with a space: the whole value, or quoted with arguments.
	t.Setenv("VISUAL", spaced)
	if got := get(); got != spaced {
		t.Errorf("path with a space = %q", got)
	}
	t.Setenv("VISUAL", `"`+spaced+`" --wait -n`)
	if got := get(); got != spaced+"|--wait|-n" {
		t.Errorf("double-quoted path = %q", got)
	}
	t.Setenv("VISUAL", `'`+spaced+`' --wait`)
	if got := get(); got != spaced+"|--wait" {
		t.Errorf("single-quoted path = %q", got)
	}
	t.Setenv("VISUAL", `"/opt/my editor`)
	if _, err := editorCommand(); err == nil || !strings.Contains(err.Error(), "$VISUAL: unterminated") {
		t.Errorf("unterminated quote: %v", err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := get(); got != "vi" && got != "notepad" {
		t.Errorf("fallback = %q", got)
	}
}

func TestSplitCommand(t *testing.T) {
	for _, c := range []struct {
		in      string
		escapes bool
		want    string
	}{
		{`emacs -nw`, true, "emacs|-nw"},
		{`  vim   -u  NONE `, true, "vim|-u|NONE"},
		{`"/a b/ed" 'x y' z""`, true, "/a b/ed|x y|z"},
		{`/a\ b/ed --x`, true, "/a b/ed|--x"},
		{`"say \"hi\" \\ \n"`, true, `say "hi" \ \n`},
		{`'it''s'`, true, "its"},
		{`""`, true, ""},
		{`C:\Program\ed.exe /w`, false, `C:\Program\ed.exe|/w`},
		{`"C:\Program Files\ed.exe" /w`, false, `C:\Program Files\ed.exe|/w`},
	} {
		got, err := splitCommand(c.in, c.escapes)
		if err != nil || strings.Join(got, "|") != c.want {
			t.Errorf("splitCommand(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{`"open`, `'open`, `end\`} {
		if _, err := splitCommand(bad, true); err == nil {
			t.Errorf("splitCommand(%q): no error", bad)
		}
	}
}

// A quoted editor path with a space runs.
func TestEditDocQuotedEditorPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the editor")
	}
	s := edTSite(t)
	script := filepath.Join(t.TempDir(), "my editor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = --flag ] || exit 3\nsed -e 's/^customer: C1$/customer: C8/' \"$2\" > \"$2.new\" && mv \"$2.new\" \"$2\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", `'`+script+`' --flag`)
	old := editInputDisabled
	editInputDisabled = func() bool { return false }
	t.Cleanup(func() { editInputDisabled = old })
	cmdTOK(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"))
	if d, _ := s.Doc("Sales Order", "SO-1"); d["customer"] != "C8" {
		t.Errorf("customer = %v", d["customer"])
	}
	// A broken $VISUAL is a usage error and saves nothing.
	t.Setenv("VISUAL", `'`+script)
	lcTCode(t, cmdTRun(t, s, "edit-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"), exitUsage, "$VISUAL: unterminated", "nothing was saved")
}
