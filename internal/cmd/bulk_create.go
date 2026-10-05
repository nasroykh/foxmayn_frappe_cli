package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"

	"github.com/spf13/cobra"
)

// bulk-create flags
var (
	bcDoctype string
	bcData    string
	bcFile    string
	bcBulk    bulkFlags
)

var bulkCreateCmd = &cobra.Command{
	Use:   "bulk-create",
	Short: "Create multiple Frappe documents from a JSON array",
	Long: `Create multiple documents in a Frappe DocType in one command.

Provide records inline with --data, from a JSON file with --file, or from
stdin with --file -. Each element of the array must be a JSON object of field
values; the whole input is validated before anything is sent.

Processing continues when individual items fail unless --fail-fast is set. A
per-item summary is printed at the end and the command exits non-zero if any
item failed. Items cut off by Ctrl+C are reported as "interrupted": the server
may or may not have created them, so check before re-running.

Examples:
  ffc bulk-create -d "ToDo" --data '[{"description":"Task 1"},{"description":"Task 2"}]'
  ffc bulk-create -d "Note" --file notes.json --concurrency 4
  cat customers.json | ffc bulk-create -d "Customer" --file - --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := readInput(bcData, bcFile)
		if err != nil {
			return err
		}
		items, err := parseObjects(raw)
		if err != nil {
			return err
		}
		rep, err := bcBulk.run(cmd, fmt.Sprintf("Creating %d %s documents…", len(items), bcDoctype), len(items), "created",
			func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
				doc, err := c.CreateDoc(ctx, bcDoctype, items[i])
				if err != nil {
					return "", err
				}
				name, _ := docName(doc["name"])
				return name, nil
			})
		if err != nil {
			return err
		}
		return printBulkReport(rep, bcDoctype)
	},
}

func init() {
	bulkCreateCmd.Flags().StringVarP(&bcDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkCreateCmd.Flags().StringVar(&bcData, "data", "", "JSON array of field-value objects to create")
	bulkCreateCmd.Flags().StringVar(&bcFile, "file", "", "Path to a JSON file containing an array of objects (- for stdin)")
	bcBulk.register(bulkCreateCmd)

	_ = bulkCreateCmd.MarkFlagRequired("doctype")
	bulkCreateCmd.MarkFlagsMutuallyExclusive("data", "file")

	addDryRun(bulkCreateCmd, false)
	rootCmd.AddCommand(bulkCreateCmd)
}
