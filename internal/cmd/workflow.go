package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// workflow flags
var (
	wfDoctype string
	wfName    string
	wfAction  string
	wfKeys    string

	wbDoctype string
	wbNames   string
	wbFile    string
	wbAction  string
	wbYes     bool
	wbBulk    bulkFlags

	wpDoctype string
	wpLimit   int
	wpPages   pageFlags
)

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Move documents through their Workflow",
	Long: `Documents of a DocType with an active Workflow change state through
workflow actions (Approve, Reject, ...), not through submit-doc or cancel-doc.
An action may submit or cancel the document.`,
}

var workflowTransitionsCmd = &cobra.Command{
	Use:   "transitions",
	Short: "List the workflow actions you can apply to a document",
	Long: `List the workflow actions the current user can apply to a document in its
current state (frappe.model.workflow.get_transitions).

Examples:
  ffc workflow transitions -d "Leave Application" -n HR-LAP-2026-00001
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		list, err := callSite(cmd, "Fetching transitions…", func(ctx context.Context, c *client.FrappeClient) ([]interface{}, error) {
			return c.WorkflowTransitions(ctx, wfDoctype, wfName)
		})
		if err != nil {
			return err
		}
		if list == nil {
			list = []interface{}{}
		}
		fields := []string{"action", "next_state", "allowed"}
		return render(list, fields, func() error {
			rows := make([]map[string]interface{}, 0, len(list))
			for _, t := range list {
				if m, ok := t.(map[string]interface{}); ok {
					rows = append(rows, m)
				}
			}
			if len(rows) == 0 {
				output.PrintWarning(fmt.Sprintf("No workflow actions available to you on %s %s.", wfDoctype, wfName))
				return nil
			}
			output.PrintTable(rows, fields)
			return nil
		})
	},
}

var workflowApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a workflow action to a document",
	Long: `Apply a workflow action to a document (frappe.model.workflow.apply_workflow).
The action must be one 'ffc workflow transitions' lists.

Examples:
  ffc workflow apply -d "Leave Application" -n HR-LAP-2026-00001 --action Approve
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		doc, err := callSite(cmd, fmt.Sprintf("Applying %s…", wfAction), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return c.ApplyWorkflow(ctx, wfDoctype, wfName, wfAction)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(selectKeys(doc, wfKeys))
		}
		msg := fmt.Sprintf("Applied %s to %s %s", wfAction, wfDoctype, wfName)
		// workflow_state is the default state field; a Workflow may use another.
		if state, ok := doc["workflow_state"].(string); ok && state != "" {
			msg += ": now " + state
		}
		output.PrintSuccess(msg)
		return nil
	},
}

var workflowBulkApplyCmd = &cobra.Command{
	Use:   "bulk-apply",
	Short: "Apply a workflow action to many documents",
	Long: `Apply one workflow action to many documents, one request per document, and
report each result like the bulk-* commands.

Provide document names as a comma-separated list with --names, or as a JSON
array of names with --file (- for stdin). You are asked to confirm unless
--yes is given. The command exits 8 if any document failed.

Examples:
  ffc workflow bulk-apply -d "Leave Application" --names "HR-LAP-2026-00001,HR-LAP-2026-00002" --action Approve
  ffc workflow bulk-apply -d "Leave Application" --file names.json --action Approve --yes --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var names []string
		switch {
		case wbFile != "":
			raw, err := readInput("", wbFile)
			if err != nil {
				return err
			}
			if names, err = parseNames(raw); err != nil {
				return err
			}
		case wbNames != "":
			names = splitCSV(wbNames)
		}
		if len(names) == 0 {
			return usageErrorf("provide --names or --file")
		}
		if !wbYes && !dryRunOn(cmd) {
			if err := confirm(fmt.Sprintf("Apply %q to %d %s document(s)?", wbAction, len(names), wbDoctype)); err != nil {
				return err
			}
		}
		rep, err := wbBulk.run(cmd, fmt.Sprintf("Applying %s to %d %s documents…", wbAction, len(names), wbDoctype), len(names), "applied",
			func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
				_, err := c.ApplyWorkflow(ctx, wbDoctype, names[i], wbAction)
				return names[i], err
			})
		if err != nil {
			return err
		}
		return printBulkReport(rep, wbDoctype)
	},
}

// workflowActionFields are the columns of workflow pending.
var workflowActionFields = []string{"name", "reference_doctype", "reference_name", "workflow_state", "creation"}

var workflowPendingCmd = &cobra.Command{
	Use:   "pending",
	Short: "List open workflow actions",
	Long: `List the open Workflow Action records the current user can see: the
documents waiting for an action from one of their roles.

Examples:
  ffc workflow pending
  ffc workflow pending -d "Leave Application" --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, err := listLimit("limit", wpLimit)
		if err != nil {
			return err
		}
		f := map[string]interface{}{"status": "Open"}
		if wpDoctype != "" {
			f["reference_doctype"] = wpDoctype
		}
		filters, err := json.Marshal(f)
		if err != nil {
			return err
		}
		opts := client.ListOptions{Fields: workflowActionFields, Filters: string(filters), Limit: limit, OrderBy: "creation asc"}
		return listDocs(cmd, wpPages, "Fetching workflow actions…", "Workflow Action", opts, workflowActionFields, func(rows []map[string]interface{}) error {
			output.PrintTable(rows, workflowActionFields)
			return nil
		})
	},
}

func init() {
	for _, c := range []*cobra.Command{workflowTransitionsCmd, workflowApplyCmd} {
		c.Flags().StringVarP(&wfDoctype, "doctype", "d", "", "Frappe DocType (required)")
		c.Flags().StringVarP(&wfName, "name", "n", "", "Name of the document (required)")
		_ = c.MarkFlagRequired("doctype")
		_ = c.MarkFlagRequired("name")
	}
	workflowApplyCmd.Flags().StringVar(&wfAction, "action", "", "Workflow action, e.g. Approve (required)")
	workflowApplyCmd.Flags().StringVar(&wfKeys, "keys", "", "Comma-separated keys to include in data output, e.g. name,workflow_state")
	_ = workflowApplyCmd.MarkFlagRequired("action")

	workflowBulkApplyCmd.Flags().StringVarP(&wbDoctype, "doctype", "d", "", "Frappe DocType (required)")
	workflowBulkApplyCmd.Flags().StringVar(&wbNames, "names", "", "Comma-separated document names")
	workflowBulkApplyCmd.Flags().StringVar(&wbFile, "file", "", "JSON array of document names (- for stdin)")
	workflowBulkApplyCmd.Flags().StringVar(&wbAction, "action", "", "Workflow action, e.g. Approve (required)")
	workflowBulkApplyCmd.Flags().BoolVarP(&wbYes, "yes", "y", false, "Skip confirmation prompt")
	wbBulk.register(workflowBulkApplyCmd)
	workflowBulkApplyCmd.MarkFlagsMutuallyExclusive("names", "file")
	_ = workflowBulkApplyCmd.MarkFlagRequired("doctype")
	_ = workflowBulkApplyCmd.MarkFlagRequired("action")

	workflowPendingCmd.Flags().StringVarP(&wpDoctype, "doctype", "d", "", "Only actions on this DocType")
	workflowPendingCmd.Flags().IntVarP(&wpLimit, "limit", "l", 50, "Maximum actions to return (0 = no limit)")
	wpPages.register(workflowPendingCmd, "limit")

	addDryRun(workflowApplyCmd, false)
	addDryRun(workflowBulkApplyCmd, false)
	workflowCmd.AddCommand(workflowTransitionsCmd, workflowApplyCmd, workflowBulkApplyCmd, workflowPendingCmd)
	rootCmd.AddCommand(workflowCmd)
}
