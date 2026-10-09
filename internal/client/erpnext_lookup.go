package client

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

// ERPNext lookups: read-only methods the desk calls to fill a form. All are
// GETs, none sets methods=, and none of the arguments below is
// ignore_permissions (which ffc never sends).

// LookupMethods are the lookup methods of one ERPNext major.
type LookupMethods struct {
	// Item is get_item_details; ItemParam names its first parameter: `args`
	// on v15 (get_item_details at v15.121.6:60), `ctx` on v16 (v16.37.0:85,
	// no alias for the old name).
	Item, ItemParam string
	// Stock is get_stock_balance (utils.py:98 on v15, :96 on v16).
	Stock string
	// Dashboard is item_dashboard.get_data: one row per Bin, 21 per page.
	Dashboard string
	// Party is get_party_details (party.py:77 on v15, :78 on v16).
	Party string
}

const (
	itemDetailsMethod    = "erpnext.stock.get_item_details.get_item_details"
	stockBalanceMethod   = "erpnext.stock.utils.get_stock_balance"
	stockDashboardMethod = "erpnext.stock.dashboard.item_dashboard.get_data"
	partyDetailsMethod   = "erpnext.accounts.party.get_party_details"
)

// erpLookups is the lookup table by ERPNext major; a new major is one row.
var erpLookups = map[int]LookupMethods{
	15: {Item: itemDetailsMethod, ItemParam: "args", Stock: stockBalanceMethod, Dashboard: stockDashboardMethod, Party: partyDetailsMethod},
	16: {Item: itemDetailsMethod, ItemParam: "ctx", Stock: stockBalanceMethod, Dashboard: stockDashboardMethod, Party: partyDetailsMethod},
}

// LookupFor returns the lookup methods of an ERPNext major.
func LookupFor(major int) (LookupMethods, bool) {
	m, ok := erpLookups[major]
	return m, ok
}

// ItemDoctypes are the DocTypes get_item_details can price an item for: it
// reads the meta of "<DocType> Item", which any other name does not have.
// Sales and Purchase say which side a DocType is on.
var (
	ItemSalesDoctypes    = []string{"Quotation", "Sales Order", "Delivery Note", "Sales Invoice"}
	ItemPurchaseDoctypes = []string{"Supplier Quotation", "Purchase Order", "Purchase Receipt", "Purchase Invoice", "Material Request"}
)

// ItemOptions are the arguments of an item lookup. Company, Doctype and
// ItemCode are required by get_item_details ("Please specify Company", the
// meta of "<Doctype> Item"); empty optional fields are not sent.
type ItemOptions struct {
	Company, Doctype, ItemCode string
	Customer, Supplier         string
	PriceList                  string
	Warehouse                  string // sent as set_warehouse: the Item's own defaults outrank `warehouse`
	Date                       string // transaction_date, YYYY-MM-DD
	Qty                        string // a plain positive number; default 1
}

// dict builds the dict get_item_details takes as its first parameter. Both
// conversion rates are 1: with a currency missing, ERPNext asks the exchange
// rate service (an outbound request) for any other rate, and throws
// "Exchange Rate is mandatory" for none. Prices in another currency than the
// company's are therefore not converted. `rate` is never sent: with a price
// list and a rate, ERPNext may insert an Item Price.
func (o ItemOptions) dict() map[string]interface{} {
	qty := json.Number("1")
	if o.Qty != "" {
		qty = json.Number(o.Qty)
	}
	d := map[string]interface{}{
		"company": o.Company, "doctype": o.Doctype, "item_code": o.ItemCode, "qty": qty,
		"conversion_rate": 1, "plc_conversion_rate": 1,
	}
	for k, v := range map[string]string{
		"customer": o.Customer, "supplier": o.Supplier, "price_list": o.PriceList,
		"set_warehouse": o.Warehouse, "transaction_date": o.Date,
	} {
		if v != "" {
			d[k] = v
		}
	}
	return d
}

// ItemDetails runs get_item_details for an item. The dict goes in `args` on
// v15 and `ctx` on v16 (m.ItemParam). The call is a GET; it answers the
// fields of a document row (rate, price_list_rate, uom, warehouse, accounts,
// stock levels).
func (c *FrappeClient) ItemDetails(ctx context.Context, m LookupMethods, o ItemOptions) (map[string]interface{}, error) {
	return c.draftDoc(ctx, m.Item, map[string]interface{}{m.ItemParam: o.dict()})
}

// StockBalance runs get_stock_balance for an item in a warehouse. date is
// YYYY-MM-DD ("" = now); a date means the end of that day, so the balance
// includes every entry posted on it. It answers actual_qty and, with
// valuation, valuation_rate; both keep the literal Frappe sent (0.0). Only
// Item read permission is checked by ERPNext, not the warehouse.
func (c *FrappeClient) StockBalance(ctx context.Context, m LookupMethods, item, warehouse, date string, valuation bool) (map[string]interface{}, error) {
	args := map[string]interface{}{"item_code": item, "warehouse": warehouse}
	if date != "" {
		args["posting_date"] = date
		args["posting_time"] = "23:59:59"
	}
	if valuation {
		args["with_valuation_rate"] = true
	}
	res, err := c.CallMethod(ctx, m.Stock, args, true)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{"item_code": item, "warehouse": warehouse}
	if date != "" {
		out["posting_date"] = date
	}
	if !valuation {
		qty, ok := res.(json.Number)
		if !ok {
			return nil, fmt.Errorf("unexpected response from %s: expected a number, got %T", m.Stock, res)
		}
		out["actual_qty"] = qty
		return out, nil
	}
	pair, ok := res.([]interface{})
	if !ok || len(pair) != 2 {
		return nil, fmt.Errorf("unexpected response from %s: expected [qty, valuation rate], got %v", m.Stock, res)
	}
	qty, qok := pair[0].(json.Number)
	rate, rok := pair[1].(json.Number)
	if !qok || !rok {
		return nil, fmt.Errorf("unexpected response from %s: expected numbers, got %v", m.Stock, res)
	}
	out["actual_qty"], out["valuation_rate"] = qty, rate
	return out, nil
}

// StockPageSize is how many Bin rows the item dashboard answers per call
// (limit_page_length=21 on v15 and v16), and StockMaxPages how many pages
// StockByWarehouse reads: 24 pages, 504 warehouses.
const (
	StockPageSize = 21
	StockMaxPages = 24
)

// stockTextFields are the strings the dashboard passes through escape_html;
// StockByWarehouse undoes that, so a warehouse "R&D - A" reads as itself and
// can be passed back to --warehouse.
var stockTextFields = []string{"item_code", "item_name", "stock_uom", "warehouse"}

// StockByWarehouse lists an item's stock per warehouse with the item
// dashboard's get_data: one row per Bin that has any quantity, read in
// pages of StockPageSize (start) and ordered by warehouse so the pages do
// not overlap. truncated is true when it stopped at StockMaxPages with a
// full last page. v15 does not check Bin read permission (it lists with
// get_all, bounded only by Warehouse user permissions); v16 answers an
// empty list without it.
func (c *FrappeClient) StockByWarehouse(ctx context.Context, m LookupMethods, item string) (rows []map[string]interface{}, truncated bool, err error) {
	rows = []map[string]interface{}{} // an empty answer is [], not null
	for page := 0; page < StockMaxPages; page++ {
		res, err := c.CallMethod(ctx, m.Dashboard, map[string]interface{}{
			"item_code": item, "start": page * StockPageSize, "sort_by": "warehouse", "sort_order": "asc",
		}, true)
		if err != nil {
			return nil, false, err
		}
		list, ok := res.([]interface{})
		if !ok {
			return nil, false, fmt.Errorf("unexpected response from %s: expected a list, got %T", m.Dashboard, res)
		}
		for _, r := range list {
			row, ok := r.(map[string]interface{})
			if !ok {
				return nil, false, fmt.Errorf("unexpected response from %s: expected rows, got %T", m.Dashboard, r)
			}
			for _, f := range stockTextFields {
				if s, ok := row[f].(string); ok {
					row[f] = html.UnescapeString(s)
				}
			}
			rows = append(rows, row)
		}
		if len(list) < StockPageSize {
			return rows, false, nil
		}
	}
	return rows, true, nil
}

// PartyOptions are the arguments of a party lookup; empty fields are not
// sent. PartyType is "Customer" or "Supplier".
type PartyOptions struct {
	PartyType, Party string
	Company          string
	Date             string // posting_date, YYYY-MM-DD
	Doctype          string // an invoice DocType adds the account and the due date
}

// PartyDetails runs get_party_details: the defaults a document takes from a
// customer or supplier (addresses, contact, price list, taxes, payment terms
// and, for an invoice DocType, debit_to or credit_to and due_date). A GET;
// ERPNext checks read permission on the party (v16's ignore_permissions
// parameter is never sent).
func (c *FrappeClient) PartyDetails(ctx context.Context, m LookupMethods, o PartyOptions) (map[string]interface{}, error) {
	args := map[string]interface{}{"party": o.Party, "party_type": o.PartyType}
	for k, v := range map[string]string{"company": o.Company, "posting_date": o.Date, "doctype": o.Doctype} {
		if strings.TrimSpace(v) != "" {
			args[k] = v
		}
	}
	return c.draftDoc(ctx, m.Party, args)
}
