package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// erp map flags
var (
	emFrom   string
	emTo     string
	emCreate bool
	emSubmit bool
	emKeys   string
)

var erpMapCmd = &cobra.Command{
	Use:   "map",
	Short: "Map a document into the next one (Sales Order to Sales Invoice)",
	Long: `Map a document into a document of the next DocType, as the desk's Create
button does: items, taxes and links are carried over by ERPNext itself.

By default the unsaved draft is printed and nothing is written. --create
saves it; --submit saves and submits it (a DocType with an active Workflow is
refused before anything is written).

Supported on ERPNext 15 and 16 (--from DocType to --to DocType):
  Quotation -> Sales Order
  Sales Order -> Sales Invoice, Delivery Note
  Delivery Note -> Sales Invoice
  Purchase Order -> Purchase Receipt, Purchase Invoice
  Purchase Receipt -> Purchase Invoice
  Material Request -> Purchase Order

A Quotation made out to a Lead or Prospect is refused (exit 6): convert the
lead to a customer first.

Examples:
  ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice"
  ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Sales Invoice" --create
  ffc erp map --from "Sales Order:SAL-ORD-2026-00001" --to "Delivery Note" --submit --keys name,docstatus
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		source, name, err := parseMapSource(emFrom)
		if err != nil {
			return err
		}
		if strings.TrimSpace(emTo) == "" {
			return usageErrorf("--to: provide the DocType to map into")
		}
		req := mapRequest{from: source, name: name, to: strings.TrimSpace(emTo), create: emCreate || emSubmit, submit: emSubmit}
		res, err := callSiteCfg(cmd, fmt.Sprintf("Mapping %s %s…", source, name), func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (*mapResult, error) {
			return runERPMap(ctx, c, cfg, req)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(selectKeys(res.doc, emKeys))
		}
		switch {
		case res.submitted:
			output.PrintSuccess(fmt.Sprintf("Created and submitted %s %s from %s %s", req.to, res.name(), req.from, req.name))
		case res.created:
			output.PrintSuccess(fmt.Sprintf("Created %s %s from %s %s", req.to, res.name(), req.from, req.name))
		default:
			fmt.Fprintf(os.Stderr, "Unsaved draft of %s: nothing was written (--create saves it).\n", req.to)
		}
		output.PrintDocTable(res.doc, nil)
		return nil
	},
}

// mapRequest is one 'erp map' run: the source document, the target DocType
// and how far to go (print the draft, create it, submit it).
type mapRequest struct {
	from, name, to string
	create, submit bool
}

type mapResult struct {
	doc                map[string]interface{}
	created, submitted bool
}

func (r *mapResult) name() string {
	n, _ := docName(r.doc["name"])
	return n
}

// parseMapSource splits --from "DocType:name" at the first colon (a DocType
// name has none).
func parseMapSource(raw string) (doctype, name string, err error) {
	doctype, name, ok := strings.Cut(raw, ":")
	doctype, name = strings.TrimSpace(doctype), strings.TrimSpace(name)
	if !ok || doctype == "" || name == "" {
		return "", "", usageErrorf(`--from: expected "DocType:name", e.g. "Sales Order:SAL-ORD-2026-00001"`)
	}
	return doctype, name, nil
}

// runERPMap maps the source into an unsaved draft and, when asked, saves and
// submits it. Order matters: every check and read comes before the first
// write, so a refusal leaves the site untouched.
func runERPMap(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, req mapRequest) (*mapResult, error) {
	info, _, err := serverInfo(ctx, c, cfg, false)
	if err != nil {
		return nil, err
	}
	major, err := erpNextMajor(info, cfg)
	if err != nil {
		return nil, err
	}
	method, ok := client.MapMethod(major, req.from, req.to)
	if !ok {
		return nil, usageErrorf("cannot map %s to %s on ERPNext %d; supported: %s", req.from, req.to, major, client.MapPairsText(major))
	}
	if req.from == "Quotation" {
		if err := refuseLeadQuotation(ctx, c, req.name); err != nil {
			return nil, err
		}
	}
	draft, err := c.MapDoc(ctx, method, req.name)
	if err != nil {
		return nil, err
	}
	if !req.create {
		return &mapResult{doc: draft}, nil
	}
	if req.submit {
		if err := refuseWorkflow(ctx, c, req.to, ""); err != nil {
			return nil, err
		}
	}
	data := client.InsertableCopy(draft)
	doc, err := c.CreateDoc(ctx, req.to, data)
	var plan *client.DryRunError
	if errors.As(err, &plan) && req.submit {
		return nil, planSubmit(ctx, c, plan, data)
	}
	if err != nil {
		return nil, err
	}
	res := &mapResult{doc: doc, created: true}
	if !req.submit {
		return res, nil
	}
	name, _ := docName(doc["name"])
	if res.doc, err = c.SubmitDoc(ctx, req.to, name); err != nil {
		return nil, fmt.Errorf("created %s %s, but the submit failed: %w", req.to, name, err)
	}
	res.submitted = true
	return res, nil
}

// refuseLeadQuotation stops the mapping of a Quotation made out to a Lead or
// Prospect: ERPNext inserts the Customer inside that call, which a GET
// rolls back, and a Sales Order needs a real one.
func refuseLeadQuotation(ctx context.Context, c *client.FrappeClient, name string) error {
	q, err := c.GetDoc(ctx, "Quotation", name)
	if err != nil {
		return err
	}
	if to, _ := q["quotation_to"].(string); to == "Lead" || to == "Prospect" {
		return &client.StateError{Message: fmt.Sprintf("Quotation %s is made out to a %s: convert the %s to a customer first", name, to, strings.ToLower(to))}
	}
	return nil
}

// planSubmit completes the plan of a dry-run insert with the submit that
// would follow it (the insert holds the run back before there is a name to
// submit), and returns the whole plan as the error.
func planSubmit(ctx context.Context, c *client.FrappeClient, plan *client.DryRunError, doc map[string]interface{}) error {
	_, err := c.CallMethod(ctx, "frappe.client.submit", map[string]interface{}{"doc": doc}, false)
	var submit *client.DryRunError
	if !errors.As(err, &submit) {
		return fmt.Errorf("planning the submit: the dry run held nothing back (%v)", err)
	}
	plan.Requests = append(plan.Requests, submit.Requests...)
	return plan
}

func init() {
	erpMapCmd.Flags().StringVar(&emFrom, "from", "", `Source document as "DocType:name" (required)`)
	erpMapCmd.Flags().StringVar(&emTo, "to", "", "DocType to map into (required)")
	erpMapCmd.Flags().BoolVar(&emCreate, "create", false, "Save the draft as a new document")
	erpMapCmd.Flags().BoolVar(&emSubmit, "submit", false, "Save and submit it (implies --create)")
	erpMapCmd.Flags().StringVar(&emKeys, "keys", "", "Comma-separated keys to include in data output, e.g. name,docstatus")
	_ = erpMapCmd.MarkFlagRequired("from")
	_ = erpMapCmd.MarkFlagRequired("to")
	addDryRun(erpMapCmd, false)
}
