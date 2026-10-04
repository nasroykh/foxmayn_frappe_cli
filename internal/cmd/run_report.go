package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// run-report flags
var (
	rrName    string
	rrFilters string
	rrLimit   int
	rrKeys    string
)

var runReportCmd = &cobra.Command{
	Use:   "run-report",
	Short: "Execute a Frappe query report",
	Long: `Run a named Frappe query report and display its results as a table.

The --filters flag accepts a JSON object of report filter values.

Examples:
  ffc run-report --name "General Ledger" --filters '{"company":"My Company","from_date":"2025-01-01"}'
  ffc run-report -n "Accounts Receivable" --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var filters map[string]interface{}
		if rrFilters != "" {
			var err error
			if filters, err = parseObject("--filters", rrFilters); err != nil {
				return err
			}
		}
		if rrLimit < 0 {
			return usageErrorf("--limit must be >= 0 (0 means all rows)")
		}

		result, err := callSite(cmd, fmt.Sprintf("Running report %q…", rrName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return c.RunReport(ctx, rrName, filters)
		})
		if err != nil {
			return err
		}
		limitReportRows(result, rrLimit)

		if jsonOutput {
			return output.PrintJSON(selectKeys(result, rrKeys))
		}

		rows, cols := reportTable(result)
		if len(rows) == 0 {
			// Empty is a successful outcome, not an error.
			fmt.Fprintf(os.Stderr, "No results for report %q.\n", rrName)
			return nil
		}
		output.PrintTable(rows, cols)
		return nil
	},
}

// limitReportRows truncates result["result"] to n rows (n <= 0: no limit) and
// records the original count so a truncated answer is never mistaken for the
// full one.
func limitReportRows(result map[string]interface{}, n int) {
	rows, ok := result["result"].([]interface{})
	if !ok || n <= 0 || len(rows) <= n {
		return
	}
	result["result"] = rows[:n]
	result["total_rows"] = len(rows)
	result["truncated"] = true
}

// reportTable converts a query-report result into table rows plus ordered
// column keys. Columns are either dicts ({"fieldname","label"}) or legacy
// "Label:Fieldtype/Options:Width" strings; rows are either arrays (zipped
// with the columns by position) or dicts keyed by fieldname. For a legacy
// string column Frappe derives the fieldname by scrubbing the label, so dict
// rows are looked up by that. Duplicate keys get a numeric suffix so two
// columns with the same fieldname do not collapse into one.
func reportTable(result map[string]interface{}) ([]map[string]interface{}, []string) {
	rawCols, _ := result["columns"].([]interface{})
	rawRows, _ := result["result"].([]interface{})

	cols := make([]string, 0, len(rawCols))
	seen := map[string]int{}
	for _, rc := range rawCols {
		key := ""
		switch col := rc.(type) {
		case string:
			label, _, _ := strings.Cut(col, ":")
			key = scrub(label)
		case map[string]interface{}:
			if fn, ok := col["fieldname"].(string); ok && fn != "" {
				key = fn
			} else if lbl, ok := col["label"].(string); ok {
				key = scrub(lbl)
			}
		}
		if key == "" {
			key = fmt.Sprintf("column_%d", len(cols)+1)
		}
		if n := seen[key]; n > 0 {
			seen[key] = n + 1
			key = fmt.Sprintf("%s_%d", key, n+1)
		} else {
			seen[key] = 1
		}
		cols = append(cols, key)
	}

	rows := make([]map[string]interface{}, 0, len(rawRows))
	for _, rr := range rawRows {
		switch row := rr.(type) {
		case []interface{}:
			m := make(map[string]interface{}, len(cols))
			for i, c := range cols {
				if i < len(row) {
					m[c] = row[i]
				}
			}
			rows = append(rows, m)
		case map[string]interface{}:
			rows = append(rows, row)
		}
	}
	// Dict rows whose keys match none of the columns: show their own keys.
	if len(rows) > 0 && len(cols) > 0 {
		hit := false
		for _, c := range cols {
			if _, ok := rows[0][c]; ok {
				hit = true
				break
			}
		}
		if !hit {
			cols = nil
		}
	}
	return rows, cols
}

// scrub mirrors frappe.scrub: lower-case, spaces and dashes to underscores.
func scrub(s string) string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(s)))
}

func init() {
	runReportCmd.Flags().StringVarP(&rrName, "name", "n", "", "Report name (required)")
	runReportCmd.Flags().StringVar(&rrFilters, "filters", "", `Report filters as a JSON object, e.g. '{"company":"Acme","from_date":"2025-01-01"}'`)
	runReportCmd.Flags().IntVarP(&rrLimit, "limit", "l", 0, "Maximum number of result rows (table and JSON; 0 = all)")
	runReportCmd.Flags().StringVar(&rrKeys, "keys", "", "Comma-separated top-level keys to include in JSON output, e.g. columns,result")
	_ = runReportCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(runReportCmd)
}
