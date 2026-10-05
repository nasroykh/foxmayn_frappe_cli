package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// discard-doc flags
var (
	dcDoctype string
	dcName    string
	dcYes     bool
)

var discardDocCmd = &cobra.Command{
	Use:   "discard-doc",
	Short: "Discard a draft document (Frappe v16+)",
	Long: `Discard a draft (docstatus 0 → 2) with frappe.desk.form.save.discard: the
draft is kept as cancelled instead of being deleted. Needs Frappe v16.

You are asked to confirm unless --yes is given.

Examples:
  ffc discard-doc -d "Sales Invoice" -n ACC-SINV-2026-00007 --yes
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !dcYes {
			if err := confirm(fmt.Sprintf("Discard the draft %s %q?", dcDoctype, dcName)); err != nil {
				return err
			}
		}
		_, err := callSite(cmd, fmt.Sprintf("Discarding %s %s…", dcDoctype, dcName), func(ctx context.Context, c *client.FrappeClient) (struct{}, error) {
			return struct{}{}, c.DiscardDoc(ctx, dcDoctype, dcName)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(map[string]interface{}{"discarded": true, "doctype": dcDoctype, "name": dcName})
		}
		output.PrintSuccess(fmt.Sprintf("Discarded %s %s", dcDoctype, dcName))
		return nil
	},
}

func init() {
	discardDocCmd.Flags().StringVarP(&dcDoctype, "doctype", "d", "", "Frappe DocType (required)")
	discardDocCmd.Flags().StringVarP(&dcName, "name", "n", "", "Name of the draft (required)")
	discardDocCmd.Flags().BoolVarP(&dcYes, "yes", "y", false, "Skip confirmation prompt")
	_ = discardDocCmd.MarkFlagRequired("doctype")
	_ = discardDocCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(discardDocCmd)
}
