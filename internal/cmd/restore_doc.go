package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// restore-doc flags
var (
	rsDoctype string
	rsName    string
	rsDeleted string
)

var restoreDocCmd = &cobra.Command{
	Use:   "restore-doc",
	Short: "Restore a deleted document",
	Long: `Restore a deleted document from its "Deleted Document" record, the undo of
delete-doc. Needs the System Manager role.

Name the document (-d and -n: its latest unrestored deletion is used) or the
Deleted Document record (--deleted). A DocType named by hash or naming
series may give the restored document a new name; the command prints it.

Examples:
  ffc restore-doc -d ToDo -n TD-0001
  ffc restore-doc --deleted 4f2a1c9e7b
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if (rsDeleted == "") == (rsDoctype == "" || rsName == "") {
			return usageErrorf("name the document with -d and -n, or the Deleted Document with --deleted")
		}
		type restored struct{ deleted, name string }
		res, err := callSite(cmd, "Restoring…", func(ctx context.Context, c *client.FrappeClient) (restored, error) {
			id := rsDeleted
			if id == "" {
				var err error
				if id, err = findDeleted(ctx, c, rsDoctype, rsName); err != nil {
					return restored{}, err
				}
			}
			name, err := c.RestoreDeleted(ctx, id)
			return restored{id, name}, err
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(map[string]interface{}{"restored": true, "deleted_document": res.deleted, "name": res.name})
		}
		output.PrintSuccess(fmt.Sprintf("Restored %s from Deleted Document %s", res.name, res.deleted))
		return nil
	},
}

// findDeleted returns the latest unrestored Deleted Document of a document.
func findDeleted(ctx context.Context, c *client.FrappeClient, doctype, name string) (string, error) {
	filters, err := json.Marshal(map[string]interface{}{"deleted_doctype": doctype, "deleted_name": name, "restored": 0})
	if err != nil {
		return "", err
	}
	rows, err := c.GetList(ctx, "Deleted Document", client.ListOptions{Fields: []string{"name"}, Filters: string(filters), Limit: 1, OrderBy: "creation desc"})
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", &client.StateError{Message: fmt.Sprintf("no unrestored deletion of %s %s found", doctype, name)}
	}
	return fmt.Sprint(rows[0]["name"]), nil
}

func init() {
	restoreDocCmd.Flags().StringVarP(&rsDoctype, "doctype", "d", "", "DocType of the deleted document")
	restoreDocCmd.Flags().StringVarP(&rsName, "name", "n", "", "Name of the deleted document")
	restoreDocCmd.Flags().StringVar(&rsDeleted, "deleted", "", "Name of the Deleted Document record")
	restoreDocCmd.MarkFlagsMutuallyExclusive("deleted", "doctype")
	restoreDocCmd.MarkFlagsMutuallyExclusive("deleted", "name")
	addDryRun(restoreDocCmd, false)
	rootCmd.AddCommand(restoreDocCmd)
}
