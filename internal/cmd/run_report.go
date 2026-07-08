package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		var filters map[string]interface{}
		if rrFilters != "" {
			if err := json.Unmarshal([]byte(rrFilters), &filters); err != nil {
				return fmt.Errorf("--filters: invalid JSON object: %w", err)
			}
		}

		var result map[string]interface{}
		var apiErr error
		c, err := client.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		_ = runSpinner(fmt.Sprintf("Running report %q…", rrName), func() {
			result, apiErr = c.RunReport(cmd.Context(), rrName, filters)
		})
		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			out := map[string]interface{}(result)
			// Honour --limit in JSON mode too (previously ignored, L11).
			if rrLimit > 0 {
				if rows, ok := out["result"].([]interface{}); ok && len(rows) > rrLimit {
					out["result"] = rows[:rrLimit]
				}
			}
			if rrKeys != "" {
				out = filterSchemaKeys(out, strings.Split(rrKeys, ","))
			}
			return output.PrintJSON(out)
		}

		// Extract columns and rows for table rendering.
		rawCols, _ := result["columns"].([]interface{})
		rawRows, _ := result["result"].([]interface{})

		// Build field name list from column definitions.
		// Frappe columns can be strings ("field:type:width") or objects.
		colFields := make([]string, 0, len(rawCols))
		for _, rc := range rawCols {
			switch col := rc.(type) {
			case string:
				// Legacy "label:fieldtype:width" columns — use the first segment
				// as the field name/header rather than the whole string (L11).
				name := col
				if i := strings.IndexByte(col, ':'); i >= 0 {
					name = col[:i]
				}
				colFields = append(colFields, name)
			case map[string]interface{}:
				if fn, ok := col["fieldname"].(string); ok && fn != "" {
					colFields = append(colFields, fn)
				} else if lbl, ok := col["label"].(string); ok {
					colFields = append(colFields, lbl)
				}
			}
		}

		// Frappe reports return rows in one of two formats:
		//   1. Array rows: each row is []interface{}{val1, val2, …} — map using colFields
		//   2. Object rows: each row is map[string]interface{}{"field": val} — use as-is
		rows := make([]map[string]interface{}, 0, len(rawRows))
		for _, rr := range rawRows {
			switch row := rr.(type) {
			case []interface{}:
				// Array format: zip column fields with values.
				m := make(map[string]interface{}, len(colFields))
				for i, f := range colFields {
					if i < len(row) {
						m[f] = row[i]
					}
				}
				rows = append(rows, m)
			case map[string]interface{}:
				// Object format: already a map — use directly.
				rows = append(rows, row)
			}
		}

		if rrLimit > 0 && len(rows) > rrLimit {
			rows = rows[:rrLimit]
		}

		if len(rows) == 0 {
			// Empty is a successful outcome, not an error — neutral message (L7).
			fmt.Fprintf(os.Stderr, "No results for report %q.\n", rrName)
			return nil
		}

		// Use colFields if available (preserves column order from report definition).
		// If empty (object rows with no column metadata), PrintTable sorts keys.
		output.PrintTable(rows, colFields)
		return nil
	},
}

func init() {
	runReportCmd.Flags().StringVarP(&rrName, "name", "n", "", "Report name (required)")
	runReportCmd.Flags().StringVar(&rrFilters, "filters", "", `Report filters as a JSON object, e.g. '{"company":"Acme","from_date":"2025-01-01"}'`)
	runReportCmd.Flags().IntVarP(&rrLimit, "limit", "l", 0, "Limit number of rows displayed")
	runReportCmd.Flags().StringVar(&rrKeys, "keys", "", "Comma-separated top-level keys to include in JSON output, e.g. columns,result")
	_ = runReportCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(runReportCmd)
}
