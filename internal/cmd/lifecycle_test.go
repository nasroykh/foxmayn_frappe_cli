package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// lcTSite has a draft, a submitted and a cancelled Sales Order (with child
// rows) and Workflow-free DocTypes.
func lcTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	items := []interface{}{map[string]interface{}{"name": "row1", "item_code": "A", "qty": json.Number("2"), "parent": "SO-3", "doctype": "Sales Order Item"}}
	s.Add("Sales Order",
		map[string]interface{}{"name": "SO-1", "customer": "C1"},
		map[string]interface{}{"name": "SO-2", "customer": "C2", "docstatus": json.Number("1"), "po_no": "PO-2", "items": items},
		map[string]interface{}{"name": "SO-3", "customer": "C3", "docstatus": json.Number("2"), "po_no": "PO-3", "items": items},
	)
	s.ChildTable("Sales Order", "Sales Order Item")
	s.NoCopy("Sales Order", "po_no")
	s.NoCopy("Sales Order Item", "qty")
	return s
}

func lcTDocstatus(t *testing.T, s *frappetest.Site, doctype, name string) string {
	t.Helper()
	d, ok := s.Doc(doctype, name)
	if !ok {
		t.Fatalf("%s %s missing", doctype, name)
	}
	return fmt.Sprint(d["docstatus"])
}

func lcTCode(t *testing.T, r cliResult, code int, wantInErr ...string) {
	t.Helper()
	cmdTFail(t, r, wantInErr...)
	if r.Code != code {
		t.Errorf("exit code = %d, want %d (err %v)", r.Code, code, r.Err)
	}
}

func TestSubmitDoc(t *testing.T) {
	s := lcTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "submit-doc", "-d", "Sales Order", "-n", "SO-1", "--json", "--keys", "name,docstatus"))
	if got := cmdTJSON(t, r).(map[string]interface{}); fmt.Sprint(got["docstatus"]) != "1" || len(got) != 2 {
		t.Errorf("output = %v", got)
	}
	if ds := lcTDocstatus(t, s, "Sales Order", "SO-1"); ds != "1" {
		t.Errorf("docstatus = %s, want 1", ds)
	}
	// The document goes back as read: Frappe's timestamp check needs modified.
	req := s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit")
	if len(req) != 1 || !strings.Contains(req[0].Body, `"modified"`) {
		t.Errorf("submit requests = %+v", req)
	}
	// Submitting a submitted document is Frappe's DocstatusTransitionError.
	lcTCode(t, cmdTRun(t, s, "submit-doc", "-d", "Sales Order", "-n", "SO-2"), exitValidation, "Cannot change docstatus from 1 to 1")
}

func TestSubmitCancelRefuseWorkflow(t *testing.T) {
	s := lcTSite(t)
	s.Add("Workflow", map[string]interface{}{"name": "SO Approval", "document_type": "Sales Order", "is_active": json.Number("1")})
	lcTCode(t, cmdTRun(t, s, "submit-doc", "-d", "Sales Order", "-n", "SO-1"), exitValidation, `workflow "SO Approval"`, "ffc workflow transitions")
	lcTCode(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-2", "--yes"), exitValidation, "SO Approval")
	if n := len(s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit")) + len(s.RequestsTo(http.MethodPost, "/api/method/frappe.client.cancel")); n != 0 {
		t.Errorf("%d submit/cancel requests sent", n)
	}
	// An inactive workflow does not count.
	s2 := lcTSite(t)
	s2.Add("Workflow", map[string]interface{}{"name": "Old", "document_type": "Sales Order", "is_active": json.Number("0")})
	cmdTOK(t, cmdTRun(t, s2, "submit-doc", "-d", "Sales Order", "-n", "SO-1"))
}

func TestSubmitWorkflowUnreadable(t *testing.T) {
	// A user who may not read Workflows: the server's own checks decide.
	s := lcTSite(t)
	s.Handle("GET /api/resource/Workflow", frappetest.ErrorHandler(frappetest.Permission("No permission for Workflow")))
	cmdTOK(t, cmdTRun(t, s, "submit-doc", "-d", "Sales Order", "-n", "SO-1"))
}

func TestCancelDoc(t *testing.T) {
	s := lcTSite(t)
	lcTCode(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-2"), exitUsage, "pass --yes")
	cmdTOK(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-2", "--yes"))
	if ds := lcTDocstatus(t, s, "Sales Order", "SO-2"); ds != "2" {
		t.Errorf("docstatus = %s, want 2", ds)
	}
	lcTCode(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"), exitValidation, "Cannot change docstatus from 0 to 2")
}

func TestCancelCheck(t *testing.T) {
	s := lcTSite(t)
	s.HandleMethod("frappe.desk.form.linked_with.get_submitted_linked_docs", func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		if args["doctype"] != "Sales Order" || args["name"] != "SO-2" {
			return nil, frappetest.Validation(fmt.Sprintf("args %v", args))
		}
		return map[string]interface{}{"docs": []interface{}{
			map[string]interface{}{"doctype": "Sales Invoice", "name": "SINV-1", "docstatus": 1},
		}, "count": 1}, nil
	})
	r := cmdTOK(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-2", "--check", "--json"))
	rows := cmdTJSON(t, r).([]interface{})
	if len(rows) != 1 || rows[0].(map[string]interface{})["name"] != "SINV-1" {
		t.Errorf("rows = %v", rows)
	}
	if ds := lcTDocstatus(t, s, "Sales Order", "SO-2"); ds != "1" {
		t.Errorf("--check changed docstatus to %s", ds)
	}
	lcTCode(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-2", "--check", "--yes"), exitUsage)
}

func TestAmendDoc(t *testing.T) {
	s := lcTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "amend-doc", "-d", "Sales Order", "-n", "SO-3", "--data", `{"customer":"C9"}`, "--json"))
	got := cmdTJSON(t, r).(map[string]interface{})
	if got["name"] != "SO-3-1" || got["amended_from"] != "SO-3" || got["customer"] != "C9" || fmt.Sprint(got["docstatus"]) != "0" {
		t.Errorf("amendment = %v", got)
	}
	// Child rows are copied without their identity.
	post := s.RequestsTo(http.MethodPost, "/api/resource/Sales Order")
	if len(post) != 1 {
		t.Fatalf("create requests = %d", len(post))
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(post[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	rows, _ := body["items"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("items = %v", body["items"])
	}
	// An amendment keeps the "no copy" fields, like the desk's Amend.
	row := rows[0].(map[string]interface{})
	if row["item_code"] != "A" || row["qty"] == nil || row["name"] != nil || row["parent"] != nil {
		t.Errorf("child row = %v", row)
	}
	if body["po_no"] != "PO-3" {
		t.Errorf("po_no = %v, want PO-3", body["po_no"])
	}
	for _, k := range []string{"name", "creation", "modified", "owner", "docstatus"} {
		if _, ok := body[k]; ok {
			t.Errorf("amendment body keeps %q", k)
		}
	}
	// The amendment of an amendment counts up.
	cmdTOK(t, cmdTRun(t, s, "submit-doc", "-d", "Sales Order", "-n", "SO-3-1"))
	cmdTOK(t, cmdTRun(t, s, "cancel-doc", "-d", "Sales Order", "-n", "SO-3-1", "--yes"))
	r = cmdTOK(t, cmdTRun(t, s, "amend-doc", "-d", "Sales Order", "-n", "SO-3-1", "--jq", ".name"))
	if strings.TrimSpace(r.Stdout) != "SO-3-2" {
		t.Errorf("amendment of SO-3-1 = %q", r.Stdout)
	}
	// SO-3 already has its amendment.
	lcTCode(t, cmdTRun(t, s, "amend-doc", "-d", "Sales Order", "-n", "SO-3"), exitValidation, "already exists")
}

func TestAmendDocNotCancelled(t *testing.T) {
	s := lcTSite(t)
	lcTCode(t, cmdTRun(t, s, "amend-doc", "-d", "Sales Order", "-n", "SO-2"), exitValidation, "not cancelled")
	if n := len(s.RequestsTo(http.MethodPost, "/api/resource/Sales Order")); n != 0 {
		t.Errorf("%d create requests", n)
	}
	lcTCode(t, cmdTRun(t, s, "amend-doc", "-d", "Sales Order", "-n", "SO-3", "--data", `[1]`), exitUsage, "expected a JSON object")
}

func TestCopyDoc(t *testing.T) {
	s := lcTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "copy-doc", "-d", "Sales Order", "-n", "SO-2", "--data", `{"customer":"C7"}`, "--json"))
	got := cmdTJSON(t, r).(map[string]interface{})
	if got["name"] == "SO-2" || got["customer"] != "C7" || fmt.Sprint(got["docstatus"]) != "0" || got["amended_from"] != nil {
		t.Errorf("copy = %v", got)
	}
	// A copy drops the "no copy" fields, on the document and its rows.
	if _, ok := got["po_no"]; ok {
		t.Errorf("copy keeps po_no: %v", got)
	}
	rows, _ := got["items"].([]interface{})
	if len(rows) != 1 || rows[0].(map[string]interface{})["item_code"] != "A" || rows[0].(map[string]interface{})["qty"] != nil {
		t.Errorf("copy items = %v", got["items"])
	}
	lcTCode(t, cmdTRun(t, s, "copy-doc", "-d", "Sales Order", "-n", "nope"), exitNotFound)
}

func TestRenameDoc(t *testing.T) {
	s := lcTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "rename-doc", "-d", "Sales Order", "-n", "SO-1", "--to", "SO-100", "--json"))
	if got := cmdTJSON(t, r).(map[string]interface{}); got["name"] != "SO-100" || got["old_name"] != "SO-1" || got["merged"] != false {
		t.Errorf("output = %v", got)
	}
	if _, ok := s.Doc("Sales Order", "SO-100"); !ok {
		t.Error("SO-100 missing after rename")
	}
	lcTCode(t, cmdTRun(t, s, "rename-doc", "-d", "Sales Order", "-n", "SO-100", "--to", "SO-2"), exitValidation, "Another Sales Order")
	// A merge cannot be undone: it needs --yes without a terminal.
	lcTCode(t, cmdTRun(t, s, "rename-doc", "-d", "Sales Order", "-n", "SO-100", "--to", "SO-2", "--merge"), exitUsage, "pass --yes")
	cmdTOK(t, cmdTRun(t, s, "rename-doc", "-d", "Sales Order", "-n", "SO-100", "--to", "SO-2", "--merge", "--yes"))
	if _, ok := s.Doc("Sales Order", "SO-100"); ok {
		t.Error("SO-100 still exists after merge")
	}
}

func TestRestoreDoc(t *testing.T) {
	s := frappetest.New(t)
	s.Add("Deleted Document",
		map[string]interface{}{"name": "del-old", "deleted_doctype": "ToDo", "deleted_name": "TD-1", "restored": json.Number("0")},
		map[string]interface{}{"name": "del-new", "deleted_doctype": "ToDo", "deleted_name": "TD-1", "restored": json.Number("0")},
		map[string]interface{}{"name": "del-done", "deleted_doctype": "ToDo", "deleted_name": "TD-2", "restored": json.Number("1")},
	)
	var restored []string
	s.HandleMethod("frappe.core.doctype.deleted_document.deleted_document.restore", func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		id := fmt.Sprint(args["name"])
		restored = append(restored, id)
		rec, _ := s.Doc("Deleted Document", id)
		rec["restored"], rec["new_name"] = json.Number("1"), "TD-9" // a hash-named DocType gets a new name
		s.Add("Deleted Document", rec)
		return nil, nil
	})
	r := cmdTOK(t, cmdTRun(t, s, "restore-doc", "-d", "ToDo", "-n", "TD-1", "--json"))
	if got := cmdTJSON(t, r).(map[string]interface{}); got["deleted_document"] != "del-new" || got["restored"] != true || got["name"] != "TD-9" {
		t.Errorf("output = %v", got)
	}
	r = cmdTOK(t, cmdTRun(t, s, "restore-doc", "--deleted", "del-old"))
	if !strings.Contains(r.Stderr, "Restored TD-9 from Deleted Document del-old") {
		t.Errorf("stderr = %q", r.Stderr)
	}
	if strings.Join(restored, ",") != "del-new,del-old" {
		t.Errorf("restored = %v", restored)
	}
	lcTCode(t, cmdTRun(t, s, "restore-doc", "-d", "ToDo", "-n", "TD-2"), exitValidation, "no unrestored deletion")
	lcTCode(t, cmdTRun(t, s, "restore-doc"), exitUsage)
	lcTCode(t, cmdTRun(t, s, "restore-doc", "-d", "ToDo"), exitUsage)
	lcTCode(t, cmdTRun(t, s, "restore-doc", "--deleted", "x", "-n", "TD-1"), exitUsage)
}

func TestDiscardDoc(t *testing.T) {
	s := lcTSite(t)
	lcTCode(t, cmdTRun(t, s, "discard-doc", "-d", "Sales Order", "-n", "SO-1"), exitUsage, "pass --yes")
	cmdTOK(t, cmdTRun(t, s, "discard-doc", "-d", "Sales Order", "-n", "SO-1", "--yes"))
	if ds := lcTDocstatus(t, s, "Sales Order", "SO-1"); ds != "2" {
		t.Errorf("docstatus = %s, want 2", ds)
	}
	// Frappe v15 has no discard method.
	s.HandleMethod("frappe.desk.form.save.discard", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Validation("Failed to get method for command frappe.desk.form.save.discard with No module named 'x'")
	})
	cmdTFail(t, cmdTRun(t, s, "discard-doc", "-d", "Sales Order", "-n", "SO-2", "--yes"), "Frappe v16")
}

func lcTWorkflowSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	s.Add("Leave Application",
		map[string]interface{}{"name": "LA-1", "workflow_state": "Open"},
		map[string]interface{}{"name": "LA-2", "workflow_state": "Open"},
		map[string]interface{}{"name": "LA-3", "workflow_state": "Approved"},
	)
	s.HandleMethod("frappe.model.workflow.get_transitions", func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		doc := wfTDoc(args)
		if doc["name"] == "LA-3" {
			return []interface{}{}, nil
		}
		return []interface{}{map[string]interface{}{"state": "Open", "action": "Approve", "next_state": "Approved", "allowed": "HR Manager"}}, nil
	})
	s.HandleMethod("frappe.model.workflow.apply_workflow", func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		doc := wfTDoc(args)
		name := fmt.Sprint(doc["name"])
		stored, ok := s.Doc("Leave Application", name)
		if !ok {
			return nil, frappetest.NotFound("Leave Application " + name + " not found")
		}
		if args["action"] != "Approve" || stored["workflow_state"] != "Open" {
			return nil, &frappetest.Error{Status: http.StatusExpectationFailed, ExcType: "WorkflowTransitionError", Message: "Not a valid Workflow Action"}
		}
		stored["workflow_state"] = "Approved"
		s.Add("Leave Application", stored)
		return stored, nil
	})
	return s
}

// wfTDoc reads the doc argument, sent as JSON in the form body.
func wfTDoc(args map[string]interface{}) map[string]interface{} {
	doc, ok := args["doc"].(map[string]interface{})
	if !ok {
		_ = json.Unmarshal([]byte(fmt.Sprint(args["doc"])), &doc)
	}
	return doc
}

func TestWorkflowTransitionsApply(t *testing.T) {
	s := lcTWorkflowSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "workflow", "transitions", "-d", "Leave Application", "-n", "LA-1", "--jq", ".[].action"))
	if strings.TrimSpace(r.Stdout) != "Approve" {
		t.Errorf("transitions = %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "workflow", "transitions", "-d", "Leave Application", "-n", "LA-3", "--json"))
	if strings.TrimSpace(r.Stdout) != "[]" {
		t.Errorf("no transitions = %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "workflow", "apply", "-d", "Leave Application", "-n", "LA-1", "--action", "Approve", "--json", "--keys", "workflow_state"))
	if got := cmdTJSON(t, r).(map[string]interface{}); got["workflow_state"] != "Approved" {
		t.Errorf("apply = %v", got)
	}
	lcTCode(t, cmdTRun(t, s, "workflow", "apply", "-d", "Leave Application", "-n", "LA-1", "--action", "Approve"), exitValidation, "Not a valid Workflow Action")
	lcTCode(t, cmdTRun(t, s, "workflow", "apply", "-d", "Leave Application", "-n", "LA-1"), exitUsage)
}

func TestWorkflowBulkApply(t *testing.T) {
	s := lcTWorkflowSite(t)
	lcTCode(t, cmdTRun(t, s, "workflow", "bulk-apply", "-d", "Leave Application", "--names", "LA-1", "--action", "Approve"), exitUsage, "pass --yes")
	r := cmdTRunStdin(t, s, `["LA-1","LA-2","LA-3"]`, "workflow", "bulk-apply", "-d", "Leave Application", "--file", "-", "--action", "Approve", "--yes", "--json")
	lcTCode(t, r, exitPartial)
	got := cmdTJSON(t, r).(map[string]interface{})
	if fmt.Sprint(got["applied"]) != "2" || fmt.Sprint(got["failed"]) != "1" {
		t.Errorf("report = %v", got)
	}
	lcTCode(t, cmdTRun(t, s, "workflow", "bulk-apply", "-d", "Leave Application", "--action", "Approve", "--yes"), exitUsage, "provide --names or --file")
}

func TestWorkflowPending(t *testing.T) {
	s := frappetest.New(t)
	s.Add("Workflow Action",
		map[string]interface{}{"name": "WA-1", "status": "Open", "reference_doctype": "Leave Application", "reference_name": "LA-1", "workflow_state": "Open"},
		map[string]interface{}{"name": "WA-2", "status": "Completed", "reference_doctype": "Leave Application", "reference_name": "LA-2", "workflow_state": "Open"},
		map[string]interface{}{"name": "WA-3", "status": "Open", "reference_doctype": "Expense Claim", "reference_name": "EC-1", "workflow_state": "Draft"},
	)
	r := cmdTOK(t, cmdTRun(t, s, "workflow", "pending", "--jq", "[.[].name]", "--output", "json"))
	if strings.Join(strings.Fields(r.Stdout), "") != `["WA-1","WA-3"]` {
		t.Errorf("pending = %s", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "workflow", "pending", "-d", "Expense Claim", "--all", "--output", "csv"))
	if !strings.Contains(r.Stdout, "WA-3,Expense Claim,EC-1") || strings.Contains(r.Stdout, "WA-1") {
		t.Errorf("pending csv = %s", r.Stdout)
	}
}

func TestMCPLifecycleTools(t *testing.T) {
	s, site := newMCPFake(t, false)
	items := []interface{}{map[string]interface{}{"name": "row1", "item_code": "A"}}
	site.Add("Sales Order",
		map[string]interface{}{"name": "SO-1"},
		map[string]interface{}{"name": "SO-2", "docstatus": json.Number("1")},
		map[string]interface{}{"name": "SO-3", "docstatus": json.Number("2"), "items": items},
	)
	doc := func(name string) map[string]interface{} {
		return map[string]interface{}{"doctype": "Sales Order", "name": name}
	}

	if got := mcpTObj(t, mcpTOK(t, s, "submit_doc", doc("SO-1"))); fmt.Sprint(got["docstatus"]) != "1" {
		t.Errorf("submit_doc = %v", got)
	}
	if got := mcpTObj(t, mcpTOK(t, s, "cancel_doc", doc("SO-2"))); fmt.Sprint(got["docstatus"]) != "2" {
		t.Errorf("cancel_doc = %v", got)
	}
	mcpTErr(t, s, "cancel_doc", doc("SO-3"), "Cannot change docstatus from 2 to 2")

	args := doc("SO-3")
	args["data"] = `{"customer":"C9"}` // a JSON-encoded string works too
	if got := mcpTObj(t, mcpTOK(t, s, "amend_doc", args)); got["name"] != "SO-3-1" || got["customer"] != "C9" {
		t.Errorf("amend_doc = %v", got)
	}
	mcpTErr(t, s, "amend_doc", doc("SO-1"), "not cancelled")

	args = doc("SO-3")
	args["data"] = map[string]interface{}{"customer": "C7"}
	if got := mcpTObj(t, mcpTOK(t, s, "copy_doc", args)); got["customer"] != "C7" || got["amended_from"] != nil {
		t.Errorf("copy_doc = %v", got)
	}

	args = doc("SO-1")
	args["new_name"] = "SO-9"
	if got := mcpTObj(t, mcpTOK(t, s, "rename_doc", args)); got["name"] != "SO-9" || got["merged"] != false {
		t.Errorf("rename_doc = %v", got)
	}
	mcpTErr(t, s, "rename_doc", doc("SO-9"), "new_name")

	site.Add("Workflow", map[string]interface{}{"name": "SO WF", "document_type": "Sales Order", "is_active": json.Number("1")})
	mcpTErr(t, s, "submit_doc", doc("SO-3-1"), "SO WF")

	site.HandleMethod("frappe.model.workflow.get_transitions", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, nil
	})
	if got := mcpTOK(t, s, "get_transitions", doc("SO-3-1")); got != "[]" {
		t.Errorf("get_transitions = %s", got)
	}
	site.HandleMethod("frappe.model.workflow.apply_workflow", func(_ *http.Request, a map[string]interface{}) (interface{}, error) {
		d := wfTDoc(a)
		d["workflow_state"] = a["action"]
		return d, nil
	})
	args = doc("SO-3-1")
	args["action"] = "Approve"
	if got := mcpTObj(t, mcpTOK(t, s, "apply_workflow", args)); got["workflow_state"] != "Approve" {
		t.Errorf("apply_workflow = %v", got)
	}
}
