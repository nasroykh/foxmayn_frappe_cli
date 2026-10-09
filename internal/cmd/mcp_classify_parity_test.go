package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// mcpTConfirmServer builds a server over its own fake site whose config sets
// the site's confirm mode ("" leaves it unset).
func mcpTConfirmServer(t *testing.T, siteMode string, o MCPOptions) (*MCPServer, string) {
	t.Helper()
	site := cmdTSite(t)
	cfg := fakeConfig(t, site, "apikey")
	if siteMode != "" {
		mcpTWriteConfig(t, cfg, fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: %q\n    api_secret: %q\n    mcp:\n      confirm: %s\n",
			site.URL, frappetest.APIKey, frappetest.APISecret, siteMode))
	}
	o.ConfigPath = cfg
	return mcpTClassifyServer(t, o), cfg
}

// The flag tightens the site's mode, "never" included.
func TestClassifyPolicyConfirmTightens(t *testing.T) {
	del := map[string]any{"doctype": "ToDo", "name": "TD-1"}
	for _, siteMode := range []string{"", config.ConfirmIfSupported, config.ConfirmNever} {
		s, _ := mcpTConfirmServer(t, siteMode, MCPOptions{Policy: config.MCPPolicy{Confirm: config.ConfirmAlways}})
		if cls := mcpTClassify(t, s, "delete_doc", del); cls.Mode != config.ConfirmAlways || !cls.WillAsk {
			t.Errorf("site confirm %q with Policy.Confirm always: %+v", siteMode, cls)
		}
	}
}

func TestClassifyNilServer(t *testing.T) {
	if _, err := (*MCPServer)(nil).Classify("get_doc", nil); err == nil {
		t.Error("nil server: no error")
	}
	if _, err := (&MCPServer{}).Classify("get_doc", nil); err == nil {
		t.Error("zero server: no error")
	}
}

// Classify says what the real call does: WillAsk is true exactly when a
// client that can ask is asked and one that cannot is refused, and arguments
// Classify refuses are refused by the call as invalid.
func TestClassifyMatchesCall(t *testing.T) {
	with := func(k string, v any) map[string]any {
		return map[string]any{"doctype": "ToDo", "name": "TD-1", "new_name": "TD-2", k: v}
	}
	rename := func(v any) map[string]any {
		return map[string]any{"method": "frappe.client.rename_doc", "args": map[string]any{"doctype": "ToDo", "old": "TD-1", "new": "TD-2", "merge": v}}
	}
	permission := func(extra map[string]any) map[string]any {
		a := map[string]any{"doctype": "ToDo", "name": "TD-1", "user": "a@example.com", "permission_to": "read"}
		for k, v := range extra {
			a[k] = v
		}
		return map[string]any{"method": "frappe.share.set_permission", "args": a}
	}
	cases := []struct {
		name    string
		tool    string
		args    map[string]any
		denyDT  string
		invalid bool
	}{
		{name: "delete_doc", tool: "delete_doc", args: map[string]any{"doctype": "ToDo", "name": "TD-1"}},
		{name: "delete_doc without name", tool: "delete_doc", args: map[string]any{"doctype": "ToDo"}, invalid: true},
		{name: "rename merge true", tool: "rename_doc", args: with("merge", true)},
		{name: "rename merge string", tool: "rename_doc", args: with("merge", "true")},
		{name: "rename merge int 0", tool: "rename_doc", args: with("merge", 0)},
		{name: "rename merge false", tool: "rename_doc", args: with("merge", false)},
		{name: "method rename merge int 0", tool: "call_method", args: rename(0)},
		{name: "method rename merge float 1", tool: "call_method", args: rename(1.0)},
		{name: "method set_permission value int 0", tool: "call_method", args: permission(map[string]any{"value": 0})},
		{name: "method set_permission value absent", tool: "call_method", args: permission(nil)},
		{name: "method args as JSON text", tool: "call_method", args: map[string]any{"method": "frappe.client.delete", "args": `{"doctype":"ToDo","name":"TD-1"}`}},
		{name: "create_doc", tool: "create_doc", args: map[string]any{"doctype": "ToDo", "data": map[string]any{"description": "x"}}},
		{name: "denied DocType", tool: "delete_doc", args: map[string]any{"doctype": "Note", "name": "N-1"}, denyDT: "Note"},
	}
	for _, mode := range []string{"", config.ConfirmIfSupported, config.ConfirmAlways, config.ConfirmNever} {
		for _, c := range cases {
			t.Run(c.name+"/"+mode, func(t *testing.T) {
				opts := MCPOptions{}
				if c.denyDT != "" {
					opts.Policy.DenyDoctypes = []string{c.denyDT}
				}
				// One server for the client that can ask, one for the other.
				asking, askCfg := mcpTConfirmServer(t, mode, opts)
				plain, plainCfg := mcpTConfirmServer(t, mode, opts)

				cls, err := asking.Classify(c.tool, c.args)
				if _, statErr := os.Stat(filepath.Join(filepath.Dir(askCfg), auditFileName)); statErr == nil {
					t.Error("Classify wrote an audit line")
				}
				a := &mcpTAsker{action: mcp.ElicitationResponseActionAccept, confirm: true}
				askOut, askErr := mcpTServerCall(t, mcpTClient(t, asking.MCPServer, a, false), c.tool, c.args)
				plainOut, plainErr := mcpTServerCall(t, mcpTClient(t, plain.MCPServer, nil, false), c.tool, c.args)

				if c.invalid {
					if err == nil || !askErr || !plainErr {
						t.Fatalf("invalid arguments: Classify error %v, call errors %v %v", err, askErr, plainErr)
					}
					if lines := mcpTAuditLines(t, askCfg); lines[0]["status"] != "invalid" {
						t.Errorf("audit = %v", lines)
					}
					return
				}
				if err != nil {
					t.Fatalf("Classify: %v", err)
				}
				pending := false
				for _, l := range mcpTAuditLines(t, askCfg) {
					pending = pending || l["status"] == "confirm_pending"
				}
				refused := plainErr && strings.Contains(plainOut, "cannot ask")
				wantMode := mode
				if wantMode == "" {
					wantMode = config.ConfirmIfSupported
				}
				if cls.Mode != wantMode {
					t.Errorf("Mode = %q, want %q", cls.Mode, wantMode)
				}
				if want := cls.Confirm && cls.Denied == "" && cls.Mode != config.ConfirmNever; pending != want {
					t.Errorf("confirm_pending = %v, want %v (%+v) %q", pending, want, cls, askOut)
				}
				if cls.WillAsk != (pending && refused) {
					t.Errorf("WillAsk = %v but pending = %v and refused = %v (%+v) %q", cls.WillAsk, pending, refused, cls, plainOut)
				}
				if cls.WillAsk {
					// Refused means it never ran.
					for _, l := range mcpTAuditLines(t, plainCfg) {
						if l["status"] == "ok" {
							t.Errorf("refused call ran: %v", l)
						}
					}
				}
				// (A share is denied too: DocShare is a sensitive DocType.)
				if c.denyDT != "" && cls.Denied == "" {
					t.Errorf("Denied = %q for deny %q", cls.Denied, c.denyDT)
				}
			})
		}
	}
}
