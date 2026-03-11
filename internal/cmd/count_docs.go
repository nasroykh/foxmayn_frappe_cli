package cmd

import (
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
	"github.com/spf13/cobra"
)

// count-docs flags
var (
	coDoctype string
	coFilters string
)

var countDocsCmd = &cobra.Command{
	Use:   "count-docs",
	Short: "Count documents matching filters",
	Long: `Return the number of documents in a DocType, optionally filtered.

The count is printed to stdout — suitable for pipes and scripts.

Examples:
  ffc count-docs -d "ToDo"
  ffc count-docs -d "Sales Invoice" --filters '{"status":"Paid"}'
  ffc count-docs -d "User" --filters '[["enabled","=","1"]]' --json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		var count int
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Counting %s…", coDoctype)).
			Action(func() {
				count, apiErr = c.GetCount(coDoctype, coFilters)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			output.PrintJSON(map[string]interface{}{"doctype": coDoctype, "count": count})
		} else {
			fmt.Println(count)
		}

		return nil
	},
}

func init() {
	countDocsCmd.Flags().StringVarP(&coDoctype, "doctype", "d", "", "Frappe DocType (required)")
	countDocsCmd.Flags().StringVar(&coFilters, "filters", "", `Filter expression as JSON: '{"status":"Open"}' or '[["status","=","Open"]]'`)
	_ = countDocsCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(countDocsCmd)
}
