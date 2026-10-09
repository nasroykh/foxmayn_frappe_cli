package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

var erpCmd = &cobra.Command{
	Use:   "erp",
	Short: "ERPNext helpers: map documents, make payments, look up items, stock, parties",
	Long: `Helpers for sites that run ERPNext. They call the whitelisted methods ERPNext
itself uses (the desk's "Create" buttons), so its validations, permissions and
hooks apply.

They need ERPNext 15 or 16. Anything else goes through 'ffc api' or
'ffc call-method'.`,
}

// erpNextMajor returns the ERPNext major of a site, or the error that ends an
// 'ffc erp' command: exit 4 when ERPNext is not installed, exit 1 when its
// major is one ffc has not checked.
func erpNextMajor(info *client.ServerInfo, cfg *config.SiteConfig) (int, error) {
	version := info.Version(client.ERPNextApp)
	if version == "" {
		site := cfg.Name
		if site == "" {
			site = redactedURL(cfg.URL)
		}
		return 0, &client.APIError{Status: http.StatusNotFound, Message: fmt.Sprintf("ERPNext is not installed on %s", site)}
	}
	major := info.Major(client.ERPNextApp)
	if !client.SupportedERPNext(major) {
		return 0, fmt.Errorf("ffc erp supports ERPNext 15 and 16, this site runs %s: call its methods with 'ffc api' or 'ffc call-method'", version)
	}
	return major, nil
}

// erpResult is the outcome of an 'erp' command that builds a document: the
// unsaved draft, or the document saved (and submitted) from it.
type erpResult struct {
	doc                map[string]interface{}
	created, submitted bool
	warning            string // printed on stderr, never part of the data
}

func (r *erpResult) name() string {
	n, _ := docName(r.doc["name"])
	return n
}

// erpLines are what an 'erp' command says about its outcome; created and
// submitted build their line from the document's name (a name is never part
// of a format string).
type erpLines struct {
	draft              string
	created, submitted func(name string) string
}

// printERP prints the outcome of an 'erp' command: the document as data, or
// a line saying what happened and the document table.
func printERP(res *erpResult, keys string, lines erpLines) error {
	if res.warning != "" {
		fmt.Fprintln(os.Stderr, "warning: "+res.warning)
	}
	if machineOutput() {
		return printResult(selectKeys(res.doc, keys))
	}
	switch {
	case res.submitted:
		output.PrintSuccess(lines.submitted(res.name()))
	case res.created:
		output.PrintSuccess(lines.created(res.name()))
	default:
		fmt.Fprintln(os.Stderr, lines.draft)
	}
	output.PrintDocTable(res.doc, nil)
	return nil
}

// runERP runs an 'erp' command's work against the site and prints its
// outcome. A submit that fails after the insert leaves a document behind: it
// is printed as a created one, so a script can pick up its name, and the
// error still ends the command.
func runERP(cmd *cobra.Command, title, keys string, lines erpLines, run func(context.Context, *client.FrappeClient, *config.SiteConfig) (*erpResult, error)) error {
	var partial *erpResult
	res, err := callSiteCfg(cmd, title, func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (*erpResult, error) {
		r, err := run(ctx, c, cfg)
		if err != nil {
			partial = r
		}
		return r, err
	})
	if err != nil {
		if partial != nil {
			_ = printERP(partial, keys, lines)
		}
		return err
	}
	return printERP(res, keys, lines)
}

// parseDocRef splits a flag value "DocType:name" at the first colon (a
// DocType name has none).
func parseDocRef(flag, example, raw string) (doctype, name string, err error) {
	doctype, name, ok := strings.Cut(raw, ":")
	doctype, name = strings.TrimSpace(doctype), strings.TrimSpace(name)
	if !ok || doctype == "" || name == "" {
		return "", "", usageErrorf(`--%s: expected "DocType:name", e.g. %q`, flag, example)
	}
	return doctype, name, nil
}

// createFromDraft saves a draft built by ERPNext as a document of a DocType
// and, with submit, submits it. It is the tail 'erp map' and 'erp payment'
// share. The workflow check comes before the insert, so a refusal leaves the
// site untouched. A dry run stops at the insert (and, with submit, plans the
// submit too). A failed submit returns the created document with the error.
func createFromDraft(ctx context.Context, c *client.FrappeClient, doctype string, draft map[string]interface{}, submit bool) (*erpResult, error) {
	if submit {
		if err := refuseWorkflow(ctx, c, doctype, ""); err != nil {
			return nil, err
		}
	}
	data := client.InsertableCopy(draft)
	doc, err := c.CreateDoc(ctx, doctype, data)
	var plan *client.DryRunError
	if errors.As(err, &plan) && submit {
		return nil, planSubmit(ctx, c, plan, data)
	}
	if err != nil {
		return nil, err
	}
	res := &erpResult{doc: doc, created: true}
	if !submit {
		return res, nil
	}
	name, ok := docName(doc["name"])
	if !ok {
		return res, fmt.Errorf("created %s, but the site returned no name for it: not submitted", doctype)
	}
	submitted, err := c.SubmitDoc(ctx, doctype, name)
	if err != nil {
		return res, fmt.Errorf("created %s %s, but the submit failed: %w", doctype, name, err)
	}
	res.doc = submitted
	res.submitted = true
	return res, nil
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
	erpCmd.AddCommand(erpMapCmd)
	erpCmd.AddCommand(erpPaymentCmd)
	erpCmd.AddCommand(erpItemCmd)
	erpCmd.AddCommand(erpStockCmd)
	erpCmd.AddCommand(erpPartyCmd)
	rootCmd.AddCommand(erpCmd)
}
