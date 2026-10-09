package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// mcpTServerClient connects an in-process client that names itself name.
func mcpTServerClient(t *testing.T, s *MCPServer, name string) *mcpclient.Client {
	t.Helper()
	c, err := mcpclient.NewInProcessClient(s.MCPServer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ClientInfo = mcp.Implementation{Name: name, Version: "1"}
	if _, err := c.Initialize(t.Context(), init); err != nil {
		t.Fatal(err)
	}
	return c
}

func mcpTClientTools(t *testing.T, c *mcpclient.Client) map[string]bool {
	t.Helper()
	res, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tool := range res.Tools {
		out[tool.Name] = true
	}
	return out
}

// mcpTAuditLines reads the audit log next to cfg.
func mcpTAuditLines(t *testing.T, cfg string) []map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(cfg), auditFileName))
	if err != nil {
		t.Fatalf("no audit log next to the config: %v", err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("audit line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// Two servers in one process, from two configs and two sets of options, do
// not share a policy, tool sets, clients or audit log.
func TestNewMCPServerIndependent(t *testing.T) {
	siteA, siteB := cmdTSite(t), cmdTSite(t)
	cfgA, cfgB := fakeConfig(t, siteA, "apikey"), fakeConfig(t, siteB, "apikey")

	a, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfgA, Policy: config.MCPPolicy{ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewMCPServer(context.Background(), MCPOptions{
		ConfigPath: cfgB,
		Policy:     config.MCPPolicy{DenyDoctypes: []string{" Note "}},
		Toolsets:   []string{"core", "collab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Warnings) != 0 || len(b.Warnings) != 0 {
		t.Errorf("warnings: %q %q", a.Warnings, b.Warnings)
	}
	if len(a.Sites) != 1 || a.Sites[0] != "t" {
		t.Errorf("Sites = %q", a.Sites)
	}
	ca, cb := mcpTServerClient(t, a, "client-a"), mcpTServerClient(t, b, "client-b")

	toolsA, toolsB := mcpTClientTools(t, ca), mcpTClientTools(t, cb)
	if !toolsA["get_doc"] || toolsA["update_doc"] || toolsA["add_comment"] {
		t.Errorf("read-only server tools: get_doc=%v update_doc=%v add_comment=%v", toolsA["get_doc"], toolsA["update_doc"], toolsA["add_comment"])
	}
	if !toolsB["update_doc"] || !toolsB["add_comment"] || toolsB["submit_doc"] {
		t.Errorf("second server tools: update_doc=%v add_comment=%v submit_doc=%v", toolsB["update_doc"], toolsB["add_comment"], toolsB["submit_doc"])
	}

	upd := map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "data": map[string]interface{}{"description": "changed"}}
	req := mcp.CallToolRequest{}
	req.Params.Name, req.Params.Arguments = "update_doc", upd
	if res, err := ca.CallTool(t.Context(), req); err == nil && !res.IsError {
		t.Error("update_doc worked on the read-only server")
	}
	if out, isErr := mcpTCall(t, cb, "update_doc", upd); isErr {
		t.Fatalf("update_doc on the second server: %s", out)
	}
	if doc, _ := siteB.Doc("ToDo", "TD-1"); doc["description"] != "changed" {
		t.Errorf("second site's TD-1 = %v", doc)
	}
	if doc, _ := siteA.Doc("ToDo", "TD-1"); doc["description"] == "changed" {
		t.Error("the read-only server's site was written")
	}
	if out, isErr := mcpTCall(t, cb, "get_doc", map[string]interface{}{"doctype": "Note", "name": "x"}); !isErr || !strings.Contains(out, "Note") {
		t.Errorf("denied doctype: %v %q", isErr, out)
	}
	if _, isErr := mcpTCall(t, ca, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}); isErr {
		t.Error("get_doc failed on the read-only server")
	}

	// Each log is next to its own config and holds only its own calls.
	linesB := mcpTAuditLines(t, cfgB)
	for _, l := range linesB {
		if l["client"] != "client-b" {
			t.Errorf("second log has a line from %v", l["client"])
		}
	}
	for _, l := range mcpTAuditLines(t, cfgA) {
		if l["client"] != "client-a" {
			t.Errorf("first log has a line from %v", l["client"])
		}
	}

	a.Close()
	a.Close()
	b.Close()
	b.Close()
}

// The name an in-process client gives in initialize is the audit line's
// client.
func TestNewMCPServerAuditsClientName(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	s, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := mcpTServerClient(t, s, "ffc-desktop")
	mcpTCall(t, c, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"})
	lines := mcpTAuditLines(t, cfg)
	if len(lines) != 1 || lines[0]["client"] != "ffc-desktop" || lines[0]["tool"] != "get_doc" {
		t.Errorf("audit = %v", lines)
	}
}

func TestNewMCPServerValidation(t *testing.T) {
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	for _, tc := range []struct {
		name string
		o    MCPOptions
		want string
	}{
		{"unknown toolset", MCPOptions{Toolsets: []string{"core", "bogus"}}, `Toolsets: unknown tool set "bogus"`},
		{"empty toolsets", MCPOptions{Toolsets: []string{" "}}, "Toolsets needs at least one value"},
		{"confirm never", MCPOptions{Policy: config.MCPPolicy{Confirm: "never"}}, "Policy.Confirm can only tighten"},
		{"confirm unknown", MCPOptions{Policy: config.MCPPolicy{Confirm: "sometimes"}}, `Policy.Confirm must be always or if-supported, not "sometimes"`},
		{"empty list entry", MCPOptions{Policy: config.MCPPolicy{AllowDoctypes: []string{""}}}, "Policy.AllowDoctypes needs at least one value"},
		{"empty list", MCPOptions{Policy: config.MCPPolicy{DenyMethods: []string{}}}, "Policy.DenyMethods needs at least one value"},
		{"sites and all", MCPOptions{Sites: []string{"t"}, AllSites: true}, "use Sites or AllSites, not both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.o.ConfigPath = cfg
			s, err := NewMCPServer(context.Background(), tc.o)
			if err == nil {
				s.Close()
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
		})
	}
	// A list given nil is unset, not an error.
	s, err := NewMCPServer(context.Background(), MCPOptions{ConfigPath: cfg, Policy: config.MCPPolicy{Confirm: " always "}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestRunJQChildIfRequestedReturnsWithoutEnv(t *testing.T) {
	t.Setenv(jqChildEnv, "")
	RunJQChildIfRequested() // would exit the test binary if it ran the child
}
