package cmd

import (
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
	"github.com/spf13/cobra"
)

// get-schema flags
var gsDoctype string

var getSchemaCmd = &cobra.Command{
	Use:   "get-schema",
	Short: "Show the field schema of a DocType",
	Long: `Display all field definitions for a Frappe DocType.

Shows fieldname, label, type, whether it is required, and any linked options.

Examples:
  ffc get-schema -d "Sales Invoice"
  ffc get-schema -d "ToDo" --json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		var doc map[string]interface{}
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Fetching schema for %s…", gsDoctype)).
			Action(func() {
				doc, apiErr = c.GetDoc("DocType", gsDoctype)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			output.PrintJSON(doc)
			return nil
		}

		// Extract the fields slice and render a schema-specific table.
		rawFields, ok := doc["fields"].([]interface{})
		if !ok || len(rawFields) == 0 {
			output.PrintError("No fields found in schema.")
			return nil
		}

		rows := make([]map[string]interface{}, 0, len(rawFields))
		for _, rf := range rawFields {
			f, ok := rf.(map[string]interface{})
			if !ok {
				continue
			}
			reqd := ""
			if r, ok := f["reqd"]; ok {
				switch v := r.(type) {
				case float64:
					if v == 1 {
						reqd = "✓"
					}
				case bool:
					if v {
						reqd = "✓"
					}
				}
			}
			rows = append(rows, map[string]interface{}{
				"fieldname": f["fieldname"],
				"label":     f["label"],
				"fieldtype": f["fieldtype"],
				"required":  reqd,
				"options":   f["options"],
				"default":   f["default"],
			})
		}

		output.PrintTable(rows, []string{"fieldname", "label", "fieldtype", "required", "options", "default"})
		return nil
	},
}

func init() {
	getSchemaCmd.Flags().StringVarP(&gsDoctype, "doctype", "d", "", "DocType to inspect (required)")
	_ = getSchemaCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(getSchemaCmd)
}
