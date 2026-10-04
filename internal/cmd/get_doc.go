package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// get-doc flags
var (
	gdDoctype string
	gdName    string
	gdFields  string
	gdKeys    string
)

var getDocCmd = &cobra.Command{
	Use:   "get-doc",
	Short: "Get a single document by name",
	Long: `Retrieve a single document from a Frappe DocType by its name.
The output is displayed as a Field/Value table by default.

For Single DocTypes (e.g. "System Settings", "HR Settings"), --name can be
omitted — the DocType name is used as the document name automatically.

Examples:
  ffc get-doc --doctype "Company" --name "My Company"
  ffc get-doc -d "User" -n "jane@example.com" --fields '["name","email","enabled"]'
  ffc get-doc -d "ToDo" -n "TDP-2024-001" --json
  ffc get-doc -d "Sales Invoice" -n "SINV-0001" --json --keys name,status,grand_total
  ffc get-doc -d "System Settings" --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var fields []string
		if gdFields != "" {
			var err error
			if fields, err = parseFields(gdFields); err != nil {
				return usageErrorf("--fields: %w", err)
			}
		}
		name := docNameOrSingle(gdName, gdDoctype)

		doc, err := callSite(cmd, fmt.Sprintf("Fetching %s %s…", gdDoctype, name), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return c.GetDoc(ctx, gdDoctype, name)
		})
		if err != nil {
			return err
		}

		if jsonOutput {
			// --keys takes priority; otherwise --fields also narrows JSON output.
			switch {
			case gdKeys != "":
				doc = selectKeys(doc, gdKeys)
			case len(fields) > 0:
				doc, _ = filterKeys(doc, fields)
			}
			return output.PrintJSON(doc)
		}
		output.PrintDocTable(doc, fields)
		return nil
	},
}

func init() {
	getDocCmd.Flags().StringVarP(&gdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	getDocCmd.Flags().StringVarP(&gdName, "name", "n", "", "Name of the document (defaults to the DocType name for Single DocTypes)")
	getDocCmd.Flags().StringVarP(&gdFields, "fields", "f", "", `Fields to display / include in output (JSON array or CSV)`)
	getDocCmd.Flags().StringVar(&gdKeys, "keys", "", "Comma-separated keys to include in JSON output, e.g. name,status,grand_total")

	_ = getDocCmd.MarkFlagRequired("doctype")

	rootCmd.AddCommand(getDocCmd)
}
