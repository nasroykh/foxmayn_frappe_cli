package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

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
                    default bank, else cash, account)
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
		if !client.CanPayAgainst(dt) {
			return usageErrorf("--against: cannot make a payment against %s; supported: %s", dt, strings.Join(client.PaymentDocTypes, ", "))
		}
		opts, err := paymentOptions(cmd)
		if err != nil {
			return err
		}
		req := paymentRequest{doctype: dt, name: name, opts: opts, create: epCreate || epSubmit, submit: epSubmit}
		lines := erpLines{
			draft:     "Unsaved draft of a Payment Entry: nothing was written (--create saves it).",
			created:   fmt.Sprintf("Created Payment Entry %%s against %s %s", dt, name),
			submitted: fmt.Sprintf("Created and submitted Payment Entry %%s against %s %s", dt, name),
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
}

// plainAmount is a positive decimal: no sign, exponent, separator or "Inf".
var plainAmount = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// paymentOptions reads the optional flags. A flag that was given and is
// empty or invalid is a usage error; one that was not given is not sent.
func paymentOptions(cmd *cobra.Command) (client.PaymentOptions, error) {
	var o client.PaymentOptions
	f := cmd.Flags()
	if f.Changed("amount") {
		a := strings.TrimSpace(epAmount)
		if v, err := strconv.ParseFloat(a, 64); !plainAmount.MatchString(a) || err != nil || v <= 0 {
			return o, usageErrorf("--amount: expected a positive number such as 50 or 12.5, got %q", epAmount)
		}
		o.Amount = a
	}
	if f.Changed("bank-account") {
		if o.BankAccount = strings.TrimSpace(epBankAccount); o.BankAccount == "" {
			return o, usageErrorf("--bank-account: provide the name of an Account")
		}
	}
	if f.Changed("reference-date") {
		d := strings.TrimSpace(epReferenceDate)
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return o, usageErrorf("--reference-date: expected YYYY-MM-DD, got %q", epReferenceDate)
		}
		o.ReferenceDate = d
	}
	return o, nil
}

// runERPPayment builds the Payment Entry draft (a GET) and, when asked,
// saves and submits it. Order matters: every check and read comes before the
// first write, so a refusal leaves the site untouched.
func runERPPayment(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, req paymentRequest) (*erpResult, error) {
	info, _, err := serverInfo(ctx, c, cfg, false)
	if err != nil {
		return nil, err
	}
	major, err := erpNextMajor(info, cfg)
	if err != nil {
		return nil, err
	}
	method, ok := client.PaymentMethod(major)
	if !ok {
		return nil, fmt.Errorf("ffc erp payment has no method for ERPNext %d", major)
	}
	draft, err := c.PaymentDraft(ctx, method, req.doctype, req.name, req.opts)
	if err != nil {
		return nil, err
	}
	gap := paymentAccountGap(draft)
	if !req.create {
		return &erpResult{doc: draft, warning: gap}, nil
	}
	if gap != "" {
		return nil, &client.StateError{Message: gap}
	}
	return createFromDraft(ctx, c, "Payment Entry", draft, req.submit)
}

// paymentAccountGap says what is missing when the draft has no bank or cash
// account. get_payment_entry does not throw then: it answers a draft whose
// paid_from or paid_to is empty (both are required), which the insert would
// refuse as a missing value.
func paymentAccountGap(draft map[string]interface{}) string {
	var missing []string
	for _, f := range []string{"paid_from", "paid_to"} {
		if v, _ := draft[f].(string); strings.TrimSpace(v) == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	company, _ := draft["company"].(string)
	if company == "" {
		company = "the document's company"
	}
	return fmt.Sprintf("the Payment Entry draft has no bank or cash account (%s is empty): set the default bank or cash account of %s, or pass --bank-account", strings.Join(missing, ", "), company)
}

func init() {
	f := erpPaymentCmd.Flags()
	f.StringVar(&epAgainst, "against", "", `Document to pay as "DocType:name" (required)`)
	f.StringVar(&epAmount, "amount", "", "Amount to pay (default: the outstanding amount)")
	f.StringVar(&epBankAccount, "bank-account", "", "Bank or cash Account to pay from or into (default: the Company's default)")
	f.StringVar(&epReferenceDate, "reference-date", "", "Reference date, YYYY-MM-DD (default: today)")
	f.BoolVar(&epCreate, "create", false, "Save the draft as a new Payment Entry")
	f.BoolVar(&epSubmit, "submit", false, "Save and submit it (implies --create)")
	f.StringVar(&epKeys, "keys", "", "Comma-separated keys to include in data output, e.g. name,docstatus")
	_ = erpPaymentCmd.MarkFlagRequired("against")
	addDryRun(erpPaymentCmd, false)
}
