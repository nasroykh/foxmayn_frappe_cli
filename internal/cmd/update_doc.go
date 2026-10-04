package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

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

The --data flag accepts a JSON object containing only the fields you want to
change. A "name" key in --data is ignored: the document is chosen by --name
(renaming is not done through update).

For Single DocTypes (e.g. "System Settings", "HR Settings"), --name can be
omitted — the DocType name is used as the document name automatically.

Examples:
  ffc update-doc -d "ToDo" -n "TD-0001" --data '{"status":"Closed"}'
  ffc update-doc -d "Note" -n "My Note" --data '{"title":"Updated Title"}' --json
  ffc update-doc -d "System Settings" --data '{"default_currency":"USD"}' --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := parseObject("--data", udData)
		if err != nil {
			return err
		}
		if _, ok := data["name"]; ok {
			fmt.Fprintln(os.Stderr, `warning: ignoring "name" in --data; use --name to choose the document`)
			data = withoutName(data)
		}
		name := docNameOrSingle(udName, udDoctype)

		doc, err := callSite(cmd, fmt.Sprintf("Updating %s %s…", udDoctype, name), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return c.UpdateDoc(ctx, udDoctype, name, data)
		})
		if err != nil {
			return err
		}

		if machineOutput() {
			return printResult(selectKeys(doc, udKeys))
		}
		output.PrintSuccess(fmt.Sprintf("Updated %s %s", udDoctype, name))
		output.PrintDocTable(doc, nil)
		return nil
	},
}

// docNameOrSingle returns name, or the DocType name for Single DocTypes
// (whose only document is named after the DocType).
func docNameOrSingle(name, doctype string) string {
	if name == "" {
		return doctype
	}
	return name
}

func init() {
	updateDocCmd.Flags().StringVarP(&udDoctype, "doctype", "d", "", "Frappe DocType (required)")
	updateDocCmd.Flags().StringVarP(&udName, "name", "n", "", "Name of the document. Defaults to DocType name for Single DocTypes.")
	updateDocCmd.Flags().StringVar(&udData, "data", "", `JSON object of fields to update, e.g. '{"status":"Closed"}' (required)`)
	updateDocCmd.Flags().StringVar(&udKeys, "keys", "", "Comma-separated keys to include in JSON output, e.g. name,status")

	_ = updateDocCmd.MarkFlagRequired("doctype")
	_ = updateDocCmd.MarkFlagRequired("data")

	rootCmd.AddCommand(updateDocCmd)
}
