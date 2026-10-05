package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// submit-doc flags
var (
	sdDoctype string
	sdName    string
	sdKeys    string
)

var submitDocCmd = &cobra.Command{
	Use:   "submit-doc",
	Short: "Submit a draft document",
	Long: `Submit a draft document of a submittable DocType (docstatus 0 → 1) with
frappe.client.submit, so its validations and hooks run (GL entries, stock
ledger, ...).

The document is read first and sent back as read: if someone changes it in
between, Frappe rejects the submit (exit code 6). A DocType with an active
Workflow is refused; move it with 'ffc workflow apply'.

Examples:
  ffc submit-doc -d "Sales Invoice" -n ACC-SINV-2026-00001
  ffc submit-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --json --keys name,docstatus
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		doc, err := callSite(cmd, fmt.Sprintf("Submitting %s %s…", sdDoctype, sdName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			if err := refuseWorkflow(ctx, c, sdDoctype, sdName); err != nil {
				return nil, err
			}
			return c.SubmitDoc(ctx, sdDoctype, sdName)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(selectKeys(doc, sdKeys))
		}
		output.PrintSuccess(fmt.Sprintf("Submitted %s %s", sdDoctype, sdName))
		return nil
	},
}

// refuseWorkflow stops a submit or cancel of a DocType that has an active
// Workflow: those documents move through workflow actions. When the user
// may not read Workflows, the server's own checks decide.
func refuseWorkflow(ctx context.Context, c *client.FrappeClient, doctype, name string) error {
	wf, _, err := c.ActiveWorkflow(ctx, doctype)
	if err != nil {
		return err
	}
	if wf != "" {
		return &client.StateError{Message: fmt.Sprintf("%s uses the workflow %q: list its actions with 'ffc workflow transitions -d %q -n %q' and apply one with 'ffc workflow apply'", doctype, wf, doctype, name)}
	}
	return nil
}

func init() {
	submitDocCmd.Flags().StringVarP(&sdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	submitDocCmd.Flags().StringVarP(&sdName, "name", "n", "", "Name of the document (required)")
	submitDocCmd.Flags().StringVar(&sdKeys, "keys", "", "Comma-separated keys to include in data output, e.g. name,docstatus")
	_ = submitDocCmd.MarkFlagRequired("doctype")
	_ = submitDocCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(submitDocCmd)
}
