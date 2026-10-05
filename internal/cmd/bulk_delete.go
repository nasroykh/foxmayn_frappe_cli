package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// bulk-delete flags
var (
	bdDoctype string
	bdNames   string
	bdFile    string
	bdFilters string
	bdYes     bool
	bdBulk    bulkFlags
)

var bulkDeleteCmd = &cobra.Command{
	Use:   "bulk-delete",
	Short: "Delete multiple Frappe documents",
	Long: `Permanently delete multiple documents from a Frappe DocType.

Provide document names as a comma-separated list with --names, or as a JSON
array of names (strings or numbers) with --file (- for stdin). Use --file for
names that contain commas. Or select them with --filters (same syntax as
list-docs): the matching names are listed first, shown, and then deleted.

You will be prompted to confirm unless --yes is provided; declining exits
non-zero. Processing continues when individual deletes fail unless
--fail-fast is set, and the command exits non-zero if any item failed.

Examples:
  ffc bulk-delete -d "ToDo" --names "TD-0001,TD-0002,TD-0003"
  ffc bulk-delete -d "Note" --file names.json --yes
  ffc bulk-delete -d "Customer" --names "CUST-001,CUST-002" --yes --json
  ffc bulk-delete -d "ToDo" --filters '{"status":"Cancelled"}' --dry-run
  ffc bulk-delete -d "ToDo" --filters '[["modified","<","2025-01-01"]]' --yes
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if bdFilters != "" {
			return bulkDeleteFiltered(cmd)
		}
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
			return usageErrorf("provide --names, --file or --filters")
		}
		if err := confirmDelete(cmd, names); err != nil {
			return err
		}
		rep, err := bdBulk.run(cmd, fmt.Sprintf("Deleting %d %s documents…", len(names), bdDoctype), len(names), "deleted", deleteOp(names))
		if err != nil {
			return err
		}
		return printBulkReport(rep, bdDoctype)
	},
}

// bulkDeleteFiltered deletes the documents matching --filters, with one
// client for the list and the deletes.
func bulkDeleteFiltered(cmd *cobra.Command) error {
	filters, err := filtersFlag(bdFilters)
	if err != nil {
		return err
	}
	if err := bdBulk.check(); err != nil {
		return err
	}
	c, err := newClient(cmd.Context())
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	names, err := filterNames(cmd, c, bdDoctype, filters)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintf(os.Stderr, "No %s documents match the filters; nothing to delete.\n", bdDoctype)
		return printBulkReport(bulkReport{Done: "deleted"}, bdDoctype)
	}
	if err := confirmDelete(cmd, names); err != nil {
		return err
	}
	rep, err := bdBulk.runOn(cmd, c, fmt.Sprintf("Deleting %d %s documents…", len(names), bdDoctype), len(names), "deleted", deleteOp(names))
	if err != nil {
		return err
	}
	return printBulkReport(rep, bdDoctype)
}

// confirmDelete shows what will be deleted and asks, unless --yes or a dry
// run.
func confirmDelete(cmd *cobra.Command, names []string) error {
	if bdYes || dryRunOn(cmd) {
		return nil
	}
	return confirm(fmt.Sprintf("Delete these %d %s document(s)?\n  %s\nThis cannot be undone.",
		len(names), text.Sanitize(bdDoctype), namePreview(names)))
}

func deleteOp(names []string) func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
	return func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
		if client.IsDryRun(ctx) {
			// Like delete-doc: the plan only lists documents that exist.
			if _, err := c.GetDoc(ctx, bdDoctype, names[i]); err != nil {
				return names[i], err
			}
		}
		return names[i], c.DeleteDoc(ctx, bdDoctype, names[i])
	}
}

func init() {
	bulkDeleteCmd.Flags().StringVarP(&bdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkDeleteCmd.Flags().StringVar(&bdNames, "names", "", "Comma-separated list of document names to delete")
	bulkDeleteCmd.Flags().StringVar(&bdFile, "file", "", "Path to a JSON file containing an array of names (- for stdin)")
	bulkDeleteCmd.Flags().StringVar(&bdFilters, "filters", "", `Delete the documents matching these filters, e.g. '{"status":"Cancelled"}' (@FILE, @- for stdin)`)
	bulkDeleteCmd.Flags().BoolVarP(&bdYes, "yes", "y", false, "Skip confirmation prompt")
	bdBulk.register(bulkDeleteCmd)

	_ = bulkDeleteCmd.MarkFlagRequired("doctype")
	bulkDeleteCmd.MarkFlagsMutuallyExclusive("names", "file", "filters")

	addDryRun(bulkDeleteCmd, false)
	rootCmd.AddCommand(bulkDeleteCmd)
}
