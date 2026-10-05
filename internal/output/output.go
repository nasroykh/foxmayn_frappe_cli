package output

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// Color palette.
var (
	purple    = lipgloss.Color("99")
	gray      = lipgloss.Color("245")
	lightGray = lipgloss.Color("241")
	green     = lipgloss.Color("42")
	red       = lipgloss.Color("196")
	yellow    = lipgloss.Color("220")
	dim       = lipgloss.Color("238")
)

// Styles for messages.
var (
	// errorStyle formats error messages in red with a ✗ prefix.
	errorStyle = lipgloss.NewStyle().Foreground(red).Bold(true)
	// warnStyle formats warnings in yellow.
	warnStyle = lipgloss.NewStyle().Foreground(yellow)
	// successStyle formats success messages in green.
	successStyle = lipgloss.NewStyle().Foreground(green)
	// dimStyle formats secondary info in dim gray.
	dimStyle = lipgloss.NewStyle().Foreground(dim)
)

// Table styles.
var (
	headerStyle = lipgloss.NewStyle().
			Foreground(purple).
			Bold(true).
			Align(lipgloss.Center).
			Padding(0, 1)

	cellStyle    = lipgloss.NewStyle().Padding(0, 1)
	oddRowStyle  = cellStyle.Foreground(gray)
	evenRowStyle = cellStyle.Foreground(lightGray)
)

// PrintTable renders rows as a styled lipgloss table.
// If fields is non-empty, only those columns appear; otherwise all keys are
// printed (sorted alphabetically).
func PrintTable(rows []map[string]interface{}, fields []string) {
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, warnStyle.Render("No records found."))
		return
	}

	cols := tableColumns(rows[0], fields)

	// Build data rows.
	data := make([][]string, len(rows))
	for i, row := range rows {
		line := make([]string, len(cols))
		for j, col := range cols {
			line[j] = formatValue(col, row[col])
		}
		data[i] = line
	}

	// Upper-case headers for visual clarity.
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = strings.ToUpper(text.Sanitize(c))
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(purple)).
		StyleFunc(func(row, col int) lipgloss.Style {
			switch {
			case row == table.HeaderRow:
				return headerStyle
			case row%2 == 0:
				return evenRowStyle
			default:
				return oddRowStyle
			}
		}).
		Headers(headers...).
		Rows(data...)

	lipgloss.Println(t)

	// Row count summary.
	summary := fmt.Sprintf("%d record(s)", len(rows))
	fmt.Fprintln(os.Stderr, dimStyle.Render(summary))
}

// PrintJSON pretty-prints data as indented JSON to stdout. It returns the
// encode error (rather than swallowing it) so callers can exit non-zero (L16).
func PrintJSON(data interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}

// PrintDocTable renders a single document as a two-column Field | Value table.
// If fields is non-empty, only those fields are shown; otherwise all non-nil
// fields are printed, sorted alphabetically.
func PrintDocTable(doc map[string]interface{}, fields []string) {
	if len(doc) == 0 {
		fmt.Fprintln(os.Stderr, warnStyle.Render("Document is empty."))
		return
	}

	// Determine which fields to show.
	keys := fields
	if len(keys) == 0 {
		for k, v := range doc {
			// Skip nil and empty-string fields when showing all.
			if v == nil || v == "" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}

	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{text.Sanitize(k), formatValue(k, doc[k])})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(purple)).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)

			if row == table.HeaderRow {
				return headerStyle
			}

			if col == 0 {
				// Field name column: bold and fixed width
				return s.Foreground(gray).Bold(true).Width(24)
			}

			// Value column: wrap text and set max width
			s = s.Width(80)
			if row%2 == 0 {
				return s.Foreground(lightGray)
			}
			return s.Foreground(gray)
		}).
		Headers("FIELD", "VALUE").
		Rows(rows...)

	lipgloss.Println(t)
}

// PrintError writes a styled error message to stderr.
func PrintError(msg string) {
	fmt.Fprintln(os.Stderr, errorStyle.Render("✗ "+text.Sanitize(msg)))
}

// PrintSuccess writes a styled success message to stderr.
func PrintSuccess(msg string) {
	fmt.Fprintln(os.Stderr, successStyle.Render("✓ "+text.Sanitize(msg)))
}

// PrintCheck writes one line of a health report to stdout: a marker for the
// status ("pass", "warn" or "fail"), the check name and its message, then
// the hint, indented, when there is one.
func PrintCheck(status, check, message, hint string) {
	style, mark := successStyle, "✓"
	switch status {
	case "warn":
		style, mark = warnStyle, "!"
	case "fail":
		style, mark = errorStyle, "✗"
	}
	fmt.Println(style.Render(mark+" "+text.Sanitize(check)) + "  " + text.Sanitize(message))
	if hint != "" {
		fmt.Println(lipgloss.NewStyle().Foreground(gray).Render("    → " + text.Sanitize(hint)))
	}
}

// PrintWarning writes a styled warning to stderr.
func PrintWarning(msg string) {
	fmt.Fprintln(os.Stderr, warnStyle.Render(text.Sanitize(msg)))
}

// tableColumns picks the columns to show. Requested fields are used as
// column keys, mapped to the key Frappe actually returns: "count(name) as n"
// comes back as "n" and "`tabToDo`.status" or "items.item_code" as the part
// after the last dot. "*" or no fields means every key, sorted.
func tableColumns(first map[string]interface{}, fields []string) []string {
	var cols []string
	for _, f := range fields {
		if f == "*" {
			cols = nil
			break
		}
		cols = append(cols, resultKey(f))
	}
	if len(cols) == 0 {
		for k := range first {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}
	return cols
}

// resultKey returns the key under which Frappe returns a requested field.
func resultKey(field string) string {
	f := strings.TrimSpace(field)
	if i := strings.LastIndex(strings.ToLower(f), " as "); i >= 0 {
		f = strings.TrimSpace(f[i+4:])
	} else if i := strings.LastIndex(f, "."); i >= 0 {
		f = f[i+1:]
	}
	return strings.Trim(f, "`\"")
}

// identityKeys are columns whose values are identifiers, shown verbatim: a
// numeric autoincrement name must stay pasteable into get-doc ("1234", not
// "1 234"), and a name that looks like a date must not be reformatted.
var identityKeys = map[string]bool{"name": true, "idx": true, "parent": true}

// formatValue converts a value to a display string: numbers and ISO dates in
// the configured format, maps/slices as indented JSON, and every string
// stripped of terminal control characters.
func formatValue(key string, v interface{}) string {
	if v == nil {
		return dimStyle.Render("—")
	}
	switch val := v.(type) {
	case float64:
		if identityKeys[key] {
			return strconv.FormatFloat(val, 'f', -1, 64)
		}
		return config.FormatNumber(val)
	case json.Number:
		// Frappe sends Float and Currency values with a decimal point (1500.0)
		// and Int/Check values without (2025). Only the former are amounts to
		// group; an integer may be a year, a count or an ID.
		s := val.String()
		if identityKeys[key] || !strings.ContainsAny(s, ".eE") {
			return s
		}
		if f, err := val.Float64(); err == nil {
			return config.FormatNumber(f)
		}
		return s
	case string:
		if identityKeys[key] {
			return text.Sanitize(val)
		}
		return text.Sanitize(config.FormatDate(val))
	case map[string]interface{}, []interface{}, []map[string]interface{}:
		if b, err := json.MarshalIndent(val, "", "  "); err == nil {
			return text.Sanitize(string(b))
		}
	}
	return text.Sanitize(fmt.Sprintf("%v", v))
}
