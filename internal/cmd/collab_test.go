package cmd

import (
	"encoding/json"
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
	// Without a DocType the call is refused, not checked against DocShare only.
	sc := call("frappe.share.add", map[string]interface{}{"name": "N1"})
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
