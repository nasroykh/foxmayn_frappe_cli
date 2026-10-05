package output

import (
	"io"
	"os"
	"strings"
	"testing"
)

// Styled messages go through lipgloss writers, which drop colors when the
// stream is not a terminal: a pipe or a file must never get escape codes.
func TestNoEscapesWhenNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	t.Setenv("CLICOLOR_FORCE", "")
	PrintWarning("careful")
	PrintError("broken")
	PrintSuccess("done")
	os.Stderr = old
	w.Close()
	b, _ := io.ReadAll(r)
	if got := string(b); strings.Contains(got, "\x1b[") || !strings.Contains(got, "careful") {
		t.Errorf("stderr = %q", got)
	}
}
