package cmd

import (
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
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
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		filters := ""
		if ltModule != "" {
			filters = fmt.Sprintf(`{"module":"%s"}`, ltModule)
		}

		opts := client.ListOptions{
			Fields:  []string{"name", "module", "is_submittable", "is_tree", "description"},
			Filters: filters,
			Limit:   ltLimit,
			OrderBy: "name asc",
		}

		var rows []map[string]interface{}
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title("Fetching DocTypes…").
			Action(func() {
				rows, apiErr = c.GetList("DocType", opts)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			output.PrintJSON(rows)
		} else {
			output.PrintTable(rows, []string{"name", "module", "is_submittable", "description"})
		}

		return nil
	},
}

func init() {
	listDoctypesCmd.Flags().StringVarP(&ltModule, "module", "m", "", "Filter by module name (e.g. \"Accounts\")")
	listDoctypesCmd.Flags().IntVarP(&ltLimit, "limit", "l", 50, "Maximum number of DocTypes to return")
	rootCmd.AddCommand(listDoctypesCmd)
}
