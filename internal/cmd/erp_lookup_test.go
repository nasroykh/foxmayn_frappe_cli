package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const (
	erpTItemMethod      = "erpnext.stock.get_item_details.get_item_details"
	erpTStockMethod     = "erpnext.stock.utils.get_stock_balance"
	erpTDashboardMethod = "erpnext.stock.dashboard.item_dashboard.get_data"
	erpTPartyMethod     = "erpnext.accounts.party.get_party_details"
)

// erpLSite is erpTSite with the four lookup methods answering like ERPNext.
// Numbers are json.Number so the literal Frappe sends (5.0) is what the
// command prints. The dashboard holds warehouses rows ("Wh 001" ...) for the
// item ITEM-1, in the order of the sort ffc asks for, 21 per page.
func erpLSite(t *testing.T, erpnext string, warehouses int) *frappetest.Site {
	t.Helper()
	s := erpTSite(t, erpnext)
	s.Add("Item", map[string]interface{}{"name": "ITEM-1"})
	s.Add("Warehouse", map[string]interface{}{"name": "Stores - A"}, map[string]interface{}{"name": "W"})
	s.HandleMethod(erpTItemMethod, func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		d := erpLDict(args)
		if d["item_code"] != "ITEM-1" {
			return nil, frappetest.NotFound(fmt.Sprintf("Item %v not found", d["item_code"]))
		}
		return map[string]interface{}{
			"item_code": "ITEM-1", "item_name": "Item One", "rate": json.Number("100.0"), "price_list_rate": json.Number("100.0"),
			"qty": json.Number("1.0"), "uom": "Nos", "warehouse": "Stores - A",
		}, nil
	})
	s.HandleMethod(erpTStockMethod, func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		// Like ERPNext: an item or warehouse that does not exist is a balance of 0,
		// not an error (utils.py:98-143).
		known := args["item_code"] == "ITEM-1" && (args["warehouse"] == "Stores - A" || args["warehouse"] == "W")
		valuation, _ := args["with_valuation_rate"].(string)
		switch {
		case valuation == "true" && !known:
			return []interface{}{json.Number("0.0"), json.Number("0.0")}, nil
		case valuation == "true":
			return []interface{}{json.Number("5.0"), json.Number("12.5")}, nil
		case !known:
			return json.Number("0.0"), nil
		}
		return json.Number("5.0"), nil
	})
	s.HandleMethod(erpTDashboardMethod, func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		start, _ := strconv.Atoi(fmt.Sprint(args["start"]))
		rows := []interface{}{}
		if args["item_code"] != "ITEM-1" { // an unknown item has no Bins: [], not an error
			return rows, nil
		}
		for i := start; i < warehouses && len(rows) < 21; i++ {
			name := fmt.Sprintf("Wh %03d", i+1)
			if i == 0 {
				name = "R&amp;D - A" // escape_html
			}
			rows = append(rows, map[string]interface{}{
				"item_code": "ITEM-1", "item_name": "Item One", "stock_uom": "Nos", "warehouse": name,
				"actual_qty": json.Number("2.0"), "reserved_qty": json.Number("0.0"), "projected_qty": json.Number("2.0"), "valuation_rate": json.Number("10.0"),
			})
		}
		return rows, nil
	})
	s.HandleMethod(erpTPartyMethod, func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		if args["party"] != "CUST-1" && args["party"] != "SUP-1" {
			return nil, frappetest.NotFound(fmt.Sprintf("%v %v not found", args["party_type"], args["party"]))
		}
		return map[string]interface{}{"customer": args["party"], "currency": "USD", "price_list": "Standard Selling", "territory": "All Territories"}, nil
	})
	return s
}

// erpLDict is the dict an item lookup sent, from the `args` or `ctx` query
// parameter (a JSON string).
func erpLDict(args map[string]interface{}) map[string]interface{} {
	for _, k := range []string{"args", "ctx"} {
		var d map[string]interface{}
		switch v := args[k].(type) {
		case string:
			_ = json.Unmarshal([]byte(v), &d)
		case map[string]interface{}:
			d = v
		}
		if d != nil {
			return d
		}
	}
	return map[string]interface{}{}
}

func erpLRequests(s *frappetest.Site, method string) []frappetest.Request {
	return s.RequestsTo(http.MethodGet, "/api/method/"+method)
}

// erpLOne returns the only request to a method.
func erpLOne(t *testing.T, s *frappetest.Site, method string) url.Values {
	t.Helper()
	req := erpLRequests(s, method)
	if len(req) != 1 {
		t.Fatalf("requests to %s = %+v", method, req)
	}
	return req[0].Query
}

func erpLNoWrites(t *testing.T, s *frappetest.Site) {
	t.Helper()
	if w := erpTWrites(s); len(w) != 0 {
		t.Errorf("writes: %+v", w)
	}
}

func TestERPItemParameterPerMajor(t *testing.T) {
	for _, tc := range []struct{ version, param, other string }{{erpTV15, "args", "ctx"}, {erpTV16, "ctx", "args"}} {
		t.Run(tc.version, func(t *testing.T) {
			s := erpLSite(t, tc.version, 0)
			r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "item", "ITEM-1", "--company", "Acme"))
			got := cmdTObj(t, r)
			// Floats keep the literal Frappe sent.
			if got["item_code"] != "ITEM-1" || !strings.Contains(r.Stdout, `"rate": 100.0`) {
				t.Errorf("output = %s", r.Stdout)
			}
			q := erpLOne(t, s, erpTItemMethod)
			if q.Get(tc.param) == "" || q.Has(tc.other) || len(q) != 1 {
				t.Fatalf("query = %v, want only %s", q, tc.param)
			}
			var d map[string]interface{}
			if err := json.Unmarshal([]byte(q.Get(tc.param)), &d); err != nil {
				t.Fatal(err)
			}
			// A plain call: the required keys and the rates, nothing else.
			want := map[string]string{"company": "Acme", "doctype": "Sales Invoice", "item_code": "ITEM-1", "qty": "1", "conversion_rate": "1", "plc_conversion_rate": "1"}
			if len(d) != len(want) {
				t.Errorf("dict = %v", d)
			}
			for k, v := range want {
				if fmt.Sprint(d[k]) != v {
					t.Errorf("%s = %v, want %s (dict %v)", k, d[k], v, d)
				}
			}
			erpLNoWrites(t, s)
		})
	}
}

func TestERPItemFlagsAreSent(t *testing.T) {
	s := erpLSite(t, erpTV16, 0)
	cmdTOK(t, cmdTRun(t, s, "--json", "erp", "item", "ITEM-1", "--company", "Acme", "--doctype", "Purchase Order", "--supplier", "SUP-1",
		"--price-list", "Standard Buying", "--qty", "2.5", "--warehouse", "Stores - A", "--date", "2026-10-09"))
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(erpLOne(t, s, erpTItemMethod).Get("ctx")), &d); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"doctype": "Purchase Order", "supplier": "SUP-1", "price_list": "Standard Buying", "qty": "2.5",
		"set_warehouse": "Stores - A", "transaction_date": "2026-10-09"} {
		if fmt.Sprint(d[k]) != v {
			t.Errorf("%s = %v, want %s (dict %v)", k, d[k], v, d)
		}
	}
	if _, ok := d["customer"]; ok {
		t.Errorf("customer sent: %v", d)
	}
}

func TestERPItemDefaultDoctype(t *testing.T) {
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{nil, "Sales Invoice"},
		{[]string{"--customer", "CUST-1"}, "Sales Invoice"},
		{[]string{"--supplier", "SUP-1"}, "Purchase Invoice"},
		{[]string{"--doctype", "Quotation"}, "Quotation"},
	} {
		s := erpLSite(t, erpTV15, 0)
		cmdTOK(t, cmdTRun(t, s, append([]string{"--json", "erp", "item", "ITEM-1", "--company", "Acme"}, tc.flags...)...))
		var d map[string]interface{}
		if err := json.Unmarshal([]byte(erpLOne(t, s, erpTItemMethod).Get("args")), &d); err != nil {
			t.Fatal(err)
		}
		if d["doctype"] != tc.want {
			t.Errorf("%v: doctype = %v, want %s", tc.flags, d["doctype"], tc.want)
		}
	}
}

func TestERPItemKeysAndTable(t *testing.T) {
	s := erpLSite(t, erpTV16, 0)
	got := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "erp", "item", "ITEM-1", "--company", "Acme", "--keys", "item_code,rate")))
	if len(got) != 2 || got["item_code"] != "ITEM-1" {
		t.Errorf("output = %v", got)
	}
	r := cmdTOK(t, cmdTRun(t, s, "erp", "item", "ITEM-1", "--company", "Acme"))
	cmdTHas(t, r.Stdout, "item_code", "ITEM-1", "Stores - A")
}

func TestERPItemMissing(t *testing.T) {
	s := erpLSite(t, erpTV16, 0)
	lcTCode(t, cmdTRun(t, s, "erp", "item", "NOPE", "--company", "Acme"), exitNotFound)
}

func TestERPStockWithWarehouse(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		t.Run(version, func(t *testing.T) {
			s := erpLSite(t, version, 3)
			r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1", "--warehouse", "Stores - A"))
			got := cmdTObj(t, r)
			if got["item_code"] != "ITEM-1" || got["warehouse"] != "Stores - A" || !strings.Contains(r.Stdout, `"actual_qty": 5.0`) || len(got) != 3 {
				t.Errorf("output = %s", r.Stdout)
			}
			q := erpLOne(t, s, erpTStockMethod)
			if len(q) != 2 || q.Get("item_code") != "ITEM-1" || q.Get("warehouse") != "Stores - A" {
				t.Errorf("query = %v", q)
			}
			if n := len(erpLRequests(s, erpTDashboardMethod)); n != 0 {
				t.Errorf("%d dashboard requests", n)
			}
			erpLNoWrites(t, s)
		})
	}
}

func TestERPStockDateAndValuation(t *testing.T) {
	s := erpLSite(t, erpTV16, 0)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1", "--warehouse", "Stores - A", "--date", "2026-09-30", "--valuation"))
	got := cmdTObj(t, r)
	if got["posting_date"] != "2026-09-30" || !strings.Contains(r.Stdout, `"actual_qty": 5.0`) || !strings.Contains(r.Stdout, `"valuation_rate": 12.5`) {
		t.Errorf("output = %s", r.Stdout)
	}
	q := erpLOne(t, s, erpTStockMethod)
	// A date means the end of that day; true is sent for the flag, nothing otherwise.
	if q.Get("posting_date") != "2026-09-30" || q.Get("posting_time") != "23:59:59" || q.Get("with_valuation_rate") != "true" || len(q) != 5 {
		t.Errorf("query = %v", q)
	}
}

func TestERPStockByWarehousePages(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		t.Run(version, func(t *testing.T) {
			s := erpLSite(t, version, 50)
			rows := cmdTRows(t, cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1")))
			if len(rows) != 50 {
				t.Fatalf("%d rows", len(rows))
			}
			// escape_html is undone: the name can be passed back to --warehouse.
			if rows[0]["warehouse"] != "R&D - A" || rows[1]["warehouse"] != "Wh 002" || rows[49]["warehouse"] != "Wh 050" {
				t.Errorf("rows = %v ... %v", rows[0], rows[49])
			}
			req := erpLRequests(s, erpTDashboardMethod)
			if len(req) != 3 {
				t.Fatalf("%d dashboard requests", len(req))
			}
			for i, want := range []string{"0", "21", "42"} {
				q := req[i].Query
				if q.Get("start") != want || q.Get("item_code") != "ITEM-1" || q.Get("sort_by") != "warehouse" || q.Get("sort_order") != "asc" || len(q) != 4 {
					t.Errorf("request %d query = %v", i, q)
				}
			}
			if n := len(erpLRequests(s, erpTStockMethod)); n != 0 {
				t.Errorf("%d get_stock_balance requests", n)
			}
			erpLNoWrites(t, s)
		})
	}
}

func TestERPStockByWarehouseEdges(t *testing.T) {
	// No rows: an empty list, no warning.
	s := erpLSite(t, erpTV16, 0)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1"))
	if rows := cmdTRows(t, r); len(rows) != 0 || strings.Contains(r.Stderr, "warning") {
		t.Errorf("rows = %v, stderr = %q", rows, r.Stderr)
	}
	// A full last page costs one more (empty) request.
	s = erpLSite(t, erpTV16, 21)
	if rows := cmdTRows(t, cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1"))); len(rows) != 21 {
		t.Errorf("%d rows", len(rows))
	}
	if n := len(erpLRequests(s, erpTDashboardMethod)); n != 2 {
		t.Errorf("%d dashboard requests, want 2", n)
	}
	// Exactly the cap: a 25th (empty) page shows nothing more, so no warning.
	s = erpLSite(t, erpTV16, 504)
	r = cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1"))
	if rows := cmdTRows(t, r); len(rows) != 504 || strings.Contains(r.Stderr, "warning") {
		t.Errorf("%d rows, stderr = %q", len(rows), r.Stderr)
	}
	if n := len(erpLRequests(s, erpTDashboardMethod)); n != 25 {
		t.Errorf("%d dashboard requests, want 25", n)
	}
	// Past the cap: 504 rows, then a warning.
	s = erpLSite(t, erpTV16, 600)
	r = cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1"))
	if rows := cmdTRows(t, r); len(rows) != 504 {
		t.Errorf("%d rows, want 504", len(rows))
	}
	if n := len(erpLRequests(s, erpTDashboardMethod)); n != 25 {
		t.Errorf("%d dashboard requests, want 25", n)
	}
	cmdTHas(t, r.Stderr, "warning: stopped after 504 warehouses", "--warehouse")
}

// ERPNext answers 0 and [] for names that do not exist: ffc reads the Item
// (and the Warehouse) first, so a typo is not-found (exit 4) and no balance
// is asked for.
func TestERPStockUnknownItemOrWarehouse(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		for _, args := range [][]string{
			{"erp", "stock", "NOPE", "--warehouse", "Stores - A"},
			{"erp", "stock", "ITEM-1", "--warehouse", "NOPE"},
			{"erp", "stock", "NOPE"},
		} {
			s := erpLSite(t, version, 3)
			lcTCode(t, cmdTRun(t, s, args...), exitNotFound)
			if n := len(erpLRequests(s, erpTStockMethod)) + len(erpLRequests(s, erpTDashboardMethod)); n != 0 {
				t.Errorf("%s %v: %d stock requests", version, args, n)
			}
		}
	}
}

// A user who may not read Warehouses is not blocked: the balance call does
// not need that right.
func TestERPStockWarehouseForbidden(t *testing.T) {
	s := erpLSite(t, erpTV16, 0)
	denied := 0
	s.Handle("GET /api/resource/Warehouse/Stores - A", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		denied++
		http.Error(w, `{"exc_type":"PermissionError"}`, http.StatusForbidden)
	}))
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1", "--warehouse", "Stores - A"))
	if denied == 0 {
		t.Error("the Warehouse read was not refused")
	}
	if !strings.Contains(r.Stdout, `"actual_qty": 5.0`) {
		t.Errorf("output = %s", r.Stdout)
	}
}

func TestERPStockByWarehouseKeysAndTable(t *testing.T) {
	s := erpLSite(t, erpTV16, 2)
	rows := cmdTRows(t, cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1", "--keys", "warehouse,actual_qty")))
	if len(rows) != 2 || len(rows[0]) != 2 || rows[1]["warehouse"] != "Wh 002" {
		t.Errorf("rows = %v", rows)
	}
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "stock", "ITEM-1", "--keys", "warehouse,nope"))
	cmdTHas(t, r.Stderr, "--keys: not present in the result: nope")
	r = cmdTOK(t, cmdTRun(t, s, "erp", "stock", "ITEM-1"))
	cmdTHas(t, r.Stdout, "WAREHOUSE", "R&D - A", "Wh 002")
}

func TestERPPartyCustomerAndSupplier(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		t.Run(version, func(t *testing.T) {
			s := erpLSite(t, version, 0)
			got := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "erp", "party", "--customer", "CUST-1")))
			if got["currency"] != "USD" {
				t.Errorf("output = %v", got)
			}
			q := erpLOne(t, s, erpTPartyMethod)
			// Only the party is sent when nothing else is asked for.
			if len(q) != 2 || q.Get("party") != "CUST-1" || q.Get("party_type") != "Customer" {
				t.Errorf("query = %v", q)
			}
			erpLNoWrites(t, s)
		})
	}
	s := erpLSite(t, erpTV16, 0)
	cmdTOK(t, cmdTRun(t, s, "--json", "erp", "party", "--supplier", "SUP-1", "--company", "Acme", "--date", "2026-10-09", "--doctype", "Purchase Invoice", "--keys", "customer"))
	q := erpLOne(t, s, erpTPartyMethod)
	if q.Get("party") != "SUP-1" || q.Get("party_type") != "Supplier" || q.Get("company") != "Acme" || q.Get("posting_date") != "2026-10-09" ||
		q.Get("doctype") != "Purchase Invoice" || len(q) != 5 {
		t.Errorf("query = %v", q)
	}
}

func TestERPPartyMissing(t *testing.T) {
	s := erpLSite(t, erpTV16, 0)
	lcTCode(t, cmdTRun(t, s, "erp", "party", "--customer", "NOPE"), exitNotFound)
}

func TestERPLookupUsage(t *testing.T) {
	s := erpLSite(t, erpTV16, 3)
	for _, args := range [][]string{
		// item
		{"erp", "item", "ITEM-1"},
		{"erp", "item", "ITEM-1", "--company", " "},
		{"erp", "item"},
		{"erp", "item", " ", "--company", "Acme"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--customer", "C", "--supplier", "S"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--customer", "C", "--doctype", "Purchase Order"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--supplier", "S", "--doctype", "Sales Order"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--doctype", "Payment Entry"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--doctype", " "},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--date", "09/10/2026"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--date", "2026-13-01"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--qty", "0"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--qty", "-2"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--qty", "abc"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--qty", "1e3"},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--qty", ""},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--price-list", " "},
		{"erp", "item", "ITEM-1", "--company", "Acme", "--warehouse", ""},
		// stock
		{"erp", "stock"},
		{"erp", "stock", " "},
		{"erp", "stock", "ITEM-1", "--warehouse", " "},
		{"erp", "stock", "ITEM-1", "--date", "2026-09-30"},
		{"erp", "stock", "ITEM-1", "--valuation"},
		{"erp", "stock", "ITEM-1", "--warehouse", "W", "--date", "yesterday"},
		// party
		{"erp", "party"},
		{"erp", "party", "--customer", "C", "--supplier", "S"},
		{"erp", "party", "--customer", " "},
		{"erp", "party", "--customer", "C", "--date", "10/09/2026"},
		{"erp", "party", "--customer", "C", "--company", ""},
		{"erp", "party", "--customer", "C", "--doctype", " "},
	} {
		lcTCode(t, cmdTRun(t, s, args...), exitUsage)
	}
	// A usage error is answered before the site is asked anything.
	if n := len(s.Requests()); n != 0 {
		t.Errorf("%d requests for usage errors: %+v", n, s.Requests())
	}
}

var erpLCommands = [][]string{
	{"erp", "item", "ITEM-1", "--company", "Acme"},
	{"erp", "stock", "ITEM-1", "--warehouse", "Stores - A"},
	{"erp", "stock", "ITEM-1"},
	{"erp", "party", "--customer", "CUST-1"},
}

func erpLLookupRequests(s *frappetest.Site) int {
	n := 0
	for _, m := range []string{erpTItemMethod, erpTStockMethod, erpTDashboardMethod, erpTPartyMethod} {
		n += len(erpLRequests(s, m))
	}
	return n
}

func TestERPLookupNotInstalled(t *testing.T) {
	for _, args := range erpLCommands {
		s := erpLSite(t, "", 3)
		lcTCode(t, cmdTRun(t, s, args...), exitNotFound, "ERPNext is not installed on t")
		if n := erpLLookupRequests(s); n != 0 {
			t.Errorf("%v: %d lookup requests", args, n)
		}
	}
}

func TestERPLookupUnsupportedMajor(t *testing.T) {
	for _, version := range []string{"17.0.0-dev", "14.80.0"} {
		for _, args := range erpLCommands {
			s := erpLSite(t, version, 3)
			lcTCode(t, cmdTRun(t, s, args...), exitGeneric, "ffc api", version)
			if n := erpLLookupRequests(s); n != 0 {
				t.Errorf("%s %v: %d lookup requests", version, args, n)
			}
		}
	}
}

func TestERPLookupNeverSendsIgnorePermissions(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		s := erpLSite(t, version, 25)
		for _, args := range erpLCommands {
			cmdTOK(t, cmdTRun(t, s, args...))
		}
		cmdTOK(t, cmdTRun(t, s, "erp", "stock", "ITEM-1", "--warehouse", "W", "--valuation", "--date", "2026-01-02"))
		cmdTOK(t, cmdTRun(t, s, "erp", "item", "ITEM-1", "--company", "Acme", "--supplier", "SUP-1", "--price-list", "P", "--qty", "3", "--warehouse", "W", "--date", "2026-01-02"))
		cmdTOK(t, cmdTRun(t, s, "erp", "party", "--supplier", "SUP-1", "--company", "Acme", "--doctype", "Purchase Invoice", "--date", "2026-01-02"))
		if erpLLookupRequests(s) == 0 {
			t.Fatal("no lookup request")
		}
		for _, r := range s.Requests() {
			if strings.Contains(r.Path+r.Query.Encode()+r.Body, "ignore_permissions") {
				t.Errorf("ignore_permissions sent: %s %s?%s %s", r.Method, r.Path, r.Query.Encode(), r.Body)
			}
		}
		erpLNoWrites(t, s)
	}
}
