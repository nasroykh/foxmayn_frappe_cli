package cmd

import (
	"context"
	"strings"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// mcpTServerCall calls a tool and returns its text and whether the call
// failed, as a protocol error or an error result.
func mcpTServerCall(t *testing.T, c *mcpclient.Client, name string, args map[string]interface{}) (string, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name, req.Params.Arguments = name, args
	res, err := c.CallTool(t.Context(), req)
	if err != nil {
		return err.Error(), true
	}
	var out strings.Builder
	for _, ct := range res.Content {
		if tc, ok := ct.(mcp.TextContent); ok {
			out.WriteString(tc.Text)
		}
	}
	return out.String(), res.IsError
}

func TestNewMCPServerToolsAndPolicy(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	ro, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg, Policy: config.MCPPolicy{ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ro.Close)
	c := mcpTServerClient(t, ro, "ro")
	upd := map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "data": map[string]interface{}{"description": "x"}}
	if out, isErr := mcpTServerCall(t, c, "update_doc", upd); !isErr || !strings.Contains(out, "update_doc") {
		t.Errorf("update_doc on the read-only server: %v %q", isErr, out)
	}
	// jq runs in a child process: TestMain answers it as the binary.
	out, isErr := mcpTServerCall(t, c, "list_docs", map[string]interface{}{"doctype": "ToDo", "fields": []string{"name"}, "jq": "length"})
	if isErr || strings.TrimSpace(out) != "3" {
		t.Errorf("list_docs with jq: %v %q", isErr, out)
	}

	// A tool that is registered but refused by the policy.
	dn, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg, Policy: config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dn.Close)
	if out, isErr := mcpTServerCall(t, mcpTServerClient(t, dn, "dn"), "update_doc", upd); !isErr || !strings.Contains(out, "ToDo") {
		t.Errorf("denied write: %v %q", isErr, out)
	}
	for _, r := range site.Requests() {
		if r.Method == "PUT" {
			t.Errorf("a refused write reached the site: %v", r)
		}
	}
	lines := mcpTAuditLines(t, cfg)
	if last := lines[len(lines)-1]; last["status"] != auditDenied {
		t.Errorf("last audit line = %v", last)
	}
}

// The options only narrow the config: they cannot unlock a sensitive
// DocType or a denied method, and a doctype list does not stand in for the
// config's allow_methods.
func TestNewMCPServerCannotWiden(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	const execute = "frappe.desk.doctype.system_console.system_console.execute_code"
	for _, tc := range []struct {
		name   string
		policy config.MCPPolicy
		tool   string
		args   map[string]interface{}
	}{
		{"sensitive doctype", config.MCPPolicy{AllowDoctypes: []string{"User"}}, "update_doc",
			map[string]interface{}{"doctype": "User", "name": "a@b.c", "data": map[string]interface{}{"enabled": 0}}},
		{"denied method", config.MCPPolicy{AllowMethods: []string{execute}}, "call_method",
			map[string]interface{}{"method": execute, "args": map[string]interface{}{}}},
		{"doctypes without allow_methods", config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, "call_method",
			map[string]interface{}{"method": "frappe.client.get_count", "args": map[string]interface{}{"doctype": "ToDo"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg, Policy: tc.policy})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			if out, isErr := mcpTServerCall(t, mcpTServerClient(t, s, "w"), tc.tool, tc.args); !isErr {
				t.Errorf("not refused: %q", out)
			}
		})
	}
	for _, r := range site.Requests() {
		if r.Method == "PUT" || strings.Contains(r.Path, "execute_code") || strings.Contains(r.Path, "get_count") {
			t.Errorf("a refused call reached the site: %v", r)
		}
	}
}

func TestNewMCPServerMultiSite(t *testing.T) {
	_, _, cfg := mcpTSites(t, nil, false, "")
	s, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg, AllSites: true, Site: "Prod"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if got := strings.Join(s.Sites, " "); got != "Prod dev" {
		t.Errorf("Sites = %q", got)
	}
	s.Sites[0] = "changed" // a copy: the server's own list is not touched
	c := mcpTServerClient(t, s, "multi")
	get := map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	if out, isErr := mcpTServerCall(t, c, "get_doc", get); !isErr || !strings.Contains(out, "site") {
		t.Errorf("get_doc without site: %v %q", isErr, out)
	}
	get["site"] = "dev"
	if out, isErr := mcpTServerCall(t, c, "get_doc", get); isErr {
		t.Errorf("get_doc on dev: %q", out)
	}

	// Errors name option fields, not flags.
	_, err = NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg, Sites: []string{"dev"}, Site: "Prod"})
	if err == nil || !strings.Contains(err.Error(), "Site Prod is not one of the served sites") || strings.Contains(err.Error(), "--") {
		t.Errorf("error = %v", err)
	}
	_, err = NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg, Sites: []string{"dev", "nope"}})
	if err == nil || !strings.HasPrefix(err.Error(), "Sites: ") {
		t.Errorf("error = %v", err)
	}
}

func TestNewMCPServerAfterClose(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "password")
	s, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg})
	if err != nil {
		t.Fatal(err)
	}
	c := mcpTServerClient(t, s, "late")
	get := map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	if out, isErr := mcpTServerCall(t, c, "get_doc", get); isErr {
		t.Fatal(out)
	}
	logins := site.Logins()
	s.Close()
	if out, isErr := mcpTServerCall(t, c, "get_doc", get); !isErr || !strings.Contains(out, "MCP server closed") {
		t.Errorf("after Close: %v %q", isErr, out)
	}
	if site.Logins() != logins {
		t.Errorf("a call after Close logged in again (%d -> %d)", logins, site.Logins())
	}
	(*MCPServer)(nil).Close()
}

// A Confirm with blanks around it reaches the policy trimmed.
func TestNewMCPServerTrimmedConfirm(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	s, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg, Policy: config.MCPPolicy{Confirm: " always "}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	out, isErr := mcpTServerCall(t, mcpTServerClient(t, s, "c"), "delete_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"})
	if !isErr || !strings.Contains(out, "cannot ask") {
		t.Errorf("delete_doc: %v %q", isErr, out)
	}
	if _, ok := site.Doc("ToDo", "TD-1"); !ok {
		t.Error("TD-1 was deleted")
	}
}
