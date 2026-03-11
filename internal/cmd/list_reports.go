package cmd

import (
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
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

		filters := ""
		if lrModule != "" {
			filters = fmt.Sprintf(`{"module":"%s"}`, lrModule)
		}

		opts := client.ListOptions{
			Fields:  []string{"name", "report_type", "module", "is_standard", "ref_doctype"},
			Filters: filters,
			Limit:   lrLimit,
			OrderBy: "name asc",
		}

		var rows []map[string]interface{}
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title("Fetching reports…").
			Action(func() {
				rows, apiErr = c.GetList("Report", opts)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			output.PrintJSON(rows)
		} else {
			output.PrintTable(rows, []string{"name", "report_type", "module", "ref_doctype"})
		}

		return nil
	},
}

func init() {
	listReportsCmd.Flags().StringVarP(&lrModule, "module", "m", "", "Filter by module name (e.g. \"Accounts\")")
	listReportsCmd.Flags().IntVarP(&lrLimit, "limit", "l", 50, "Maximum number of reports to return")
	rootCmd.AddCommand(listReportsCmd)
}
