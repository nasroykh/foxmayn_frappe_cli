package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func mcpTClassifyServer(t *testing.T, o MCPOptions) *MCPServer {
	t.Helper()
	s, err := NewMCPServer(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func mcpTClassify(t *testing.T, s *MCPServer, tool string, args map[string]any) ToolClass {
	t.Helper()
	cls, err := s.Classify(tool, args)
	if err != nil {
		t.Fatalf("Classify(%s): %v", tool, err)
	}
	return cls
}

func TestClassifyConfirm(t *testing.T) {
	cfg := fakeConfig(t, cmdTSite(t), "apikey")
	del := map[string]any{"doctype": "ToDo", "name": "TD-1"}

	for _, mode := range []string{"", config.ConfirmIfSupported, config.ConfirmAlways} {
		s := mcpTClassifyServer(t, MCPOptions{ConfigPath: cfg, Policy: config.MCPPolicy{Confirm: mode}})
		cls := mcpTClassify(t, s, "delete_doc", del)
		want := mode
		if want == "" {
			want = config.ConfirmIfSupported
		}
		if cls.Action != "write" || !cls.Confirm || cls.WillAsk != (want == config.ConfirmAlways) || cls.Mode != want || cls.Site != "t" || cls.Denied != "" ||
			len(cls.Doctypes) != 1 || cls.Doctypes[0] != "ToDo" || len(cls.Names) != 1 || cls.Names[0] != "TD-1" {
			t.Errorf("delete_doc with confirm %q: %+v", mode, cls)
		}
	}

	// A site that turns confirmation off still marks the call, but no card is shown.
	site := cmdTSite(t)
	never := fakeConfig(t, site, "apikey")
	body := fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n    mcp:\n      confirm: never\n",
		site.URL, frappetest.APIKey, frappetest.APISecret)
	mcpTWriteConfig(t, never, body)
	s := mcpTClassifyServer(t, MCPOptions{ConfigPath: never})
	if cls := mcpTClassify(t, s, "delete_doc", del); !cls.Confirm || cls.WillAsk || cls.Mode != config.ConfirmNever {
		t.Errorf("confirm never: %+v", cls)
	}

	s = mcpTClassifyServer(t, MCPOptions{ConfigPath: cfg})
	for _, c := range []struct {
		tool    string
		args    map[string]any
		action  string
		confirm bool
	}{
		{"create_doc", map[string]any{"doctype": "ToDo", "data": map[string]any{"description": "x"}}, "write", false},
		{"get_doc", del, "read", false},
		{"rename_doc", map[string]any{"doctype": "ToDo", "name": "TD-1", "new_name": "TD-2", "merge": true}, "write", true},
		{"rename_doc", map[string]any{"doctype": "ToDo", "name": "TD-1", "new_name": "TD-2"}, "write", false},
		{"rename_doc", map[string]any{"doctype": "ToDo", "name": "TD-1", "new_name": "TD-2", "merge": false}, "write", false},
		{"call_method", map[string]any{"method": "frappe.client.delete", "args": map[string]any{"doctype": "ToDo", "name": "TD-1"}}, "method", true},
		{"call_method", map[string]any{"method": "frappe.client.get_list", "args": map[string]any{"doctype": "ToDo"}}, "method", false},
	} {
		cls := mcpTClassify(t, s, c.tool, c.args)
		if cls.Action != c.action || cls.Confirm != c.confirm || cls.WillAsk || cls.Denied != "" {
			t.Errorf("%s %v: %+v", c.tool, c.args, cls)
		}
		if c.tool == "call_method" && cls.Method != c.args["method"] {
			t.Errorf("call_method method = %q", cls.Method)
		}
	}
}

func TestClassifyDenied(t *testing.T) {
	cfg := fakeConfig(t, cmdTSite(t), "apikey")
	s := mcpTClassifyServer(t, MCPOptions{ConfigPath: cfg, Policy: config.MCPPolicy{DenyDoctypes: []string{"Note"}}})
	cls := mcpTClassify(t, s, "get_doc", map[string]any{"doctype": "Note", "name": "x"})
	if !strings.Contains(cls.Denied, "Note") {
		t.Errorf("denied doctype: %+v", cls)
	}
	if cls := mcpTClassify(t, s, "get_doc", map[string]any{"doctype": "ToDo", "name": "x"}); cls.Denied != "" {
		t.Errorf("allowed doctype: %+v", cls)
	}
}

func TestClassifyErrors(t *testing.T) {
	cfg := fakeConfig(t, cmdTSite(t), "apikey")
	ro := mcpTClassifyServer(t, MCPOptions{ConfigPath: cfg, Policy: config.MCPPolicy{ReadOnly: true}})
	if _, err := ro.Classify("delete_doc", map[string]any{"doctype": "ToDo", "name": "TD-1"}); err == nil {
		t.Error("delete_doc on a read-only server: no error")
	}
	if _, err := ro.Classify("get_doc", map[string]any{"doctype": "ToDo", "name": "TD-1"}); err != nil {
		t.Errorf("get_doc on a read-only server: %v", err)
	}
	if _, err := ro.Classify("no_such_tool", nil); err == nil {
		t.Error("unknown tool: no error")
	}
	// A tool of a set the server does not expose is not served.
	if _, err := ro.Classify("add_comment", map[string]any{"doctype": "ToDo", "name": "TD-1", "content": "x"}); err == nil {
		t.Error("tool outside the tool sets: no error")
	}
	// Invalid arguments are an error, as the call's own scope check refuses them.
	if _, err := ro.Classify("get_doc", map[string]any{"doctype": "", "name": "TD-1"}); err == nil {
		t.Error("empty doctype: no error")
	}
}

func TestClassifyMultiSite(t *testing.T) {
	_, _, cfg := mcpTSites(t, nil, false, "")
	s := mcpTClassifyServer(t, MCPOptions{ConfigPath: cfg, AllSites: true, Site: "dev"})
	args := map[string]any{"doctype": "ToDo", "name": "TD-1"}
	if _, err := s.Classify("delete_doc", args); err == nil || !strings.Contains(err.Error(), "site") {
		t.Errorf("no site argument: %v", err)
	}
	args["site"] = "bogus"
	if _, err := s.Classify("delete_doc", args); err == nil {
		t.Error("unknown site: no error")
	}
	args["site"] = "dev"
	if cls := mcpTClassify(t, s, "delete_doc", args); cls.Site != "dev" || cls.WillAsk || cls.Denied != "" {
		t.Errorf("dev: %+v", cls)
	}
	args["site"] = "prod" // case-folded, as a call is
	cls := mcpTClassify(t, s, "delete_doc", args)
	if cls.Site != "Prod" || cls.Denied == "" {
		t.Errorf("read-only Prod: %+v", cls)
	}
	if cls := mcpTClassify(t, s, "list_sites", nil); cls.Action != "read" || cls.Site != "" {
		t.Errorf("list_sites: %+v", cls)
	}
}

// A run id set with WithRunID is on every audit line of the call, the first
// line of a confirmation included; calls without one have no run_id key.
func TestAuditRunID(t *testing.T) {
	get := map[string]any{"doctype": "ToDo", "name": "TD-1"}
	call := func(t *testing.T, c interface {
		CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}, ctx context.Context, name string, args map[string]any) {
		t.Helper()
		req := mcp.CallToolRequest{}
		req.Params.Name, req.Params.Arguments = name, args
		res, err := c.CallTool(ctx, req)
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
	}

	t.Run("read", func(t *testing.T) {
		cfg := fakeConfig(t, cmdTSite(t), "apikey")
		s := mcpTClassifyServer(t, MCPOptions{ConfigPath: cfg})
		c := mcpTClient(t, s.MCPServer, nil, false)
		call(t, c, WithRunID(context.Background(), "run-1"), "get_doc", get)
		call(t, c, context.Background(), "get_doc", get)
		lines := mcpTAuditLines(t, cfg)
		if len(lines) != 2 || lines[0]["run_id"] != "run-1" || lines[0]["client"] != "confirm-test" {
			t.Errorf("audit = %v", lines)
		}
		if _, has := lines[1]["run_id"]; has {
			t.Errorf("a call without WithRunID has run_id: %v", lines[1])
		}
	})

	t.Run("clipped", func(t *testing.T) {
		cfg := fakeConfig(t, cmdTSite(t), "apikey")
		s := mcpTClassifyServer(t, MCPOptions{ConfigPath: cfg})
		c := mcpTClient(t, s.MCPServer, nil, false)
		call(t, c, WithRunID(context.Background(), "a\x1b[31m"+strings.Repeat("x", 300)), "get_doc", get)
		id, _ := mcpTAuditLines(t, cfg)[0]["run_id"].(string)
		if strings.ContainsRune(id, 0x1b) || len([]rune(id)) > 101 {
			t.Errorf("run_id = %q", id)
		}
	})

	for _, legacy := range []bool{false, true} {
		name := map[bool]string{false: "modern", true: "legacy"}[legacy]
		t.Run(name+"/confirmed delete", func(t *testing.T) {
			site := cmdTSite(t)
			cfg := fakeConfig(t, site, "apikey")
			s := mcpTClassifyServer(t, MCPOptions{ConfigPath: cfg})
			a := &mcpTAsker{action: mcp.ElicitationResponseActionAccept, confirm: true}
			c := mcpTClient(t, s.MCPServer, a, legacy)
			call(t, c, WithRunID(context.Background(), "run-2"), "delete_doc", get)
			if _, ok := site.Doc("ToDo", "TD-1"); ok {
				t.Error("TD-1 still exists")
			}
			lines := mcpTAuditLines(t, cfg)
			if len(lines) != 2 || lines[0]["status"] != "confirm_pending" || lines[1]["status"] != "ok" {
				t.Fatalf("audit = %v", lines)
			}
			for _, l := range lines {
				if l["run_id"] != "run-2" || l["client"] != "confirm-test" {
					t.Errorf("line %v: run_id/client lost", l)
				}
			}
		})
	}
}
