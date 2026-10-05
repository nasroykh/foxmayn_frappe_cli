package output

import (
	"io"
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Styled messages go through lipgloss writers, which drop colors when the
// stream is not a terminal: a pipe or a file must never get escape codes.
func TestNoEscapesWhenNotATerminal(t *testing.T) {
	for _, k := range []string{"CLICOLOR_FORCE", "TTY_FORCE", "NO_COLOR"} {
		t.Setenv(k, "")
	}
	capture := func(t *testing.T, print func(w *os.File)) string {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		print(w)
		w.Close()
		b, _ := io.ReadAll(r)
		return string(b)
	}
	t.Run("stderr", func(t *testing.T) {
		got := capture(t, func(w *os.File) {
			old := os.Stderr
			os.Stderr = w
			defer func() { os.Stderr = old }()
			PrintWarning("careful")
			PrintError("broken")
			PrintSuccess("done")
		})
		if strings.Contains(got, "\x1b[") || !strings.Contains(got, "careful") {
			t.Errorf("stderr = %q", got)
		}
	})
	t.Run("stdout", func(t *testing.T) {
		got := capture(t, func(w *os.File) {
			old := lipgloss.Writer
			lipgloss.Writer = colorprofile.NewWriter(w, os.Environ())
			defer func() { lipgloss.Writer = old }()
			PrintCheck("fail", "net.reachable", "no answer", "check the URL")
		})
		if strings.Contains(got, "\x1b[") || !strings.Contains(got, "check the URL") {
			t.Errorf("stdout = %q", got)
		}
	})
}
