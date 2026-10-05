package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// bulk-update flags
var (
	buDoctype string
	buData    string
	buFile    string
	buFilters string
	buSet     string
	buYes     bool
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

Or select the documents with --filters (same syntax as list-docs) and give
the change for all of them with --set: the matching names are listed first,
shown, and then updated. That form asks for confirmation unless --yes.

Processing continues when individual items fail unless --fail-fast is set. A
per-item summary is printed at the end and the command exits non-zero if any
item failed.

Examples:
  ffc bulk-update -d "ToDo" --data '[{"name":"TD-0001","status":"Closed"},{"name":"TD-0002","priority":"High"}]'
  ffc bulk-update -d "Customer" --file updates.json --fail-fast
  ffc bulk-update -d "Item" --file items.json --concurrency 4 --json
  ffc bulk-update -d "ToDo" --filters '{"status":"Open","owner":"a@example.com"}' --set '{"status":"Closed"}' --yes
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if buFilters != "" || buSet != "" {
			return bulkUpdateFiltered(cmd)
		}
		if buYes {
			return usageErrorf("--yes applies to --filters only")
		}
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

// bulkUpdateFiltered applies --set to every document matching --filters,
// with one client for the list and the updates.
func bulkUpdateFiltered(cmd *cobra.Command) error {
	if buFilters == "" || buSet == "" {
		return usageErrorf("--filters and --set go together")
	}
	filters, err := filtersFlag(buFilters)
	if err != nil {
		return err
	}
	patch, err := parseObject("--set", buSet)
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return usageErrorf("--set: give at least one field to change")
	}
	if _, ok := patch["name"]; ok {
		return usageErrorf("--set cannot change name; use rename-doc")
	}
	if err := buBulk.check(); err != nil {
		return err
	}
	c, err := newClient(cmd.Context())
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	names, err := filterNames(cmd, c, buDoctype, filters)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintf(os.Stderr, "No %s documents match the filters; nothing to update.\n", buDoctype)
		return printBulkReport(bulkReport{Done: "updated"}, buDoctype)
	}
	if !buYes && !dryRunOn(cmd) {
		fields := make([]string, 0, len(patch))
		for k := range patch {
			fields = append(fields, text.Sanitize(k))
		}
		sort.Strings(fields)
		prompt := fmt.Sprintf("Set %s on these %d %s document(s)?\n  %s",
			strings.Join(fields, ", "), len(names), text.Sanitize(buDoctype), namePreview(names))
		if err := confirm(prompt); err != nil {
			return err
		}
	}
	rep, err := buBulk.runOn(cmd, c, fmt.Sprintf("Updating %d %s documents…", len(names), buDoctype), len(names), "updated",
		func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
			_, err := c.UpdateDoc(ctx, buDoctype, names[i], patch)
			return names[i], err
		})
	if err != nil {
		return err
	}
	return printBulkReport(rep, buDoctype)
}

func init() {
	bulkUpdateCmd.Flags().StringVarP(&buDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkUpdateCmd.Flags().StringVar(&buData, "data", "", `JSON array of objects; each must include "name" plus fields to update`)
	bulkUpdateCmd.Flags().StringVar(&buFile, "file", "", "Path to a JSON file containing an array of objects (- for stdin)")
	bulkUpdateCmd.Flags().StringVar(&buFilters, "filters", "", `Update the documents matching these filters (with --set; @FILE, @- for stdin)`)
	bulkUpdateCmd.Flags().StringVar(&buSet, "set", "", `JSON object of fields to set on every matching document (with --filters; @FILE, @- for stdin)`)
	bulkUpdateCmd.Flags().BoolVarP(&buYes, "yes", "y", false, "Skip the confirmation prompt of --filters")
	buBulk.register(bulkUpdateCmd)

	_ = bulkUpdateCmd.MarkFlagRequired("doctype")
	bulkUpdateCmd.MarkFlagsMutuallyExclusive("data", "file", "filters")
	bulkUpdateCmd.MarkFlagsMutuallyExclusive("data", "file", "set")

	addDryRun(bulkUpdateCmd, false)
	rootCmd.AddCommand(bulkUpdateCmd)
}
