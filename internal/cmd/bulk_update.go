package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"

	"github.com/spf13/cobra"
)

// bulk-update flags
var (
	buDoctype string
	buData    string
	buFile    string
	buBulk    bulkFlags
)

var bulkUpdateCmd = &cobra.Command{
	Use:   "bulk-update",
	Short: "Update multiple Frappe documents from a JSON array",
	Long: `Update multiple documents in a Frappe DocType in one command.

Provide records inline with --data, from a JSON file with --file, or from
stdin with --file -. Each element must be an object with a "name" field
identifying the document plus the fields to change; the whole input is
validated before anything is sent.

Processing continues when individual items fail unless --fail-fast is set. A
per-item summary is printed at the end and the command exits non-zero if any
item failed.

Examples:
  ffc bulk-update -d "ToDo" --data '[{"name":"TD-0001","status":"Closed"},{"name":"TD-0002","priority":"High"}]'
  ffc bulk-update -d "Customer" --file updates.json --fail-fast
  ffc bulk-update -d "Item" --file items.json --concurrency 4 --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := readInput(buData, buFile)
		if err != nil {
			return err
		}
		items, err := parseObjects(raw)
		if err != nil {
			return err
		}
		names, payloads, err := splitUpdates(items)
		if err != nil {
			return err
		}
		rep, err := buBulk.run(cmd, fmt.Sprintf("Updating %d %s documents…", len(items), buDoctype), len(items), "updated",
			func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
				_, err := c.UpdateDoc(ctx, buDoctype, names[i], payloads[i])
				return names[i], err
			})
		if err != nil {
			return err
		}
		return printBulkReport(rep, buDoctype)
	},
}

func init() {
	bulkUpdateCmd.Flags().StringVarP(&buDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkUpdateCmd.Flags().StringVar(&buData, "data", "", `JSON array of objects; each must include "name" plus fields to update`)
	bulkUpdateCmd.Flags().StringVar(&buFile, "file", "", "Path to a JSON file containing an array of objects (- for stdin)")
	buBulk.register(bulkUpdateCmd)

	_ = bulkUpdateCmd.MarkFlagRequired("doctype")
	bulkUpdateCmd.MarkFlagsMutuallyExclusive("data", "file")

	rootCmd.AddCommand(bulkUpdateCmd)
}
