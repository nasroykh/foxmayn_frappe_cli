package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// The erp tool set: the read side of `ffc erp`. ERPNext 15 and 16 build a
// document (a mapped draft, a Payment Entry) or answer a lookup through
// GET-safe methods that write nothing, so every tool here is a read. A draft
// comes back as data; saving and submitting it is create_doc and submit_doc,
// so confirmation, policy and audit stay in one place. The logic and the
// validation are the commands' (erp.go, erp_map.go, erp_payment.go,
// erp_lookup.go, erp_input.go).

// erpDoctypeArgs are the erp tools whose `doctype` argument names the
// document a row is for (a lookup), not a document the call reads: it picks
// the item row's meta and defaults, so it is not part of the policy scope.
var erpDoctypeArgs = map[string]bool{"erp_item": true, "erp_party": true}

// erpScope returns the DocTypes and the documents an erp tool call reads,
// for the DocType rules. The tool's own parse step has validated the
// arguments; a missing or blank one adds nothing. It lists what ffc and the
// server methods are known to read beyond the named documents: the item
// lookup answers stock levels from Bin and prices from a Price List (the
// default one from Selling/Buying Settings when none is given), and the
// party lookup answers address and contact fields, all read without a
// permission check of their own (get_item_details.py, party.py). The party of
// a payment (Customer, Supplier) is covered through the source document: it
// must be readable, and the draft only repeats its fields.
func erpScope(tool string, args map[string]interface{}) (doctypes, names []string) {
	str := func(k string) string {
		s, _ := docName(args[k])
		return strings.TrimSpace(s)
	}
	add := func(list *[]string, vals ...string) {
		for _, v := range vals {
			if v != "" {
				*list = append(*list, v)
			}
		}
	}
	switch tool {
	case "erp_map":
		add(&doctypes, str("from_doctype"), str("to_doctype"))
		if str("from_doctype") == "Quotation" {
			doctypes = append(doctypes, "Customer") // refuseLeadQuotation looks for the lead's Customer
		}
		add(&names, str("from_name"))
	case "erp_payment":
		add(&doctypes, str("against_doctype"), "Payment Entry")
		if str("bank_account") != "" {
			doctypes = append(doctypes, "Account")
		}
		add(&names, str("against_name"))
	case "erp_item":
		doctypes = append(doctypes, "Item")
		if str("customer") != "" {
			doctypes = append(doctypes, "Customer")
		}
		if str("supplier") != "" {
			doctypes = append(doctypes, "Supplier")
		}
		doctypes = append(doctypes, "Price List") // given, or the default of the settings
		if str("warehouse") != "" {
			doctypes = append(doctypes, "Warehouse")
		}
		doctypes = append(doctypes, "Bin") // actual_qty, projected_qty
		add(&names, str("item_code"))
	case "erp_stock":
		doctypes = append(doctypes, "Item", "Warehouse", "Bin")
		add(&names, str("item_code"))
	case "erp_party":
		if str("customer") != "" {
			doctypes = append(doctypes, "Customer")
		}
		if str("supplier") != "" {
			doctypes = append(doctypes, "Supplier")
		}
		doctypes = append(doctypes, "Address", "Contact") // address_display, contact_email/mobile/phone
		add(&names, str("customer"), str("supplier"))
	}
	return doctypes, names
}

// erpSite is the site config the call runs against (env.run sets it): the
// ERPNext version is cached per site.
func erpSite(ctx context.Context) (*config.SiteConfig, error) {
	site, ok := siteFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("erp: the site of the call is unknown")
	}
	return site, nil
}

// erpOptArg reads an optional text argument; blank counts as not given, so
// a client that sends "" for what it leaves out is not refused.
func erpOptArg(req mcp.CallToolRequest, key string) (erpOpt, error) {
	s, err := nameArg(req, key, false)
	if err != nil {
		return erpOpt{}, err
	}
	s = strings.TrimSpace(s)
	return erpOpt{set: s != "", val: s}, nil
}

// erpNumberArg reads an optional number argument (a JSON number or text)
// as the text that is sent; blank counts as not given.
func erpNumberArg(req mcp.CallToolRequest, key string) (erpOpt, error) {
	var s string
	switch v := req.GetArguments()[key].(type) {
	case nil:
	case string:
		s = strings.TrimSpace(v)
	case float64:
		s = strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		s = v.String()
	case int:
		s = strconv.Itoa(v)
	default:
		return erpOpt{}, fmt.Errorf("%s: expected a number", key)
	}
	return erpOpt{set: s != "", val: s}, nil
}

// erpOptArgs reads several optional text arguments in order.
func erpOptArgs(req mcp.CallToolRequest, keys ...string) ([]erpOpt, error) {
	out := make([]erpOpt, len(keys))
	for i, k := range keys {
		o, err := erpOptArg(req, k)
		if err != nil {
			return nil, err
		}
		out[i] = o
	}
	return out, nil
}

// erpDraftResult is what a draft tool answers: the draft as create_doc takes
// it (InsertableCopy: no `__` keys, no empty name) and the notes about it.
func erpDraftResult(draft map[string]interface{}, warnings []string) map[string]interface{} {
	out := map[string]interface{}{"draft": client.InsertableCopy(draft)}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	return out
}

func registerERPTools(s *server.MCPServer, env *mcpEnv) {
	ro := mcp.WithReadOnlyHintAnnotation(true)
	idem := mcp.WithIdempotentHintAnnotation(true)
	open := mcp.WithOpenWorldHintAnnotation(true)

	s.AddTool(mcp.NewTool("erp_map",
		mcp.WithDescription("Map an ERPNext document into the next one in its chain, as the desk's Create button does (items, taxes and links come across): Quotation to Sales Order; Sales Order to Sales Invoice or Delivery Note; Delivery Note to Sales Invoice; Purchase Order to Purchase Receipt or Purchase Invoice; Purchase Receipt to Purchase Invoice; Material Request to Purchase Order. ERPNext 15 or 16. Returns {draft}: an UNSAVED document, nothing is written. Save it with create_doc (doctype = to_doctype, data = draft), then submit_doc if it should be final. A Quotation made out to a Lead or Prospect with no customer is refused: convert the lead first."),
		ro, idem, open,
		mcp.WithString("from_doctype", mcp.Required(), mcp.Description("DocType of the source document, e.g. 'Sales Order'")),
		mcp.WithString("from_name", mcp.Required(), mcp.Description("Name of the source document, e.g. 'SAL-ORD-2026-00001'")),
		mcp.WithString("to_doctype", mcp.Required(), mcp.Description("DocType to map into, e.g. 'Sales Invoice'")),
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		from, err := nameArg(req, "from_doctype", true)
		if err != nil {
			return nil, err
		}
		name, err := nameArg(req, "from_name", true)
		if err != nil {
			return nil, err
		}
		to, err := nameArg(req, "to_doctype", true)
		if err != nil {
			return nil, err
		}
		r := mapRequest{from: strings.TrimSpace(from), name: strings.TrimSpace(name), to: strings.TrimSpace(to), in: erpArgs}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			site, err := erpSite(ctx)
			if err != nil {
				return nil, err
			}
			res, err := runERPMap(ctx, c, site, r)
			if err != nil {
				return nil, err
			}
			return erpDraftResult(res.doc, nil), nil
		}, nil
	}))

	s.AddTool(mcp.NewTool("erp_payment",
		mcp.WithDescription("Make a Payment Entry against a Sales Invoice, Sales Order, Purchase Invoice, Purchase Order or Dunning, as the desk's Create > Payment button does: ERPNext picks the party account, the bank or cash account, the payment type and the amount still outstanding. ERPNext 15 or 16. Returns {draft, warnings?}: an UNSAVED Payment Entry, nothing is written. Save it with create_doc (doctype 'Payment Entry', data = draft), then submit_doc. warnings says when the draft has no bank or cash account (set the Company's default or pass bank_account; the insert would fail) or when bank_account was not used because the document's Mode of Payment takes precedence. An order that is already fully billed is refused."),
		ro, idem, open,
		mcp.WithString("against_doctype", mcp.Required(), mcp.Description("Sales Invoice, Sales Order, Purchase Invoice, Purchase Order or Dunning")),
		mcp.WithString("against_name", mcp.Required(), mcp.Description("Name of the document to pay")),
		mcp.WithNumber("amount", mcp.Description("Pay this much instead of the outstanding amount (a positive number)")),
		mcp.WithString("bank_account", mcp.Description("Bank or cash Account to pay from or into (default: the Company's default)")),
		mcp.WithString("reference_date", mcp.Description("The date the payment is made for, YYYY-MM-DD (default today)")),
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		dt, err := nameArg(req, "against_doctype", true)
		if err != nil {
			return nil, err
		}
		name, err := nameArg(req, "against_name", true)
		if err != nil {
			return nil, err
		}
		dt, name = strings.TrimSpace(dt), strings.TrimSpace(name)
		if err := checkPayDoctype("against_doctype", dt); err != nil {
			return nil, err
		}
		amount, err := erpNumberArg(req, "amount")
		if err != nil {
			return nil, err
		}
		o, err := erpOptArgs(req, "bank_account", "reference_date")
		if err != nil {
			return nil, err
		}
		opts, err := buildPaymentOptions(erpArgs, amount, o[0], o[1])
		if err != nil {
			return nil, err
		}
		r := paymentRequest{doctype: dt, name: name, opts: opts, in: erpArgs}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			site, err := erpSite(ctx)
			if err != nil {
				return nil, err
			}
			draft, warnings, _, err := paymentDraft(ctx, c, site, r)
			if err != nil {
				return nil, err
			}
			return erpDraftResult(draft, warnings), nil
		}, nil
	}))

	s.AddTool(mcp.NewTool("erp_item",
		mcp.WithDescription("Look up what ERPNext fills into a document row when an item is picked, as the desk does: the rate (price list rate, pricing rules), UOM, warehouse, accounts, taxes and stock levels. Nothing is written. ERPNext 15 or 16. doctype is the document the row is for: a sales one (Quotation, Sales Order, Delivery Note, Sales Invoice) with customer, or a purchase one (Supplier Quotation, Purchase Order, Purchase Receipt, Purchase Invoice, Material Request) with supplier; default Sales Invoice, or Purchase Invoice with supplier. Prices are not converted between currencies. Returns the row's fields."),
		ro, idem, open,
		mcp.WithString("item_code", mcp.Required(), mcp.Description("The Item code")),
		mcp.WithString("company", mcp.Required(), mcp.Description("The Company the row is for")),
		mcp.WithString("doctype", mcp.Description("The document the row is for (default Sales Invoice, or Purchase Invoice with supplier)")),
		mcp.WithString("customer", mcp.Description("Customer, for customer prices (a sales doctype)")),
		mcp.WithString("supplier", mcp.Description("Supplier (a purchase doctype)")),
		mcp.WithString("price_list", mcp.Description("Price List to take the rate from")),
		mcp.WithNumber("qty", mcp.Description("Quantity (default 1; matters for quantity-based prices and packing units)")),
		mcp.WithString("warehouse", mcp.Description("Warehouse to use instead of the Item's default")),
		mcp.WithString("date", mcp.Description("Transaction date, YYYY-MM-DD (default today)")),
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		item, err := nameArg(req, "item_code", true)
		if err != nil {
			return nil, err
		}
		if item, err = checkItemCode(item); err != nil {
			return nil, err
		}
		company, err := nameArg(req, "company", false)
		if err != nil {
			return nil, err
		}
		o, err := erpOptArgs(req, "doctype", "customer", "supplier", "price_list", "warehouse", "date")
		if err != nil {
			return nil, err
		}
		qty, err := erpNumberArg(req, "qty")
		if err != nil {
			return nil, err
		}
		opts, err := buildItemOptions(erpArgs, item, itemInput{
			company: company, doctype: o[0], customer: o[1], supplier: o[2], priceList: o[3], warehouse: o[4], date: o[5], qty: qty,
		})
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			site, err := erpSite(ctx)
			if err != nil {
				return nil, err
			}
			return runERPItem(ctx, c, site, erpArgs, opts)
		}, nil
	}))

	s.AddTool(mcp.NewTool("erp_stock",
		mcp.WithDescription("Show an item's stock. With warehouse: the balance there (actual_qty, and valuation_rate with valuation), now or, with date, at the end of that day. Without warehouse: {item_code, rows, truncated}, one row per warehouse that holds any quantity or has a reservation, order or plan (actual, reserved, projected quantity, valuation rate), at most 504 rows. date and valuation need warehouse. The Item, and the Warehouse when given, must exist (a typo is not-found, not zero). Quantities keep the number the site sends. ERPNext 15 or 16."),
		ro, idem, open,
		mcp.WithString("item_code", mcp.Required(), mcp.Description("The Item code")),
		mcp.WithString("warehouse", mcp.Description("Show the balance in this Warehouse (default: every warehouse)")),
		mcp.WithString("date", mcp.Description("Balance as of the end of this day, YYYY-MM-DD (needs warehouse)")),
		mcp.WithBoolean("valuation", mcp.Description("Include the valuation rate (needs warehouse)")),
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		item, err := nameArg(req, "item_code", true)
		if err != nil {
			return nil, err
		}
		if item, err = checkItemCode(item); err != nil {
			return nil, err
		}
		o, err := erpOptArgs(req, "warehouse", "date")
		if err != nil {
			return nil, err
		}
		valuation, err := strictBoolArg(req, "valuation")
		if err != nil {
			return nil, err
		}
		q, err := buildStockQuery(erpArgs, item, o[0], o[1], valuation)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			site, err := erpSite(ctx)
			if err != nil {
				return nil, err
			}
			res, err := runERPStock(ctx, c, site, erpArgs, q)
			if err != nil {
				return nil, err
			}
			if res.doc != nil {
				return res.doc, nil
			}
			out := map[string]interface{}{"item_code": q.item, "rows": res.rows, "truncated": res.truncated}
			if res.truncated {
				out["warning"] = stockTruncatedNote(erpArgs)
			}
			return out, nil
		}, nil
	}))

	s.AddTool(mcp.NewTool("erp_party",
		mcp.WithDescription("Look up what ERPNext fills into a document when a customer or supplier is picked, as the desk does: address and contact, price list, currency, taxes template, payment terms and, with an invoice doctype (Sales Invoice, Purchase Invoice), the receivable or payable account and the due date. Nothing is written. ERPNext 15 or 16. Give customer or supplier, not both; company is optional but the accounts, taxes and payment terms depend on it. Returns the fields."),
		ro, idem, open,
		mcp.WithString("customer", mcp.Description("Customer to look up")),
		mcp.WithString("supplier", mcp.Description("Supplier to look up")),
		mcp.WithString("company", mcp.Description("Company the document is for")),
		mcp.WithString("date", mcp.Description("Posting date, YYYY-MM-DD (default today; sets the due date)")),
		mcp.WithString("doctype", mcp.Description("Document the party is for; an invoice adds the account and due date")),
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		o, err := erpOptArgs(req, "customer", "supplier", "company", "doctype", "date")
		if err != nil {
			return nil, err
		}
		opts, err := buildPartyOptions(erpArgs, o[0], o[1], o[2], o[3], o[4])
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			site, err := erpSite(ctx)
			if err != nil {
				return nil, err
			}
			return runERPParty(ctx, c, site, erpArgs, opts)
		}, nil
	}))
}
