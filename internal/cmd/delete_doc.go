package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// delete-doc flags
var (
	ddDoctype string
	ddName    string
	ddYes     bool
)

var deleteDocCmd = &cobra.Command{
	Use:   "delete-doc",
	Short: "Delete a Frappe document",
	Long: `Permanently delete a document from a Frappe DocType.

You will be prompted to confirm deletion unless --yes is provided. Declining
the prompt exits with a non-zero code.

Examples:
  ffc delete-doc --doctype "ToDo" --name "TD-0001"
  ffc delete-doc -d "Note" -n "Old Note" --yes
  ffc delete-doc -d "Note" -n "Old Note" --yes --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !ddYes && !dryRunOn(cmd) {
			if err := confirm(fmt.Sprintf("Delete %s %q? This cannot be undone.", ddDoctype, ddName)); err != nil {
				return err
			}
		}

		_, err := callSite(cmd, fmt.Sprintf("Deleting %s %s…", ddDoctype, ddName), func(ctx context.Context, c *client.FrappeClient) (struct{}, error) {
			if client.IsDryRun(ctx) {
				// A dry run still proves the document is there to delete.
				if _, err := c.GetDoc(ctx, ddDoctype, ddName); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, c.DeleteDoc(ctx, ddDoctype, ddName)
		})
		if err != nil {
			return err
		}

		if machineOutput() {
			return printResult(map[string]interface{}{"deleted": true, "doctype": ddDoctype, "name": ddName})
		}
		output.PrintSuccess(fmt.Sprintf("Deleted %s %s", ddDoctype, ddName))
		return nil
	},
}

func init() {
	deleteDocCmd.Flags().StringVarP(&ddDoctype, "doctype", "d", "", "Frappe DocType (required)")
	deleteDocCmd.Flags().StringVarP(&ddName, "name", "n", "", "Name of the document (required)")
	deleteDocCmd.Flags().BoolVarP(&ddYes, "yes", "y", false, "Skip confirmation prompt")

	_ = deleteDocCmd.MarkFlagRequired("doctype")
	_ = deleteDocCmd.MarkFlagRequired("name")

	addDryRun(deleteDocCmd, false)
	rootCmd.AddCommand(deleteDocCmd)
}
