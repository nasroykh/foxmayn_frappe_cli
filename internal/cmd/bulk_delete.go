package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"

	"github.com/spf13/cobra"
)

// bulk-delete flags
var (
	bdDoctype string
	bdNames   string
	bdFile    string
	bdYes     bool
	bdBulk    bulkFlags
)

var bulkDeleteCmd = &cobra.Command{
	Use:   "bulk-delete",
	Short: "Delete multiple Frappe documents",
	Long: `Permanently delete multiple documents from a Frappe DocType.

Provide document names as a comma-separated list with --names, or as a JSON
array of names (strings or numbers) with --file (- for stdin). Use --file for
names that contain commas.

You will be prompted to confirm unless --yes is provided; declining exits
non-zero. Processing continues when individual deletes fail unless
--fail-fast is set, and the command exits non-zero if any item failed.

Examples:
  ffc bulk-delete -d "ToDo" --names "TD-0001,TD-0002,TD-0003"
  ffc bulk-delete -d "Note" --file names.json --yes
  ffc bulk-delete -d "Customer" --names "CUST-001,CUST-002" --yes --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var names []string
		switch {
		case bdFile != "":
			raw, err := readInput("", bdFile)
			if err != nil {
				return err
			}
			if names, err = parseNames(raw); err != nil {
				return err
			}
		case bdNames != "":
			names = splitCSV(bdNames)
		}
		if len(names) == 0 {
			return usageErrorf("provide --names or --file")
		}

		if !bdYes {
			preview := names
			if len(preview) > 10 {
				preview = append(append([]string{}, names[:10]...), fmt.Sprintf("… and %d more", len(names)-10))
			}
			prompt := fmt.Sprintf("Delete these %d %s document(s)?\n  %s\nThis cannot be undone.",
				len(names), bdDoctype, strings.Join(preview, ", "))
			if err := confirm(prompt); err != nil {
				return err
			}
		}

		rep, err := bdBulk.run(cmd, fmt.Sprintf("Deleting %d %s documents…", len(names), bdDoctype), len(names), "deleted",
			func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
				return names[i], c.DeleteDoc(ctx, bdDoctype, names[i])
			})
		if err != nil {
			return err
		}
		return printBulkReport(rep, bdDoctype)
	},
}

func init() {
	bulkDeleteCmd.Flags().StringVarP(&bdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkDeleteCmd.Flags().StringVar(&bdNames, "names", "", "Comma-separated list of document names to delete")
	bulkDeleteCmd.Flags().StringVar(&bdFile, "file", "", "Path to a JSON file containing an array of names (- for stdin)")
	bulkDeleteCmd.Flags().BoolVarP(&bdYes, "yes", "y", false, "Skip confirmation prompt")
	bdBulk.register(bulkDeleteCmd)

	_ = bulkDeleteCmd.MarkFlagRequired("doctype")
	bulkDeleteCmd.MarkFlagsMutuallyExclusive("names", "file")

	rootCmd.AddCommand(bulkDeleteCmd)
}
