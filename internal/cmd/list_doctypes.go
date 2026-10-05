package cmd

import (
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// list-doctypes flags
var (
	ltModule string
	ltLimit  int
	ltPages  pageFlags
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
		return listDocsTo(cmd, ltPages, "Fetching DocTypes…", "DocType", opts, doctypeListFields, func(rows []map[string]interface{}) error {
			output.PrintTable(rows, []string{"name", "module", "is_submittable", "description"})
			return nil
		}, listCacher("DocType", ltModule))
	},
}

func init() {
	listDoctypesCmd.Flags().StringVarP(&ltModule, "module", "m", "", "Filter by module name (e.g. \"Accounts\")")
	listDoctypesCmd.Flags().IntVarP(&ltLimit, "limit", "l", 50, "Maximum DocTypes to return (0 = no limit)")
	ltPages.register(listDoctypesCmd, "limit")
	rootCmd.AddCommand(listDoctypesCmd)
}
