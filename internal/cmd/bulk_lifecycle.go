package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// lifecycleBulk is bulk-submit or bulk-cancel: the flags they share, and
// what sets them apart. Each document goes through the same single-document
// path as submit-doc / cancel-doc, one request at a time by default.
type lifecycleBulk struct {
	doctype, names, file, filters string
	yes                           bool
	bulk                          bulkFlags

	verb, progress, done string // "submit", "Submitting", "submitted"
	from                 int    // docstatus a document must have
	// blocked says, by docstatus, why a document cannot be acted on.
	blocked map[string]string
	caution string // last line of the confirmation
	act     func(ctx context.Context, c *client.FrappeClient, doctype, name string) error
}

var bulkSubmit = &lifecycleBulk{
	verb: "submit", progress: "Submitting", done: "submitted", from: 0,
	blocked: map[string]string{
		"1": "already submitted",
		"2": "cancelled and cannot be submitted again; amend it with 'ffc amend-doc'",
	},
	caution: "A submitted document can only be cancelled, not edited.",
	act: func(ctx context.Context, c *client.FrappeClient, doctype, name string) error {
		_, err := c.SubmitDoc(ctx, doctype, name)
		return err
	},
}

var bulkCancel = &lifecycleBulk{
	verb: "cancel", progress: "Cancelling", done: "cancelled", from: 1,
	blocked: map[string]string{
		"0": "a draft, not submitted: only a submitted document can be cancelled",
		"2": "already cancelled",
	},
	caution: "A cancelled document cannot be submitted again.",
	act: func(ctx context.Context, c *client.FrappeClient, doctype, name string) error {
		_, err := c.CancelDoc(ctx, doctype, name)
		return err
	},
}

var bulkSubmitCmd = &cobra.Command{
	Use:   "bulk-submit",
	Short: "Submit multiple draft documents",
	Long: `Submit many draft documents of a submittable DocType (docstatus 0 → 1), one
frappe.client.submit per document like 'ffc submit-doc', so each document's
validations and hooks run (GL entries, stock ledger, ...). Each document is
read first and sent back as read, so one changed in between is rejected.

Provide document names as a comma-separated list with --names, or as a JSON
array of names (strings or numbers) with --file (- for stdin). Or select the
drafts with --filters (same syntax as list-docs): docstatus 0 is added to
them, the matching names are listed first, shown, and then submitted.

A DocType with an active Workflow is refused before anything is sent; use
'ffc workflow bulk-apply'. A document that is not a draft fails on its own.
You will be prompted to confirm unless --yes is provided.

Documents are submitted one at a time, in the order given (by name with
--filters): concurrent submits can deadlock on ERPNext's GL and stock
postings, so raise --concurrency only for DocTypes without them. Processing
continues when a document fails unless --fail-fast is set, and the command
exits 8 if any failed.

Examples:
  ffc bulk-submit -d "Sales Invoice" --names "ACC-SINV-2026-00001,ACC-SINV-2026-00002"
  ffc bulk-submit -d "Journal Entry" --file names.json --yes --json
  ffc bulk-submit -d "Sales Invoice" --filters '{"customer":"CUST-001"}' --dry-run
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return bulkSubmit.run(cmd) },
}

var bulkCancelCmd = &cobra.Command{
	Use:   "bulk-cancel",
	Short: "Cancel multiple submitted documents",
	Long: `Cancel many submitted documents (docstatus 1 → 2), one frappe.client.cancel
per document like 'ffc cancel-doc'. A cancelled document cannot be edited or
submitted again; amend it with 'ffc amend-doc'.

Provide document names as a comma-separated list with --names, or as a JSON
array of names (strings or numbers) with --file (- for stdin). Or select the
submitted documents with --filters (same syntax as list-docs): docstatus 1 is
added to them, the matching names are listed first, shown, and then cancelled.

Submitted documents that link to one block its cancel (exit code 6 for that
document); cancel the linking ones first, and see them with
'ffc cancel-doc --check'. A DocType with an active Workflow is refused before
anything is sent; use 'ffc workflow bulk-apply'. A document that is not
submitted fails on its own.

Documents are cancelled one at a time, in the order given (by name with
--filters), so the order of --names can respect those links. Processing
continues when a document fails unless --fail-fast is set, and the command
exits 8 if any failed. You will be prompted to confirm unless --yes is
provided.

Examples:
  ffc bulk-cancel -d "Sales Invoice" --names "ACC-SINV-2026-00002,ACC-SINV-2026-00001"
  ffc bulk-cancel -d "Journal Entry" --file names.json --yes --fail-fast
  ffc bulk-cancel -d "Sales Invoice" --filters '{"customer":"CUST-001"}' --dry-run
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return bulkCancel.run(cmd) },
}

// run selects the documents, refuses a DocType with a Workflow once, asks,
// and then goes through them.
func (l *lifecycleBulk) run(cmd *cobra.Command) error {
	names, err := bulkNames(l.names, l.file)
	if err != nil {
		return err
	}
	var filters string
	switch {
	case l.filters != "":
		if filters, err = bulkFilters(l.filters); err != nil {
			return err
		}
		if filters, err = withDocstatus(filters, l.from); err != nil {
			return err
		}
	case len(names) == 0:
		return usageErrorf("provide --names, --file or --filters")
	}
	if err := l.bulk.check(); err != nil {
		return err
	}
	c, err := newClient(cmd.Context())
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	if err := refuseWorkflow(cmd.Context(), c, l.doctype, ""); err != nil {
		return err
	}
	if filters != "" {
		if names, err = filterNames(cmd, c, l.doctype, filters); err != nil {
			return err
		}
		if len(names) == 0 {
			return noMatches(l.doctype, l.verb, l.done)
		}
	}
	if err := l.confirm(cmd, names); err != nil {
		return err
	}
	rep, err := l.bulk.runOn(cmd, c, fmt.Sprintf("%s %d %s documents…", l.progress, len(names), l.doctype), len(names), l.done, l.op(l.doctype, names))
	if err != nil {
		return err
	}
	return printBulkReport(rep, l.doctype)
}

// confirm shows what will change and asks, unless --yes or a dry run.
func (l *lifecycleBulk) confirm(cmd *cobra.Command, names []string) error {
	if l.yes || dryRunOn(cmd) {
		return nil
	}
	return confirm(fmt.Sprintf("%s these %d %s document(s)?\n  %s\n%s",
		text.Sanitize(capitalize(l.verb)), len(names), text.Sanitize(l.doctype), namePreview(names), l.caution))
}

// op acts on names[i] of doctype (the MCP tools share it). The document is
// read first, so one that is not in the state the command needs gets a clear
// result instead of the server's DocstatusTransitionError. A dry run stops
// at the first such document (planAll), as docs/cli/bulk.md says.
func (l *lifecycleBulk) op(doctype string, names []string) func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
	return func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
		name := names[i]
		doc, err := c.GetDoc(ctx, doctype, name)
		if err != nil {
			return name, err
		}
		if why, ok := l.blocked[fmt.Sprint(doc["docstatus"])]; ok {
			return name, &client.StateError{Message: fmt.Sprintf("%s is %s", name, why)}
		}
		return name, l.act(ctx, c, doctype, name)
	}
}

// capitalize upper-cases the first letter of a verb.
func capitalize(verb string) string {
	return strings.ToUpper(verb[:1]) + verb[1:]
}

func (l *lifecycleBulk) register(cmd *cobra.Command, what string) {
	cmd.Flags().StringVarP(&l.doctype, "doctype", "d", "", "Frappe DocType (required)")
	cmd.Flags().StringVar(&l.names, "names", "", "Comma-separated list of document names to "+l.verb)
	cmd.Flags().StringVar(&l.file, "file", "", "Path to a JSON file containing an array of names (- for stdin)")
	cmd.Flags().StringVar(&l.filters, "filters", "", fmt.Sprintf(`%s the %s matching these filters, e.g. '{"customer":"CUST-001"}' (@FILE, @- for stdin)`, capitalize(l.verb), what))
	cmd.Flags().BoolVarP(&l.yes, "yes", "y", false, "Skip confirmation prompt")
	l.bulk.register(cmd)

	_ = cmd.MarkFlagRequired("doctype")
	cmd.MarkFlagsMutuallyExclusive("names", "file", "filters")

	addDryRun(cmd, false)
	rootCmd.AddCommand(cmd)
}

func init() {
	bulkSubmit.register(bulkSubmitCmd, "draft documents")
	bulkCancel.register(bulkCancelCmd, "submitted documents")
}
