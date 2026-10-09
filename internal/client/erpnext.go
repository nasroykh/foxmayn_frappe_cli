package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ERPNext helpers: document mappers, called through the whitelisted methods
// ERPNext itself offers. The desk uses the same methods ("Create > Sales
// Invoice"), so permissions, naming and hooks stay the server's.

// ERPNextApp is the name get_versions gives the ERPNext app.
const ERPNextApp = "erpnext"

// MapPair is a source and a target DocType of a mapper.
type MapPair struct{ From, To string }

func (p MapPair) String() string { return p.From + " -> " + p.To }

// erpMapperMethods are the mappers (source_name, target_doc=None, ...) that
// return one unsaved target document. They are the same on v15 and v16
// (source of v15.121.6 and v16.37.0). Sales Order -> Purchase Order is left
// out on purpose: it returns a list of documents, one per supplier.
var erpMapperMethods = map[MapPair]string{
	{"Quotation", "Sales Order"}:             "erpnext.selling.doctype.quotation.quotation.make_sales_order",
	{"Sales Order", "Sales Invoice"}:         "erpnext.selling.doctype.sales_order.sales_order.make_sales_invoice",
	{"Sales Order", "Delivery Note"}:         "erpnext.selling.doctype.sales_order.sales_order.make_delivery_note",
	{"Delivery Note", "Sales Invoice"}:       "erpnext.stock.doctype.delivery_note.delivery_note.make_sales_invoice",
	{"Purchase Order", "Purchase Receipt"}:   "erpnext.buying.doctype.purchase_order.purchase_order.make_purchase_receipt",
	{"Purchase Order", "Purchase Invoice"}:   "erpnext.buying.doctype.purchase_order.purchase_order.make_purchase_invoice",
	{"Purchase Receipt", "Purchase Invoice"}: "erpnext.stock.doctype.purchase_receipt.purchase_receipt.make_purchase_invoice",
	{"Material Request", "Purchase Order"}:   "erpnext.stock.doctype.material_request.material_request.make_purchase_order",
}

// erpMappers is the mapper table by ERPNext major. Develop (17) moved most
// mappers into <doctype>/mapper.py, so a newer major is refused rather than
// guessed at; adding one is a row here.
var erpMappers = map[int]map[MapPair]string{
	15: erpMapperMethods,
	16: erpMapperMethods,
}

// SupportedERPNext reports whether ffc knows the ERPNext helpers of a major.
func SupportedERPNext(major int) bool {
	_, ok := erpMappers[major]
	return ok
}

// MapMethod returns the method that maps a document of from into one of to
// on the given ERPNext major.
func MapMethod(major int, from, to string) (string, bool) {
	m, ok := erpMappers[major][MapPair{From: from, To: to}]
	return m, ok
}

// MapPairs returns the pairs ffc can map on an ERPNext major, sorted.
func MapPairs(major int) []MapPair {
	var out []MapPair
	for p := range erpMappers[major] {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// MapPairsText lists the pairs of a major for an error message.
func MapPairsText(major int) string {
	pairs := MapPairs(major)
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.String()
	}
	return strings.Join(parts, "; ")
}

// Major returns the major version of an installed app (15, 16), or 0 when it
// is not installed or the version is unknown.
func (s *ServerInfo) Major(app string) int { return ParseMajor(s.Version(app)) }

// MapDoc runs a mapper on a source document and returns the unsaved target
// document (it carries __islocal and no real name). The call is a GET: no
// mapper sets methods=, and a GET never commits, so nothing is kept. Only
// source_name is sent; ignore_permissions, which some mappers take, never is.
func (c *FrappeClient) MapDoc(ctx context.Context, method, source string) (map[string]interface{}, error) {
	return c.draftDoc(ctx, method, map[string]interface{}{"source_name": source})
}

// draftDoc calls a GET-safe ERPNext method that answers one unsaved document.
func (c *FrappeClient) draftDoc(ctx context.Context, method string, args map[string]interface{}) (map[string]interface{}, error) {
	res, err := c.CallMethod(ctx, method, args, true)
	if err != nil {
		return nil, err
	}
	doc, ok := res.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response from %s: expected a document, got %T", method, res)
	}
	return doc, nil
}

// paymentEntryMethod builds a Payment Entry from an invoice or an order. It
// is the same on v15 and v16 (get_payment_entry at v15.121.6:2901 and
// v16.37.0:2898); it never inserts, and has no methods=, so a GET is allowed.
const paymentEntryMethod = "erpnext.accounts.doctype.payment_entry.payment_entry.get_payment_entry"

// erpPaymentMethods is the Payment Entry method by ERPNext major.
var erpPaymentMethods = map[int]string{
	15: paymentEntryMethod,
	16: paymentEntryMethod,
}

// PaymentDocTypes are the DocTypes get_payment_entry handles as dt
// (set_party_type fails with a server error for any other).
var PaymentDocTypes = []string{"Sales Invoice", "Sales Order", "Purchase Invoice", "Purchase Order", "Dunning"}

// CanPayAgainst reports whether a Payment Entry can be made against a DocType.
func CanPayAgainst(doctype string) bool {
	for _, d := range PaymentDocTypes {
		if d == doctype {
			return true
		}
	}
	return false
}

// PaymentMethod returns the method that builds a Payment Entry on an
// ERPNext major.
func PaymentMethod(major int) (string, bool) {
	m, ok := erpPaymentMethods[major]
	return m, ok
}

// PaymentOptions are the optional arguments of get_payment_entry; an empty
// one is not sent, so the server picks its own default.
type PaymentOptions struct {
	Amount        string // party_amount: a plain positive decimal
	BankAccount   string // bank_account: an Account of the company
	ReferenceDate string // reference_date: YYYY-MM-DD
}

// PaymentDraft builds an unsaved Payment Entry against a document (dt, dn).
// The call is a GET and inserts nothing. Only the arguments given are sent;
// ignore_permissions is not a parameter of the method and never is sent.
func (c *FrappeClient) PaymentDraft(ctx context.Context, method, doctype, name string, o PaymentOptions) (map[string]interface{}, error) {
	args := map[string]interface{}{"dt": doctype, "dn": name}
	if o.Amount != "" {
		args["party_amount"] = json.Number(o.Amount)
	}
	if o.BankAccount != "" {
		args["bank_account"] = o.BankAccount
	}
	if o.ReferenceDate != "" {
		args["reference_date"] = o.ReferenceDate
	}
	return c.draftDoc(ctx, method, args)
}

// InsertableCopy returns a copy of a mapped document that can be inserted:
// keys starting with "__" (__islocal, __temporary_name, ...) are removed from
// it and from its child rows, and so is a null or empty name (the server
// names the document). Child rows keep parent, parenttype, parentfield and
// idx. Only lists of documents (items with a "doctype") are walked, so a
// JSON field is left as it is.
func InsertableCopy(doc map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(doc))
	for k, v := range doc {
		switch {
		case strings.HasPrefix(k, "__"):
		case k == "name" && (v == nil || v == ""):
		default:
			out[k] = insertableRows(v)
		}
	}
	return out
}

// insertableRows applies InsertableCopy to a child table value: a list whose
// items are all documents. Anything else is returned as it is.
func insertableRows(v interface{}) interface{} {
	rows, ok := v.([]interface{})
	if !ok || len(rows) == 0 {
		return v
	}
	out := make([]interface{}, len(rows))
	for i, r := range rows {
		m, ok := r.(map[string]interface{})
		if _, hasType := m["doctype"]; !ok || !hasType {
			return v
		}
		out[i] = InsertableCopy(m)
	}
	return out
}
