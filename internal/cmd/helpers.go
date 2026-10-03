package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh/spinner"
	"github.com/mattn/go-isatty"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/spf13/cobra"
)

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
	switch v.(type) {
	case map[string]interface{}, []interface{}:
		return nil
	}
	return errors.New("--filters: expected a JSON object or array")
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

// Fields fetched by list-doctypes / list-reports (CLI and MCP).
var (
	doctypeListFields = []string{"name", "module", "is_submittable", "is_tree", "description"}
	reportListFields  = []string{"name", "report_type", "module", "is_standard", "ref_doctype"}
)

// moduleListOptions builds the list options shared by list-doctypes and
// list-reports: an optional module filter, a limit where 0 means "all", and
// name ordering.
func moduleListOptions(fields []string, module string, limit int) (client.ListOptions, error) {
	filters, err := moduleFilter(module)
	if err != nil {
		return client.ListOptions{}, err
	}
	l, err := listLimit("limit", limit)
	if err != nil {
		return client.ListOptions{}, err
	}
	return client.ListOptions{Fields: fields, Filters: filters, Limit: l, OrderBy: "name asc"}, nil
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
	if !spinnerEnabled() {
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

// spinnerEnabled reports whether progress animation should be drawn: only on
// an interactive stderr, and not when --quiet, NO_COLOR or CI is set. A
// redirected stderr (2>log, cron) would otherwise fill up with ANSI frames.
func spinnerEnabled() bool {
	if quiet || os.Getenv("NO_COLOR") != "" || os.Getenv("CI") != "" {
		return false
	}
	fd := os.Stderr.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// confirm asks a yes/no question and returns nil only on an explicit yes; a
// decline or Esc is errAborted, so a script can tell "nothing was done" from
// success by the exit code. Without a terminal it fails with a hint instead
// of hanging or silently succeeding.
func confirm(prompt string) error {
	ok, err := confirmPrompt(prompt, "")
	switch {
	case errors.Is(err, errAborted):
		return err
	case err != nil:
		return fmt.Errorf("confirmation prompt failed (pass --yes to skip it): %w", err)
	case !ok:
		return errAborted
	}
	return nil
}

// callSite builds a client for the selected site and runs fn under a spinner.
func callSite[T any](cmd *cobra.Command, title string, fn func(ctx context.Context, c *client.FrappeClient) (T, error)) (T, error) {
	var zero T
	c, err := newClient(cmd.Context())
	if err != nil {
		return zero, err
	}
	var out T
	var apiErr error
	spinErr := runSpinner(title, func() { out, apiErr = fn(cmd.Context(), c) })
	if apiErr != nil {
		return zero, apiErr
	}
	if spinErr != nil {
		return zero, spinErr
	}
	return out, nil
}

// readInput returns the JSON for a --data/--file pair: the inline value, the
// file's content, or stdin when the file is "-".
func readInput(inline, file string) ([]byte, error) {
	switch file {
	case "":
		if inline == "" {
			return nil, errors.New("provide --data or --file")
		}
		return []byte(inline), nil
	case "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		return b, nil
	default:
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading file: %w", err)
		}
		return b, nil
	}
}

// parseObject decodes a JSON object flag; null and non-objects are rejected.
func parseObject(flag, raw string) (map[string]interface{}, error) {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		if err == nil {
			err = errors.New("got null")
		}
		return nil, fmt.Errorf("%s: expected a JSON object: %w", flag, err)
	}
	return m, nil
}

// splitCSV splits a comma-separated flag value, trimming blanks.
func splitCSV(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// filterKeys returns a new map with only the given top-level keys. Requested
// keys that do not exist are returned as missing so callers can warn instead
// of silently printing {}.
func filterKeys(doc map[string]interface{}, keys []string) (map[string]interface{}, []string) {
	out := make(map[string]interface{}, len(keys))
	var missing []string
	for _, k := range keys {
		if v, ok := doc[k]; ok {
			out[k] = v
		} else {
			missing = append(missing, k)
		}
	}
	return out, missing
}

// selectKeys applies a --keys flag to doc for the CLI, warning on stderr
// about keys that are not present.
func selectKeys(doc map[string]interface{}, keys string) map[string]interface{} {
	if keys == "" {
		return doc
	}
	out, missing := filterKeys(doc, splitCSV(keys))
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "warning: --keys: not present in the result: %s\n", strings.Join(missing, ", "))
	}
	return out
}

// listLimit maps a user-facing limit (0 = no limit) to client.ListOptions.Limit
// (<0 = no limit) and rejects negatives.
func listLimit(flag string, n int) (int, error) {
	switch {
	case n < 0:
		return 0, fmt.Errorf("%s must be >= 0 (0 means no limit)", flag)
	case n == 0:
		return -1, nil
	}
	return n, nil
}
