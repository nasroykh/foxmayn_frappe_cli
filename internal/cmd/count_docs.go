package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"

	"github.com/spf13/cobra"
)

// count-docs flags
var (
	coDoctype string
	coFilters string
	coGroupBy string
	coAtLeast int
)

var countDocsCmd = &cobra.Command{
	Use:   "count-docs",
	Short: "Count documents matching filters",
	Long: `Return the number of documents in a DocType, optionally filtered.

The count is printed to stdout — suitable for pipes and scripts.

With --group-by FIELD, ffc runs the list view's sidebar count
(frappe.desk.listview.get_group_by_count): one row per value of FIELD with
its count, most frequent first, at most 50 groups (ffc aggregate has no
such cap). Two values are special: "owner" puts your own group first, and
"assigned_to" is not a field: it counts, per System User, the ToDo records
allocated to them that are not Cancelled (Open and Closed) and whose
reference_name is a matching document's name. Frappe does not compare
reference_type, so a ToDo on a document of another DocType with the same
name counts too.

With --at-least N, ffc asks whether there are N or more matching documents
and stops counting at N (frappe.desk.reportview.get_count with a limit), so
the answer stays cheap on a large table. It prints true or false; --json
adds the count, which is exact below N and N otherwise. On MariaDB, Frappe
v16 gives that query 1 second: if it runs out, the answer is unknown
(result and count null, a warning on stderr, exit 0).

Examples:
  ffc count-docs -d "ToDo"
  ffc count-docs -d "Sales Invoice" --filters '{"status":"Paid"}'
  ffc count-docs -d "User" --filters '[["enabled","=","1"]]' --json
  ffc count-docs -d "ToDo" --group-by status
  ffc count-docs -d "Sales Invoice" --group-by assigned_to --filters '{"docstatus":0}'
  ffc count-docs -d "Error Log" --at-least 1000
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		filters, err := filtersFlag(coFilters)
		if err != nil {
			return err
		}
		atLeast := cmd.Flags().Changed("at-least")
		if atLeast && cmd.Flags().Changed("group-by") {
			return usageErrorf("--at-least and --group-by cannot be combined")
		}
		if cmd.Flags().Changed("group-by") {
			return countByGroup(cmd, coDoctype, filters, coGroupBy)
		}
		if atLeast {
			return countAtLeast(cmd, coDoctype, filters, coAtLeast)
		}
		count, err := callSite(cmd, fmt.Sprintf("Counting %s…", coDoctype), func(ctx context.Context, c *client.FrappeClient) (int, error) {
			return c.GetCount(ctx, coDoctype, filters)
		})
		if err != nil {
			return err
		}

		if machineOutput() {
			return printResult(map[string]interface{}{"doctype": coDoctype, "count": count})
		}
		fmt.Println(count)
		return nil
	},
}

func init() {
	countDocsCmd.Flags().StringVarP(&coDoctype, "doctype", "d", "", "Frappe DocType (required)")
	countDocsCmd.Flags().StringVar(&coFilters, "filters", "", `Filter expression as JSON: '{"status":"Open"}' or '[["status","=","Open"]]'`)
	countDocsCmd.Flags().StringVar(&coGroupBy, "group-by", "", `Count per value of this field (at most 50 groups; "assigned_to" counts ToDo allocations)`)
	countDocsCmd.Flags().IntVar(&coAtLeast, "at-least", 0, "Only answer whether there are at least N matching documents (stops counting at N)")
	_ = countDocsCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(countDocsCmd)
}

// countAtLeast is count-docs --at-least: whether n or more documents match,
// from a count that stops at n.
func countAtLeast(cmd *cobra.Command, doctype, filters string, n int) error {
	if n < 1 {
		return usageErrorf("--at-least must be at least 1, got %d", n)
	}
	res, err := callSite(cmd, fmt.Sprintf("Counting %s…", doctype), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
		return atLeastResult(ctx, c, doctype, filters, n)
	})
	if err != nil {
		return err
	}
	if w, ok := res["warning"].(string); ok {
		// Diagnostics go to stderr; MCP keeps it in the result.
		delete(res, "warning")
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	if machineOutput() {
		return printResult(res)
	}
	switch r := res["result"].(type) {
	case bool:
		fmt.Println(r)
	default:
		fmt.Println("unknown")
	}
	return nil
}

// atLeastResult is the answer count-docs --at-least and MCP count_docs
// at_least share: {doctype, at_least, result, count}, result and count null
// and a warning when the site gave up counting.
func atLeastResult(ctx context.Context, c *client.FrappeClient, doctype, filters string, n int) (map[string]interface{}, error) {
	count, ok, err := c.CountAtLeast(ctx, doctype, filters, n)
	if err != nil {
		return nil, err
	}
	res := map[string]interface{}{"doctype": doctype, "at_least": n, "result": nil, "count": nil}
	if !ok {
		res["warning"] = "the site stopped counting at its time limit (1 second on MariaDB), so the answer is unknown; a full count has no such limit"
		return res, nil
	}
	res["result"] = count >= n
	res["count"] = count
	return res, nil
}
