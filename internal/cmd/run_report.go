package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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

	rrPrepared     bool
	rrFresh        bool
	rrWait         time.Duration
	rrPreparedName string
)

var runReportCmd = &cobra.Command{
	Use:   "run-report",
	Short: "Execute a Frappe query report",
	Long: `Run a named Frappe query report and display its results as a table.

The --filters flag accepts a JSON object of report filter values.

ffc runs the report in the request, even one marked "prepared" in Frappe.
A heavy report can then outlast the server's request timeout. --prepared
uses Frappe's background job instead (a worker on the "long" queue):

  - If you already have a finished result for the same filters, ffc
    returns it. It may be old: the note on stderr names the Prepared Report
    and when it finished. --fresh prepares a new one instead.
  - Otherwise ffc reuses your queued job for these filters, or starts one,
    and waits up to --wait.
  - If the wait runs out, the error names the Prepared Report. Resume with
    --prepared-name and the same --filters.

Each new job saves a Prepared Report document on the site (Frappe deletes
them after 30 days). On a report that is not marked "prepared", --prepared
runs it normally.

Examples:
  ffc run-report --name "General Ledger" --filters '{"company":"My Company","from_date":"2025-01-01"}'
  ffc run-report -n "Accounts Receivable" --json
  ffc run-report -n "Stock Balance" --filters '{"company":"Acme"}' --prepared --wait 10m
  ffc run-report -n "Stock Balance" --filters '{"company":"Acme"}' --prepared-name 0a1b2c3d4e
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
		prepared := rrPrepared || rrPreparedName != ""
		switch {
		case !prepared && (cmd.Flags().Changed("fresh") || cmd.Flags().Changed("wait")):
			return usageErrorf("--fresh and --wait need --prepared")
		case rrFresh && rrPreparedName != "":
			return usageErrorf("--fresh and --prepared-name cannot be combined")
		case rrWait < time.Second:
			return usageErrorf("--wait must be at least 1s, got %s", rrWait)
		}

		title := fmt.Sprintf("Running report %q…", rrName)
		if prepared {
			title = fmt.Sprintf("Running report %q (prepared; waiting up to %s)…", rrName, rrWait)
		}
		result, err := callSite(cmd, title, func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			if prepared {
				return c.RunPreparedReport(ctx, rrName, filters, client.PreparedOptions{Fresh: rrFresh, Name: rrPreparedName, Wait: rrWait})
			}
			return c.RunReport(ctx, rrName, filters)
		})
		if err != nil {
			return preparedError(err, rrFilters)
		}
		if doc, ok := result["doc"].(map[string]interface{}); ok {
			fmt.Fprintf(os.Stderr, "From prepared report %v, finished %v.\n", doc["name"], doc["report_end_time"])
		}
		limitReportRows(result, rrLimit)

		if machineOutput() {
			return printResult(selectKeys(result, rrKeys))
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

// preparedError explains a prepared report without a result (exit 7): how
// to resume one still queued, or why one has none.
func preparedError(err error, filters string) error {
	var pe *client.PreparedReportError
	if !errors.As(err, &pe) {
		return err
	}
	if !pe.Pending {
		return &codeError{exitNetwork, pe.Error() + " (the job failed, or the Prepared Report was made with other --filters)"}
	}
	resume := fmt.Sprintf("ffc run-report -n %s --prepared-name %s", shellQuote(pe.Report), shellQuote(pe.Name))
	if filters != "" {
		resume += " --filters " + shellQuote(filters)
	}
	return &codeError{exitNetwork, fmt.Sprintf("%s. Is a worker running on the site's \"long\" queue? Wait longer with: %s --wait 30m", pe.Error(), resume)}
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
	runReportCmd.Flags().BoolVar(&rrPrepared, "prepared", false, "Use Frappe's background job for a prepared report (a finished result for the same filters is reused)")
	runReportCmd.Flags().BoolVar(&rrFresh, "fresh", false, "With --prepared: prepare a new result even if a finished one exists")
	runReportCmd.Flags().DurationVar(&rrWait, "wait", 5*time.Minute, "With --prepared: how long to wait for the background job")
	runReportCmd.Flags().StringVar(&rrPreparedName, "prepared-name", "", "Wait for this Prepared Report (from an earlier --prepared run with the same --filters) and return its result")
	_ = runReportCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(runReportCmd)
}
