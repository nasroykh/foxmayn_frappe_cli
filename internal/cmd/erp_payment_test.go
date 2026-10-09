package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const erpTPaymentMethod = "erpnext.accounts.doctype.payment_entry.payment_entry.get_payment_entry"

var erpTPayable = []string{"Sales Invoice", "Sales Order", "Purchase Invoice", "Purchase Order", "Dunning"}

// erpTPaymentDraft is what get_payment_entry answers: an unsaved Payment
// Entry with a bank account and one reference row that has no name.
func erpTPaymentDraft() map[string]interface{} {
	return map[string]interface{}{
		"doctype": "Payment Entry", "__islocal": 1, "__unsaved": 1, "docstatus": 0, "company": "Acme",
		"payment_type": "Receive", "party_type": "Customer", "party": "C1",
		"paid_from": "Debtors - A", "paid_to": "Cash - A", "paid_amount": 100,
		"references": []interface{}{map[string]interface{}{
			"doctype": "Payment Entry Reference", "name": nil, "parent": nil, "parenttype": "Payment Entry", "parentfield": "references", "idx": 1,
			"docstatus": 0, "__islocal": 1, "__temporary_name": "row1", "reference_doctype": "Sales Invoice", "reference_name": "SRC-1", "allocated_amount": 100,
		}},
	}
}

// erpPSite is erpTSite with get_payment_entry answering erpTPaymentDraft for
// every payable document SRC-1.
func erpPSite(t *testing.T, erpnext string) *frappetest.Site {
	t.Helper()
	s := erpTSite(t, erpnext)
	s.AddDocType("Payment Entry")
	for _, dt := range erpTPayable {
		s.Add(dt, map[string]interface{}{"name": "SRC-1"})
	}
	s.HandleMethod(erpTPaymentMethod, func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		if args["dn"] != "SRC-1" {
			return nil, frappetest.NotFound(fmt.Sprintf("%v %v not found", args["dt"], args["dn"]))
		}
		return erpTPaymentDraft(), nil
	})
	return s
}

func erpPAgainst(dt string) string { return dt + ":SRC-1" }

func erpPMethodRequests(s *frappetest.Site) []frappetest.Request {
	return s.RequestsTo(http.MethodGet, "/api/method/"+erpTPaymentMethod)
}

func TestERPPaymentEveryDocType(t *testing.T) {
	for _, version := range []string{erpTV15, erpTV16} {
		for _, dt := range erpTPayable {
			t.Run(version+" "+dt, func(t *testing.T) {
				s := erpPSite(t, version)
				r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "payment", "--against", erpPAgainst(dt)))
				got := cmdTObj(t, r)
				if got["doctype"] != "Payment Entry" || fmt.Sprint(got["__islocal"]) != "1" {
					t.Errorf("output = %v", got)
				}
				req := erpPMethodRequests(s)
				if len(req) != 1 {
					t.Fatalf("method requests = %+v", req)
				}
				// Only the document is sent: the server picks everything else.
				if q := req[0].Query; len(q) != 2 || q.Get("dt") != dt || q.Get("dn") != "SRC-1" {
					t.Errorf("query = %v", q)
				}
				if w := erpTWrites(s); len(w) != 0 {
					t.Errorf("writes: %+v", w)
				}
				if n := s.Count("Payment Entry"); n != 0 {
					t.Errorf("%d Payment Entry saved", n)
				}
			})
		}
	}
}

func TestERPPaymentFlagsAreSent(t *testing.T) {
	s := erpPSite(t, erpTV16)
	cmdTOK(t, cmdTRun(t, s, "--json", "erp", "payment", "--against", erpPAgainst("Purchase Invoice"),
		"--amount", "12.50", "--bank-account", "Bank - A", "--reference-date", "2026-10-09"))
	req := erpPMethodRequests(s)
	if len(req) != 1 {
		t.Fatalf("method requests = %+v", req)
	}
	q := req[0].Query
	// A number goes as a number, so the server's int | float check accepts it.
	if q.Get("dt") != "Purchase Invoice" || q.Get("dn") != "SRC-1" || q.Get("party_amount") != "12.50" ||
		q.Get("bank_account") != "Bank - A" || q.Get("reference_date") != "2026-10-09" || len(q) != 5 {
		t.Errorf("query = %v", q)
	}
}

func TestERPPaymentDefaultPrintsDraftOnly(t *testing.T) {
	s := erpPSite(t, erpTV16)
	r := cmdTOK(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Invoice")))
	if !strings.Contains(r.Stderr, "Unsaved draft of a Payment Entry: nothing was written") {
		t.Errorf("stderr = %q", r.Stderr)
	}
	if w := erpTWrites(s); len(w) != 0 {
		t.Errorf("writes: %+v", w)
	}
}

func TestERPPaymentCreate(t *testing.T) {
	s := erpPSite(t, erpTV15)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--create", "--keys", "name,docstatus"))
	got := cmdTObj(t, r)
	if got["name"] != "Payment-Entry-0001" || fmt.Sprint(got["docstatus"]) != "0" || len(got) != 2 {
		t.Errorf("output = %v", got)
	}
	posts := s.RequestsTo(http.MethodPost, "/api/resource/Payment Entry")
	if len(posts) != 1 {
		t.Fatalf("insert requests = %d", len(posts))
	}
	if strings.Contains(posts[0].Body, "__") {
		t.Errorf("insert body = %s", posts[0].Body)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(posts[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	refs, _ := body["references"].([]interface{})
	var row map[string]interface{}
	if len(refs) == 1 {
		row, _ = refs[0].(map[string]interface{})
	}
	if body["paid_to"] != "Cash - A" || row["reference_name"] != "SRC-1" || row["parentfield"] != "references" {
		t.Errorf("insert body = %s", posts[0].Body)
	}
	if n := len(s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit")); n != 0 {
		t.Errorf("%d submits", n)
	}
}

func TestERPPaymentSubmit(t *testing.T) {
	s := erpPSite(t, erpTV16)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "payment", "--against", erpPAgainst("Sales Order"), "--submit", "--keys", "name,docstatus"))
	got := cmdTObj(t, r)
	if fmt.Sprint(got["docstatus"]) != "1" {
		t.Errorf("output = %v", got)
	}
	if ds := lcTDocstatus(t, s, "Payment Entry", fmt.Sprint(got["name"])); ds != "1" {
		t.Errorf("docstatus = %s", ds)
	}
	if n := len(s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit")); n != 1 {
		t.Errorf("%d submits", n)
	}
	// The table view says what happened.
	s = erpPSite(t, erpTV16)
	r = cmdTOK(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Order"), "--submit"))
	if !strings.Contains(r.Stderr, "Created and submitted Payment Entry Payment-Entry-0001 against Sales Order SRC-1") {
		t.Errorf("stderr = %q", r.Stderr)
	}
}

func TestERPPaymentSubmitRefusesWorkflowBeforeAnyWrite(t *testing.T) {
	s := erpPSite(t, erpTV16)
	s.Add("Workflow", map[string]interface{}{"name": "PE Approval", "document_type": "Payment Entry", "is_active": json.Number("1")})
	lcTCode(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--submit"), exitValidation, `workflow "PE Approval"`)
	if w := erpTWrites(s); len(w) != 0 {
		t.Errorf("writes before the refusal: %+v", w)
	}
	// A draft may still be created: only the submit moves through the workflow.
	cmdTOK(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--create"))
	if n := s.Count("Payment Entry"); n != 1 {
		t.Errorf("%d Payment Entry saved, want 1", n)
	}
}

// A submit that fails after the insert leaves a draft behind: its name is
// printed like a created document's, the error keeps its exit class, and
// nothing is inserted twice.
func TestERPPaymentSubmitFailsAfterInsert(t *testing.T) {
	s := erpPSite(t, erpTV16)
	s.HandleMethod("frappe.client.submit", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Validation("Reference Date is mandatory")
	})
	r := cmdTRun(t, s, "--json", "erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--submit", "--keys", "name,docstatus")
	lcTCode(t, r, exitValidation, "created Payment Entry Payment-Entry-0001, but the submit failed", "Reference Date is mandatory")
	if got := cmdTObj(t, r); got["name"] != "Payment-Entry-0001" || fmt.Sprint(got["docstatus"]) != "0" {
		t.Errorf("stdout = %s", r.Stdout)
	}
	if n := len(s.RequestsTo(http.MethodPost, "/api/resource/Payment Entry")); n != 1 {
		t.Errorf("%d inserts, want 1", n)
	}
}

func TestERPPaymentInsertWithoutName(t *testing.T) {
	s := erpPSite(t, erpTV16)
	s.Handle("POST /api/resource/Payment Entry", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"doctype":"Payment Entry"}}`))
	}))
	lcTCode(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--submit"), exitGeneric, "no name")
	if n := len(s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit")); n != 0 {
		t.Errorf("%d submits", n)
	}
}

func TestERPPaymentDryRun(t *testing.T) {
	s := erpPSite(t, erpTV16)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--dry-run"))
	if got := cmdTObj(t, r); got["doctype"] != "Payment Entry" {
		t.Errorf("output = %v", got)
	}
	for _, flag := range []string{"--create", "--submit"} {
		s := erpPSite(t, erpTV16)
		r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "payment", "--against", erpPAgainst("Sales Invoice"), flag, "--dry-run"))
		// The draft is a GET and still runs; the writes are only planned.
		if n := len(erpPMethodRequests(s)); n != 1 {
			t.Errorf("%s: %d method requests", flag, n)
		}
		if w := erpTWrites(s); len(w) != 0 {
			t.Errorf("%s: writes sent: %+v", flag, w)
		}
		if n := s.Count("Payment Entry"); n != 0 {
			t.Errorf("%s: %d Payment Entry saved", flag, n)
		}
		plan := cmdTObj(t, r)
		reqs, _ := plan["requests"].([]interface{})
		want := map[string]int{"--create": 1, "--submit": 2}[flag]
		if plan["dry_run"] != true || len(reqs) != want {
			t.Fatalf("%s: plan = %v", flag, plan)
		}
		if insert := reqs[0].(map[string]interface{}); insert["method"] != "POST" || !strings.Contains(fmt.Sprint(insert["url"]), "/api/resource/Payment") {
			t.Errorf("%s: insert plan = %v", flag, insert)
		}
	}
}

func TestERPPaymentUsage(t *testing.T) {
	s := erpPSite(t, erpTV16)
	lcTCode(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Quotation")), exitUsage,
		"cannot make a payment against Quotation", "Sales Invoice, Sales Order, Purchase Invoice, Purchase Order, Dunning")
	for _, args := range [][]string{
		{"erp", "payment"},
		{"erp", "payment", "--against", "Sales Invoice"},
		{"erp", "payment", "--against", "Sales Invoice:"},
		{"erp", "payment", "--against", ":SINV-1"},
		{"erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--amount", "0"},
		{"erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--amount", "-5"},
		{"erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--amount", "1e3"},
		{"erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--amount", "Inf"},
		{"erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--amount", ""},
		{"erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--bank-account", " "},
		{"erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--reference-date", "09/10/2026"},
		{"erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--reference-date", "2026-13-01"},
	} {
		lcTCode(t, cmdTRun(t, s, args...), exitUsage)
	}
	if n := len(erpPMethodRequests(s)); n != 0 {
		t.Errorf("%d method requests for usage errors", n)
	}
	// A usage error is answered before the site is asked anything.
	if n := len(s.Requests()); n != 0 {
		t.Errorf("%d requests for usage errors", n)
	}
}

func TestERPPaymentNotInstalled(t *testing.T) {
	s := erpPSite(t, "")
	lcTCode(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Invoice")), exitNotFound, "ERPNext is not installed on t")
	if n := len(erpPMethodRequests(s)); n != 0 {
		t.Errorf("%d method requests", n)
	}
}

func TestERPPaymentUnsupportedMajor(t *testing.T) {
	for _, version := range []string{"17.0.0-dev", "14.80.0"} {
		s := erpPSite(t, version)
		lcTCode(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Invoice")), exitGeneric, "ffc api", version)
		if n := len(erpPMethodRequests(s)); n != 0 {
			t.Errorf("%s: %d method requests", version, n)
		}
	}
}

// What Frappe's throws look like through the existing error mapping: a
// validation (417) is exit 6, a permission (403) exit 5, a missing document
// (404) exit 4. Nothing is written after any of them.
func TestERPPaymentServerErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
		msg  string
	}{
		{"fully billed", frappetest.Validation("Can only make payment against unbilled Sales Order"), exitValidation, "Can only make payment against unbilled Sales Order"},
		{"supplier blocked", frappetest.Validation("Supplier S1 is blocked, so this transaction cannot proceed"), exitValidation, "is blocked"},
		{"no permission", frappetest.Permission("No permission for Payment Entry"), exitPermission, "No permission"},
		{"missing document", frappetest.NotFound("Sales Invoice SRC-1 not found"), exitNotFound, "not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := erpPSite(t, erpTV16)
			s.HandleMethod(erpTPaymentMethod, func(*http.Request, map[string]interface{}) (interface{}, error) { return nil, tc.err })
			lcTCode(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Order"), "--submit"), tc.code, tc.msg)
			if w := erpTWrites(s); len(w) != 0 {
				t.Errorf("writes: %+v", w)
			}
		})
	}
}

// A company without a default bank or cash account gets a draft with no
// account from get_payment_entry, not an error: the draft is printed with a
// warning, and --create refuses it before any write.
func TestERPPaymentNoBankAccount(t *testing.T) {
	noAccount := func(s *frappetest.Site) {
		s.HandleMethod(erpTPaymentMethod, func(*http.Request, map[string]interface{}) (interface{}, error) {
			d := erpTPaymentDraft()
			d["paid_to"] = nil
			return d, nil
		})
	}
	s := erpPSite(t, erpTV16)
	noAccount(s)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "erp", "payment", "--against", erpPAgainst("Sales Invoice")))
	if !strings.Contains(r.Stderr, "warning: the Payment Entry draft has no bank or cash account (paid_to is empty)") || !strings.Contains(r.Stderr, "Acme") {
		t.Errorf("stderr = %q", r.Stderr)
	}
	if got := cmdTObj(t, r); got["doctype"] != "Payment Entry" {
		t.Errorf("stdout = %s", r.Stdout)
	}

	for _, flag := range []string{"--create", "--submit"} {
		s := erpPSite(t, erpTV16)
		noAccount(s)
		lcTCode(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Invoice"), flag), exitValidation, "no bank or cash account", "--bank-account")
		if w := erpTWrites(s); len(w) != 0 {
			t.Errorf("%s: writes: %+v", flag, w)
		}
	}
}

// ignore_permissions is not a parameter of get_payment_entry; ffc must never
// send it, whatever the command.
func TestERPPaymentNeverSendsIgnorePermissions(t *testing.T) {
	s := erpPSite(t, erpTV16)
	cmdTOK(t, cmdTRun(t, s, "erp", "payment", "--against", erpPAgainst("Sales Invoice"), "--amount", "5", "--bank-account", "B", "--reference-date", "2026-01-02", "--submit"))
	for _, r := range s.Requests() {
		if strings.Contains(r.Path+r.Query.Encode()+r.Body, "ignore_permissions") {
			t.Errorf("ignore_permissions sent: %s %s?%s %s", r.Method, r.Path, r.Query.Encode(), r.Body)
		}
	}
}
