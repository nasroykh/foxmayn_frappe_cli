package cmd

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"

	"github.com/spf13/cobra"
)

// The validation of the 'ffc erp' inputs, shared by the commands (flags) and
// the MCP erp tools (arguments): one set of rules and messages, with the
// input named the way its caller knows it.

// erpInput says how a message names an input: the commands read flags
// (--bank-account), the MCP tools arguments (bank_account). The zero value
// is the commands.
type erpInput struct{ tool bool }

var erpArgs = erpInput{tool: true}

// name is the flag or argument called n (flags are spelled with dashes,
// arguments with underscores).
func (in erpInput) name(n string) string {
	if in.tool {
		return strings.ReplaceAll(n, "-", "_")
	}
	return "--" + n
}

// apiHint says where to go for a method ffc does not know.
func (in erpInput) apiHint() string {
	if in.tool {
		return "call its methods with call_method"
	}
	return "call its methods with 'ffc api' or 'ffc call-method'"
}

// erpOpt is an optional text input: set tells whether the caller gave it
// (for a flag, whether it was on the command line, even when blank).
type erpOpt struct {
	set bool
	val string
}

// flagOpt reads an optional flag.
func flagOpt(cmd *cobra.Command, flag, value string) erpOpt {
	return erpOpt{set: cmd.Flags().Changed(flag), val: value}
}

// text checks an optional text input: given means not blank.
func (in erpInput) text(name string, o erpOpt) (string, error) {
	if !o.set {
		return "", nil
	}
	v := strings.TrimSpace(o.val)
	if v == "" {
		return "", usageErrorf("%s: provide a value", in.name(name))
	}
	return v, nil
}

// date checks an optional YYYY-MM-DD input.
func (in erpInput) date(name string, o erpOpt) (string, error) {
	if !o.set {
		return "", nil
	}
	d := strings.TrimSpace(o.val)
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", usageErrorf("%s: expected YYYY-MM-DD, got %q", in.name(name), o.val)
	}
	return d, nil
}

// number checks an optional positive number written as plain decimal text:
// the text is sent as it is, so it must read as the number it is.
func (in erpInput) number(name, example string, o erpOpt) (string, error) {
	if !o.set {
		return "", nil
	}
	a := strings.TrimSpace(o.val)
	if v, err := strconv.ParseFloat(a, 64); !plainAmount.MatchString(a) || err != nil || v <= 0 {
		return "", usageErrorf("%s: expected a positive number such as %s, got %q", in.name(name), example, o.val)
	}
	return a, nil
}

// checkItemCode checks the Item code.
func checkItemCode(raw string) (string, error) {
	item := strings.TrimSpace(raw)
	if item == "" {
		return "", usageErrorf("provide the Item code")
	}
	return item, nil
}

// checkPayDoctype refuses a DocType a Payment Entry cannot be made against;
// label names the input in the message.
func checkPayDoctype(label, doctype string) error {
	if !client.CanPayAgainst(doctype) {
		return usageErrorf("%s: cannot make a payment against %s; supported: %s", label, doctype, strings.Join(client.PaymentDocTypes, ", "))
	}
	return nil
}

// buildPaymentOptions checks the optional inputs of a payment. An input that
// was given and is blank or invalid is a usage error; one that was not given
// is not sent.
func buildPaymentOptions(in erpInput, amount, bankAccount, referenceDate erpOpt) (client.PaymentOptions, error) {
	var o client.PaymentOptions
	var err error
	if o.Amount, err = in.number("amount", "50 or 12.5", amount); err != nil {
		return o, err
	}
	if bankAccount.set {
		if o.BankAccount = strings.TrimSpace(bankAccount.val); o.BankAccount == "" {
			return o, usageErrorf("%s: provide the name of an Account", in.name("bank-account"))
		}
	}
	if o.ReferenceDate, err = in.date("reference-date", referenceDate); err != nil {
		return o, err
	}
	return o, nil
}

// itemInput is what an item lookup is asked for, before it is checked.
type itemInput struct {
	company                                                      string
	doctype, customer, supplier, priceList, qty, warehouse, date erpOpt
}

// buildItemOptions checks the inputs of an item lookup.
func buildItemOptions(in erpInput, item string, a itemInput) (client.ItemOptions, error) {
	o := client.ItemOptions{ItemCode: item}
	var err error
	if o.Company = strings.TrimSpace(a.company); o.Company == "" {
		return o, usageErrorf("%s: provide the Company", in.name("company"))
	}
	if o.Customer, err = in.text("customer", a.customer); err != nil {
		return o, err
	}
	if o.Supplier, err = in.text("supplier", a.supplier); err != nil {
		return o, err
	}
	if o.Customer != "" && o.Supplier != "" {
		return o, usageErrorf("%s and %s cannot be used together", in.name("customer"), in.name("supplier"))
	}
	if o.Doctype, err = in.text("doctype", a.doctype); err != nil {
		return o, err
	}
	sales, purchase := slices.Contains(client.ItemSalesDoctypes, o.Doctype), slices.Contains(client.ItemPurchaseDoctypes, o.Doctype)
	switch {
	case o.Doctype == "":
		o.Doctype = "Sales Invoice"
		if o.Supplier != "" {
			o.Doctype = "Purchase Invoice"
		}
	case !sales && !purchase:
		return o, usageErrorf("%s: cannot look up an item for %s; supported: %s, %s", in.name("doctype"), o.Doctype,
			strings.Join(client.ItemSalesDoctypes, ", "), strings.Join(client.ItemPurchaseDoctypes, ", "))
	case o.Customer != "" && !sales:
		return o, usageErrorf("%s: %s is a purchase DocType, use %s", in.name("customer"), o.Doctype, in.name("supplier"))
	case o.Supplier != "" && !purchase:
		return o, usageErrorf("%s: %s is a sales DocType, use %s", in.name("supplier"), o.Doctype, in.name("customer"))
	}
	if o.PriceList, err = in.text("price-list", a.priceList); err != nil {
		return o, err
	}
	if o.Warehouse, err = in.text("warehouse", a.warehouse); err != nil {
		return o, err
	}
	if o.Date, err = in.date("date", a.date); err != nil {
		return o, err
	}
	if o.Qty, err = in.number("qty", "5 or 2.5", a.qty); err != nil {
		return o, err
	}
	return o, nil
}

// stockQuery is a stock lookup: one warehouse's balance, or with no
// Warehouse every warehouse's rows.
type stockQuery struct {
	item, warehouse, date string
	valuation             bool
}

// buildStockQuery checks the inputs of a stock lookup.
func buildStockQuery(in erpInput, item string, warehouse, date erpOpt, valuation bool) (stockQuery, error) {
	q := stockQuery{item: item, valuation: valuation}
	var err error
	if q.warehouse, err = in.text("warehouse", warehouse); err != nil {
		return q, err
	}
	if q.date, err = in.date("date", date); err != nil {
		return q, err
	}
	if q.warehouse == "" && (q.date != "" || valuation) {
		return q, usageErrorf("%s and %s need %s: without one the rows show the current stock and their valuation rate", in.name("date"), in.name("valuation"), in.name("warehouse"))
	}
	return q, nil
}

// buildPartyOptions checks the inputs of a party lookup.
func buildPartyOptions(in erpInput, customer, supplier, company, doctype, date erpOpt) (client.PartyOptions, error) {
	var o client.PartyOptions
	c, err := in.text("customer", customer)
	if err != nil {
		return o, err
	}
	s, err := in.text("supplier", supplier)
	if err != nil {
		return o, err
	}
	switch {
	case c != "" && s != "":
		return o, usageErrorf("%s and %s cannot be used together", in.name("customer"), in.name("supplier"))
	case c != "":
		o.PartyType, o.Party = "Customer", c
	case s != "":
		o.PartyType, o.Party = "Supplier", s
	default:
		return o, usageErrorf("provide %s or %s", in.name("customer"), in.name("supplier"))
	}
	if o.Company, err = in.text("company", company); err != nil {
		return o, err
	}
	if o.Doctype, err = in.text("doctype", doctype); err != nil {
		return o, err
	}
	if o.Date, err = in.date("date", date); err != nil {
		return o, err
	}
	return o, nil
}

// paymentWarnings are the notes on a Payment Entry draft: gap is the missing
// bank or cash account (also the reason a draft cannot be saved), and the
// bank account override.
func paymentWarnings(in erpInput, draft map[string]interface{}, wantBank string) (warnings []string, gap string) {
	if gap = paymentAccountGap(in, draft); gap != "" {
		warnings = append(warnings, gap)
	}
	if w := bankAccountOverride(in, draft, wantBank); w != "" {
		warnings = append(warnings, w)
	}
	return warnings, gap
}

// bankAccountOverride says so when the draft does not use the bank account
// asked for. get_payment_entry lets the Mode of Payment of the document
// override the account (get_default_bank_cash_account: the account of the
// mode wins), so a document with a mode_of_payment pays from or into that
// mode's account.
func bankAccountOverride(in erpInput, draft map[string]interface{}, want string) string {
	if want == "" {
		return ""
	}
	field := map[string]string{"Receive": "paid_to", "Pay": "paid_from"}[fmt.Sprint(draft["payment_type"])]
	if field == "" {
		return ""
	}
	got, _ := draft[field].(string)
	if got == want {
		return ""
	}
	return fmt.Sprintf("%s %q was not used: the draft has %s %q (the Mode of Payment of the document takes precedence over %s)", in.name("bank-account"), want, field, got, in.name("bank-account"))
}

// paymentAccountGap says what is missing when the draft has no bank or cash
// account. get_payment_entry does not throw then: it answers a draft whose
// paid_from or paid_to is empty (both are required), which the insert would
// refuse as a missing value.
func paymentAccountGap(in erpInput, draft map[string]interface{}) string {
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
	return fmt.Sprintf("the Payment Entry draft has no bank or cash account (%s is empty): set the default bank or cash account of %s, or pass %s", strings.Join(missing, ", "), company, in.name("bank-account"))
}
