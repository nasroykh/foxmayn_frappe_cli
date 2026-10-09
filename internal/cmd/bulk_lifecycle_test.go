package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// blcTSite is lcTSite plus two more drafts: SO-1, SO-4 and SO-5 are drafts,
// SO-2 is submitted, SO-3 is cancelled.
func blcTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := lcTSite(t)
	s.Add("Sales Order",
		map[string]interface{}{"name": "SO-4", "customer": "C4"},
		map[string]interface{}{"name": "SO-5", "customer": "C5"},
	)
	return s
}

type blcReport struct {
	Submitted int `json:"submitted"`
	Cancelled int `json:"cancelled"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	Results   []struct {
		Index  int    `json:"index"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"results"`
}

func blcTReport(t *testing.T, r cliResult) blcReport {
	t.Helper()
	var rep blcReport
	if err := json.Unmarshal([]byte(r.Stdout), &rep); err != nil {
		t.Fatalf("not a bulk report: %v\n%s", err, r.Stdout)
	}
	return rep
}

// blcTWrites counts the requests that could change a document.
func blcTWrites(s *frappetest.Site) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !strings.HasSuffix(r.Path, "/logout") && !strings.HasSuffix(r.Path, "/login") {
			n++
		}
	}
	return n
}

func TestBulkSubmit(t *testing.T) {
	s := blcTSite(t)
	r := cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--names", "SO-1,SO-2,SO-3,SO-4,missing", "--yes", "--json")
	if r.Code != exitPartial {
		t.Fatalf("exit %d, want %d: %s", r.Code, exitPartial, r.Stderr)
	}
	rep := blcTReport(t, r)
	if rep.Submitted != 2 || rep.Failed != 3 || rep.Skipped != 0 || len(rep.Results) != 5 {
		t.Fatalf("report = %+v", rep)
	}
	want := []struct{ name, status, err string }{
		{"SO-1", "submitted", ""},
		{"SO-2", "error", "SO-2 is already submitted"},
		{"SO-3", "error", "SO-3 is cancelled and cannot be submitted again"},
		{"SO-4", "submitted", ""},
		{"missing", "error", "not found"},
	}
	for i, w := range want {
		got := rep.Results[i]
		if got.Index != i+1 || got.Name != w.name || got.Status != w.status || !strings.Contains(got.Error, w.err) {
			t.Errorf("result %d = %+v, want %+v", i+1, got, w)
		}
	}
	for name, ds := range map[string]string{"SO-1": "1", "SO-2": "1", "SO-3": "2", "SO-4": "1", "SO-5": "0"} {
		if got := lcTDocstatus(t, s, "Sales Order", name); got != ds {
			t.Errorf("%s docstatus = %s, want %s", name, got, ds)
		}
	}
	// A document is submitted as read, like submit-doc does.
	for _, req := range s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit") {
		if !strings.Contains(req.Body, `"modified"`) {
			t.Errorf("submit sent without modified: %s", req.Body)
		}
	}
}

func TestBulkSubmitAllDone(t *testing.T) {
	s := blcTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--names", "SO-1,SO-4", "--yes"))
	cmdTHas(t, r.Stdout, "SO-1", "submitted")
	cmdTHas(t, r.Stderr+r.Stdout, "All 2 Sales Order documents submitted")
	if ds := lcTDocstatus(t, s, "Sales Order", "SO-4"); ds != "1" {
		t.Errorf("SO-4 docstatus = %s", ds)
	}
}

func TestBulkCancel(t *testing.T) {
	s := blcTSite(t)
	cmdTOK(t, cmdTRun(t, s, "submit-doc", "-d", "Sales Order", "-n", "SO-4"))
	r := cmdTRun(t, s, "bulk-cancel", "-d", "Sales Order", "--names", "SO-1,SO-2,SO-3,SO-4", "--yes", "--json")
	if r.Code != exitPartial {
		t.Fatalf("exit %d, want %d: %s", r.Code, exitPartial, r.Stderr)
	}
	rep := blcTReport(t, r)
	if rep.Cancelled != 2 || rep.Failed != 2 || len(rep.Results) != 4 {
		t.Fatalf("report = %+v", rep)
	}
	want := []struct{ name, status, err string }{
		{"SO-1", "error", "SO-1 is a draft, not submitted"},
		{"SO-2", "cancelled", ""},
		{"SO-3", "error", "SO-3 is already cancelled"},
		{"SO-4", "cancelled", ""},
	}
	for i, w := range want {
		got := rep.Results[i]
		if got.Name != w.name || got.Status != w.status || !strings.Contains(got.Error, w.err) {
			t.Errorf("result %d = %+v, want %+v", i+1, got, w)
		}
	}
	for name, ds := range map[string]string{"SO-1": "0", "SO-2": "2", "SO-3": "2", "SO-4": "2"} {
		if got := lcTDocstatus(t, s, "Sales Order", name); got != ds {
			t.Errorf("%s docstatus = %s, want %s", name, got, ds)
		}
	}
}

func TestBulkLifecycleFile(t *testing.T) {
	s := blcTSite(t)
	cmdTOK(t, cmdTRunStdin(t, s, `["SO-1","SO-4"]`, "bulk-submit", "-d", "Sales Order", "--file", "-", "--yes"))
	cmdTOK(t, cmdTRunStdin(t, s, `["SO-4"]`, "bulk-cancel", "-d", "Sales Order", "--file", "-", "--yes"))
	if a, b := lcTDocstatus(t, s, "Sales Order", "SO-1"), lcTDocstatus(t, s, "Sales Order", "SO-4"); a != "1" || b != "2" {
		t.Errorf("docstatus SO-1 = %s, SO-4 = %s", a, b)
	}
}

// An active Workflow is refused once, before any document is read or written.
func TestBulkLifecycleRefusesWorkflow(t *testing.T) {
	for _, cmd := range []string{"bulk-submit", "bulk-cancel"} {
		t.Run(cmd, func(t *testing.T) {
			s := blcTSite(t)
			s.Add("Workflow", map[string]interface{}{"name": "SO Approval", "document_type": "Sales Order", "is_active": json.Number("1")})
			for _, extra := range [][]string{{"--names", "SO-1,SO-4", "--yes"}, {"--filters", `{"customer":"C1"}`, "--yes"}, {"--names", "SO-1", "--dry-run"}} {
				r := cmdTRun(t, s, append([]string{cmd, "-d", "Sales Order"}, extra...)...)
				lcTCode(t, r, exitValidation, `workflow "SO Approval"`, "ffc workflow bulk-apply")
				if strings.Contains(r.Err.Error(), "-n ") {
					t.Errorf("the hint names a single document: %v", r.Err)
				}
			}
			if n := blcTWrites(s); n != 0 {
				t.Errorf("%d write requests sent", n)
			}
			if n := len(s.RequestsTo(http.MethodGet, "/api/resource/Sales Order/SO-1")); n != 0 {
				t.Errorf("%d documents read before the workflow check", n)
			}
		})
	}
}

// A user who may not read Workflows: the server's own checks decide.
func TestBulkSubmitWorkflowUnreadable(t *testing.T) {
	s := blcTSite(t)
	s.Handle("GET /api/resource/Workflow", frappetest.ErrorHandler(frappetest.Permission("No permission for Workflow")))
	cmdTOK(t, cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--names", "SO-1", "--yes"))
	if ds := lcTDocstatus(t, s, "Sales Order", "SO-1"); ds != "1" {
		t.Errorf("SO-1 docstatus = %s", ds)
	}
}

func TestBulkLifecycleDryRun(t *testing.T) {
	s := blcTSite(t)
	reqs := dryTPlan(t, cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--names", "SO-1,SO-4", "--dry-run", "--json"))
	if len(reqs) != 2 || reqs[0]["method"] != "POST" || !strings.Contains(reqs[0]["url"].(string), "frappe.client.submit") {
		t.Errorf("submit plan = %v", reqs)
	}
	reqs = dryTPlan(t, cmdTRun(t, s, "bulk-cancel", "-d", "Sales Order", "--names", "SO-2", "--dry-run", "--json"))
	if len(reqs) != 1 || !strings.Contains(reqs[0]["url"].(string), "frappe.client.cancel") {
		t.Errorf("cancel plan = %v", reqs)
	}
	if n := blcTWrites(s); n != 0 || lcTDocstatus(t, s, "Sales Order", "SO-1") != "0" || lcTDocstatus(t, s, "Sales Order", "SO-2") != "1" {
		t.Errorf("the dry run wrote: %d write requests", n)
	}
	// A document that is not in the right state or does not exist fails the plan.
	lcTCode(t, cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--names", "SO-1,SO-2", "--dry-run"), exitValidation, "SO-2 is already submitted")
	lcTCode(t, cmdTRun(t, s, "bulk-cancel", "-d", "Sales Order", "--names", "nope", "--dry-run"), exitNotFound)
}

func TestBulkLifecycleNeedsConfirmation(t *testing.T) {
	for _, cmd := range []string{"bulk-submit", "bulk-cancel"} {
		t.Run(cmd, func(t *testing.T) {
			s := blcTSite(t)
			lcTCode(t, cmdTRun(t, s, cmd, "-d", "Sales Order", "--names", "SO-1,SO-2"), exitUsage, "pass --yes")
			lcTCode(t, cmdTRun(t, s, cmd, "-d", "Sales Order", "--filters", `{"customer":["in",["C1","C2"]]}`), exitUsage, "pass --yes")
			if n := blcTWrites(s); n != 0 {
				t.Errorf("%d write requests sent without confirmation", n)
			}
		})
	}
}

func TestBulkLifecycleFilters(t *testing.T) {
	filtersOf := func(s *frappetest.Site) string {
		for _, req := range s.RequestsTo(http.MethodGet, "/api/resource/Sales Order") {
			return req.Query.Get("filters")
		}
		return ""
	}
	t.Run("submit takes the drafts", func(t *testing.T) {
		s := blcTSite(t)
		r := cmdTOK(t, cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--filters", `[["customer","like","C%"]]`, "--yes", "--json"))
		rep := blcTReport(t, r)
		if rep.Submitted != 3 || rep.Failed != 0 || len(rep.Results) != 3 {
			t.Fatalf("report = %+v", rep)
		}
		for i, name := range []string{"SO-1", "SO-4", "SO-5"} {
			if rep.Results[i].Name != name {
				t.Errorf("result %d = %s, want %s", i+1, rep.Results[i].Name, name)
			}
		}
		if f := filtersOf(s); !strings.Contains(f, `"docstatus","=",0`) {
			t.Errorf("list filters = %s", f)
		}
		if ds := lcTDocstatus(t, s, "Sales Order", "SO-3"); ds != "2" {
			t.Errorf("SO-3 docstatus = %s", ds)
		}
	})
	t.Run("cancel takes the submitted ones", func(t *testing.T) {
		s := blcTSite(t)
		r := cmdTOK(t, cmdTRun(t, s, "bulk-cancel", "-d", "Sales Order", "--filters", `{"customer":["like","C%"]}`, "--yes", "--json"))
		rep := blcTReport(t, r)
		if rep.Cancelled != 1 || rep.Failed != 0 || len(rep.Results) != 1 || rep.Results[0].Name != "SO-2" {
			t.Fatalf("report = %+v", rep)
		}
		if f := filtersOf(s); !strings.Contains(f, `"docstatus":1`) || !strings.Contains(f, `"customer"`) {
			t.Errorf("list filters = %s", f)
		}
	})
	t.Run("no match", func(t *testing.T) {
		s := blcTSite(t)
		r := cmdTOK(t, cmdTRun(t, s, "bulk-cancel", "-d", "Sales Order", "--filters", `{"customer":"C1"}`, "--yes", "--json"))
		if !strings.Contains(r.Stderr, "No Sales Order documents match the filters; nothing to cancel") {
			t.Errorf("stderr = %s", r.Stderr)
		}
		if rep := blcTReport(t, r); len(rep.Results) != 0 || rep.Cancelled != 0 {
			t.Errorf("report = %+v", rep)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		s := blcTSite(t)
		reqs := dryTPlan(t, cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--filters", `{"customer":["in",["C1","C4","C2"]]}`, "--dry-run", "--json"))
		if len(reqs) != 2 {
			t.Errorf("%d planned submits, want 2: %v", len(reqs), reqs)
		}
	})
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"empty filters", []string{"--filters", `{}`}, "empty filters match every document"},
		{"docstatus in an object", []string{"--filters", `{"docstatus":1}`}, "selects docstatus 0 itself"},
		{"nothing selected", nil, "provide --names, --file or --filters"},
		{"names and filters", []string{"--names", "SO-1", "--filters", `{"customer":"C1"}`}, "none of the others can be"},
		{"bad concurrency", []string{"--names", "SO-1", "--concurrency", "11"}, "--concurrency must be between 1 and 10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := blcTSite(t)
			r := cmdTRun(t, s, append([]string{"bulk-submit", "-d", "Sales Order", "--yes"}, tc.args...)...)
			lcTCode(t, r, exitUsage, tc.want)
			if len(s.Requests()) != 0 {
				t.Errorf("%d requests sent for a usage error", len(s.Requests()))
			}
		})
	}
}

func TestBulkLifecycleFailFast(t *testing.T) {
	s := blcTSite(t)
	r := cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--names", "SO-1,SO-2,SO-4,SO-5", "--yes", "--fail-fast", "--json")
	if r.Code != exitPartial {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	rep := blcTReport(t, r)
	got := make([]string, len(rep.Results))
	for i, res := range rep.Results {
		got[i] = res.Status
	}
	if strings.Join(got, ",") != "submitted,error,skipped,skipped" || rep.Submitted != 1 || rep.Failed != 1 || rep.Skipped != 2 {
		t.Errorf("statuses = %v, report %+v", got, rep)
	}
	for name, ds := range map[string]string{"SO-1": "1", "SO-4": "0", "SO-5": "0"} {
		if cur := lcTDocstatus(t, s, "Sales Order", name); cur != ds {
			t.Errorf("%s docstatus = %s, want %s", name, cur, ds)
		}
	}
}

// One request at a time unless asked: the order of --names is the order of
// the submits.
func TestBulkSubmitOrder(t *testing.T) {
	s := blcTSite(t)
	cmdTOK(t, cmdTRun(t, s, "bulk-submit", "-d", "Sales Order", "--names", "SO-5,SO-4,SO-1", "--yes"))
	var order []string
	for _, req := range s.RequestsTo(http.MethodPost, "/api/method/frappe.client.submit") {
		var body struct {
			Doc struct {
				Name string `json:"name"`
			} `json:"doc"`
		}
		if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
			t.Fatal(err)
		}
		order = append(order, body.Doc.Name)
	}
	if strings.Join(order, ",") != "SO-5,SO-4,SO-1" {
		t.Errorf("submit order = %v", order)
	}
}
