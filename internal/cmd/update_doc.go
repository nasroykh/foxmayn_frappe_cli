package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// update-doc flags
var (
	udDoctype string
	udName    string
	udData    string
	udKeys    string

	udDiff         bool
	udIfUnmodified string
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

--diff reads the document first and, once the update is saved, prints on
stderr each field it changed with its old and new value (add --dry-run to
only look). The update then carries the "modified" it read, so if anyone
saves the document in between nothing is saved (exit 6) and the diff is
never stale. --if-unmodified sends the document's "modified"
timestamp as you read it (get-doc --keys modified): if anyone saved the
document since, Frappe refuses the update and nothing is saved (exit 6).
To edit a document interactively, see 'ffc edit-doc'.

Examples:
  ffc update-doc -d "ToDo" -n "TD-0001" --data '{"status":"Closed"}'
  ffc update-doc -d "Note" -n "My Note" --data '{"title":"Updated Title"}' --json
  ffc update-doc -d "System Settings" --data '{"default_currency":"USD"}' --json
  ffc update-doc -d "ToDo" -n "TD-0001" --data '{"status":"Closed"}' --diff
  ffc update-doc -d "ToDo" -n "TD-0001" --data '{"status":"Closed"}' --if-unmodified "2026-10-05 16:41:58.083711"
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
		// The diff is about the fields, not the timestamp sent with them.
		shown := map[string]interface{}{}
		for k, v := range data {
			if k != "modified" {
				shown[k] = v
			}
		}
		if udIfUnmodified != "" {
			data["modified"] = udIfUnmodified
		}

		var changes map[string]interface{}
		pinned := false // modified set from the document read for --diff
		doc, err := callSite(cmd, fmt.Sprintf("Updating %s %s…", udDoctype, name), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			if udDiff || client.IsDryRun(ctx) {
				// Show what the update would change, from the current document.
				current, err := c.GetDoc(ctx, udDoctype, name)
				if err != nil {
					return nil, err
				}
				changes = fieldChanges(current, shown)
				// The diff shown must be the change made: send the modified
				// it was computed from, so a save in between fails.
				if _, set := data["modified"]; udDiff && !set {
					data["modified"], pinned = current["modified"], true
				}
			}
			d, err := c.UpdateDoc(ctx, udDoctype, name, data)
			var plan *client.DryRunError
			if errors.As(err, &plan) {
				plan.Requests[0].Changes = changes
			}
			return d, err
		})
		if err != nil {
			since := "the modified timestamp given"
			switch {
			case udIfUnmodified != "":
				since = fmt.Sprintf("--if-unmodified %q", udIfUnmodified)
			case pinned:
				since = "it was read for --diff"
			}
			return conflictError(err, udDoctype, name, since)
		}
		if udDiff {
			printFieldChanges(udDoctype, name, changes)
		}

		if machineOutput() {
			return printResult(selectKeys(doc, udKeys))
		}
		output.PrintSuccess(fmt.Sprintf("Updated %s %s", udDoctype, name))
		output.PrintDocTable(doc, nil)
		return nil
	},
}

// printFieldChanges shows update-doc --diff's changes on stderr.
func printFieldChanges(doctype, name string, changes map[string]interface{}) {
	if len(changes) == 0 {
		fmt.Fprintf(os.Stderr, "No changes to %s %s: it already has these values.\n", text.Sanitize(doctype), text.Sanitize(name))
		return
	}
	fmt.Fprintf(os.Stderr, "Changes to %s %s:\n", text.Sanitize(doctype), text.Sanitize(name))
	for _, k := range mapKeys(changes) {
		ch, _ := changes[k].(map[string]interface{})
		fmt.Fprintf(os.Stderr, "  %s: %s → %s\n", text.Sanitize(k), diffValue(ch["from"]), diffValue(ch["to"]))
	}
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
	updateDocCmd.Flags().BoolVar(&udDiff, "diff", false, "Print the changed fields (old → new) on stderr; fails (exit 6) if the document changes in between")
	updateDocCmd.Flags().StringVar(&udIfUnmodified, "if-unmodified", "", `Fail (exit 6) if the document's "modified" is no longer this value`)

	_ = updateDocCmd.MarkFlagRequired("doctype")
	_ = updateDocCmd.MarkFlagRequired("data")

	addDryRun(updateDocCmd, false)
	rootCmd.AddCommand(updateDocCmd)
}
