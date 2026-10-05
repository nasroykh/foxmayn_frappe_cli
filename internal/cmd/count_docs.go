package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"

	"github.com/spf13/cobra"
)

// count-docs flags
var (
	coDoctype string
	coFilters string
	coGroupBy string
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

Examples:
  ffc count-docs -d "ToDo"
  ffc count-docs -d "Sales Invoice" --filters '{"status":"Paid"}'
  ffc count-docs -d "User" --filters '[["enabled","=","1"]]' --json
  ffc count-docs -d "ToDo" --group-by status
  ffc count-docs -d "Sales Invoice" --group-by assigned_to --filters '{"docstatus":0}'
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		filters, err := filtersFlag(coFilters)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("group-by") {
			return countByGroup(cmd, coDoctype, filters, coGroupBy)
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
	_ = countDocsCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(countDocsCmd)
}
