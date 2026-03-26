package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
	"github.com/spf13/cobra"
)

// update-doc flags
var (
	udDoctype string
	udName    string
	udData    string
	udKeys    string
)

var updateDocCmd = &cobra.Command{
	Use:   "update-doc",
	Short: "Update an existing Frappe document",
	Long: `Update one or more fields on an existing Frappe document.

The --data flag accepts a JSON object containing only the fields you want to change.

Examples:
  ffc update-doc -d "ToDo" -n "TD-0001" --data '{"status":"Closed"}'
  ffc update-doc -d "Note" -n "My Note" --data '{"title":"Updated Title"}' --json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(udData), &data); err != nil {
			return fmt.Errorf("--data: invalid JSON object: %w", err)
		}

		var doc map[string]interface{}
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Updating %s %s…", udDoctype, udName)).
			Action(func() {
				doc, apiErr = c.UpdateDoc(udDoctype, udName, data)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			result := map[string]interface{}(doc)
			if udKeys != "" {
				result = filterSchemaKeys(result, strings.Split(udKeys, ","))
			}
			output.PrintJSON(result)
		} else {
			output.PrintSuccess(fmt.Sprintf("Updated %s %s", udDoctype, udName))
			output.PrintDocTable(doc, nil)
		}

		return nil
	},
}

func init() {
	updateDocCmd.Flags().StringVarP(&udDoctype, "doctype", "d", "", "Frappe DocType (required)")
	updateDocCmd.Flags().StringVarP(&udName, "name", "n", "", "Name of the document (required)")
	updateDocCmd.Flags().StringVar(&udData, "data", "", `JSON object of fields to update, e.g. '{"status":"Closed"}' (required)`)
	updateDocCmd.Flags().StringVar(&udKeys, "keys", "", "Comma-separated keys to include in JSON output, e.g. name,status")

	_ = updateDocCmd.MarkFlagRequired("doctype")
	_ = updateDocCmd.MarkFlagRequired("name")
	_ = updateDocCmd.MarkFlagRequired("data")

	rootCmd.AddCommand(updateDocCmd)
}
