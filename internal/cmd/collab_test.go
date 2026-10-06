package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// collabTSite is a fake site with a Note, two users and a submittable
// DocType.
func collabTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	s.Add("Note", map[string]interface{}{"name": "N1", "title": "Plan"})
	s.Add("User",
		map[string]interface{}{"name": "jane@example.com", "full_name": "Jane Doe"},
		map[string]interface{}{"name": "bob@example.com", "full_name": "Bob"})
	return s
}

// collabTOut runs a command and decodes its --json result.
func collabTOut(t *testing.T, s *frappetest.Site, args ...string) map[string]interface{} {
	t.Helper()
	r := cmdTOK(t, cmdTRun(t, s, append(args, "--json")...))
	m, ok := cmdTJSON(t, r).(map[string]interface{})
	if !ok {
		t.Fatalf("not an object: %s", r.Stdout)
	}
	return m
}

func collabTList(m map[string]interface{}, k string) []string {
	out := []string{}
	for _, v := range m[k].([]interface{}) {
		out = append(out, v.(string))
	}
	return out
}

func TestCommentCmd(t *testing.T) {
	s := collabTSite(t)
	out := collabTOut(t, s, "comment", "-d", "Note", "-n", "N1", "a < b", "&", "<b>c</b>")
	c, ok := s.Doc("Comment", out["comment"].(string))
	if !ok {
		t.Fatalf("no comment stored: %v", out)
	}
	if got := c["content"]; got != "a &lt; b &amp; &lt;b&gt;c&lt;/b&gt;" {
		t.Errorf("content = %q", got)
	}
	if c["comment_email"] != "Administrator" || c["comment_by"] != "Administrator" {
		t.Errorf("author = %v / %v", c["comment_email"], c["comment_by"])
	}

	// stdin, line breaks, and the full name of the signed-in user.
	s.SetUser("jane@example.com")
	r := cmdTOK(t, cmdTRunStdin(t, s, "line 1\nline 2\n", "comment", "-d", "Note", "-n", "N1", "-", "--json"))
	c, _ = s.Doc("Comment", cmdTJSON(t, r).(map[string]interface{})["comment"].(string))
	if c["content"] != "line 1<br>line 2" || c["comment_by"] != "Jane Doe" || c["comment_email"] != "jane@example.com" {
		t.Errorf("comment = %v", c)
	}

	// --html is sent as is.
	out = collabTOut(t, s, "comment", "-d", "Note", "-n", "N1", "--html", "<b>bold</b>")
	if out["content"] != "<b>bold</b>" {
		t.Errorf("html content = %v", out["content"])
	}

	lcTCode(t, cmdTRun(t, s, "comment", "-d", "Note", "-n", "N1", "  "), exitUsage, "empty")
	lcTCode(t, cmdTRun(t, s, "comment", "-d", "Note", "-n", "nope", "x"), exitNotFound, `Note "nope" not found`)
	s.Deny("Note", "read")
	lcTCode(t, cmdTRun(t, s, "comment", "-d", "Note", "-n", "N1", "x"), exitPermission)
}

func TestAssignCmds(t *testing.T) {
	s := collabTSite(t)
	out := collabTOut(t, s, "assign", "-d", "Note", "-n", "N1", "--to", "jane@example.com, bob@example.com",
		"--priority", "high", "--date", "2026-11-01", "--description", "Review")
	if got := collabTList(out, "assigned"); !reflect.DeepEqual(got, []string{"jane@example.com", "bob@example.com"}) {
		t.Errorf("assigned = %v", got)
	}
	if got := collabTList(out, "assignees"); len(got) != 2 {
		t.Errorf("assignees = %v", got)
	}
	reqs := s.RequestsTo("POST", "/api/method/frappe.desk.form.assign_to.add")
	if len(reqs) != 1 {
		t.Fatalf("%d add requests", len(reqs))
	}
	var body map[string]interface{}
	_ = json.Unmarshal([]byte(reqs[0].Body), &body)
	if body["priority"] != "High" || body["date"] != "2026-11-01" || body["description"] != "Review" || body["doctype"] != "Note" {
		t.Errorf("add body = %v", body)
	}

	// A duplicate is reported and not sent again.
	out = collabTOut(t, s, "assign", "-d", "Note", "-n", "N1", "--to", "jane@example.com")
	if got := collabTList(out, "already_assigned"); !reflect.DeepEqual(got, []string{"jane@example.com"}) || len(collabTList(out, "assigned")) != 0 {
		t.Errorf("duplicate: %v", out)
	}
	if n := len(s.RequestsTo("POST", "/api/method/frappe.desk.form.assign_to.add")); n != 1 {
		t.Errorf("duplicate sent: %d add requests", n)
	}

	out = collabTOut(t, s, "unassign", "-d", "Note", "-n", "N1", "--to", "jane@example.com,nobody@example.com")
	if !reflect.DeepEqual(collabTList(out, "unassigned"), []string{"jane@example.com"}) ||
		!reflect.DeepEqual(collabTList(out, "not_assigned"), []string{"nobody@example.com"}) ||
		!reflect.DeepEqual(collabTList(out, "assignees"), []string{"bob@example.com"}) {
		t.Errorf("unassign = %v", out)
	}

	lcTCode(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "N1", "--to", "ghost@example.com"), exitValidation, "ghost@example.com")
	lcTCode(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "nope", "--to", "bob@example.com"), exitNotFound, `Note "nope" not found`)
	lcTCode(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "N1", "--to", "bob@example.com", "--date", "01/11/2026"), exitUsage, "YYYY-MM-DD")
	lcTCode(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "N1", "--to", "bob@example.com", "--priority", "urgent"), exitUsage, "Low, Medium or High")
	lcTCode(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "N1", "--to", " , "), exitUsage, "no user")
	s.Deny("Note", "read")
	lcTCode(t, cmdTRun(t, s, "unassign", "-d", "Note", "-n", "N1", "--to", "bob@example.com"), exitPermission)
}

func TestTagCmds(t *testing.T) {
	s := collabTSite(t)
	out := collabTOut(t, s, "tag", "-d", "Note", "-n", "N1", "vip", "export", "vip")
	if !reflect.DeepEqual(collabTList(out, "added"), []string{"vip", "export"}) || !reflect.DeepEqual(collabTList(out, "tags"), []string{"vip", "export"}) {
		t.Errorf("tag = %v", out)
	}
	out = collabTOut(t, s, "tag", "-d", "Note", "-n", "N1", "vip")
	if !reflect.DeepEqual(collabTList(out, "already_tagged"), []string{"vip"}) {
		t.Errorf("tag again = %v", out)
	}
	if _, ok := s.Doc("Tag", "export"); !ok {
		t.Error("Tag export not created")
	}
	out = collabTOut(t, s, "untag", "-d", "Note", "-n", "N1", "VIP", "missing")
	if !reflect.DeepEqual(collabTList(out, "removed"), []string{"VIP"}) || !reflect.DeepEqual(collabTList(out, "not_tagged"), []string{"missing"}) ||
		!reflect.DeepEqual(collabTList(out, "tags"), []string{"export"}) {
		t.Errorf("untag = %v", out)
	}
	if n := len(s.RequestsTo("POST", "/api/method/frappe.desk.doctype.tag.tag.remove_tag")); n != 1 {
		t.Errorf("%d remove_tag requests, want 1", n)
	}
	lcTCode(t, cmdTRun(t, s, "tag", "-d", "Note", "-n", "N1", "a,b"), exitUsage, "comma")
	lcTCode(t, cmdTRun(t, s, "tag", "-d", "Note", "-n", "N1"), exitUsage)
	lcTCode(t, cmdTRun(t, s, "tag", "-d", "Note", "-n", "nope", "x"), exitNotFound)
	s.Deny("Note", "write")
	lcTCode(t, cmdTRun(t, s, "tag", "-d", "Note", "-n", "N1", "new"), exitPermission)
}

func TestShareCmds(t *testing.T) {
	s := collabTSite(t)
	lcTCode(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--user", "jane@example.com"), exitUsage, "pass --yes")
	if n := len(s.RequestsTo("POST", "/api/method/frappe.share.add")); n != 0 {
		t.Fatalf("share sent without confirmation")
	}
	out := collabTOut(t, s, "share", "-d", "Note", "-n", "N1", "--user", "jane@example.com", "--write", "--yes")
	if out["user"] != "jane@example.com" || out["read"] != true || out["write"] != true || out["share"] != false || out["everyone"] != false {
		t.Errorf("share = %v", out)
	}
	// Sharing again sets exactly the rights given.
	out = collabTOut(t, s, "share", "-d", "Note", "-n", "N1", "--user", "jane@example.com", "--yes")
	if out["write"] != false {
		t.Errorf("re-share = %v", out)
	}
	out = collabTOut(t, s, "share", "-d", "Note", "-n", "N1", "--everyone", "--yes")
	if out["everyone"] != true || out["user"] != nil {
		t.Errorf("everyone = %v", out)
	}
	if n := s.Count("DocShare"); n != 2 {
		t.Errorf("%d DocShares, want 2", n)
	}

	out = collabTOut(t, s, "unshare", "-d", "Note", "-n", "N1", "--user", "JANE@example.com")
	if out["removed"] != true {
		t.Errorf("unshare = %v", out)
	}
	out = collabTOut(t, s, "unshare", "-d", "Note", "-n", "N1", "--user", "bob@example.com")
	if out["removed"] != false {
		t.Errorf("unshare of no share = %v", out)
	}
	out = collabTOut(t, s, "unshare", "-d", "Note", "-n", "N1", "--everyone")
	if out["removed"] != true || s.Count("DocShare") != 0 {
		t.Errorf("unshare everyone = %v, %d left", out, s.Count("DocShare"))
	}

	lcTCode(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--user", "x@y", "--everyone", "--yes"), exitUsage, "not both")
	lcTCode(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--yes"), exitUsage)
	lcTCode(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--user", "x@y", "--read=false", "--yes"), exitUsage, "always grants read")
	lcTCode(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--user", "ghost@example.com", "--yes"), exitValidation, "ghost@example.com")
	lcTCode(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--user", "bob@example.com", "--submit", "--yes"), exitValidation, "not submittable")
	lcTCode(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "nope", "--user", "bob@example.com", "--yes"), exitNotFound, `Note "nope" not found`)
	lcTCode(t, cmdTRun(t, s, "unshare", "-d", "Note", "-n", "nope", "--user", "bob@example.com"), exitNotFound)
	s.Deny("Note", "share")
	lcTCode(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--user", "bob@example.com", "--yes"), exitPermission)
}

func TestCollabDryRun(t *testing.T) {
	s := collabTSite(t)
	for _, args := range [][]string{
		{"comment", "-d", "Note", "-n", "N1", "hi"},
		{"assign", "-d", "Note", "-n", "N1", "--to", "jane@example.com"},
		{"tag", "-d", "Note", "-n", "N1", "vip"},
		{"share", "-d", "Note", "-n", "N1", "--user", "jane@example.com"}, // no --yes needed
	} {
		reqs := dryTPlan(t, cmdTRun(t, s, append(args, "--dry-run", "--json")...))
		if len(reqs) != 1 || reqs[0]["method"] != "POST" {
			t.Errorf("%s: plan %v", args[0], reqs)
		}
	}
	if s.Count("Comment")+s.Count("ToDo")+s.Count("DocShare") != 0 {
		t.Error("a dry run wrote")
	}
	// The reads still run: a missing document fails the dry run.
	lcTCode(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "nope", "--to", "jane@example.com", "--dry-run"), exitNotFound)

	// The removals read the current state (get_users is a GET) and hold
	// back only the write.
	cmdTOK(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "N1", "--to", "jane@example.com"))
	cmdTOK(t, cmdTRun(t, s, "tag", "-d", "Note", "-n", "N1", "vip"))
	cmdTOK(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--user", "jane@example.com", "--yes"))
	for _, c := range []struct {
		args   []string
		method string
	}{
		{[]string{"unassign", "-d", "Note", "-n", "N1", "--to", "jane@example.com"}, "frappe.desk.form.assign_to.remove"},
		{[]string{"untag", "-d", "Note", "-n", "N1", "vip"}, "frappe.desk.doctype.tag.tag.remove_tag"},
		{[]string{"unshare", "-d", "Note", "-n", "N1", "--user", "jane@example.com"}, "frappe.share.set_permission"},
	} {
		reqs := dryTPlan(t, cmdTRun(t, s, append(c.args, "--dry-run", "--json")...))
		if len(reqs) != 1 || reqs[0]["method"] != "POST" || !strings.HasSuffix(fmt.Sprint(reqs[0]["url"]), "/api/method/"+c.method) {
			t.Errorf("%s: plan %v", c.args[0], reqs)
		}
	}
	if s.Count("DocShare") != 1 || s.Count("ToDo") != 1 {
		t.Errorf("a dry run removed something: %d DocShare, %d ToDo", s.Count("DocShare"), s.Count("ToDo"))
	}
	if tags, _ := s.Doc("Note", "N1"); tags["_user_tags"] != "vip" {
		t.Errorf("a dry run untagged: %v", tags["_user_tags"])
	}
	if n := len(s.RequestsTo("GET", "/api/method/frappe.share.get_users")); n != 1 {
		t.Errorf("get_users sent as GET %d times, want 1", n)
	}
}

func TestMCPCollabTools(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("Note", map[string]interface{}{"name": "N1"})
	site.Add("User", map[string]interface{}{"name": "jane@example.com"})
	doc := func(extra map[string]interface{}) map[string]interface{} {
		m := map[string]interface{}{"doctype": "Note", "name": "N1"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	out := mcpTOK(t, s, "add_comment", doc(map[string]interface{}{"text": "x < y"}))
	if !strings.Contains(out, `x \u0026lt; y`) {
		t.Errorf("add_comment = %s", out)
	}
	if out := mcpTOK(t, s, "assign_to", doc(map[string]interface{}{"users": []string{"jane@example.com"}, "priority": "Low"})); !strings.Contains(out, `"assigned":["jane@example.com"]`) {
		t.Errorf("assign_to = %s", out)
	}
	if out := mcpTOK(t, s, "assign_to", doc(map[string]interface{}{"users": "jane@example.com"})); !strings.Contains(out, `"already_assigned":["jane@example.com"]`) {
		t.Errorf("assign_to again = %s", out)
	}
	if out := mcpTOK(t, s, "remove_assignment", doc(map[string]interface{}{"users": `["jane@example.com"]`})); !strings.Contains(out, `"unassigned":["jane@example.com"]`) {
		t.Errorf("remove_assignment = %s", out)
	}
	if out := mcpTOK(t, s, "add_tag", doc(map[string]interface{}{"tags": []string{"a", "b"}})); !strings.Contains(out, `"tags":["a","b"]`) {
		t.Errorf("add_tag = %s", out)
	}
	if out := mcpTOK(t, s, "remove_tag", doc(map[string]interface{}{"tags": "a"})); !strings.Contains(out, `"tags":["b"]`) {
		t.Errorf("remove_tag = %s", out)
	}
	mcpTErr(t, s, "add_tag", doc(map[string]interface{}{"tags": []string{"a,b"}}), "comma")
	mcpTErr(t, s, "assign_to", doc(map[string]interface{}{"users": []string{"jane@example.com"}, "date": "soon"}), "YYYY-MM-DD")
	mcpTErr(t, s, "assign_to", map[string]interface{}{"doctype": "Note", "name": "nope", "users": "jane@example.com"}, `Note "nope" not found`)
	// DocShare is sensitive: sharing is refused unless the config allows it.
	mcpTErr(t, s, "share_doc", doc(map[string]interface{}{"user": "jane@example.com"}), `DocType "DocShare" is sensitive`)
	mcpTErr(t, s, "unshare_doc", doc(map[string]interface{}{"everyone": true}), `DocType "DocShare" is sensitive`)
	if n := len(site.RequestsTo("POST", "/api/method/frappe.share.add")); n != 0 {
		t.Errorf("share_doc reached the site")
	}
}

func TestMCPCollabPolicy(t *testing.T) {
	note := map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	with := func(extra map[string]interface{}) map[string]interface{} {
		m := map[string]interface{}{}
		for k, v := range note {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	// The implicit DocType is checked like the document's.
	s, _, _, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"Comment"}}, config.MCPPolicy{})
	mcpTErr(t, s, "add_comment", with(map[string]interface{}{"text": "x"}), `DocType "Comment" is denied`)
	// The author's full name is read from User, unless the policy denies it.
	s, site, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	mcpTOK(t, s, "add_comment", with(map[string]interface{}{"text": "x"}))
	if n := len(site.RequestsTo("GET", "/api/resource/User/Administrator")); n != 1 {
		t.Errorf("User read %d times, want 1", n)
	}
	s, site, _, _ = mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"User"}}, config.MCPPolicy{})
	mcpTOK(t, s, "add_comment", with(map[string]interface{}{"text": "x"}))
	if n := len(site.RequestsTo("GET", "/api/resource/User/Administrator")); n != 0 {
		t.Errorf("User read %d times under deny_doctypes: [User]", n)
	}
	s, _, _, _ = mcpTPolicy(t, nil, config.MCPPolicy{AllowDoctypes: []string{"ToDo"}})
	mcpTErr(t, s, "add_tag", with(map[string]interface{}{"tags": "x"}), `DocType "Tag Link" is not in --allow-doctypes`)
	s, _, _, _ = mcpTPolicy(t, nil, config.MCPPolicy{ReadOnly: true})
	if _, ok := s.ListTools()["assign_to"]; ok {
		t.Error("assign_to registered read-only")
	}

	// With DocShare allowed, share_doc needs confirmation; a client that
	// cannot ask gets the CLI equivalent under confirm: always.
	cfg := &config.MCPPolicy{AllowDoctypes: []string{"ToDo", "DocShare"}, Confirm: "always"}
	s, site, _, _ = mcpTPolicy(t, cfg, config.MCPPolicy{})
	mcpTErr(t, s, "share_doc", with(map[string]interface{}{"user": "u@example.com", "write": true}),
		"Run it in a terminal instead: ffc --site prod share --doctype ToDo --name TD-1 --user 'u@example.com' --write")
	if n := len(site.RequestsTo("POST", "/api/method/frappe.share.add")); n != 0 {
		t.Error("share_doc sent without confirmation")
	}
	// The user is asked, and the question says what is widened.
	cfg.Confirm = ""
	s, site, _, audit := mcpTPolicy(t, cfg, config.MCPPolicy{})
	a := &mcpTAsker{action: mcp.ElicitationResponseActionAccept, confirm: true}
	if out, isErr := mcpTCall(t, mcpTClient(t, s, a, false), "share_doc", with(map[string]interface{}{"everyone": true})); isErr {
		t.Fatalf("share_doc: %s", out)
	}
	if len(a.asked) != 1 || !strings.Contains(a.asked[0], `share "ToDo" "TD-1" on site "prod" with every user (read)`) || strings.Contains(a.asked[0], "cannot be undone") {
		t.Errorf("asked %q", a.asked)
	}
	if site.Count("DocShare") != 1 {
		t.Error("not shared")
	}
	if got := strings.Join(mcpTStatuses(t, audit), ","); got != "confirm_pending,ok" {
		t.Errorf("audit = %s", got)
	}
	// unshare_doc narrows access and is not confirmed.
	if out, isErr := mcpTCall(t, mcpTClient(t, s, a, false), "unshare_doc", with(map[string]interface{}{"everyone": true})); isErr || !strings.Contains(out, `"removed":true`) {
		t.Errorf("unshare_doc: %s", out)
	}
	if len(a.asked) != 1 {
		t.Errorf("unshare_doc asked")
	}
}

func TestMCPCollabCallMethodScope(t *testing.T) {
	call := func(method string, args map[string]interface{}) toolScope {
		req := mcp.CallToolRequest{}
		req.Params.Name = "call_method"
		req.Params.Arguments = map[string]interface{}{"method": method, "args": args}
		sc, err := scopeOf(req)
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}
	if sc := call("frappe.share.add", map[string]interface{}{"doctype": "Note", "name": "N1", "user": "u"}); !reflect.DeepEqual(sc.Doctypes, []string{"Note", "DocShare"}) {
		t.Errorf("share.add doctypes = %v", sc.Doctypes)
	}
	if sc := call("frappe.desk.doctype.tag.tag.add_tag", map[string]interface{}{"dt": "Note", "dn": "N1", "tag": "x"}); !reflect.DeepEqual(sc.Doctypes, []string{"Note", "Tag Link", "Tag"}) {
		t.Errorf("add_tag doctypes = %v", sc.Doctypes)
	}
	// The query arguments (queryScope) are checked as well as the implicit
	// DocType.
	sc := call("frappe.share.add", map[string]interface{}{"doctype": "Note", "name": "N1", "filters": []interface{}{[]interface{}{"Customer", "name", "=", "x"}}})
	if !contains(sc.Doctypes, "DocShare", false) || !contains(sc.Doctypes, "Customer", false) || !contains(sc.FilterFields, "name", false) {
		t.Errorf("share.add with filters: doctypes %v, filter fields %v", sc.Doctypes, sc.FilterFields)
	}
	// Without a DocType the call is refused, not checked against DocShare only.
	sc = call("frappe.share.add", map[string]interface{}{"name": "N1"})
	if len(sc.Doctypes) != 0 {
		t.Errorf("doctypes = %v", sc.Doctypes)
	}
	if err := (mcpPolicy{}).check("call_method", sc); err == nil || !strings.Contains(err.Error(), "name no DocType") {
		t.Errorf("check = %v", err)
	}
	if err := (mcpPolicy{}).check("call_method", call("frappe.share.add", map[string]interface{}{"doctype": "Note"})); err == nil || !strings.Contains(err.Error(), "DocShare") {
		t.Errorf("share.add via call_method: %v", err)
	}
}

func TestCollabWidenConfirm(t *testing.T) {
	req := func(tool string, args map[string]interface{}) mcp.CallToolRequest {
		r := mcp.CallToolRequest{}
		r.Params.Name, r.Params.Arguments = tool, args
		return r
	}
	method := func(m string, args map[string]interface{}) bool {
		return needsConfirm(req("call_method", map[string]interface{}{"method": m, "args": args}), m)
	}
	if !needsConfirm(req("assign_to", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "users": "u"}), "") {
		t.Error("assign_to is not confirmed")
	}
	for _, c := range []struct {
		method string
		args   map[string]interface{}
		want   bool
	}{
		{"frappe.share.add", map[string]interface{}{"doctype": "Note", "name": "N1", "user": "u"}, true},
		{"frappe.desk.form.assign_to.add", map[string]interface{}{"doctype": "Note", "name": "N1", "assign_to": `["u"]`}, true},
		{"frappe.desk.form.assign_to.add_multiple", map[string]interface{}{"doctype": "Note", "name": `["N1"]`}, true},
		{"frappe.share.set_permission", map[string]interface{}{"doctype": "Note", "name": "N1", "user": "u", "permission_to": "write"}, true},
		{"frappe.share.set_permission", map[string]interface{}{"doctype": "Note", "name": "N1", "permission_to": "write", "value": float64(1)}, true},
		{"frappe.share.set_permission", map[string]interface{}{"doctype": "Note", "name": "N1", "permission_to": "write", "value": "1"}, true},
		{"frappe.share.set_permission", map[string]interface{}{"doctype": "Note", "name": "N1", "permission_to": "read", "value": float64(0)}, false},
		{"frappe.share.set_permission", map[string]interface{}{"doctype": "Note", "name": "N1", "permission_to": "read", "value": "0"}, false},
		{"frappe.desk.form.assign_to.remove", map[string]interface{}{"doctype": "Note", "name": "N1", "assign_to": "u"}, false},
	} {
		if got := method(c.method, c.args); got != c.want {
			t.Errorf("%s %v: confirm %v, want %v", c.method, c.args, got, c.want)
		}
	}

	// The question says what may be widened, and the CLI equivalent assigns.
	cfg := &config.MCPPolicy{Confirm: "always"}
	s, site, _, _ := mcpTPolicy(t, cfg, config.MCPPolicy{})
	todo := map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "users": []string{"u@example.com"}, "priority": "High"}
	mcpTErr(t, s, "assign_to", todo, "Run it in a terminal instead: ffc --site prod assign --doctype ToDo --name TD-1 --to 'u@example.com' --priority High")
	if n := len(site.RequestsTo("POST", "/api/method/frappe.desk.form.assign_to.add")); n != 0 {
		t.Error("assign_to sent without confirmation")
	}
	cfg.Confirm = ""
	s, site, _, _ = mcpTPolicy(t, cfg, config.MCPPolicy{})
	a := &mcpTAsker{action: mcp.ElicitationResponseActionAccept, confirm: true}
	if out, isErr := mcpTCall(t, mcpTClient(t, s, a, false), "assign_to", todo); isErr {
		t.Fatalf("assign_to: %s", out)
	}
	if len(a.asked) != 1 || !strings.Contains(a.asked[0], `to "u@example.com"`) || !strings.Contains(a.asked[0], "read access to it through a share") {
		t.Errorf("asked %q", a.asked)
	}
	if site.Count("ToDo") != 2 { // TD-1 and the assignment
		t.Errorf("%d ToDos", site.Count("ToDo"))
	}

	// call_method frappe.share.add is refused by default (DocShare is
	// sensitive); allowed by the config, it is confirmed like share_doc.
	share := map[string]interface{}{"method": "frappe.share.add", "args": map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "user": "u@example.com"}}
	mcpTErr(t, s, "call_method", share, `DocType "DocShare" is sensitive`)
	s, _, _, _ = mcpTPolicy(t, &config.MCPPolicy{AllowDoctypes: []string{"ToDo", "DocShare"}, AllowMethods: []string{"frappe.share.*"}}, config.MCPPolicy{})
	a = &mcpTAsker{action: mcp.ElicitationResponseActionDecline}
	if out, _ := mcpTCall(t, mcpTClient(t, s, a, false), "call_method", share); !strings.Contains(out, "cancelled by the user") {
		t.Errorf("call_method share.add: %s", out)
	}
	if len(a.asked) != 1 || !strings.Contains(a.asked[0], "access to documents beyond what their roles allow") || strings.Contains(a.asked[0], "cannot be undone") {
		t.Errorf("asked %q", a.asked)
	}
}

func TestMCPCommentFilesAndAuthor(t *testing.T) {
	scope := func(tool string, args map[string]interface{}) []string {
		req := mcp.CallToolRequest{}
		req.Params.Name, req.Params.Arguments = tool, args
		sc, err := scopeOf(req)
		if err != nil {
			t.Fatal(err)
		}
		return sc.Doctypes
	}
	img := `<p><img src="data:image/png;base64,iVBORw0KGgo="></p>`
	for _, c := range []struct {
		tool string
		args map[string]interface{}
		want []string
	}{
		{"add_comment", map[string]interface{}{"doctype": "Note", "name": "N1", "text": img, "html": true}, []string{"Note", "Comment", "File"}},
		{"add_comment", map[string]interface{}{"doctype": "Note", "name": "N1", "text": img}, []string{"Note", "Comment"}},
		{"add_comment", map[string]interface{}{"doctype": "Note", "name": "N1", "text": "<b>hi</b>", "html": true}, []string{"Note", "Comment"}},
		{"call_method", map[string]interface{}{"method": addCommentMethod, "args": map[string]interface{}{"reference_doctype": "Note", "content": img}}, []string{"Note", "Comment", "File"}},
		{"call_method", map[string]interface{}{"method": addCommentMethod, "args": map[string]interface{}{"reference_doctype": "Note", "content": "hi"}}, []string{"Note", "Comment"}},
	} {
		if got := scope(c.tool, c.args); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v: doctypes %v, want %v", c.tool, c.args, got, c.want)
		}
	}

	// File is sensitive: an HTML comment with an image is refused by default.
	s, site, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	mcpTErr(t, s, "add_comment", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "text": img, "html": true}, `DocType "File" is sensitive`)
	if site.Count("Comment") != 0 {
		t.Error("comment added")
	}

	// call_method add_comment may only post as the signed-in user.
	site.SetUser("jane@example.com")
	site.Add("User", map[string]interface{}{"name": "jane@example.com", "full_name": "Jane Doe"})
	comment := func(email, by interface{}) map[string]interface{} {
		return map[string]interface{}{"method": addCommentMethod, "args": map[string]interface{}{
			"reference_doctype": "ToDo", "reference_name": "TD-1", "content": "hi", "comment_email": email, "comment_by": by,
		}}
	}
	mcpTErr(t, s, "call_method", comment("boss@example.com", "jane@example.com"), `comment_email "boss@example.com" would post the comment under another name than the signed-in user "jane@example.com"; use the add_comment tool`)
	mcpTErr(t, s, "call_method", comment("jane@example.com", "The Boss"), `comment_by "The Boss"`)
	mcpTErr(t, s, "call_method", comment(float64(1), "jane@example.com"), "comment_email must be a string")
	if site.Count("Comment") != 0 {
		t.Fatal("a refused comment was added")
	}
	mcpTOK(t, s, "call_method", comment("JANE@example.com", "jane@example.com"))
	mcpTOK(t, s, "call_method", comment("", ""))
	mcpTOK(t, s, "call_method", comment("jane@example.com", "Jane Doe"))
	if site.Count("Comment") != 3 {
		t.Errorf("%d comments", site.Count("Comment"))
	}
	if len(site.RequestsTo("GET", "/api/resource/User/jane@example.com")) == 0 {
		t.Error("the full name was accepted without reading User")
	}
	// A policy that denies reading User leaves only the user ID.
	s, site, _, _ = mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"User"}}, config.MCPPolicy{})
	site.SetUser("jane@example.com")
	site.Add("User", map[string]interface{}{"name": "jane@example.com", "full_name": "Jane Doe"})
	mcpTErr(t, s, "call_method", comment("jane@example.com", "Jane Doe"), `comment_by "Jane Doe"`)
	if n := len(site.RequestsTo("GET", "/api/resource/User/jane@example.com")); n != 0 {
		t.Errorf("User read %d times against the policy", n)
	}
}

// TestCollabFakeShares pins the share side effects the fake models after
// Frappe (share.py, assign_to.py); contract_collab_test checks them on a
// real site.
func TestCollabFakeShares(t *testing.T) {
	s := collabTSite(t)
	shares := func() []map[string]interface{} {
		r := cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "frappe.share.get_users", "--args", `{"doctype":"Note","name":"N1"}`, "--json"))
		var out []map[string]interface{}
		if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
			t.Fatalf("%v: %s", err, r.Stdout)
		}
		return out
	}

	// An assignee who cannot read the document gets a read-only share; one
	// who can read gets none.
	s.DenyUser("Note", "jane@example.com")
	cmdTOK(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "N1", "--to", "jane@example.com,bob@example.com"))
	if sh := shares(); len(sh) != 1 || sh[0]["user"] != "jane@example.com" || fmt.Sprint(sh[0]["read"]) != "1" || fmt.Sprint(sh[0]["write"]) != "0" {
		t.Errorf("shares after assign = %v", sh)
	}
	cmdTOK(t, cmdTRun(t, s, "unassign", "-d", "Note", "-n", "N1", "--to", "jane@example.com,bob@example.com"))
	cmdTOK(t, cmdTRun(t, s, "unshare", "-d", "Note", "-n", "N1", "--user", "jane@example.com"))

	// share.add without a user shares with the session user; with
	// everyone, the share has no user.
	cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "frappe.share.add", "--args", `{"doctype":"Note","name":"N1"}`))
	cmdTOK(t, cmdTRun(t, s, "share", "-d", "Note", "-n", "N1", "--everyone", "--yes"))
	sh := shares()
	users := map[string]bool{}
	for _, d := range sh {
		users[fmt.Sprint(d["user"])+"/"+fmt.Sprint(d["everyone"])] = true
	}
	if len(sh) != 2 || !users[frappetest.Username+"/0"] || !users["<nil>/1"] {
		t.Errorf("shares = %v", sh)
	}

	// set_permission with a true (or absent) value adds the right, creating
	// a read share when there is none; value 0 on read removes the share.
	cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "frappe.share.set_permission", "--args",
		`{"doctype":"Note","name":"N1","user":"bob@example.com","permission_to":"write","value":1}`))
	cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "frappe.share.set_permission", "--args",
		`{"doctype":"Note","name":"N1","user":"jane@example.com","permission_to":"share"}`))
	got := map[string]string{}
	for _, d := range shares() {
		got[fmt.Sprint(d["user"])] = fmt.Sprintf("r%v w%v s%v", d["read"], d["write"], d["share"])
	}
	if got["bob@example.com"] != "r1 w1 s0" || got["jane@example.com"] != "r1 w0 s1" {
		t.Errorf("set_permission shares = %v", got)
	}
	cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "frappe.share.set_permission", "--args",
		`{"doctype":"Note","name":"N1","user":"bob@example.com","permission_to":"read","value":0}`))
	if len(shares()) != 3 {
		t.Errorf("shares after removing bob = %v", shares())
	}

	// Sharing write needs write permission; an assignment that must share
	// needs share permission. An everyone-share lets every user read, so it
	// goes first.
	cmdTOK(t, cmdTRun(t, s, "unshare", "-d", "Note", "-n", "N1", "--everyone"))
	s.Deny("Note", "write")
	lcTCode(t, cmdTRun(t, s, "call-method", "--method", "frappe.share.set_permission", "--args",
		`{"doctype":"Note","name":"N1","user":"bob@example.com","permission_to":"write"}`), exitPermission)
	s.Deny("Note", "share")
	s.DenyUser("Note", "bob@example.com")
	lcTCode(t, cmdTRun(t, s, "assign", "-d", "Note", "-n", "N1", "--to", "bob@example.com"), exitPermission)
	if s.Count("ToDo") != 2 { // both closed by unassign; none added
		t.Errorf("%d ToDos", s.Count("ToDo"))
	}
}

// TestStoredUserIDs checks that results name users as the site stores them.
func TestStoredUserIDs(t *testing.T) {
	got := stored([]string{"Administrator", "jane@example.com"}, []string{"administrator", "JANE@example.com", "bob@example.com"})
	if want := []string{"Administrator", "jane@example.com", "bob@example.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stored = %v, want %v", got, want)
	}
}
