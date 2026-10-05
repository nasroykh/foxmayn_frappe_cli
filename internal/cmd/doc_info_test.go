package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const (
	docInfoMethod  = "/api/method/frappe.desk.form.load.get_docinfo"
	getdocMethodT  = "/api/method/frappe.desk.form.load.getdoc"
	timelineMethod = "/api/method/frappe.desk.form.activity.get_activity_timeline"
	openCountPath  = "/api/method/frappe.desk.notifications.get_open_count"
)

// docInfoTSite has SINV-0001 with two versions (jane changed grand_total,
// bob a row and remarks), comments, a file, an assignment, a share, a tag,
// an email, a workflow log entry and two Payment Entries that link to it.
func docInfoTSite(t *testing.T, s *frappetest.Site) {
	t.Helper()
	s.Add("Sales Invoice", map[string]interface{}{"name": "SINV-0001", "grand_total": json.Number("300.0")})
	ref := func(extra map[string]interface{}) map[string]interface{} {
		extra["reference_doctype"], extra["reference_name"] = "Sales Invoice", "SINV-0001"
		return extra
	}
	s.Add("Version",
		map[string]interface{}{"name": "v1", "ref_doctype": "Sales Invoice", "docname": "SINV-0001", "owner": "jane@example.com",
			"data": `{"changed":[["grand_total","200.00","300.00"],["status","Draft","Unpaid"]],"added":[],"removed":[],"row_changed":[]}`},
		map[string]interface{}{"name": "v2", "ref_doctype": "Sales Invoice", "docname": "SINV-0001", "owner": "bob@example.com",
			"data": `{"changed":[["remarks",null,"` + strings.Repeat("x", 300) + `"]],"added":[["items",{}],["items",{}]],"removed":[],` +
				`"row_changed":[["items",1,"row2",[["qty",2,3]]]],"impersonated_by":"admin@example.com"}`},
	)
	s.Add("Comment",
		ref(map[string]interface{}{"name": "c1", "comment_type": "Comment", "owner": "jane@example.com", "content": "<p>Check the <b>total</b> &amp; tax\x1b\x07</p>"}),
		ref(map[string]interface{}{"name": "c2", "comment_type": "Workflow", "owner": "bob@example.com", "content": "Approved"}),
		ref(map[string]interface{}{"name": "c3", "comment_type": "Assigned", "owner": "bob@example.com", "content": "assigned jane"}),
		map[string]interface{}{"name": "c4", "comment_type": "Comment", "reference_doctype": "Sales Invoice", "reference_name": "SINV-0002", "content": "other doc"},
	)
	s.Add("File", map[string]interface{}{"name": "f1", "attached_to_doctype": "Sales Invoice", "attached_to_name": "SINV-0001",
		"file_name": "scan.pdf", "file_url": "/private/files/scan.pdf", "is_private": json.Number("1"), "file_size": json.Number("1024")})
	s.Add("ToDo",
		map[string]interface{}{"name": "td1", "reference_type": "Sales Invoice", "reference_name": "SINV-0001", "allocated_to": "jane@example.com", "status": "Open", "description": "Review"},
		map[string]interface{}{"name": "td2", "reference_type": "Sales Invoice", "reference_name": "SINV-0001", "allocated_to": "bob@example.com", "status": "Closed"},
	)
	s.Add("DocShare", map[string]interface{}{"name": "sh1", "share_doctype": "Sales Invoice", "share_name": "SINV-0001", "user": "bob@example.com",
		"read": json.Number("1"), "write": json.Number("0"), "share": json.Number("0"), "submit": json.Number("0"), "everyone": json.Number("0")})
	s.Add("Tag Link", map[string]interface{}{"name": "tl1", "document_type": "Sales Invoice", "document_name": "SINV-0001", "tag": "urgent"})
	s.Add("Communication", ref(map[string]interface{}{"name": "m1", "communication_type": "Communication", "communication_medium": "Email",
		"subject": "Your invoice", "sender": "jane@example.com", "communication_date": "2026-01-02 10:00:00"}))
	s.Add("Payment Entry",
		map[string]interface{}{"name": "PE-1", "reference_name": "SINV-0001"},
		map[string]interface{}{"name": "PE-2", "reference_name": "SINV-0001"},
		map[string]interface{}{"name": "PE-3", "reference_name": "SINV-0009"},
	)
	s.Add("Delivery Note", map[string]interface{}{"name": "DN-1", "against_sales_invoice": "SINV-0001"})
	s.Add("Journal Entry", map[string]interface{}{"name": "JV-1", "reference_name": "SINV-0002"})
	s.Dashboard("Sales Invoice", map[string]string{"Payment Entry": "reference_name", "Delivery Note": "against_sales_invoice", "Journal Entry": "reference_name"})
}

func docInfoTDecode(t *testing.T, out string) docContext {
	t.Helper()
	var d docContext
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("not a document context (%v): %.300s", err, out)
	}
	return d
}

// changeBy finds who changed field, newest version first.
func changeBy(d docContext, field string) (string, ctxChange, bool) {
	for _, v := range d.Versions {
		for _, c := range v.Changed {
			if c.Field == field {
				return v.By, c, true
			}
		}
	}
	return "", ctxChange{}, false
}

func TestCmdDocInfoJSON(t *testing.T) {
	s := frappetest.New(t)
	docInfoTSite(t, s)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "doc-info", "-d", "Sales Invoice", "-n", "SINV-0001", "--links"))
	d := docInfoTDecode(t, r.Stdout)

	// The done-when question: who changed grand_total, and what links here.
	by, ch, ok := changeBy(d, "grand_total")
	if !ok || by != "jane@example.com" || ch.From != "200.00" || ch.To != "300.00" {
		t.Errorf("grand_total change = %v %+v %v", by, ch, ok)
	}
	var links []string
	for _, l := range d.Links {
		links = append(links, fmt.Sprintf("%s=%d", l.Doctype, l.Count))
	}
	cmdTEq(t, links, "Delivery Note=1", "Payment Entry=2") // Journal Entry has none: left out

	if len(d.Versions) != 2 || d.Versions[0].Name != "v2" {
		t.Fatalf("versions not newest first: %+v", d.Versions)
	}
	v2 := d.Versions[0]
	if by, ch, _ := changeBy(docContext{Versions: []ctxVersion{v2}}, "items[2].qty"); by != "bob@example.com" || fmt.Sprint(ch.From)+fmt.Sprint(ch.To) != "23" {
		t.Errorf("row change = %+v", v2.Changed)
	}
	if v2.RowsAdded["items"] != 2 || v2.ImpersonatedBy != "admin@example.com" {
		t.Errorf("v2 = %+v", v2)
	}
	if _, ch, _ := changeBy(d, "remarks"); ch.From != nil || len([]rune(fmt.Sprint(ch.To))) != ctxValueMax {
		t.Errorf("remarks change not clipped to %d: %+v", ctxValueMax, ch)
	}
	if len(d.Comments) != 1 || d.Comments[0].Text != "Check the total & tax" || d.Comments[0].By != "jane@example.com" {
		t.Errorf("comments = %+v", d.Comments)
	}
	if len(d.WorkflowLog) != 1 || d.WorkflowLog[0].Text != "Approved" {
		t.Errorf("workflow log = %+v", d.WorkflowLog)
	}
	if len(d.Attachments) != 1 || d.Attachments[0].FileName != "scan.pdf" || !d.Attachments[0].Private {
		t.Errorf("attachments = %+v", d.Attachments)
	}
	if len(d.Assignments) != 1 || d.Assignments[0].User != "jane@example.com" || d.Assignments[0].Status != "Open" {
		t.Errorf("assignments = %+v (closed ones are left out)", d.Assignments)
	}
	if len(d.Shares) != 1 || d.Shares[0].User != "bob@example.com" || !d.Shares[0].Read || d.Shares[0].Write {
		t.Errorf("shares = %+v", d.Shares)
	}
	cmdTEq(t, d.Tags, "urgent")
	if len(d.Communications) != 1 || d.Communications[0].Subject != "Your invoice" {
		t.Errorf("communications = %+v", d.Communications)
	}
	if !strings.Contains(strings.Join(d.Permissions, ","), "write") {
		t.Errorf("permissions = %v", d.Permissions)
	}
	if d.HiddenByPolicy != nil || d.Timeline != nil || d.Onload != nil {
		t.Errorf("CLI output has MCP or unrequested parts: %s", r.Stdout)
	}
	// One request per part, both reads.
	if n, m := len(s.RequestsTo("GET", docInfoMethod)), len(s.RequestsTo("GET", openCountPath)); n != 1 || m != 1 {
		t.Errorf("requests: docinfo %d, open_count %d", n, m)
	}

	// Without --links, no count request and no links key.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "doc-info", "-d", "Sales Invoice", "-n", "SINV-0001"))
	if strings.Contains(r.Stdout, `"links"`) || len(s.RequestsTo("GET", openCountPath)) != 1 {
		t.Errorf("links without --links: %s", r.Stdout)
	}
	// Linked DocTypes with no document still give an empty list, not null.
	s.Dashboard("Sales Invoice", map[string]string{"Journal Entry": "reference_name"})
	r = cmdTOK(t, cmdTRun(t, s, "--json", "doc-info", "-d", "Sales Invoice", "-n", "SINV-0001", "--links"))
	if !strings.Contains(r.Stdout, `"links": []`) {
		t.Errorf("empty links: %s", r.Stdout)
	}
}

func TestCmdDocInfoTable(t *testing.T) {
	s := frappetest.New(t)
	docInfoTSite(t, s)
	r := cmdTOK(t, cmdTRun(t, s, "doc-info", "-d", "Sales Invoice", "-n", "SINV-0001", "--links"))
	cmdTHas(t, r.Stdout, "Sales Invoice SINV-0001", "Versions", "jane@example.com", "grand_total: 200.00 → 300.00",
		"items[2].qty: 2 → 3", "items: 2 row(s) added", "impersonated by admin@example.com",
		"Comments", "Check the total & tax", "scan.pdf (private)", "jane@example.com  Open  Review",
		"bob@example.com: read", "urgent", "Approved", "Payment Entry: 2", "Delivery Note: 1", "Your invoice")
	if strings.ContainsAny(r.Stdout, "\x1b\x07") {
		t.Error("escape sequence from a comment reached the terminal")
	}
}

func TestCmdDocInfoErrors(t *testing.T) {
	s := frappetest.New(t)
	docInfoTSite(t, s)
	r := cmdTRun(t, s, "doc-info", "-d", "Sales Invoice", "-n", "NOPE")
	cmdTFail(t, r, `Sales Invoice "NOPE" not found`)
	if r.Code != 4 {
		t.Errorf("missing document exit = %d, want 4", r.Code)
	}
	if r = cmdTRun(t, s, "doc-info", "-d", "No Such DT", "-n", "x"); r.Code != 4 {
		t.Errorf("missing DocType exit = %d, want 4 (%v)", r.Code, r.Err)
	}
	if r = cmdTRun(t, s, "doc-info", "-d", "Sales Invoice", "-n", "NOPE", "--onload"); r.Code != 4 {
		t.Errorf("getdoc of a missing document exit = %d, want 4 (%v)", r.Code, r.Err)
	}
	if r = cmdTRun(t, s, "doc-info", "-n", "x"); r.Code != 2 {
		t.Errorf("no --doctype exit = %d, want 2", r.Code)
	}
}

func TestCmdDocInfoTimeline(t *testing.T) {
	s := frappetest.New(t)
	docInfoTSite(t, s)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "doc-info", "-d", "Sales Invoice", "-n", "SINV-0001", "--timeline"))
	d := docInfoTDecode(t, r.Stdout)
	var lines []string
	for _, a := range d.Timeline {
		lines = append(lines, a.Type+"|"+a.Field+"|"+a.Text)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"log||Administrator created this document", "version|grand_total|changed grand_total: 200.00 → 300.00",
		"comment||Check the total & tax", "log||bob@example.com Approved"} {
		if !strings.Contains(got, want) {
			t.Errorf("timeline lacks %q:\n%s", want, got)
		}
	}
	if d.Notes != nil {
		t.Errorf("notes = %v", d.Notes)
	}

	// A Frappe without the timeline: a note, the rest still printed, exit 0.
	s.HandleMethod("frappe.desk.form.activity.get_activity_timeline", nil)
	r = cmdTOK(t, cmdTRun(t, s, "--json", "doc-info", "-d", "Sales Invoice", "-n", "SINV-0001", "--timeline"))
	d = docInfoTDecode(t, r.Stdout)
	if len(d.Notes) != 1 || !strings.Contains(d.Notes[0], "no activity timeline") || d.Timeline != nil || len(d.Versions) != 2 {
		t.Errorf("without the method: notes %v, timeline %v, versions %d", d.Notes, d.Timeline, len(d.Versions))
	}
	r = cmdTOK(t, cmdTRun(t, s, "doc-info", "-d", "Sales Invoice", "-n", "SINV-0001", "--timeline"))
	cmdTHas(t, r.Stderr, "note: this Frappe release has no activity timeline")
}

func TestCmdDocInfoOnloadAndFull(t *testing.T) {
	s := frappetest.New(t)
	s.Add("Customer", map[string]interface{}{"name": "Acme"})
	s.Onload("Customer", "Acme", map[string]interface{}{
		"dashboard_info": []interface{}{map[string]interface{}{"company": "Acme Ltd", "currency": "EUR",
			"billing_this_year": json.Number("1500.0"), "total_unpaid": json.Number("250.0")}},
	})
	r := cmdTOK(t, cmdTRun(t, s, "--json", "doc-info", "-d", "Customer", "-n", "Acme", "--onload"))
	d := docInfoTDecode(t, r.Stdout)
	if info, _ := d.Onload["dashboard_info"].([]interface{}); len(info) != 1 {
		t.Fatalf("onload = %v", d.Onload)
	}
	// getdoc returns the docinfo too: one request, no get_docinfo.
	if n, m := len(s.RequestsTo("GET", getdocMethodT)), len(s.RequestsTo("GET", docInfoMethod)); n != 1 || m != 0 {
		t.Errorf("requests: getdoc %d, get_docinfo %d", n, m)
	}
	r = cmdTOK(t, cmdTRun(t, s, "doc-info", "-d", "Customer", "-n", "Acme", "--onload"))
	cmdTHas(t, r.Stdout, "Onload", "Acme Ltd: billing this year 1500.0, total unpaid 250.0 EUR")

	// --full: the raw answers by part.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "doc-info", "-d", "Customer", "-n", "Acme", "--full", "--links"))
	var full map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &full); err != nil {
		t.Fatal(err)
	}
	info, _ := full["docinfo"].(map[string]interface{})
	links, _ := full["links"].(map[string]interface{})
	if info["doctype"] != "Customer" || info["user_info"] == nil || links["count"] == nil || full["onload"] != nil {
		t.Errorf("--full = %v", full)
	}
}

func TestCompactLinksInternalAndTimeouts(t *testing.T) {
	raw := map[string]interface{}{"count": map[string]interface{}{
		"internal_links_found": []interface{}{
			map[string]interface{}{"doctype": "Sales Order", "count": json.Number("1"), "open_count": json.Number("0"), "names": []interface{}{"SO-1"}},
			map[string]interface{}{"doctype": "Quotation", "count": json.Number("0"), "names": []interface{}{}},
		},
		"external_links_found": []interface{}{
			map[string]interface{}{"doctype": "GL Entry", "count": "?", "open_count": json.Number("0")},
			map[string]interface{}{"doctype": "Payment Entry", "count": json.Number("100"), "open_count": json.Number("3")},
			map[string]interface{}{"doctype": "Dunning", "count": json.Number("0"), "open_count": json.Number("0")},
		},
	}}
	got := compactLinks(raw)
	want := []ctxLink{
		{Doctype: "Sales Order", Count: 1, Internal: true, Names: []string{"SO-1"}},
		{Doctype: "GL Entry", TimedOut: true},
		{Doctype: "Payment Entry", Count: 100, OpenCount: 3, Capped: true},
	}
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("compactLinks = %+v\nwant %+v", got, want)
	}
}

func TestMCPGetDocContext(t *testing.T) {
	s, site := newMCPFake(t, true) // a read tool: present in read-only mode
	docInfoTSite(t, site)
	d := docInfoTDecode(t, mcpTOK(t, s, "get_doc_context", map[string]interface{}{"doctype": "Sales Invoice", "name": "SINV-0001"}))
	// One call answers who changed grand_total and what links here.
	if by, _, ok := changeBy(d, "grand_total"); !ok || by != "jane@example.com" {
		t.Errorf("grand_total changed by %q", by)
	}
	if len(d.Links) != 2 || d.Links[0].Doctype != "Delivery Note" || d.Links[1].Count != 2 {
		t.Errorf("links = %+v", d.Links)
	}
	if d.HiddenByPolicy == nil || len(d.HiddenByPolicy.Sections) != 0 || d.HiddenByPolicy.LinkedDoctypes != 0 {
		t.Errorf("hidden_by_policy = %+v", d.HiddenByPolicy)
	}
	if d.Timeline != nil || d.Onload != nil {
		t.Errorf("unrequested parts: timeline %v onload %v", d.Timeline, d.Onload)
	}
	if len(site.RequestsTo("GET", getdocMethodT)) != 0 {
		t.Error("get_doc_context called getdoc, which writes a view log")
	}

	// links: false skips the count; timeline: true adds it.
	d = docInfoTDecode(t, mcpTOK(t, s, "get_doc_context", map[string]interface{}{"doctype": "Sales Invoice", "name": "SINV-0001", "links": false, "timeline": true}))
	if d.Links != nil || len(d.Timeline) == 0 || len(site.RequestsTo("GET", openCountPath)) != 1 {
		t.Errorf("links %v, timeline %d entries", d.Links, len(d.Timeline))
	}
	mcpTErr(t, s, "get_doc_context", map[string]interface{}{"doctype": "Sales Invoice", "name": "NOPE"}, "not found")
}

// TestMCPGetDocContextPolicy: the document's DocType is checked before the
// call; the parts read from other DocTypes are filtered and reported.
func TestMCPGetDocContextPolicy(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"Payment Entry", "Comment"}}, config.MCPPolicy{})
	docInfoTSite(t, site)
	d := docInfoTDecode(t, mcpTOK(t, s, "get_doc_context", map[string]interface{}{"doctype": "Sales Invoice", "name": "SINV-0001", "timeline": true}))
	cmdTEq(t, d.HiddenByPolicy.Sections, "comments", "workflow_log")
	if len(d.Comments) != 0 || len(d.WorkflowLog) != 0 || len(d.Versions) != 2 {
		t.Errorf("comments %v, workflow %v, versions %d", d.Comments, d.WorkflowLog, len(d.Versions))
	}
	if len(d.Links) != 1 || d.Links[0].Doctype != "Delivery Note" || d.HiddenByPolicy.LinkedDoctypes != 1 {
		t.Errorf("links %+v, hidden %+v", d.Links, d.HiddenByPolicy)
	}
	for _, a := range d.Timeline {
		if a.Type == "comment" || strings.Contains(a.Text, "Approved") {
			t.Errorf("timeline kept a comment: %+v", a)
		}
	}
	if d.HiddenByPolicy.TimelineEntries != 3 {
		t.Errorf("hidden timeline entries = %d, want 3", d.HiddenByPolicy.TimelineEntries)
	}

	// An allow list: only the document's DocType and Version; the creation
	// entry is the document's own and stays.
	s, site, _, _ = mcpTPolicy(t, &config.MCPPolicy{AllowDoctypes: []string{"Sales Invoice", "Version"}}, config.MCPPolicy{})
	docInfoTSite(t, site)
	d = docInfoTDecode(t, mcpTOK(t, s, "get_doc_context", map[string]interface{}{"doctype": "Sales Invoice", "name": "SINV-0001", "timeline": true}))
	cmdTEq(t, d.HiddenByPolicy.Sections, "comments", "workflow_log", "communications", "attachments", "assignments", "shares", "tags")
	if len(d.Links) != 0 || d.HiddenByPolicy.LinkedDoctypes != 2 || len(d.Versions) != 2 {
		t.Errorf("links %+v, versions %d", d.Links, len(d.Versions))
	}
	for _, a := range d.Timeline {
		if a.Type != "version" && !strings.Contains(a.Text, "created this document") {
			t.Errorf("timeline kept %+v", a)
		}
	}
	// The document's own DocType is refused before anything is sent.
	before := len(site.Requests())
	mcpTErr(t, s, "get_doc_context", map[string]interface{}{"doctype": "Customer", "name": "x"}, "policy:")
	if len(site.Requests()) != before {
		t.Error("a refused call reached the site")
	}
}

// TestMCPGetDocContextFailsClosed: without a policy in the context the call
// sends nothing.
func TestMCPGetDocContextFailsClosed(t *testing.T) {
	site := frappetest.New(t)
	docInfoTSite(t, site)
	c, err := client.New(context.Background(), &config.SiteConfig{URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	var req mcp.CallToolRequest
	req.Params.Name = "get_doc_context"
	req.Params.Arguments = map[string]interface{}{"doctype": "Sales Invoice", "name": "SINV-0001"}
	call, err := parseDocContext(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(context.Background(), c); err == nil || !strings.Contains(err.Error(), "policy: no policy") {
		t.Fatalf("err = %v", err)
	}
	if n := len(site.Requests()); n != 0 {
		t.Errorf("%d requests sent without a policy", n)
	}
}

func TestTrimDocContext(t *testing.T) {
	d := &docContext{}
	for i := 0; i < mcpContextComments+5; i++ {
		d.Comments = append(d.Comments, ctxComment{Name: fmt.Sprint(i)})
	}
	for i := 0; i < mcpContextTimeline+7; i++ {
		d.Timeline = append(d.Timeline, ctxActivity{Text: fmt.Sprint(i)})
	}
	trimDocContext(d)
	if len(d.Comments) != mcpContextComments || d.Comments[0].Name != "0" || d.Omitted["comments"] != 5 {
		t.Errorf("comments: %d kept, first %s, omitted %v", len(d.Comments), d.Comments[0].Name, d.Omitted)
	}
	if len(d.Timeline) != mcpContextTimeline || d.Timeline[0].Text != "7" || d.Omitted["timeline"] != 7 {
		t.Errorf("timeline: %d kept, first %s, omitted %v", len(d.Timeline), d.Timeline[0].Text, d.Omitted)
	}
}
