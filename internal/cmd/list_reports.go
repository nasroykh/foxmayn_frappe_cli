package cmd

import (
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// list-reports flags
var (
	lrModule string
	lrLimit  int
	lrPages  pageFlags
)

var listReportsCmd = &cobra.Command{
	Use:   "list-reports",
	Short: "List available Frappe reports",
	Long: `List all reports available on the Frappe site.

Optionally filter by module name.

Examples:
  ffc list-reports
  ffc list-reports --module "Accounts" --limit 20
  ffc list-reports --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts, err := moduleListOptions(reportListFields, lrModule, lrLimit)
		if err != nil {
			return err
		}
		return listDocsTo(cmd, lrPages, "Fetching reports…", "Report", opts, reportListFields, func(rows []map[string]interface{}) error {
			output.PrintTable(rows, []string{"name", "report_type", "module", "ref_doctype"})
			return nil
		}, listCacher("Report", lrModule))
	},
}

func init() {
	listReportsCmd.Flags().StringVarP(&lrModule, "module", "m", "", "Filter by module name (e.g. \"Accounts\")")
	listReportsCmd.Flags().IntVarP(&lrLimit, "limit", "l", 50, "Maximum reports to return (0 = no limit)")
	lrPages.register(listReportsCmd, "limit")
	rootCmd.AddCommand(listReportsCmd)
}
