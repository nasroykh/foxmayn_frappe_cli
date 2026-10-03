package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

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
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateFiltersJSON(coFilters); err != nil {
			return err
		}
		count, err := callSite(cmd, fmt.Sprintf("Counting %s…", coDoctype), func(ctx context.Context, c *client.FrappeClient) (int, error) {
			return c.GetCount(ctx, coDoctype, coFilters)
		})
		if err != nil {
			return err
		}

		if jsonOutput {
			return output.PrintJSON(map[string]interface{}{"doctype": coDoctype, "count": count})
		}
		fmt.Println(count)
		return nil
	},
}

func init() {
	countDocsCmd.Flags().StringVarP(&coDoctype, "doctype", "d", "", "Frappe DocType (required)")
	countDocsCmd.Flags().StringVar(&coFilters, "filters", "", `Filter expression as JSON: '{"status":"Open"}' or '[["status","=","Open"]]'`)
	_ = countDocsCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(countDocsCmd)
}
