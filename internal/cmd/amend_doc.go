package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// amend-doc and copy-doc flags
var (
	amDoctype string
	amName    string
	amData    string
	amKeys    string

	cpDoctype string
	cpName    string
	cpData    string
	cpKeys    string
)

var amendDocCmd = &cobra.Command{
	Use:   "amend-doc",
	Short: "Create the amendment of a cancelled document",
	Long: `Create a new draft from a cancelled document, linked to it by amended_from,
the way the desk's Amend button does. Frappe names it <name>-1; amending that one again gives <name>-2. Fields
marked "no copy" are kept, as in the desk. --data overrides fields of the
copy (a JSON object, @FILE or @-).

Submit the amendment with 'ffc submit-doc' when it is ready.

Examples:
  ffc amend-doc -d "Sales Invoice" -n ACC-SINV-2026-00001
  ffc amend-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --data '{"due_date":"2026-11-30"}'
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		overrides, err := optionalObject("--data", amData)
		if err != nil {
			return err
		}
		doc, err := callSite(cmd, fmt.Sprintf("Amending %s %s…", amDoctype, amName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return c.AmendDoc(ctx, amDoctype, amName, overrides)
		})
		if err != nil {
			return err
		}
		return printNewDoc(doc, amKeys, fmt.Sprintf("Amended %s %s as", amDoctype, amName))
	},
}

var copyDocCmd = &cobra.Command{
	Use:   "copy-doc",
	Short: "Duplicate a document",
	Long: `Create a new document from an existing one, like the desk's Duplicate:
identity fields and fields marked "no copy" are left out, child rows are
copied. --data overrides fields of the copy (a JSON object, @FILE or @-).

Examples:
  ffc copy-doc -d "Item" -n "SKU-001" --data '{"item_code":"SKU-002","item_name":"Copy"}'
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		overrides, err := optionalObject("--data", cpData)
		if err != nil {
			return err
		}
		doc, err := callSite(cmd, fmt.Sprintf("Copying %s %s…", cpDoctype, cpName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return c.DuplicateDoc(ctx, cpDoctype, cpName, overrides)
		})
		if err != nil {
			return err
		}
		return printNewDoc(doc, cpKeys, fmt.Sprintf("Copied %s %s to", cpDoctype, cpName))
	},
}

// optionalObject parses an optional JSON object flag ("" → nil).
func optionalObject(flag, raw string) (map[string]interface{}, error) {
	if raw == "" {
		return nil, nil
	}
	return parseObject(flag, raw)
}

// printNewDoc prints a document a command created: the data, or a success
// line with its name and the document table.
func printNewDoc(doc map[string]interface{}, keys, what string) error {
	if machineOutput() {
		return printResult(selectKeys(doc, keys))
	}
	name, _ := docName(doc["name"])
	output.PrintSuccess(fmt.Sprintf("%s %s", what, name))
	output.PrintDocTable(doc, nil)
	return nil
}

func init() {
	for _, c := range []struct {
		cmd                 *cobra.Command
		doctype, name, data *string
		keys                *string
	}{
		{amendDocCmd, &amDoctype, &amName, &amData, &amKeys},
		{copyDocCmd, &cpDoctype, &cpName, &cpData, &cpKeys},
	} {
		c.cmd.Flags().StringVarP(c.doctype, "doctype", "d", "", "Frappe DocType (required)")
		c.cmd.Flags().StringVarP(c.name, "name", "n", "", "Name of the source document (required)")
		c.cmd.Flags().StringVar(c.data, "data", "", `JSON object of fields to override in the copy (or @FILE, @-)`)
		c.cmd.Flags().StringVar(c.keys, "keys", "", "Comma-separated keys to include in data output, e.g. name,amended_from")
		_ = c.cmd.MarkFlagRequired("doctype")
		_ = c.cmd.MarkFlagRequired("name")
		addDryRun(c.cmd, false)
		rootCmd.AddCommand(c.cmd)
	}
}
