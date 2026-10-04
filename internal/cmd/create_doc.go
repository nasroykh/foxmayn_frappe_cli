package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

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
  ffc create-doc -d "Note" --data '{"title":"Meeting","content":"Discussed Q1"}' --json
  ffc create-doc -d "ToDo" --data '{"description":"x"}' --json --keys name
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := parseObject("--data", cdData)
		if err != nil {
			return err
		}
		doc, err := callSite(cmd, fmt.Sprintf("Creating %s…", cdDoctype), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return c.CreateDoc(ctx, cdDoctype, data)
		})
		if err != nil {
			return err
		}

		if machineOutput() {
			return printResult(selectKeys(doc, cdKeys))
		}
		if name, ok := docName(doc["name"]); ok {
			output.PrintSuccess(fmt.Sprintf("Created %s %s", cdDoctype, name))
		}
		output.PrintDocTable(doc, nil)
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
