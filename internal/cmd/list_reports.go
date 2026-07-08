package cmd

import (
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		filters, err := moduleFilter(lrModule)
		if err != nil {
			return err
		}

		limit := lrLimit
		if limit == 0 { // --limit 0 => no limit (M12)
			limit = -1
		}

		opts := client.ListOptions{
			Fields:  []string{"name", "report_type", "module", "is_standard", "ref_doctype"},
			Filters: filters,
			Limit:   limit,
			OrderBy: "name asc",
		}

		var rows []map[string]interface{}
		var apiErr error
		c, err := client.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		_ = runSpinner("Fetching reports…", func() {
			rows, apiErr = c.GetList(cmd.Context(), "Report", opts)
		})
		if apiErr != nil {
			return apiErr
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
