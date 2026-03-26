package cmd

import (
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
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

Examples:
  ffc get-doc --doctype "Company" --name "My Company"
  ffc get-doc -d "User" -n "jane@example.com" --fields '["name","email","enabled"]'
  ffc get-doc -d "ToDo" -n "TDP-2024-001" --json
  ffc get-doc -d "Sales Invoice" -n "SINV-0001" --json --keys name,status,grand_total
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load site config
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		// Parse --fields flag
		var fields []string
		if gdFields != "" {
			fields, err = parseFields(gdFields)
			if err != nil {
				return fmt.Errorf("--fields: %w", err)
			}
		}

		// Call the API with a spinner
		var doc map[string]interface{}
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Fetching %s %s…", gdDoctype, gdName)).
			Action(func() {
				doc, apiErr = c.GetDoc(gdDoctype, gdName)
			}).
			Run()

		if apiErr != nil {
			return apiErr
		}

		// Output
		if jsonOutput {
			result := map[string]interface{}(doc)
			if gdKeys != "" {
				result = filterSchemaKeys(result, strings.Split(gdKeys, ","))
			}
			output.PrintJSON(result)
		} else {
			output.PrintDocTable(doc, fields)
		}

		return nil
	},
}

func init() {
	getDocCmd.Flags().StringVarP(&gdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	getDocCmd.Flags().StringVarP(&gdName, "name", "n", "", "Name of the document (required)")
	getDocCmd.Flags().StringVarP(&gdFields, "fields", "f", "", `Fields to fetch (JSON array or CSV)`)
	getDocCmd.Flags().StringVar(&gdKeys, "keys", "", "Comma-separated keys to include in JSON output, e.g. name,status,grand_total")

	_ = getDocCmd.MarkFlagRequired("doctype")
	_ = getDocCmd.MarkFlagRequired("name")

	rootCmd.AddCommand(getDocCmd)
}
