package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

var mcpTERPTools = []string{"erp_item", "erp_map", "erp_party", "erp_payment", "erp_stock"}

// erpMCP starts every tool set on a fake ERPNext site (erpTSite and its
// relatives) under the site policy pol.
func erpMCP(t *testing.T, site *frappetest.Site, pol *config.MCPPolicy) *server.MCPServer {
	t.Helper()
	sc := &config.SiteConfig{Name: "test", URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret, MCP: pol}
	c, err := client.New(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewMCPServer("test", "0")
	mcpTRegister(s, c, sc, nil)
	return s
}

// mcpTRequest is a tool call as scopeOf sees it.
func mcpTRequest(tool string, args map[string]interface{}) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	return req
}

// erpMCPJSON calls a tool, requires success and decodes its object.
func erpMCPJSON(t *testing.T, s *server.MCPServer, tool string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	text := mcpTOK(t, s, tool, args)
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: %v in %s", tool, err, text)
	}
	return out
}

// erpMCPDraft is the draft of a result.
func erpMCPDraft(t *testing.T, res map[string]interface{}) map[string]interface{} {
	t.Helper()
	d, ok := res["draft"].(map[string]interface{})
	if !ok {
		t.Fatalf("no draft in %v", res)
	}
	return d
}

// erpMCPNoUnderscoreKeys fails when a document or one of its rows still has
// a key a local document carries: create_doc would send it on.
func erpMCPNoUnderscoreKeys(t *testing.T, doc map[string]interface{}) {
	t.Helper()
	for k, v := range doc {
		if strings.HasPrefix(k, "__") {
			t.Errorf("draft has key %s", k)
		}
		if rows, ok := v.([]interface{}); ok {
			for _, r := range rows {
				erpMCPNoUnderscoreKeys(t, r.(map[string]interface{}))
			}
		}
	}
}

func TestMCPERPToolsetIsOptIn(t *testing.T) {
	if !contains(knownToolsets, toolsetERP, false) || contains(defaultToolsets, toolsetERP, false) {
		t.Fatalf("known %v, default %v", knownToolsets, defaultToolsets)
	}
	for _, sets := range [][]string{nil, {"core", "lifecycle"}, {"core", "lifecycle", "collab", "admin", "files"}} {
		for _, n := range mcpTToolNames(t, mcpTToolsets(t, sets)) {
			if strings.HasPrefix(n, "erp_") {
				t.Errorf("toolsets %v register %s", sets, n)
			}
		}
	}
	want := append(append([]string(nil), mcpTERPTools...), "list_sites")
	if got := mcpTToolNames(t, mcpTToolsets(t, []string{"erp"})); !reflect.DeepEqual(got, []string{"erp_item", "erp_map", "erp_party", "erp_payment", "erp_stock", "list_sites"}) {
		t.Errorf("erp = %v, want %v", got, want)
	}
	// All are reads and carry the jq argument of the big read tools.
	s := mcpTToolsets(t, []string{"erp"})
	for _, name := range mcpTERPTools {
		tool := s.ListTools()[name].Tool
		if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not annotated read-only", name)
		}
		if _, ok := tool.InputSchema.Properties["jq"]; !ok {
			t.Errorf("%s has no jq argument", name)
		}
		if info := toolSurface[name]; info.toolset != toolsetERP || toolActions[name] != actRead {
			t.Errorf("%s: surface %+v, action %v", name, info, toolActions[name])
		}
	}
}

func TestMCPERPMapEveryPair(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		for _, p := range erpTPairs {
			t.Run(version+" "+p.from+" to "+p.to, func(t *testing.T) {
				site := erpTSite(t, version)
				before := site.Count(p.to)
				s := erpMCP(t, site, nil)
				res := erpMCPJSON(t, s, "erp_map", map[string]interface{}{"from_doctype": p.from, "from_name": "SRC-1", "to_doctype": p.to})
				d := erpMCPDraft(t, res)
				if d["doctype"] != p.to {
					t.Errorf("draft = %v", d)
				}
				// The draft is what create_doc takes: no local-document keys.
				erpMCPNoUnderscoreKeys(t, d)
				if rows := d["items"].([]interface{}); len(rows) != 1 || rows[0].(map[string]interface{})["parentfield"] != "items" {
					t.Errorf("rows = %v", rows)
				}
				if req := site.RequestsTo(http.MethodGet, "/api/method/"+p.method); len(req) != 1 || req[0].Query.Get("source_name") != "SRC-1" || len(req[0].Query) != 1 {
					t.Errorf("mapper requests = %+v", req)
				}
				if w := erpTWrites(site); len(w) != 0 {
					t.Errorf("writes: %+v", w)
				}
				if n := site.Count(p.to); n != before {
					t.Errorf("%s: %d documents before, %d after", p.to, before, n)
				}
			})
		}
	}
}

func TestMCPERPMapRefusals(t *testing.T) {
	args := func(from, to string) map[string]interface{} {
		return map[string]interface{}{"from_doctype": from, "from_name": "SRC-1", "to_doctype": to}
	}
	site := erpTSite(t, erpTV16)
	s := erpMCP(t, site, nil)
	mcpTErr(t, s, "erp_map", args("Sales Invoice", "Sales Order"), "cannot map Sales Invoice to Sales Order on ERPNext 16; supported: ")
	mcpTErr(t, s, "erp_map", map[string]interface{}{"from_doctype": "Sales Order", "to_doctype": "Sales Invoice"}, `required argument "from_name"`)
	mcpTErr(t, s, "erp_map", map[string]interface{}{"from_doctype": "Sales Order", "from_name": "NOPE", "to_doctype": "Sales Invoice"}, "not found")
	if w := erpTWrites(site); len(w) != 0 {
		t.Errorf("writes: %+v", w)
	}

	mcpTErr(t, erpMCP(t, erpTSite(t, ""), nil), "erp_map", args("Sales Order", "Sales Invoice"), "ERPNext is not installed")
	mcpTErr(t, erpMCP(t, erpTSite(t, "17.0.0-dev"), nil), "erp_map", args("Sales Order", "Sales Invoice"), "supports ERPNext 15 and 16, this site runs 17.0.0-dev: call its methods with call_method")

	// A Quotation made out to a Lead with no Customer is refused before the
	// mapper (which would insert one).
	for _, tc := range []struct{ to, link string }{{"Lead", "lead_name"}, {"Prospect", "prospect_name"}} {
		site := erpTSite(t, erpTV16)
		site.Add("Quotation", map[string]interface{}{"name": "SRC-1", "quotation_to": tc.to, "party_name": "P-1"})
		site.AddDocType("Customer", "lead_name", "prospect_name")
		s := erpMCP(t, site, nil)
		q := map[string]interface{}{"from_doctype": "Quotation", "from_name": "SRC-1", "to_doctype": "Sales Order"}
		mcpTErr(t, s, "erp_map", q, "convert the "+strings.ToLower(tc.to)+" to a customer first")
		for _, r := range site.Requests() {
			if strings.Contains(r.Path, "make_sales_order") {
				t.Errorf("%s: the mapper ran", tc.to)
			}
		}
		// The Customer already made from it is reused by the mapper: maps.
		site.Add("Customer", map[string]interface{}{"name": "C-1", tc.link: "P-1"})
		erpMCPJSON(t, s, "erp_map", q)
	}
}

func TestMCPERPPayment(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		for _, dt := range erpTPayable {
			t.Run(version+" "+dt, func(t *testing.T) {
				site := erpPSite(t, version)
				s := erpMCP(t, site, nil)
				res := erpMCPJSON(t, s, "erp_payment", map[string]interface{}{"against_doctype": dt, "against_name": "SRC-1"})
				d := erpMCPDraft(t, res)
				if d["doctype"] != "Payment Entry" || d["paid_to"] != "Cash - A" {
					t.Errorf("draft = %v", d)
				}
				erpMCPNoUnderscoreKeys(t, d)
				if _, ok := res["warnings"]; ok {
					t.Errorf("warnings = %v", res["warnings"])
				}
				// Only the document is sent: the server picks everything else.
				req := erpPMethodRequests(site)
				if len(req) != 1 || len(req[0].Query) != 2 || req[0].Query.Get("dt") != dt || req[0].Query.Get("dn") != "SRC-1" {
					t.Fatalf("requests = %+v", req)
				}
				if w := erpTWrites(site); len(w) != 0 {
					t.Errorf("writes: %+v", w)
				}
				if n := site.Count("Payment Entry"); n != 0 {
					t.Errorf("%d Payment Entry saved", n)
				}
			})
		}
	}

	// Options: a JSON number or text for the amount.
	for _, amount := range []interface{}{12.5, "12.5"} {
		site := erpPSite(t, erpTV16)
		s := erpMCP(t, site, nil)
		erpMCPJSON(t, s, "erp_payment", map[string]interface{}{"against_doctype": "Sales Invoice", "against_name": "SRC-1",
			"amount": amount, "bank_account": "Cash - A", "reference_date": "2026-10-09"})
		q := erpPMethodRequests(site)[0].Query
		if q.Get("party_amount") != "12.5" || q.Get("bank_account") != "Cash - A" || q.Get("reference_date") != "2026-10-09" || len(q) != 5 {
			t.Errorf("amount %v: query = %v", amount, q)
		}
	}
	// A blank option is not sent.
	site := erpPSite(t, erpTV16)
	s := erpMCP(t, site, nil)
	erpMCPJSON(t, s, "erp_payment", map[string]interface{}{"against_doctype": "Sales Invoice", "against_name": "SRC-1", "bank_account": "", "reference_date": " "})
	if q := erpPMethodRequests(site)[0].Query; len(q) != 2 {
		t.Errorf("query = %v", q)
	}

	for args, want := range map[string]string{
		`{"against_doctype":"Quotation","against_name":"SRC-1"}`:                                   "against_doctype: cannot make a payment against Quotation; supported: Sales Invoice, Sales Order",
		`{"against_doctype":"Sales Invoice"}`:                                                      `required argument "against_name"`,
		`{"against_doctype":"Sales Invoice","against_name":"SRC-1","amount":-5}`:                   `amount: expected a positive number such as 50 or 12.5, got "-5"`,
		`{"against_doctype":"Sales Invoice","against_name":"SRC-1","amount":"1e3"}`:                "amount: expected a positive number",
		`{"against_doctype":"Sales Invoice","against_name":"SRC-1","amount":true}`:                 "amount: expected a number",
		`{"against_doctype":"Sales Invoice","against_name":"SRC-1","reference_date":"09/10/2026"}`: `reference_date: expected YYYY-MM-DD, got "09/10/2026"`,
		`{"against_doctype":"Sales Invoice","against_name":"NOPE"}`:                                "not found",
	} {
		var a map[string]interface{}
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			t.Fatal(err)
		}
		before := len(erpPMethodRequests(site))
		mcpTErr(t, s, "erp_payment", a, want)
		if n := len(erpPMethodRequests(site)); n != before && !strings.Contains(want, "not found") {
			t.Errorf("%s: a bad call reached the method", args)
		}
	}
	mcpTErr(t, erpMCP(t, erpTSite(t, ""), nil), "erp_payment", map[string]interface{}{"against_doctype": "Sales Invoice", "against_name": "SRC-1"}, "ERPNext is not installed")
}

func TestMCPERPPaymentWarnings(t *testing.T) {
	args := map[string]interface{}{"against_doctype": "Sales Invoice", "against_name": "SRC-1"}

	// No bank or cash account: a draft with the reason, not an error.
	site := erpPSite(t, erpTV16)
	site.HandleMethod(erpTPaymentMethod, func(*http.Request, map[string]interface{}) (interface{}, error) {
		d := erpTPaymentDraft()
		d["paid_to"] = nil
		return d, nil
	})
	res := erpMCPJSON(t, erpMCP(t, site, nil), "erp_payment", args)
	w, _ := res["warnings"].([]interface{})
	if len(w) != 1 || !strings.Contains(fmt.Sprint(w[0]), "no bank or cash account (paid_to is empty)") ||
		!strings.Contains(fmt.Sprint(w[0]), "Acme") || !strings.Contains(fmt.Sprint(w[0]), "or pass bank_account") || strings.Contains(fmt.Sprint(w[0]), "--") {
		t.Errorf("warnings = %v", w)
	}
	if erpMCPDraft(t, res)["doctype"] != "Payment Entry" {
		t.Errorf("result = %v", res)
	}

	// A Mode of Payment on the document takes precedence over bank_account.
	site = erpPSite(t, erpTV16)
	s := erpMCP(t, site, nil)
	args["bank_account"] = "Bank - A"
	res = erpMCPJSON(t, s, "erp_payment", args)
	w, _ = res["warnings"].([]interface{})
	if len(w) != 1 || !strings.Contains(fmt.Sprint(w[0]), `bank_account "Bank - A" was not used: the draft has paid_to "Cash - A"`) || !strings.Contains(fmt.Sprint(w[0]), "Mode of Payment") {
		t.Errorf("warnings = %v", w)
	}
	// The account the draft uses: no warning.
	args["bank_account"] = "Cash - A"
	if res = erpMCPJSON(t, s, "erp_payment", args); res["warnings"] != nil {
		t.Errorf("warnings = %v", res["warnings"])
	}
}

func TestMCPERPItemParameterPerMajor(t *testing.T) {
	for _, tc := range []struct{ version, param, other string }{{erpTV15, "args", "ctx"}, {erpTV16, "ctx", "args"}} {
		t.Run(tc.version, func(t *testing.T) {
			site := erpLSite(t, tc.version, 0)
			s := erpMCP(t, site, nil)
			text := mcpTOK(t, s, "erp_item", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme"})
			// Floats keep the literal Frappe sent.
			if !strings.Contains(text, `"rate":100.0`) || !strings.Contains(text, `"item_code":"ITEM-1"`) {
				t.Errorf("result = %s", text)
			}
			q := erpLOne(t, site, erpTItemMethod)
			if q.Get(tc.param) == "" || q.Has(tc.other) || len(q) != 1 {
				t.Fatalf("query = %v, want only %s", q, tc.param)
			}
			var d map[string]interface{}
			if err := json.Unmarshal([]byte(q.Get(tc.param)), &d); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"company": "Acme", "doctype": "Sales Invoice", "item_code": "ITEM-1", "qty": "1", "conversion_rate": "1", "plc_conversion_rate": "1"}
			if len(d) != len(want) {
				t.Errorf("dict = %v", d)
			}
			for k, v := range want {
				if fmt.Sprint(d[k]) != v {
					t.Errorf("%s = %v, want %s (dict %v)", k, d[k], v, d)
				}
			}
			erpLNoWrites(t, site)
		})
	}
}

func TestMCPERPItemArgumentsAreSent(t *testing.T) {
	site := erpLSite(t, erpTV16, 0)
	s := erpMCP(t, site, nil)
	mcpTOK(t, s, "erp_item", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme", "doctype": "Purchase Order", "supplier": "SUP-1",
		"price_list": "Standard Buying", "qty": 2.5, "warehouse": "Stores - A", "date": "2026-10-09", "customer": ""})
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(erpLOne(t, site, erpTItemMethod).Get("ctx")), &d); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"doctype": "Purchase Order", "supplier": "SUP-1", "price_list": "Standard Buying", "qty": "2.5",
		"set_warehouse": "Stores - A", "transaction_date": "2026-10-09"} {
		if fmt.Sprint(d[k]) != v {
			t.Errorf("%s = %v, want %s (dict %v)", k, d[k], v, d)
		}
	}
	if _, ok := d["customer"]; ok {
		t.Errorf("a blank customer was sent: %v", d)
	}
	// With a supplier the default document is a Purchase Invoice.
	site = erpLSite(t, erpTV16, 0)
	mcpTOK(t, erpMCP(t, site, nil), "erp_item", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme", "supplier": "SUP-1"})
	d = nil
	if err := json.Unmarshal([]byte(erpLOne(t, site, erpTItemMethod).Get("ctx")), &d); err != nil || d["doctype"] != "Purchase Invoice" {
		t.Errorf("dict = %v (%v)", d, err)
	}
}

func TestMCPERPItemRefusals(t *testing.T) {
	site := erpLSite(t, erpTV16, 0)
	s := erpMCP(t, site, nil)
	item := func(extra map[string]interface{}) map[string]interface{} {
		a := map[string]interface{}{"item_code": "ITEM-1", "company": "Acme"}
		for k, v := range extra {
			a[k] = v
		}
		return a
	}
	for _, tc := range []struct {
		args map[string]interface{}
		want string
	}{
		{map[string]interface{}{"company": "Acme"}, `required argument "item_code"`},
		{map[string]interface{}{"item_code": "ITEM-1"}, "company: provide the Company"},
		{item(map[string]interface{}{"customer": "C", "supplier": "S"}), "customer and supplier cannot be used together"},
		{item(map[string]interface{}{"doctype": "Item"}), "doctype: cannot look up an item for Item; supported: Quotation, Sales Order"},
		{item(map[string]interface{}{"doctype": "Purchase Order", "customer": "C"}), "customer: Purchase Order is a purchase DocType, use supplier"},
		{item(map[string]interface{}{"doctype": "Sales Order", "supplier": "S"}), "supplier: Sales Order is a sales DocType, use customer"},
		{item(map[string]interface{}{"qty": 0}), `qty: expected a positive number such as 5 or 2.5, got "0"`},
		{item(map[string]interface{}{"qty": []int{1}}), "qty: expected a number"},
		{item(map[string]interface{}{"date": "tomorrow"}), `date: expected YYYY-MM-DD, got "tomorrow"`},
		{item(map[string]interface{}{"item_code": "NOPE"}), "not found"},
	} {
		mcpTErr(t, s, "erp_item", tc.args, tc.want)
	}
	// A bad call is refused before any request to the method.
	if n := len(erpLRequests(site, erpTItemMethod)); n != 1 { // only the unknown item reached it
		t.Errorf("%d item requests", n)
	}
	mcpTErr(t, erpMCP(t, erpTSite(t, ""), nil), "erp_item", item(nil), "ERPNext is not installed")
}

func TestMCPERPStock(t *testing.T) {
	site := erpLSite(t, erpTV16, 2)
	s := erpMCP(t, site, nil)

	// One warehouse: the number keeps Frappe's literal.
	text := mcpTOK(t, s, "erp_stock", map[string]interface{}{"item_code": "ITEM-1", "warehouse": "Stores - A"})
	if !strings.Contains(text, `"actual_qty":5.0`) || !strings.Contains(text, `"warehouse":"Stores - A"`) || strings.Contains(text, "valuation_rate") {
		t.Errorf("result = %s", text)
	}
	text = mcpTOK(t, s, "erp_stock", map[string]interface{}{"item_code": "ITEM-1", "warehouse": "Stores - A", "date": "2026-09-30", "valuation": true})
	if !strings.Contains(text, `"valuation_rate":12.5`) || !strings.Contains(text, `"posting_date":"2026-09-30"`) {
		t.Errorf("result = %s", text)
	}
	reqs := erpLRequests(site, erpTStockMethod)
	if len(reqs) != 2 {
		t.Fatalf("requests = %+v", reqs)
	}
	if q := reqs[1].Query; q.Get("posting_date") != "2026-09-30" || q.Get("posting_time") != "23:59:59" || q.Get("with_valuation_rate") != "true" {
		t.Errorf("query = %v", q)
	}
	// "false" and absent send nothing.
	mcpTOK(t, s, "erp_stock", map[string]interface{}{"item_code": "ITEM-1", "warehouse": "Stores - A", "valuation": "false"})
	if q := erpLRequests(site, erpTStockMethod)[2].Query; q.Has("with_valuation_rate") {
		t.Errorf("query = %v", q)
	}

	// Every warehouse: one row each, names as text, not HTML.
	res := erpMCPJSON(t, s, "erp_stock", map[string]interface{}{"item_code": "ITEM-1"})
	rows, _ := res["rows"].([]interface{})
	if res["item_code"] != "ITEM-1" || res["truncated"] != false || len(rows) != 2 || rows[0].(map[string]interface{})["warehouse"] != "R&D - A" || res["warning"] != nil {
		t.Errorf("result = %v", res)
	}

	for _, tc := range []struct {
		args map[string]interface{}
		want string
	}{
		{map[string]interface{}{"item_code": "ITEM-1", "date": "2026-09-30"}, "date and valuation need warehouse"},
		{map[string]interface{}{"item_code": "ITEM-1", "valuation": true}, "date and valuation need warehouse"},
		{map[string]interface{}{"item_code": "ITEM-1", "warehouse": "W", "valuation": "yes"}, "valuation: expected true or false"},
		{map[string]interface{}{"item_code": "ITEM-1", "warehouse": "W", "date": "30/09/2026"}, "date: expected YYYY-MM-DD"},
		{map[string]interface{}{"warehouse": "W"}, `required argument "item_code"`},
		{map[string]interface{}{"item_code": "NOPE", "warehouse": "W"}, "not found"},
		{map[string]interface{}{"item_code": "ITEM-1", "warehouse": "NOPE"}, "not found"},
		{map[string]interface{}{"item_code": "NOPE"}, "not found"},
	} {
		mcpTErr(t, s, "erp_stock", tc.args, tc.want)
	}
	erpLNoWrites(t, site)

	// More rows than the pages cover: truncated, with the reason.
	site = erpLSite(t, erpTV15, client.StockPageSize*client.StockMaxPages+1)
	res = erpMCPJSON(t, erpMCP(t, site, nil), "erp_stock", map[string]interface{}{"item_code": "ITEM-1"})
	if rows, _ := res["rows"].([]interface{}); len(rows) != client.StockPageSize*client.StockMaxPages || res["truncated"] != true ||
		!strings.Contains(fmt.Sprint(res["warning"]), "stopped after 504 warehouses") || !strings.Contains(fmt.Sprint(res["warning"]), "ask for one with warehouse") {
		t.Errorf("rows %d, truncated %v, warning %v", len(rows), res["truncated"], res["warning"])
	}
}

func TestMCPERPParty(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		site := erpLSite(t, version, 0)
		s := erpMCP(t, site, nil)
		res := erpMCPJSON(t, s, "erp_party", map[string]interface{}{"customer": "CUST-1", "company": "Acme", "doctype": "Sales Invoice", "date": "2026-10-09"})
		if res["customer"] != "CUST-1" || res["currency"] != "USD" {
			t.Errorf("%s: result = %v", version, res)
		}
		q := erpLOne(t, site, erpTPartyMethod)
		for k, v := range map[string]string{"party": "CUST-1", "party_type": "Customer", "company": "Acme", "doctype": "Sales Invoice", "posting_date": "2026-10-09"} {
			if q.Get(k) != v {
				t.Errorf("%s: %s = %q, want %q (query %v)", version, k, q.Get(k), v, q)
			}
		}
		erpMCPJSON(t, s, "erp_party", map[string]interface{}{"supplier": "SUP-1"})
		if q := erpLRequests(site, erpTPartyMethod)[1].Query; q.Get("party_type") != "Supplier" || q.Has("company") || q.Has("doctype") || q.Has("posting_date") {
			t.Errorf("%s: supplier query = %v", version, q)
		}
		erpLNoWrites(t, site)
	}
	site := erpLSite(t, erpTV16, 0)
	s := erpMCP(t, site, nil)
	mcpTErr(t, s, "erp_party", map[string]interface{}{}, "provide customer or supplier")
	mcpTErr(t, s, "erp_party", map[string]interface{}{"customer": "C", "supplier": "S"}, "customer and supplier cannot be used together")
	mcpTErr(t, s, "erp_party", map[string]interface{}{"customer": "CUST-1", "date": "x"}, "date: expected YYYY-MM-DD")
	mcpTErr(t, s, "erp_party", map[string]interface{}{"customer": "NOPE"}, "not found")
	mcpTErr(t, erpMCP(t, erpTSite(t, "17.1.0"), nil), "erp_party", map[string]interface{}{"customer": "C"}, "call_method")
}

// No tool sends ignore_permissions, whatever it is given: every request the
// site received for each call (path, query and body) is checked.
func TestMCPERPNeverSendsIgnorePermissions(t *testing.T) {
	site := erpLSite(t, erpTV16, 1)
	erpTSiteWithPayment(site)
	s := erpMCP(t, site, nil)
	extra := map[string]interface{}{"ignore_permissions": true}
	for tool, args := range map[string]map[string]interface{}{
		"erp_map":     {"from_doctype": "Sales Order", "from_name": "SRC-1", "to_doctype": "Sales Invoice"},
		"erp_payment": {"against_doctype": "Sales Invoice", "against_name": "SRC-1"},
		"erp_item":    {"item_code": "ITEM-1", "company": "Acme"},
		"erp_stock":   {"item_code": "ITEM-1"},
		"erp_party":   {"customer": "CUST-1"},
	} {
		for k, v := range extra {
			args[k] = v
		}
		before := len(site.Requests())
		mcpTOK(t, s, tool, args)
		reqs := site.Requests()[before:]
		if len(reqs) == 0 {
			t.Errorf("%s: no request reached the site", tool)
		}
		for _, r := range reqs {
			if strings.Contains(r.Path+r.Query.Encode()+r.Body, "ignore_permissions") {
				t.Errorf("%s: ignore_permissions sent: %s %s?%s %s", tool, r.Method, r.Path, r.Query.Encode(), r.Body)
			}
		}
	}
}

// erpTSiteWithPayment adds get_payment_entry to a lookup site.
func erpTSiteWithPayment(s *frappetest.Site) {
	s.AddDocType("Payment Entry")
	s.Add("Sales Invoice", map[string]interface{}{"name": "SRC-1"})
	s.HandleMethod(erpTPaymentMethod, func(*http.Request, map[string]interface{}) (interface{}, error) {
		return erpTPaymentDraft(), nil
	})
}

// erpMCPCalls are one valid call per tool and the DocTypes the policy sees in
// it (extra arguments add some).
var erpMCPCalls = map[string]struct {
	args     map[string]interface{}
	doctypes []string
}{
	"erp_map":     {map[string]interface{}{"from_doctype": "Sales Order", "from_name": "SRC-1", "to_doctype": "Sales Invoice"}, []string{"Sales Order", "Sales Invoice"}},
	"erp_payment": {map[string]interface{}{"against_doctype": "Sales Invoice", "against_name": "SRC-1"}, []string{"Sales Invoice", "Payment Entry"}},
	"erp_item": {map[string]interface{}{"item_code": "ITEM-1", "company": "Acme", "customer": "CUST-1", "price_list": "Standard Selling", "warehouse": "Stores - A"},
		[]string{"Item", "Customer", "Price List", "Warehouse", "Bin"}},
	"erp_stock": {map[string]interface{}{"item_code": "ITEM-1", "warehouse": "Stores - A"}, []string{"Item", "Warehouse", "Bin"}},
	"erp_party": {map[string]interface{}{"customer": "CUST-1"}, []string{"Customer", "Address", "Contact"}},
}

func TestMCPERPScope(t *testing.T) {
	for tool, c := range erpMCPCalls {
		req := mcpTRequest(tool, c.args)
		sc, err := scopeOf(req)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if sc.Action != actRead || !reflect.DeepEqual(sc.Doctypes, c.doctypes) {
			t.Errorf("%s: scope %+v, want read of %v", tool, sc, c.doctypes)
		}
	}
	// What is given adds to the scope; what is not given does not.
	for _, tc := range []struct {
		tool string
		args map[string]interface{}
		want []string
	}{
		{"erp_item", map[string]interface{}{"item_code": "I", "company": "C"}, []string{"Item", "Price List", "Bin"}},
		{"erp_item", map[string]interface{}{"item_code": "I", "company": "C", "supplier": "S", "customer": " "}, []string{"Item", "Supplier", "Price List", "Bin"}},
		// The document a row is for is not read: it is not in the scope.
		{"erp_item", map[string]interface{}{"item_code": "I", "company": "C", "doctype": "Purchase Order"}, []string{"Item", "Price List", "Bin"}},
		{"erp_party", map[string]interface{}{"supplier": "S", "doctype": "Purchase Invoice"}, []string{"Supplier", "Address", "Contact"}},
		{"erp_stock", map[string]interface{}{"item_code": "I"}, []string{"Item", "Warehouse", "Bin"}},
		// A Quotation mapping looks for the lead's Customer.
		{"erp_map", map[string]interface{}{"from_doctype": "Quotation", "from_name": "Q", "to_doctype": "Sales Order"}, []string{"Quotation", "Sales Order", "Customer"}},
		{"erp_payment", map[string]interface{}{"against_doctype": " Purchase Order ", "against_name": "P"}, []string{"Purchase Order", "Payment Entry"}},
		// A bank account is read to build the draft; the party is covered by the source document.
		{"erp_payment", map[string]interface{}{"against_doctype": "Sales Invoice", "against_name": "P", "bank_account": "Bank - A"}, []string{"Sales Invoice", "Payment Entry", "Account"}},
	} {
		sc, err := scopeOf(mcpTRequest(tc.tool, tc.args))
		if err != nil || !reflect.DeepEqual(sc.Doctypes, tc.want) {
			t.Errorf("%s %v: doctypes %v (%v), want %v", tc.tool, tc.args, sc.Doctypes, err, tc.want)
		}
	}
	// A blank doctype is "none" for the lookups, and a name is recorded.
	sc, err := scopeOf(mcpTRequest("erp_item", map[string]interface{}{"item_code": 1001, "company": "C", "doctype": ""}))
	if err != nil || !reflect.DeepEqual(sc.Names, []string{"1001"}) {
		t.Errorf("scope %+v, %v", sc, err)
	}
	// A control character in a DocType is refused, as everywhere.
	if _, err := scopeOf(mcpTRequest("erp_map", map[string]interface{}{"from_doctype": "Sales\x00Order", "from_name": "S", "to_doctype": "Sales Invoice"})); err == nil {
		t.Error("control character accepted")
	}
}

func TestMCPERPPolicy(t *testing.T) {
	seed := func(t *testing.T, pol *config.MCPPolicy) (*server.MCPServer, *frappetest.Site) {
		site := erpLSite(t, erpTV16, 1)
		erpTSiteWithPayment(site)
		return erpMCP(t, site, pol), site
	}

	// Every DocType in a tool's scope can refuse it, and nothing is sent.
	for tool, c := range erpMCPCalls {
		for _, dt := range c.doctypes {
			s, site := seed(t, &config.MCPPolicy{DenyDoctypes: []string{strings.ToLower(dt)}})
			before := len(site.Requests())
			mcpTErr(t, s, tool, c.args, fmt.Sprintf("policy: DocType %q is denied by sites.test.mcp.deny_doctypes", dt))
			if n := len(site.Requests()); n != before {
				t.Errorf("%s denied for %s, but %d requests reached the site", tool, dt, n-before)
			}
		}
		// An allow list that lacks one of them refuses it; one with all allows.
		for _, left := range c.doctypes {
			var allow []string
			for _, dt := range c.doctypes {
				if dt != left {
					allow = append(allow, dt)
				}
			}
			s, _ := seed(t, &config.MCPPolicy{AllowDoctypes: append(allow, "ToDo")})
			mcpTErr(t, s, tool, c.args, fmt.Sprintf("policy: DocType %q is not in sites.test.mcp.allow_doctypes", left))
		}
		s, _ := seed(t, &config.MCPPolicy{AllowDoctypes: c.doctypes})
		mcpTOK(t, s, tool, c.args)
	}

	// The flags (--deny-doctypes) apply too.
	site := erpLSite(t, erpTV16, 1)
	sc := &config.SiteConfig{Name: "test", URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}
	c, err := client.New(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	env := &mcpEnv{
		confirm: newConfirmer(),
		sites:   []string{"test"}, toolsets: knownToolsets,
		flags:  config.MCPPolicy{DenyDoctypes: []string{"Bin"}},
		site:   func(context.Context, string) (*config.SiteConfig, error) { return sc, nil },
		client: func(context.Context, *config.SiteConfig) (*client.FrappeClient, error) { return c, nil },
	}
	s := server.NewMCPServer("test", "0")
	registerTools(s, env, []mcpPolicy{newMCPPolicy(sc, env.flags)})
	mcpTErr(t, s, "erp_stock", erpMCPCalls["erp_stock"].args, `DocType "Bin" is denied by --deny-doctypes`)
	mcpTErr(t, s, "erp_item", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme"}, `DocType "Bin" is denied by --deny-doctypes`)

	// Denying what is not in the scope changes nothing: erp_item for a
	// Sales Invoice row reads no Sales Invoice, and without a customer no Customer.
	s, _ = seed(t, &config.MCPPolicy{DenyDoctypes: []string{"Sales Invoice", "Customer", "Supplier"}})
	mcpTOK(t, s, "erp_item", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme"})
	mcpTOK(t, s, "erp_item", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme", "doctype": "Sales Invoice"})
	mcpTErr(t, s, "erp_item", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme", "customer": "CUST-1"}, `DocType "Customer" is denied`)
	mcpTErr(t, s, "erp_party", map[string]interface{}{"supplier": "SUP-1"}, `DocType "Supplier" is denied`)

	// What the server methods read on their own is in the scope: stock levels
	// (Bin) and the default Price List for an item, address and contact for a party,
	// the Account of a payment.
	for _, tc := range []struct {
		tool, deny string
		args       map[string]interface{}
	}{
		{"erp_item", "Bin", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme"}},
		{"erp_item", "Price List", map[string]interface{}{"item_code": "ITEM-1", "company": "Acme"}},
		{"erp_party", "Address", map[string]interface{}{"customer": "CUST-1"}},
		{"erp_party", "Contact", map[string]interface{}{"customer": "CUST-1"}},
		{"erp_party", "Address", map[string]interface{}{"supplier": "SUP-1"}},
		{"erp_payment", "Account", map[string]interface{}{"against_doctype": "Sales Invoice", "against_name": "SRC-1", "bank_account": "Cash - A"}},
	} {
		s, site := seed(t, &config.MCPPolicy{DenyDoctypes: []string{tc.deny}})
		mcpTErr(t, s, tc.tool, tc.args, fmt.Sprintf("DocType %q is denied", tc.deny))
		if n := len(site.Requests()); n != 0 {
			t.Errorf("%s denied for %s: %d requests reached the site", tc.tool, tc.deny, n)
		}
	}
	// Without bank_account, denying Account changes nothing for a payment.
	s, _ = seed(t, &config.MCPPolicy{DenyDoctypes: []string{"Account"}})
	mcpTOK(t, s, "erp_payment", map[string]interface{}{"against_doctype": "Sales Invoice", "against_name": "SRC-1"})

	// read_only keeps them: they are reads.
	s, site2 := seed(t, &config.MCPPolicy{ReadOnly: true})
	for tool, c := range erpMCPCalls {
		mcpTOK(t, s, tool, c.args)
	}
	for _, r := range site2.Requests() {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			t.Errorf("read-only: %s %s", r.Method, r.Path)
		}
	}
	// allow_tools can still drop them.
	s, _ = seed(t, &config.MCPPolicy{AllowTools: []string{"erp_item", "get_doc"}})
	if got := mcpTToolNames(t, s); !reflect.DeepEqual(got, []string{"erp_item", "get_doc"}) {
		t.Errorf("allow_tools: %v", got)
	}
}

func TestMCPERPMultiSite(t *testing.T) {
	site := erpLSite(t, erpTV16, 1)
	sc := &config.SiteConfig{Name: "a", URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}
	c, err := client.New(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	env := &mcpEnv{
		confirm: newConfirmer(),
		sites:   []string{"a", "b"}, toolsets: []string{"erp"},
		site: func(_ context.Context, name string) (*config.SiteConfig, error) {
			cp := *sc
			cp.Name = name
			return &cp, nil
		},
		client: func(context.Context, *config.SiteConfig) (*client.FrappeClient, error) { return c, nil },
	}
	s := server.NewMCPServer("test", "0")
	registerTools(s, env, []mcpPolicy{newMCPPolicy(sc, env.flags), newMCPPolicy(&config.SiteConfig{Name: "b"}, env.flags)})
	for _, name := range mcpTERPTools {
		tool := s.ListTools()[name].Tool
		if _, ok := tool.InputSchema.Properties["site"]; !ok || !contains(tool.InputSchema.Required, "site", false) {
			t.Errorf("%s: site is not a required argument", name)
		}
	}
	args := map[string]interface{}{"item_code": "ITEM-1", "company": "Acme"}
	mcpTErr(t, s, "erp_item", args, "site is required")
	args["site"] = "b"
	mcpTOK(t, s, "erp_item", args)
}
