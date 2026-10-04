package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// cancel-doc flags
var (
	cnDoctype string
	cnName    string
	cnCheck   bool
	cnYes     bool
	cnKeys    string
)

var cancelDocCmd = &cobra.Command{
	Use:   "cancel-doc",
	Short: "Cancel a submitted document",
	Long: `Cancel a submitted document (docstatus 1 → 2) with frappe.client.cancel.
A cancelled document cannot be edited or submitted again; amend it with
'ffc amend-doc' to make a corrected copy.

Submitted documents that link to it block the cancel (LinkExistsError,
exit code 6). --check lists them without cancelling anything.

You are asked to confirm unless --yes is given. A DocType with an active
Workflow is refused; use 'ffc workflow apply'.

Examples:
  ffc cancel-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --check
  ffc cancel-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --yes
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cnCheck {
			return cancelCheck(cmd)
		}
		if !cnYes {
			if err := confirm(fmt.Sprintf("Cancel %s %q? A cancelled document cannot be submitted again.", cnDoctype, cnName)); err != nil {
				return err
			}
		}
		doc, err := callSite(cmd, fmt.Sprintf("Cancelling %s %s…", cnDoctype, cnName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			if err := refuseWorkflow(ctx, c, cnDoctype, cnName); err != nil {
				return nil, err
			}
			return c.CancelDoc(ctx, cnDoctype, cnName)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(selectKeys(doc, cnKeys))
		}
		output.PrintSuccess(fmt.Sprintf("Cancelled %s %s", cnDoctype, cnName))
		return nil
	},
}

// cancelCheck lists the submitted documents that would block the cancel.
func cancelCheck(cmd *cobra.Command) error {
	linked, err := callSite(cmd, fmt.Sprintf("Checking links to %s %s…", cnDoctype, cnName), func(ctx context.Context, c *client.FrappeClient) ([]client.LinkedDoc, error) {
		return c.SubmittedLinkedDocs(ctx, cnDoctype, cnName)
	})
	if err != nil {
		return err
	}
	rows := make([]map[string]interface{}, len(linked))
	for i, l := range linked {
		rows[i] = map[string]interface{}{"doctype": l.DocType, "name": l.Name}
	}
	return render(rows, []string{"doctype", "name"}, func() error {
		if len(rows) == 0 {
			output.PrintSuccess(fmt.Sprintf("No submitted documents link to %s %s: it can be cancelled.", cnDoctype, cnName))
			return nil
		}
		fmt.Fprintf(os.Stderr, "%d submitted documents link to %s %s; cancel them first:\n", len(rows), cnDoctype, cnName)
		output.PrintTable(rows, []string{"doctype", "name"})
		return nil
	})
}

func init() {
	cancelDocCmd.Flags().StringVarP(&cnDoctype, "doctype", "d", "", "Frappe DocType (required)")
	cancelDocCmd.Flags().StringVarP(&cnName, "name", "n", "", "Name of the document (required)")
	cancelDocCmd.Flags().BoolVar(&cnCheck, "check", false, "List the submitted documents that block the cancel; cancel nothing")
	cancelDocCmd.Flags().BoolVarP(&cnYes, "yes", "y", false, "Skip confirmation prompt")
	cancelDocCmd.Flags().StringVar(&cnKeys, "keys", "", "Comma-separated keys to include in data output, e.g. name,docstatus")
	cancelDocCmd.MarkFlagsMutuallyExclusive("check", "yes")
	_ = cancelDocCmd.MarkFlagRequired("doctype")
	_ = cancelDocCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(cancelDocCmd)
}
