package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/charmbracelet/huh/spinner"
)

// atomicWriteFile writes data to a temp file in the same directory and renames
// it over path. A crash mid-write can no longer truncate an existing config and
// destroy every site's credentials (M9). perm is applied to the final file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ffc-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // best-effort cleanup if we bail before the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// docName coerces a document "name" value to a string. Frappe autoincrement /
// integer-named DocTypes surface names as JSON numbers, so a plain string
// type-assertion would reject them (L27). Returns ok=false only for a missing
// or empty name.
func docName(v interface{}) (string, bool) {
	switch n := v.(type) {
	case string:
		return n, n != ""
	case json.Number:
		return n.String(), n.String() != ""
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64), true
	case int:
		return strconv.Itoa(n), true
	case int64:
		return strconv.FormatInt(n, 10), true
	default:
		return "", false
	}
}

// validateFiltersJSON returns a clear client-side error when raw is a
// non-empty, invalid JSON string, instead of letting it reach the server and
// come back as a raw JSONDecodeError (500). Mirrors the validation create-doc,
// update-doc, bulk-*, run-report, and the MCP tools already do for their own
// JSON flags.
func validateFiltersJSON(raw string) error {
	if raw == "" {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return fmt.Errorf("--filters: invalid JSON: %w", err)
	}
	return nil
}

// moduleFilter builds a Frappe list-filter JSON for an optional module name,
// JSON-encoding the value so a module containing " or \ cannot break the
// filter (L28). Returns "" when module is empty.
func moduleFilter(module string) (string, error) {
	if module == "" {
		return "", nil
	}
	b, err := json.Marshal(map[string]string{"module": module})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// runSpinner runs fn while displaying a spinner. The spinner is written to
// stderr (never stdout), so it can never corrupt `--json` output piped to
// another program (H4). It returns spinner.Run()'s error so callers that care
// (bulk loops, the login probe) can detect a Ctrl+C interrupt.
//
// On interrupt, huh/spinner's Run returns before the action goroutine finishes.
// We wait for fn to actually complete before returning, so a caller that reads
// the variables fn wrote can never race the still-running goroutine. This stays
// responsive because every fn issues a client call bound to the command's
// context, which Ctrl+C cancels — aborting the in-flight request promptly.
func runSpinner(title string, fn func()) error {
	if quiet || os.Getenv("NO_COLOR") != "" || os.Getenv("CI") != "" {
		fn()
		return nil
	}
	done := make(chan struct{})
	err := spinner.New().
		Title(title).
		Action(func() { defer close(done); fn() }).
		Output(os.Stderr).
		Run()
	<-done
	return err
}
