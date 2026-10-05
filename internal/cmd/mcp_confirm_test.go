package cmd

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// mcpTAsker answers elicitation requests like a user would.
type mcpTAsker struct {
	action  mcp.ElicitationResponseAction
	confirm bool
	asked   []string // the messages shown
}

func (a *mcpTAsker) Elicit(_ context.Context, req mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	a.asked = append(a.asked, req.Params.Message)
	return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
		Action: a.action, Content: map[string]interface{}{"confirm": a.confirm},
	}}, nil
}

// mcpTClient connects an in-process MCP client to s. With asker nil the
// client cannot elicit; legacy pins the 2025-11-25 protocol, where mcp-go
// bridges the input request to elicitation/create.
func mcpTClient(t *testing.T, s *server.MCPServer, asker *mcpTAsker, legacy bool) *mcpclient.Client {
	t.Helper()
	var opts []mcpclient.ClientOption
	if asker != nil {
		opts = append(opts, mcpclient.WithElicitationHandler(asker))
	}
	if legacy {
		opts = append(opts, mcpclient.WithProtocolVersion("2025-11-25"))
	}
	c, err := mcpclient.NewInProcessClientWithOptions(s, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ClientInfo = mcp.Implementation{Name: "confirm-test", Version: "1"}
	if _, err := c.Initialize(t.Context(), init); err != nil {
		t.Fatal(err)
	}
	return c
}

func mcpTCall(t *testing.T, c *mcpclient.Client, name string, args map[string]interface{}) (string, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name, req.Params.Arguments = name, args
	res, err := c.CallTool(t.Context(), req)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out strings.Builder
	for _, ct := range res.Content {
		if tc, ok := ct.(mcp.TextContent); ok {
			out.WriteString(tc.Text)
		}
	}
	return out.String(), res.IsError
}

// mcpTStatuses returns the status of every audit line, in order.
func mcpTStatuses(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r auditRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r.Status)
	}
	return out
}

func TestMCPConfirm(t *testing.T) {
	del := map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	for _, legacy := range []bool{false, true} {
		proto := map[bool]string{false: "modern", true: "legacy"}[legacy]
		t.Run(proto+"/accept", func(t *testing.T) {
			s, site, _, audit := mcpTPolicy(t, nil, config.MCPPolicy{})
			a := &mcpTAsker{action: mcp.ElicitationResponseActionAccept, confirm: true}
			if out, isErr := mcpTCall(t, mcpTClient(t, s, a, legacy), "delete_doc", del); isErr {
				t.Fatalf("error: %s", out)
			}
			if _, ok := site.Doc("ToDo", "TD-1"); ok {
				t.Error("TD-1 still exists")
			}
			if len(a.asked) != 1 || !strings.Contains(a.asked[0], `site "prod"`) || !strings.Contains(a.asked[0], `Delete "ToDo" "TD-1".`) {
				t.Errorf("asked %q", a.asked)
			}
			if got := strings.Join(mcpTStatuses(t, audit), ","); got != "confirm_pending,ok" {
				t.Errorf("audit statuses = %s", got)
			}
		})
		for _, answer := range []struct {
			name    string
			action  mcp.ElicitationResponseAction
			confirm bool
		}{
			{"decline", mcp.ElicitationResponseActionDecline, false},
			{"cancel", mcp.ElicitationResponseActionCancel, false},
			{"unchecked", mcp.ElicitationResponseActionAccept, false},
		} {
			t.Run(proto+"/"+answer.name, func(t *testing.T) {
				s, site, _, audit := mcpTPolicy(t, nil, config.MCPPolicy{})
				a := &mcpTAsker{action: answer.action, confirm: answer.confirm}
				out, isErr := mcpTCall(t, mcpTClient(t, s, a, legacy), "delete_doc", del)
				if !isErr || !strings.Contains(out, "cancelled by the user; nothing was changed") {
					t.Errorf("result %v %q", isErr, out)
				}
				if _, ok := site.Doc("ToDo", "TD-1"); !ok {
					t.Error("TD-1 was deleted")
				}
				if got := strings.Join(mcpTStatuses(t, audit), ","); got != "confirm_pending,declined" {
					t.Errorf("audit statuses = %s", got)
				}
			})
		}
	}
}

func TestMCPConfirmModes(t *testing.T) {
	del := map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	yes := func() *mcpTAsker { return &mcpTAsker{action: mcp.ElicitationResponseActionAccept, confirm: true} }
	for _, tc := range []struct {
		name    string
		cfg     *config.MCPPolicy
		flags   config.MCPPolicy
		asker   *mcpTAsker
		asked   int
		refused string // the error, when the call is refused
	}{
		{name: "if-supported without elicitation goes ahead"},
		{name: "never does not ask", cfg: &config.MCPPolicy{Confirm: "never"}, asker: yes()},
		{name: "always asks", cfg: &config.MCPPolicy{Confirm: "always"}, asker: yes(), asked: 1},
		{name: "always without elicitation refuses", cfg: &config.MCPPolicy{Confirm: "always"},
			refused: "sites.prod.mcp.confirm: always) and this MCP client cannot ask for it; nothing was changed. Run it in a terminal instead: ffc --site prod delete-doc --doctype ToDo --name TD-1"},
		{name: "the flag tightens never", cfg: &config.MCPPolicy{Confirm: "never"}, flags: config.MCPPolicy{Confirm: "always"},
			refused: "(--confirm always)"},
		{name: "the flag tightens the default", flags: config.MCPPolicy{Confirm: "always"}, refused: "(--confirm always)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, site, _, audit := mcpTPolicy(t, tc.cfg, tc.flags)
			c := mcpTClient(t, s, tc.asker, false)
			out, isErr := mcpTCall(t, c, "delete_doc", del)
			_, exists := site.Doc("ToDo", "TD-1")
			if tc.refused != "" {
				if !isErr || !strings.Contains(out, tc.refused) || !exists {
					t.Errorf("want refusal %q, got %v %q (exists %v)", tc.refused, isErr, out, exists)
				}
				if got := mcpTStatuses(t, audit); len(got) != 1 || got[0] != auditDenied {
					t.Errorf("audit statuses = %v", got)
				}
				return
			}
			if isErr || exists {
				t.Errorf("want delete, got %v %q (exists %v)", isErr, out, exists)
			}
			if tc.asker != nil && len(tc.asker.asked) != tc.asked {
				t.Errorf("asked %d times, want %d", len(tc.asker.asked), tc.asked)
			}
		})
	}
}

// Reads, creates and a rename that does not merge are not confirmed.
func TestMCPConfirmOnlyDestructive(t *testing.T) {
	s, _, _, _ := mcpTPolicy(t, &config.MCPPolicy{Confirm: "always"}, config.MCPPolicy{})
	a := &mcpTAsker{action: mcp.ElicitationResponseActionDecline}
	c := mcpTClient(t, s, a, false)
	if out, isErr := mcpTCall(t, c, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}); isErr {
		t.Fatal(out)
	}
	if out, isErr := mcpTCall(t, c, "create_doc", map[string]interface{}{"doctype": "ToDo", "data": map[string]interface{}{"description": "x"}}); isErr {
		t.Fatal(out)
	}
	out, isErr := mcpTCall(t, c, "rename_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "new_name": "TD-2", "merge": true})
	if !isErr || !strings.Contains(out, "cancelled by the user") {
		t.Errorf("merge: %v %q", isErr, out)
	}
	if len(a.asked) != 1 || !strings.Contains(a.asked[0], `Merge "ToDo" "TD-1" into "TD-2". "TD-1" will no longer exist.`) {
		t.Errorf("asked %q", a.asked)
	}
}

func TestMCPConfirmBulkMessage(t *testing.T) {
	s, _, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	a := &mcpTAsker{action: mcp.ElicitationResponseActionDecline}
	names := []interface{}{"TD-1\u202eevil", "x\ny"}
	for i := 0; i < 10; i++ {
		names = append(names, "N"+string(rune('a'+i)))
	}
	mcpTCall(t, mcpTClient(t, s, a, false), "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": names})
	if len(a.asked) != 1 {
		t.Fatalf("asked %q", a.asked)
	}
	msg := a.asked[0]
	if !strings.Contains(msg, `Delete 12 "ToDo" documents: "TD-1\u202eevil", "x\ny", "Na",`) || !strings.Contains(msg, `"Nh" and 2 more.`) {
		t.Errorf("message %q", msg)
	}
	if strings.ContainsAny(msg, "\u202e\n") {
		t.Errorf("message keeps control characters: %q", msg)
	}
}

func TestNeedsConfirm(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]interface{}
		want bool
	}{
		{"delete_doc", nil, true},
		{"bulk_delete", nil, true},
		{"cancel_doc", nil, true},
		{"update_doc", nil, false},
		{"submit_doc", nil, false},
		{"rename_doc", map[string]interface{}{"merge": false}, false},
		{"rename_doc", map[string]interface{}{"merge": "true"}, true},
		{"rename_doc", map[string]interface{}{}, false},
		{"call_method", map[string]interface{}{"method": "frappe.client.delete"}, true},
		{"call_method", map[string]interface{}{"method": "frappe.desk.reportview.delete_items"}, true},
		{"call_method", map[string]interface{}{"method": "frappe.client.get_list"}, false},
		{"call_method", map[string]interface{}{"method": "frappe.client.rename_doc", "args": map[string]interface{}{"merge": 0.0}}, false},
		{"call_method", map[string]interface{}{"method": "frappe.client.rename_doc", "args": map[string]interface{}{"merge": "0"}}, true},
		{"call_method", map[string]interface{}{"method": "frappe.client.rename_doc", "args": `{"merge": 1}`}, true},
		{"call_method", map[string]interface{}{"method": "frappe.client.rename_doc", "args": `{"doctype": "ToDo"}`}, false},
		{"apply_workflow", nil, true},
		{"call_method", map[string]interface{}{"method": "frappe.desk.form.save.discard"}, true},
		{"call_method", map[string]interface{}{"method": "frappe.desk.form.linked_with.cancel_all_linked_docs"}, true},
		{"call_method", map[string]interface{}{"method": "frappe.model.workflow.apply_workflow"}, true},
		{"call_method", map[string]interface{}{"method": "frappe.model.workflow.bulk_workflow_approval"}, true},
		{"call_method", map[string]interface{}{"method": "run_doc_method", "args": map[string]interface{}{"dt": "ToDo", "method": "cancel"}}, true},
		{"call_method", map[string]interface{}{"method": "frappe.handler.run_doc_method", "args": `{"dt": "ToDo", "method": " Discard "}`}, true},
		{"call_method", map[string]interface{}{"method": "frappe.handler.run_doc_method", "args": map[string]interface{}{"dt": "ToDo", "method": "rename"}}, true},
		{"call_method", map[string]interface{}{"method": "frappe.handler.run_doc_method", "args": map[string]interface{}{"dt": "ToDo", "method": "get_feed"}}, false},
		{"call_method", map[string]interface{}{"method": "frappe.desk.form.save.savedocs", "args": map[string]interface{}{"doc": "{}", "action": "Cancel"}}, true},
		{"call_method", map[string]interface{}{"method": "frappe.desk.form.save.savedocs", "args": map[string]interface{}{"doc": "{}", "action": "Save"}}, false},
		{"call_method", map[string]interface{}{"method": "frappe.desk.doctype.bulk_update.bulk_update.submit_cancel_or_update_docs", "args": map[string]interface{}{"doctype": "ToDo", "action": "cancel"}}, true},
		{"call_method", map[string]interface{}{"method": "frappe.desk.doctype.bulk_update.bulk_update.submit_cancel_or_update_docs", "args": map[string]interface{}{"doctype": "ToDo"}}, false},
	} {
		req := mcp.CallToolRequest{}
		req.Params.Name, req.Params.Arguments = tc.tool, tc.args
		method, _ := tc.args["method"].(string)
		if got := needsConfirm(req, method); got != tc.want {
			t.Errorf("%s %v = %v, want %v", tc.tool, tc.args, got, tc.want)
		}
	}
}

// A request state is good once, for the call it was issued for, until it
// expires.
func TestConfirmState(t *testing.T) {
	c := newConfirmer()
	call := func(args map[string]interface{}, state string) mcp.CallToolRequest {
		req := mcp.CallToolRequest{}
		req.Params.Name, req.Params.Arguments, req.Params.RequestState = "delete_doc", args, state
		return req
	}
	a := map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	b := map[string]interface{}{"doctype": "ToDo", "name": "TD-2"}
	st := c.newState("prod", call(a, ""), time.Now().Add(time.Minute))
	if again := c.newState("prod", call(a, ""), time.Now().Add(time.Minute)); again == st {
		t.Error("two questions got the same state")
	}
	if c.spend("prod", call(b, st)) {
		t.Error("state spent for other arguments")
	}
	if c.spend("staging", call(a, st)) {
		t.Error("state spent for another site")
	}
	if c.spend("prod", call(a, st+"x")) || c.spend("prod", call(a, "")) || c.spend("prod", call(a, "9999999999.abc")) {
		t.Error("forged state accepted")
	}
	if newConfirmer().spend("prod", call(a, st)) {
		t.Error("state accepted by another process")
	}
	if !c.spend("prod", call(a, st)) {
		t.Fatal("valid state refused")
	}
	if c.spend("prod", call(a, st)) {
		t.Error("state spent twice")
	}
	old := c.newState("prod", call(a, ""), time.Now().Add(-time.Second))
	if c.spend("prod", call(a, old)) {
		t.Error("expired state accepted")
	}
}

func TestCLIEquivalent(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]interface{}
		want string
	}{
		{"delete_doc", map[string]interface{}{"doctype": "Sales Invoice", "name": "SINV-1"}, "ffc --site prod delete-doc --doctype 'Sales Invoice' --name SINV-1"},
		{"bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": []interface{}{"a", "b c"}}, "ffc --site prod bulk-delete --doctype ToDo --names 'a,b c'"},
		{"rename_doc", map[string]interface{}{"doctype": "ToDo", "name": "a", "new_name": "it's", "merge": true}, `ffc --site prod rename-doc --doctype ToDo --name a --to 'it'\''s' --merge`},
		{"call_method", map[string]interface{}{"method": "frappe.client.delete", "args": map[string]interface{}{"doctype": "ToDo", "name": "a"}}, `ffc --site prod call-method --method frappe.client.delete --args '{"doctype":"ToDo","name":"a"}'`},
		{"delete_doc", map[string]interface{}{"doctype": "ToDo", "name": "a\x1b[2J"}, "ffc delete-doc"},
		{"bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": []interface{}{"A,B"}}, "ffc bulk-delete"},
		{"bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": []interface{}{"a", " b"}}, "ffc bulk-delete"},
		{"apply_workflow", map[string]interface{}{"doctype": "ToDo", "name": "a", "action": "Reject it"}, "ffc --site prod workflow apply --doctype ToDo --name a --action 'Reject it'"},
	} {
		req := mcp.CallToolRequest{}
		req.Params.Name, req.Params.Arguments = tc.tool, tc.args
		sc, err := scopeOf(req)
		if err != nil {
			t.Fatal(err)
		}
		if got := cliEquivalent("prod", req, sc); got != tc.want {
			t.Errorf("%s = %s\nwant  %s", tc.tool, got, tc.want)
		}
	}
}

func TestMCPConfirmFlag(t *testing.T) {
	s := cmdTSite(t)
	for _, tc := range []struct{ value, want string }{
		{"never", "--confirm can only tighten"},
		{"sometimes", `--confirm must be always or if-supported, not "sometimes"`},
	} {
		r := runFFC(t, fakeConfig(t, s, "apikey"), "", "mcp", "--confirm", tc.value)
		if r.Code != exitUsage || !strings.Contains(r.Stderr, tc.want) {
			t.Errorf("--confirm %s: exit %d: %s", tc.value, r.Code, r.Stderr)
		}
	}
	prev := mcpFlags
	t.Cleanup(func() { mcpFlags = prev })
	mcpFlags = config.MCPPolicy{Confirm: "always"}
	if got := strings.Join(daemonArgs("", 1), " "); !strings.HasSuffix(got, " --confirm=always") {
		t.Errorf("daemon args = %q", got)
	}
}

// Padding arguments cannot push the key ones out of the question, and an
// invisible character in a name shows escaped instead of disappearing.
func TestConfirmMessageShowsWhatMatters(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Name = "call_method"
	req.Params.Arguments = map[string]interface{}{"method": "frappe.desk.reportview.delete_items", "args": map[string]interface{}{
		"a": strings.Repeat("x", 2000), "doctype": "Sales Invoice", "items": `["SINV-1"]`, "api_key": "secret-value",
	}}
	sc, err := scopeOf(req)
	if err != nil {
		t.Fatal(err)
	}
	msg := confirmMessage("prod", req, sc)
	for _, want := range []string{`doctype="\"Sales Invoice\""`, `items="\"[\\\"SINV-1\\\"]\""`, `2 other arguments: "a", "api_key"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %s:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "xxxx") || strings.Contains(msg, "secret-value") {
		t.Errorf("message shows padding or a secret: %s", msg)
	}

	req.Params.Name = "delete_doc"
	req.Params.Arguments = map[string]interface{}{"doctype": "ToDo", "name": "INV-001\u200b"}
	if sc, err = scopeOf(req); err != nil {
		t.Fatal(err)
	}
	if msg := confirmMessage("prod", req, sc); !strings.Contains(msg, `"INV-001\u200b"`) {
		t.Errorf("zero-width space not shown: %s", msg)
	}
}

// The audit line records whether a call that needed confirmation got it.
func TestMCPConfirmAudit(t *testing.T) {
	del := map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	for _, tc := range []struct {
		name  string
		cfg   *config.MCPPolicy
		asker *mcpTAsker
		want  string
	}{
		{"confirmed", nil, &mcpTAsker{action: mcp.ElicitationResponseActionAccept, confirm: true}, "confirmed"},
		{"unsupported", nil, nil, "unsupported"},
		{"never", &config.MCPPolicy{Confirm: "never"}, nil, "never"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, audit := mcpTPolicy(t, tc.cfg, config.MCPPolicy{})
			if out, isErr := mcpTCall(t, mcpTClient(t, s, tc.asker, false), "delete_doc", del); isErr {
				t.Fatal(out)
			}
			raw, err := os.ReadFile(audit)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			var r auditRecord
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
				t.Fatal(err)
			}
			if r.Status != auditOK || r.Confirm != tc.want {
				t.Errorf("last line status %q confirm %q, want ok %q", r.Status, r.Confirm, tc.want)
			}
		})
	}
}
