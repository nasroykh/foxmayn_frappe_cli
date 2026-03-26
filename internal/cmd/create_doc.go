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

// create-doc flags
var (
	cdDoctype string
	cdData    string
	cdKeys    string
)

var createDocCmd = &cobra.Command{
	Use:   "create-doc",
	Short: "Create a new Frappe document",
	Long: `Create a new document in a Frappe DocType.

The --data flag accepts a JSON object of field values.

Examples:
  ffc create-doc --doctype "ToDo" --data '{"description":"Test","priority":"Medium"}'
  ffc create-doc -d "Note" --data '{"title":"Hello","content":"World"}' --json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(cdData), &data); err != nil {
			return fmt.Errorf("--data: invalid JSON object: %w", err)
		}

		var doc map[string]interface{}
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Creating %s…", cdDoctype)).
			Action(func() {
				doc, apiErr = c.CreateDoc(cdDoctype, data)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			result := map[string]interface{}(doc)
			if cdKeys != "" {
				result = filterSchemaKeys(result, strings.Split(cdKeys, ","))
			}
			output.PrintJSON(result)
		} else {
			if name, ok := doc["name"].(string); ok {
				output.PrintSuccess(fmt.Sprintf("Created %s %s", cdDoctype, name))
			}
			output.PrintDocTable(doc, nil)
		}

		return nil
	},
}

func init() {
	createDocCmd.Flags().StringVarP(&cdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	createDocCmd.Flags().StringVar(&cdData, "data", "", `JSON object of field values, e.g. '{"title":"Hello"}' (required)`)
	createDocCmd.Flags().StringVar(&cdKeys, "keys", "", "Comma-separated keys to include in JSON output, e.g. name,status")

	_ = createDocCmd.MarkFlagRequired("doctype")
	_ = createDocCmd.MarkFlagRequired("data")

	rootCmd.AddCommand(createDocCmd)
}
