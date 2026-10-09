package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"

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
		source, name, err := parseDocRef("from", "Sales Order:SAL-ORD-2026-00001", emFrom)
		if err != nil {
			return err
		}
		if strings.TrimSpace(emTo) == "" {
			return usageErrorf("--to: provide the DocType to map into")
		}
		req := mapRequest{from: source, name: name, to: strings.TrimSpace(emTo), create: emCreate || emSubmit, submit: emSubmit}
		lines := erpLines{
			draft:     fmt.Sprintf("Unsaved draft of %s: nothing was written (--create saves it).", req.to),
			created:   fmt.Sprintf("Created %s %%s from %s %s", req.to, req.from, req.name),
			submitted: fmt.Sprintf("Created and submitted %s %%s from %s %s", req.to, req.from, req.name),
		}
		return runERP(cmd, fmt.Sprintf("Mapping %s %s…", source, name), emKeys, lines, func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (*erpResult, error) {
			return runERPMap(ctx, c, cfg, req)
		})
	},
}

// mapRequest is one 'erp map' run: the source document, the target DocType
// and how far to go (print the draft, create it, submit it).
type mapRequest struct {
	from, name, to string
	create, submit bool
}

// runERPMap maps the source into an unsaved draft and, when asked, saves and
// submits it. Order matters: every check and read comes before the first
// write, so a refusal leaves the site untouched.
func runERPMap(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, req mapRequest) (*erpResult, error) {
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
		return &erpResult{doc: draft}, nil
	}
	return createFromDraft(ctx, c, req.to, draft, req.submit)
}

// refuseLeadQuotation stops the mapping of a Quotation made out to a Lead or
// Prospect that has no Customer yet: ERPNext then inserts the Customer
// inside the mapper call (quotation.py _make_customer), which a GET rolls
// back, and a Sales Order needs a real one. A Customer that points back at
// the lead or prospect (lead_name, prospect_name) is reused by the mapper,
// so that case maps. When the Customers cannot be read, the answer is "no".
func refuseLeadQuotation(ctx context.Context, c *client.FrappeClient, name string) error {
	q, err := c.GetDoc(ctx, "Quotation", name)
	if err != nil {
		return err
	}
	to, _ := q["quotation_to"].(string)
	link := map[string]string{"Lead": "lead_name", "Prospect": "prospect_name"}[to]
	if link == "" {
		return nil
	}
	filters, err := json.Marshal(map[string]interface{}{link: q["party_name"]})
	if err != nil {
		return err
	}
	rows, err := c.GetList(ctx, "Customer", client.ListOptions{Fields: []string{"name"}, Filters: string(filters), Limit: 1})
	var api *client.APIError
	switch {
	case errors.As(err, &api) && api.Status == http.StatusForbidden:
	case err != nil:
		return err
	case len(rows) > 0:
		return nil
	}
	return &client.StateError{Message: fmt.Sprintf("Quotation %s is made out to a %s with no customer: convert the %s to a customer first", name, to, strings.ToLower(to))}
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
