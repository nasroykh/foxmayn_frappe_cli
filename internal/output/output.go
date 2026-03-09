package output

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/olekukonko/tablewriter"
)

// PrintTable renders rows as an ASCII table. If fields is non-empty, only
// those columns appear; otherwise all keys are printed (sorted).
func PrintTable(rows []map[string]interface{}, fields []string) {
	if len(rows) == 0 {
		fmt.Println("No records found.")
		return
	}

	// Determine columns
	cols := fields
	if len(cols) == 0 {
		// Collect all keys from the first row, sorted
		for k := range rows[0] {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}

	// v1 API: NewTable + variadic Header + Bulk with [][]any
	table := tablewriter.NewTable(os.Stdout)

	// Header takes variadic any
	headerArgs := make([]any, len(cols))
	for i, c := range cols {
		headerArgs[i] = c
	}
	table.Header(headerArgs...)

	// Build data rows as [][]any
	data := make([][]any, len(rows))
	for i, row := range rows {
		line := make([]any, len(cols))
		for j, col := range cols {
			val := row[col]
			if val == nil {
				line[j] = ""
			} else {
				line[j] = fmt.Sprintf("%v", val)
			}
		}
		data[i] = line
	}

	table.Bulk(data)

	if err := table.Render(); err != nil {
		fmt.Fprintf(os.Stderr, "error rendering table: %v\n", err)
	}
}

// PrintJSON pretty-prints data as indented JSON to stdout.
func PrintJSON(data interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding JSON: %v\n", err)
	}
}
