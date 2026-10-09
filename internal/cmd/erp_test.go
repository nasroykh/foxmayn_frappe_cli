package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitecache"
)

// erpTPairs are the mappers ffc knows, written out here on purpose: the
// table in client/erpnext.go must give exactly these paths.
var erpTPairs = []struct{ from, to, method string }{
	{"Quotation", "Sales Order", "erpnext.selling.doctype.quotation.quotation.make_sales_order"},
	{"Sales Order", "Sales Invoice", "erpnext.selling.doctype.sales_order.sales_order.make_sales_invoice"},
	{"Sales Order", "Delivery Note", "erpnext.selling.doctype.sales_order.sales_order.make_delivery_note"},
	{"Delivery Note", "Sales Invoice", "erpnext.stock.doctype.delivery_note.delivery_note.make_sales_invoice"},
	{"Purchase Order", "Purchase Receipt", "erpnext.buying.doctype.purchase_order.purchase_order.make_purchase_receipt"},
	{"Purchase Order", "Purchase Invoice", "erpnext.buying.doctype.purchase_order.purchase_order.make_purchase_invoice"},
	{"Purchase Receipt", "Purchase Invoice", "erpnext.stock.doctype.purchase_receipt.purchase_receipt.make_purchase_invoice"},
	{"Material Request", "Purchase Order", "erpnext.stock.doctype.material_request.material_request.make_purchase_order"},
}

const (
	erpTV15 = "15.121.6"
	erpTV16 = "16.37.0"
)

// erpTDraft is what a mapper answers: an unsaved document with a local name
// and a child row, both carrying the __ keys the desk's form uses.
func erpTDraft(doctype string) map[string]interface{} {
	return map[string]interface{}{
		"doctype": doctype, "name": "new-" + strings.ToLower(strings.ReplaceAll(doctype, " ", "-")) + "-abc",
		"__islocal": 1, "__unsaved": 1, "docstatus": 0, "customer": "C1",
		"items": []interface{}{map[string]interface{}{
			"doctype": doctype + " Item", "name": "new-item-1", "__islocal": 1, "item_code": "I1", "qty": 2,
		}},
	}
}

// erpTSite is a site with ERPNext at the given version ("" = not installed)
// whose mappers answer erpTDraft for the source document SRC-1.
func erpTSite(t *testing.T, erpnext string) *frappetest.Site {
	t.Helper()
	// The version cache is keyed by site name and URL: never share it between sites.
	dir := t.TempDir()
	old := sitecache.UserCacheDir
	sitecache.UserCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { sitecache.UserCacheDir = old })

	s := frappetest.New(t)
	apps := map[string]frappetest.App{"frappe": {Title: "Frappe Framework", Version: "16.36.1"}}
	if erpnext != "" {
		apps["erpnext"] = frappetest.App{Title: "ERPNext", Version: erpnext}
	}
	s.SetApps(apps)
	for _, p := range erpTPairs {
		p := p
		s.Add(p.from, map[string]interface{}{"name": "SRC-1", "quotation_to": "Customer"})
		s.AddDocType(p.to)
		s.HandleMethod(p.method, func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
			if args["source_name"] != "SRC-1" {
				return nil, frappetest.NotFound(fmt.Sprintf("%s %v not found", p.from, args["source_name"]))
			}
			return erpTDraft(p.to), nil
		})
	}
	return s
}

func erpTFrom(from string) string { return from + ":SRC-1" }

// erpTWrites returns the requests that could have changed the site.
func erpTWrites(s *frappetest.Site) []frappetest.Request {
	var out []frappetest.Request
	for _, r := range s.Requests() {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			out = append(out, r)
		}
	}
	return out
}

func TestERPMapEveryPair(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		for _, p := range erpTPairs {
			t.Run(version+" "+p.from+" to "+p.to, func(t *testing.T) {
				s := erpTSite(t, version)
				before := s.Count(p.to) // the source DocType of another pair holds SRC-1
				r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "map", "--from", erpTFrom(p.from), "--to", p.to))
				// The draft is printed as the mapper answered it, and nothing is saved.
				got := cmdTObj(t, r)
				if got["doctype"] != p.to || fmt.Sprint(got["__islocal"]) != "1" {
					t.Errorf("output = %v", got)
				}
				if req := s.RequestsTo(http.MethodGet, "/api/method/"+p.method); len(req) != 1 || req[0].Query.Get("source_name") != "SRC-1" {
					t.Errorf("mapper requests = %+v", req)
				}
				if w := erpTWrites(s); len(w) != 0 {
					t.Errorf("writes: %+v", w)
				}
				if n := s.Count(p.to); n != before {
					t.Errorf("%s: %d documents before, %d after", p.to, before, n)
				}
			})
		}
	}
}

func TestERPMapCreate(t *testing.T) {
	s := erpTSite(t, erpTV16)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "map", "--from", erpTFrom("Sales Order"), "--to", "Sales Invoice", "--create", "--keys", "name,docstatus"))
	got := cmdTObj(t, r)
	if got["name"] != "Sales-Invoice-0001" || fmt.Sprint(got["docstatus"]) != "0" || len(got) != 2 {
		t.Errorf("output = %v", got)
	}
	posts := s.RequestsTo(http.MethodPost, "/api/resource/Sales Invoice")
	if len(posts) != 1 {
		t.Fatalf("insert requests = %d", len(posts))
	}
	// Nothing the form adds travels back, and the local names stay home.
	if strings.Contains(posts[0].Body, "__") || strings.Contains(posts[0].Body, "new-") {
		t.Errorf("insert body = %s", posts[0].Body)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(posts[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	items, _ := body["items"].([]interface{})
	if body["customer"] != "C1" || len(items) != 1 || items[0].(map[string]interface{})["item_code"] != "I1" {
		t.Errorf("insert body = %s", posts[0].Body)
	}
	if n := len(s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit")); n != 0 {
		t.Errorf("%d submits", n)
	}
}

func TestERPMapSubmit(t *testing.T) {
	s := erpTSite(t, erpTV15)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "map", "--from", erpTFrom("Purchase Order"), "--to", "Purchase Invoice", "--submit", "--keys", "name,docstatus"))
	got := cmdTObj(t, r)
	if fmt.Sprint(got["docstatus"]) != "1" {
		t.Errorf("output = %v", got)
	}
	if ds := lcTDocstatus(t, s, "Purchase Invoice", fmt.Sprint(got["name"])); ds != "1" {
		t.Errorf("docstatus = %s", ds)
	}
	if n := len(s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit")); n != 1 {
		t.Errorf("%d submits", n)
	}
}

func TestERPMapSubmitRefusesWorkflowBeforeAnyWrite(t *testing.T) {
	s := erpTSite(t, erpTV16)
	s.Add("Workflow", map[string]interface{}{"name": "SI Approval", "document_type": "Sales Invoice", "is_active": json.Number("1")})
	lcTCode(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Sales Order"), "--to", "Sales Invoice", "--submit"), exitValidation, `workflow "SI Approval"`)
	if w := erpTWrites(s); len(w) != 0 {
		t.Errorf("writes before the refusal: %+v", w)
	}
	if n := s.Count("Sales Invoice"); n != 0 {
		t.Errorf("%d Sales Invoice saved", n)
	}
	// A draft may still be created: only the submit moves through the workflow.
	cmdTOK(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Sales Order"), "--to", "Sales Invoice", "--create"))
	if n := s.Count("Sales Invoice"); n != 1 {
		t.Errorf("%d Sales Invoice saved, want 1", n)
	}
}

func TestERPMapNotInstalled(t *testing.T) {
	s := erpTSite(t, "")
	lcTCode(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Sales Order"), "--to", "Sales Invoice"), exitNotFound, "ERPNext is not installed on t")
	if n := len(s.Requests()); n == 0 {
		t.Fatal("no request")
	}
	for _, r := range s.Requests() {
		if strings.Contains(r.Path, "make_") {
			t.Errorf("mapper called: %s", r.Path)
		}
	}
}

func TestERPMapUnsupportedMajor(t *testing.T) {
	for _, version := range []string{"17.0.0-dev", "14.80.0", "develop"} {
		s := erpTSite(t, version)
		lcTCode(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Sales Order"), "--to", "Sales Invoice"), exitGeneric, "ffc api", version)
		for _, r := range s.Requests() {
			if strings.Contains(r.Path, "make_") {
				t.Errorf("%s: mapper called: %s", version, r.Path)
			}
		}
	}
}

func TestERPMapUsage(t *testing.T) {
	s := erpTSite(t, erpTV16)
	// A pair ffc does not know lists the ones it does.
	lcTCode(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Sales Invoice"), "--to", "Sales Order"), exitUsage,
		"cannot map Sales Invoice to Sales Order", "Sales Order -> Sales Invoice", "Material Request -> Purchase Order")
	for _, args := range [][]string{
		{"erp", "map", "--from", "Sales Order", "--to", "Sales Invoice"},
		{"erp", "map", "--from", "Sales Order:", "--to", "Sales Invoice"},
		{"erp", "map", "--from", ":SO-1", "--to", "Sales Invoice"},
		{"erp", "map", "--from", erpTFrom("Sales Order")},
		{"erp", "map", "--from", erpTFrom("Sales Order"), "--to", " "},
	} {
		lcTCode(t, cmdTRun(t, s, args...), exitUsage)
	}
	for _, r := range s.Requests() {
		if strings.Contains(r.Path, "make_") {
			t.Errorf("mapper called: %s", r.Path)
		}
	}
}

func TestERPMapMissingSource(t *testing.T) {
	s := erpTSite(t, erpTV16)
	lcTCode(t, cmdTRun(t, s, "erp", "map", "--from", "Sales Order:NOPE", "--to", "Sales Invoice"), exitNotFound)
	if w := erpTWrites(s); len(w) != 0 {
		t.Errorf("writes: %+v", w)
	}
}

func TestERPMapLeadQuotation(t *testing.T) {
	for _, to := range []string{"Lead", "Prospect"} {
		s := erpTSite(t, erpTV16)
		s.Add("Quotation", map[string]interface{}{"name": "SRC-1", "quotation_to": to})
		lcTCode(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Quotation"), "--to", "Sales Order", "--create"), exitValidation,
			"convert the "+strings.ToLower(to)+" to a customer first")
		for _, r := range s.Requests() {
			if strings.Contains(r.Path, "make_sales_order") {
				t.Errorf("%s: the mapper ran (it would create the Customer): %s", to, r.Path)
			}
		}
		if w := erpTWrites(s); len(w) != 0 {
			t.Errorf("%s: writes: %+v", to, w)
		}
	}
	// A Customer quotation maps; one that does not exist is not found.
	s := erpTSite(t, erpTV16)
	cmdTOK(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Quotation"), "--to", "Sales Order"))
	lcTCode(t, cmdTRun(t, s, "erp", "map", "--from", "Quotation:NOPE", "--to", "Sales Order"), exitNotFound)
}

func TestERPMapDryRun(t *testing.T) {
	// Without --create the dry run changes nothing: the draft is printed.
	s := erpTSite(t, erpTV16)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "map", "--from", erpTFrom("Sales Order"), "--to", "Sales Invoice", "--dry-run"))
	if got := cmdTObj(t, r); got["doctype"] != "Sales Invoice" {
		t.Errorf("output = %v", got)
	}

	for _, flag := range []string{"--create", "--submit"} {
		s := erpTSite(t, erpTV16)
		r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "map", "--from", erpTFrom("Sales Order"), "--to", "Sales Invoice", flag, "--dry-run"))
		// The mapping is a GET and still runs; the writes are only planned.
		if n := len(s.RequestsTo(http.MethodGet, "/api/method/"+erpTPairs[1].method)); n != 1 {
			t.Errorf("%s: %d mapper requests", flag, n)
		}
		if w := erpTWrites(s); len(w) != 0 {
			t.Errorf("%s: writes sent: %+v", flag, w)
		}
		if n := s.Count("Sales Invoice"); n != 0 {
			t.Errorf("%s: %d Sales Invoice saved", flag, n)
		}
		plan := cmdTObj(t, r)
		reqs, _ := plan["requests"].([]interface{})
		want := map[string]int{"--create": 1, "--submit": 2}[flag]
		if plan["dry_run"] != true || len(reqs) != want {
			t.Fatalf("%s: plan = %v", flag, plan)
		}
		insert := reqs[0].(map[string]interface{})
		if insert["method"] != "POST" || !strings.HasSuffix(fmt.Sprint(insert["url"]), "/api/resource/Sales%20Invoice") && !strings.HasSuffix(fmt.Sprint(insert["url"]), "/api/resource/Sales Invoice") {
			t.Errorf("%s: insert plan = %v", flag, insert)
		}
		if flag == "--submit" {
			if submit := reqs[1].(map[string]interface{}); !strings.HasSuffix(fmt.Sprint(submit["url"]), "/api/method/frappe.client.submit") {
				t.Errorf("submit plan = %v", submit)
			}
		}
	}
}

// ignore_permissions skips the permission checks of the mappers that take
// it: ffc must never send it, whatever the command.
func TestERPMapNeverSendsIgnorePermissions(t *testing.T) {
	s := erpTSite(t, erpTV16)
	cmdTOK(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Sales Order"), "--to", "Sales Invoice", "--submit"))
	cmdTOK(t, cmdTRun(t, s, "erp", "map", "--from", erpTFrom("Quotation"), "--to", "Sales Order", "--dry-run", "--submit"))
	for _, r := range s.Requests() {
		if strings.Contains(r.Path+r.Query.Encode()+r.Body, "ignore_permissions") {
			t.Errorf("ignore_permissions sent: %s %s?%s %s", r.Method, r.Path, r.Query.Encode(), r.Body)
		}
	}
}
