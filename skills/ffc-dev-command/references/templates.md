# Command templates

Shapes taken from `create_doc.go`, `delete_doc.go` and `list_docs.go`. Replace `xx` with an unused two-letter prefix and check the real files for anything newer.

## Write command (with --dry-run and an optional confirmation)

```go
package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// do-thing flags
var (
	xxDoctype string
	xxName    string
	xxData    string
	xxKeys    string
	xxYes     bool
)

var doThingCmd = &cobra.Command{
	Use:   "do-thing",
	Short: "One line: what it does",
	Long: `What it does, which Frappe method it calls, what it needs (permissions,
Frappe version), what it prints and when it fails.

Examples:
  ffc do-thing -d ToDo -n TD-0001 --data '{"status":"Closed"}'
  ffc do-thing -d ToDo -n TD-0001 --data @change.json --dry-run --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := parseObject("--data", xxData) // inline, @FILE or @-; usage error when invalid
		if err != nil {
			return err
		}
		name := docNameOrSingle(xxName, xxDoctype) // only if Single DocTypes make sense here
		if !xxYes && !dryRunOn(cmd) {
			if err := confirm(fmt.Sprintf("Do the thing to %s %q?", xxDoctype, name)); err != nil {
				return err // errAborted, or "pass --yes" without a terminal
			}
		}
		doc, err := callSite(cmd, fmt.Sprintf("Doing the thing to %s…", name), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return c.DoThing(ctx, xxDoctype, name, data) // new client method, through c.do
		})
		if err != nil {
			return err // a dry run arrives here as *client.DryRunError; execute prints the plan
		}
		if machineOutput() {
			return printResult(selectKeys(doc, xxKeys))
		}
		output.PrintSuccess(fmt.Sprintf("Did the thing to %s %s", xxDoctype, name))
		output.PrintDocTable(doc, nil)
		return nil
	},
}

func init() {
	doThingCmd.Flags().StringVarP(&xxDoctype, "doctype", "d", "", "Frappe DocType (required)")
	doThingCmd.Flags().StringVarP(&xxName, "name", "n", "", "Name of the document")
	doThingCmd.Flags().StringVar(&xxData, "data", "", `JSON object of fields (or @FILE, @-)`)
	doThingCmd.Flags().StringVar(&xxKeys, "keys", "", "Comma-separated keys to include in JSON output")
	doThingCmd.Flags().BoolVarP(&xxYes, "yes", "y", false, "Skip the confirmation prompt")
	_ = doThingCmd.MarkFlagRequired("doctype")

	addDryRun(doThingCmd, false) // reads still run; true would hold back every request
	rootCmd.AddCommand(doThingCmd)
}
```

## Read command

Same shape without `--yes`/`addDryRun`. `get_doc.go` shows `--fields` plus `--keys`: `parseFields` for fields, then in machine output `selectKeys` (when `--keys`) or `filterKeys` (when `--fields`), and `printResult(doc, fields...)` so CSV/TSV columns follow `--fields`.

## List command

```go
var xxPages pageFlags // in the flag var block

// RunE:
fields, err := parseFields(xxFields)                    // when given
filters, err := filtersFlag(xxFilters)
limit, err := listLimit("--limit", xxLimit)
opts := client.ListOptions{Fields: fields, Filters: filters, Limit: limit, Start: xxStart, OrderBy: xxOrderBy}
return listDocs(cmd, xxPages, "Fetching…", doctype, opts, fields, func(rows []map[string]interface{}) error {
	output.PrintTable(rows, fields)
	return nil
})

// init():
xxPages.register(cmd, "limit", "start") // adds --all and --page-size; these flags conflict with --all
```

`listDocs` handles machine output, `--jq` and streaming of `--all` for json/ndjson/csv/tsv. Use `listDocsTo` with a `listCacher` only when the full, unfiltered result may fill the metadata cache (as list-doctypes does).

## Subcommand

```go
parentCmd.AddCommand(childCmd) // in the child's init(), not rootCmd
```

The parent may keep its own `RunE` (runs without a subcommand), as `config` does.

## Bulk command

Reuse `bulkFlags` and `runBulk` from bulk.go: the worker pool (`--concurrency` 1-10, `--fail-fast`), per-item `bulkResult`, `bulkReport.JSON()` for machine output and `bulkReport.err()` (exit 8) as the return value. Dry runs of bulk commands go through `planAll`.
