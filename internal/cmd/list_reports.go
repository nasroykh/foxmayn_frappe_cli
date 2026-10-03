package cmd

import (
	"context"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// list-reports flags
var (
	lrModule string
	lrLimit  int
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
		rows, err := callSite(cmd, "Fetching reports…", func(ctx context.Context, c *client.FrappeClient) ([]map[string]interface{}, error) {
			return c.GetList(ctx, "Report", opts)
		})
		if err != nil {
			return err
		}

		if jsonOutput {
			return output.PrintJSON(rows)
		}
		output.PrintTable(rows, []string{"name", "report_type", "module", "ref_doctype"})
		return nil
	},
}

func init() {
	listReportsCmd.Flags().StringVarP(&lrModule, "module", "m", "", "Filter by module name (e.g. \"Accounts\")")
	listReportsCmd.Flags().IntVarP(&lrLimit, "limit", "l", 50, "Maximum reports to return (0 = no limit)")
	rootCmd.AddCommand(listReportsCmd)
}
