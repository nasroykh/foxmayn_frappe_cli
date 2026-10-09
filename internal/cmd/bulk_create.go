package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"

	"github.com/spf13/cobra"
)

// bulk-create flags
var (
	bcDoctype string
	bcData    string
	bcFile    string
	bcAtomic  bool
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

With --atomic all items go in one frappe.client.insert_many request, which is
one database transaction: if any item fails none is created, and the command
fails with that item's error instead of a per-item report. At most 200 items;
--concurrency and --fail-fast do not apply. All items must be of the -d
DocType (an item with another "doctype" is refused). A controller hook that
commits, or DDL (inserting a Custom Field or DocType), ends the transaction
early and breaks all-or-nothing. If the request times out the batch may or may
not exist: check the site, and raise --timeout (200 inserts can exceed 30s).

Examples:
  ffc bulk-create -d "ToDo" --data '[{"description":"Task 1"},{"description":"Task 2"}]'
  ffc bulk-create -d "Note" --file notes.json --concurrency 4
  cat customers.json | ffc bulk-create -d "Customer" --file - --json
  ffc bulk-create -d "ToDo" --file todos.json --atomic --timeout 2m
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
		if bcAtomic {
			return bulkCreateAtomic(cmd, items)
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

// bulkCreateAtomic creates items in one insert_many request. The input is
// checked first (the 200 limit, a foreign "doctype") so a bad batch sends
// nothing; a dry run plans the one request.
func bulkCreateAtomic(cmd *cobra.Command, items []map[string]interface{}) error {
	if cmd.Flags().Changed("concurrency") || bcBulk.failFast {
		return usageErrorf("--atomic sends one request, so --concurrency and --fail-fast do not apply")
	}
	docs, err := client.InsertManyDocs(bcDoctype, items)
	if err != nil {
		return usageErrorf("--atomic: %w", err)
	}
	c, err := newClient(cmd.Context())
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	var names []string
	var callErr error
	spinErr := runSpinner(fmt.Sprintf("Creating %d %s documents in one request…", len(docs), bcDoctype), func() {
		names, callErr = c.InsertMany(cmd.Context(), bcDoctype, docs)
	})
	if callErr != nil {
		var plan *client.DryRunError
		if errors.As(callErr, &plan) {
			return callErr
		}
		return atomicError(callErr)
	}
	// The batch exists once the call succeeded, so it is reported even when
	// the spinner itself failed.
	_ = spinErr
	rep := bulkReport{Done: "created", Total: len(names), OK: len(names), Results: make([]bulkResult, len(names))}
	for i, n := range names {
		rep.Results[i] = bulkResult{Index: i + 1, Name: n, Status: "created"}
	}
	return printBulkReport(rep, bcDoctype)
}

// atomicError says what a failed --atomic request means for the batch, and
// keeps the error's class for the exit code. A 4xx refusal, or a 500/501 with
// Frappe's exc_type, means Frappe rolled the transaction back. A success
// answer ffc cannot read means the batch was most likely committed. Anything
// else leaves the outcome unknown: no answer, a dropped connection, a 408 or
// 499, any 502 and above (proxies and CDNs: 502-504, 520-524), or a 5xx that
// is not Frappe's.
func atomicError(err error) error {
	if errors.Is(err, client.ErrUnusableReply) {
		return fmt.Errorf("the batch was probably created: check the site before re-running: %w", err)
	}
	var api *client.APIError
	if errors.As(err, &api) {
		st := api.Status
		unknown := st == http.StatusRequestTimeout || st == 499 || st >= http.StatusBadGateway ||
			(st >= http.StatusInternalServerError && api.ExcType == "")
		if st >= 400 && !unknown {
			return fmt.Errorf("nothing was created: %w", err)
		}
	}
	return fmt.Errorf("the batch may or may not have been created: check the site before re-running, and raise --timeout (now %s; 200 inserts can take longer): %w", client.Timeout, err)
}

func init() {
	bulkCreateCmd.Flags().StringVarP(&bcDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkCreateCmd.Flags().StringVar(&bcData, "data", "", "JSON array of field-value objects to create")
	bulkCreateCmd.Flags().StringVar(&bcFile, "file", "", "Path to a JSON file containing an array of objects (- for stdin)")
	bulkCreateCmd.Flags().BoolVar(&bcAtomic, "atomic", false, "Create all items in one request (frappe.client.insert_many): all or none, at most 200")
	bcBulk.register(bulkCreateCmd)

	_ = bulkCreateCmd.MarkFlagRequired("doctype")
	bulkCreateCmd.MarkFlagsMutuallyExclusive("data", "file")

	addDryRun(bulkCreateCmd, false)
	rootCmd.AddCommand(bulkCreateCmd)
}
