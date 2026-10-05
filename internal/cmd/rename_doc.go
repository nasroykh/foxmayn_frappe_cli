package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// rename-doc flags
var (
	rnDoctype string
	rnName    string
	rnTo      string
	rnMerge   bool
	rnYes     bool
)

var renameDocCmd = &cobra.Command{
	Use:   "rename-doc",
	Short: "Rename a document",
	Long: `Rename a document with frappe.client.rename_doc; links to it are updated.
The DocType must allow renaming.

--merge merges the document into an existing one named --to: the source
disappears and its links point to the target. It cannot be undone, so you
are asked to confirm unless --yes is given.

Examples:
  ffc rename-doc -d Customer -n "Acme Ltd" --to "Acme Limited"
  ffc rename-doc -d Customer -n "Acme Ltd" --to "Acme Limited" --merge --yes
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if rnMerge && !rnYes {
			if err := confirm(fmt.Sprintf("Merge %s %q into %q? This cannot be undone.", rnDoctype, rnName, rnTo)); err != nil {
				return err
			}
		}
		newName, err := callSite(cmd, fmt.Sprintf("Renaming %s %s…", rnDoctype, rnName), func(ctx context.Context, c *client.FrappeClient) (string, error) {
			return c.RenameDoc(ctx, rnDoctype, rnName, rnTo, rnMerge)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(map[string]interface{}{"doctype": rnDoctype, "old_name": rnName, "name": newName, "merged": rnMerge})
		}
		verb := "Renamed"
		if rnMerge {
			verb = "Merged"
		}
		output.PrintSuccess(fmt.Sprintf("%s %s %s → %s", verb, rnDoctype, rnName, newName))
		return nil
	},
}

func init() {
	renameDocCmd.Flags().StringVarP(&rnDoctype, "doctype", "d", "", "Frappe DocType (required)")
	renameDocCmd.Flags().StringVarP(&rnName, "name", "n", "", "Current name of the document (required)")
	renameDocCmd.Flags().StringVar(&rnTo, "to", "", "New name, or the existing document to merge into (required)")
	renameDocCmd.Flags().BoolVar(&rnMerge, "merge", false, "Merge into the existing document named --to")
	renameDocCmd.Flags().BoolVarP(&rnYes, "yes", "y", false, "Skip the confirmation prompt of --merge")
	for _, f := range []string{"doctype", "name", "to"} {
		_ = renameDocCmd.MarkFlagRequired(f)
	}
	rootCmd.AddCommand(renameDocCmd)
}
