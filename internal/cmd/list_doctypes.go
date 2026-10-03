package cmd

import (
	"context"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// list-doctypes flags
var (
	ltModule string
	ltLimit  int
)

var listDoctypesCmd = &cobra.Command{
	Use:   "list-doctypes",
	Short: "List available DocTypes",
	Long: `List all DocTypes registered on the Frappe site, optionally filtered by module.

Examples:
  ffc list-doctypes
  ffc list-doctypes --module "Accounts" --limit 20
  ffc list-doctypes --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts, err := moduleListOptions(doctypeListFields, ltModule, ltLimit)
		if err != nil {
			return err
		}
		rows, err := callSite(cmd, "Fetching DocTypes…", func(ctx context.Context, c *client.FrappeClient) ([]map[string]interface{}, error) {
			return c.GetList(ctx, "DocType", opts)
		})
		if err != nil {
			return err
		}

		if jsonOutput {
			return output.PrintJSON(rows)
		}
		output.PrintTable(rows, []string{"name", "module", "is_submittable", "description"})
		return nil
	},
}

func init() {
	listDoctypesCmd.Flags().StringVarP(&ltModule, "module", "m", "", "Filter by module name (e.g. \"Accounts\")")
	listDoctypesCmd.Flags().IntVarP(&ltLimit, "limit", "l", 50, "Maximum DocTypes to return (0 = no limit)")
	rootCmd.AddCommand(listDoctypesCmd)
}
