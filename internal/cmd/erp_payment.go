package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"

	"github.com/spf13/cobra"
)

// erp payment flags
var (
	epAgainst       string
	epAmount        string
	epBankAccount   string
	epReferenceDate string
	epCreate        bool
	epSubmit        bool
	epKeys          string
)

var erpPaymentCmd = &cobra.Command{
	Use:   "payment",
	Short: "Make a Payment Entry against an invoice or an order",
	Long: `Make a Payment Entry against an invoice or an order, as the desk's Create >
Payment button does: ERPNext picks the party account, the bank or cash
account, the payment type and the amount still outstanding.

By default the unsaved draft is printed and nothing is written. --create
saves it; --submit saves and submits it (a Payment Entry with an active
Workflow is refused before anything is written).

--against takes "DocType:name" for a Sales Invoice, Sales Order, Purchase
Invoice, Purchase Order or Dunning on ERPNext 15 and 16. Only the options you
give are sent; ERPNext fills in the rest:
  --amount          pay this much instead of the outstanding amount
  --bank-account    pay from or into this Account (default: the Company's
                    default bank, else cash, account). A Mode of Payment set
                    on the document overrides it; a warning says so
  --reference-date  the date the payment is made for (YYYY-MM-DD, default today)

A Sales Order or Purchase Order that is already fully billed is refused by
ERPNext (exit 6). So is a draft that has no bank or cash account: set the
Company's default bank or cash account, or pass --bank-account.

Examples:
  ffc erp payment --against "Sales Invoice:ACC-SINV-2026-00001"
  ffc erp payment --against "Sales Invoice:ACC-SINV-2026-00001" --amount 50 --create
  ffc erp payment --against "Purchase Invoice:ACC-PINV-2026-00001" --bank-account "Bank Account - ABC" --submit --keys name,docstatus
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dt, name, err := parseDocRef("against", "Sales Invoice:ACC-SINV-2026-00001", epAgainst)
		if err != nil {
			return err
		}
		if err := checkPayDoctype("--against", dt); err != nil {
			return err
		}
		opts, err := paymentOptions(cmd)
		if err != nil {
			return err
		}
		req := paymentRequest{doctype: dt, name: name, opts: opts, create: epCreate || epSubmit, submit: epSubmit}
		lines := erpLines{
			draft:   "Unsaved draft of a Payment Entry: nothing was written (--create saves it).",
			created: func(n string) string { return fmt.Sprintf("Created Payment Entry %s against %s %s", n, dt, name) },
			submitted: func(n string) string {
				return fmt.Sprintf("Created and submitted Payment Entry %s against %s %s", n, dt, name)
			},
		}
		return runERP(cmd, fmt.Sprintf("Paying %s %s…", dt, name), epKeys, lines, func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (*erpResult, error) {
			return runERPPayment(ctx, c, cfg, req)
		})
	},
}

// paymentRequest is one 'erp payment' run: the document paid, what is sent
// along and how far to go (print the draft, create it, submit it).
type paymentRequest struct {
	doctype, name  string
	opts           client.PaymentOptions
	create, submit bool
	in             erpInput
}

// plainAmount is a plain decimal: no sign, exponent, separator, "Inf" or
// leading zeros ("05"), so what is sent reads as the number it is.
var plainAmount = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// paymentOptions reads the optional flags. A flag that was given and is
// empty or invalid is a usage error; one that was not given is not sent.
func paymentOptions(cmd *cobra.Command) (client.PaymentOptions, error) {
	return buildPaymentOptions(erpInput{}, flagOpt(cmd, "amount", epAmount), flagOpt(cmd, "bank-account", epBankAccount), flagOpt(cmd, "reference-date", epReferenceDate))
}

// paymentDraft builds the unsaved Payment Entry (a GET) and the notes about
// it; gap, one of them, is what is missing when it has no bank or cash
// account. 'erp payment' and the MCP erp_payment tool share it.
func paymentDraft(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, req paymentRequest) (draft map[string]interface{}, warnings []string, gap string, err error) {
	info, _, err := serverInfo(ctx, c, cfg, false)
	if err != nil {
		return nil, nil, "", err
	}
	major, err := erpNextMajor(info, cfg, req.in)
	if err != nil {
		return nil, nil, "", err
	}
	method, ok := client.PaymentMethod(major)
	if !ok {
		return nil, nil, "", fmt.Errorf("ffc erp payment has no method for ERPNext %d", major)
	}
	if draft, err = c.PaymentDraft(ctx, method, req.doctype, req.name, req.opts); err != nil {
		return nil, nil, "", err
	}
	warnings, gap = paymentWarnings(req.in, draft, req.opts.BankAccount)
	return draft, warnings, gap, nil
}

// runERPPayment builds the Payment Entry draft (a GET) and, when asked,
// saves and submits it. Order matters: every check and read comes before the
// first write, so a refusal leaves the site untouched.
func runERPPayment(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, req paymentRequest) (*erpResult, error) {
	draft, warnings, gap, err := paymentDraft(ctx, c, cfg, req)
	if err != nil {
		return nil, err
	}
	warning := strings.Join(warnings, "\nwarning: ")
	if !req.create {
		return &erpResult{doc: draft, warning: warning}, nil
	}
	if gap != "" {
		return nil, &client.StateError{Message: gap}
	}
	res, err := createFromDraft(ctx, c, "Payment Entry", draft, req.submit)
	if res != nil {
		res.warning = warning
	}
	return res, err
}

func init() {
	f := erpPaymentCmd.Flags()
	f.StringVar(&epAgainst, "against", "", `Document to pay as "DocType:name" (required)`)
	f.StringVar(&epAmount, "amount", "", "Amount to pay (default: the outstanding amount)")
	f.StringVar(&epBankAccount, "bank-account", "", "Bank or cash Account to pay from or into (default: the Company's default; a Mode of Payment on the document overrides it)")
	f.StringVar(&epReferenceDate, "reference-date", "", "Reference date, YYYY-MM-DD (default: today)")
	f.BoolVar(&epCreate, "create", false, "Save the draft as a new Payment Entry")
	f.BoolVar(&epSubmit, "submit", false, "Save and submit it (implies --create)")
	f.StringVar(&epKeys, "keys", "", "Comma-separated keys to include in data output, e.g. name,docstatus")
	_ = erpPaymentCmd.MarkFlagRequired("against")
	addDryRun(erpPaymentCmd, false)
}
