package output

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

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

	// Determine columns.
	cols := fields
	if len(cols) == 0 {
		for k := range rows[0] {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}

	// Build data rows.
	data := make([][]string, len(rows))
	for i, row := range rows {
		line := make([]string, len(cols))
		for j, col := range cols {
			line[j] = formatValue(row[col])
		}
		data[i] = line
	}

	// Upper-case headers for visual clarity.
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = strings.ToUpper(c)
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

// PrintJSON pretty-prints data as indented JSON to stdout.
func PrintJSON(data interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("✗ error encoding JSON: "+err.Error()))
	}
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
		rows = append(rows, []string{k, formatValue(doc[k])})
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
	fmt.Fprintln(os.Stderr, errorStyle.Render("✗ "+msg))
}

// PrintSuccess writes a styled success message to stderr.
func PrintSuccess(msg string) {
	fmt.Fprintln(os.Stderr, successStyle.Render("✓ "+msg))
}

// formatValue converts a value to a string, pretty-printing maps/slices as JSON.
func formatValue(v interface{}) string {
	if v == nil {
		return dimStyle.Render("—")
	}

	switch val := v.(type) {
	case map[string]interface{}, []interface{}, []map[string]interface{}:
		b, err := json.MarshalIndent(val, "", "  ")
		if err == nil {
			return string(b)
		}
	}

	return fmt.Sprintf("%v", v)
}
