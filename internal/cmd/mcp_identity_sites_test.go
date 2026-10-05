package cmd

import (
	"fmt"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// whoami and check_permission serve several sites like every other tool: they
// take a required site argument, run against that site with its credentials,
// its cache entry and its policy.
func TestMCPIdentityToolsMultiSite(t *testing.T) {
	cacheTEnv(t)
	prod, dev, cfgPath := mcpTSites(t, []string{"prod", "dev"}, false, "")
	prod.SetUser("auditor@prod.example", "Auditor")
	prod.SetApps(map[string]frappetest.App{"frappe": {Version: "15.2.0"}})
	dev.SetUser("dev@dev.example", "Developer")
	prod.Deny("ToDo", "write")

	writeCfg := func(prodMCP string) {
		mcpTWriteConfig(t, cfgPath, fmt.Sprintf("default_site: dev\nsites:\n  Prod:\n    url: %q\n    access_token: %q\n    mcp:\n      read_only: true\n%s  dev:\n    url: %q\n    api_key: %q\n    api_secret: %q\n",
			prod.URL, frappetest.Token, prodMCP, dev.URL, frappetest.APIKey, frappetest.APISecret))
	}
	// Prod hides the User DocType from MCP: whoami reads it, so it is refused
	// there and only there.
	writeCfg("      deny_doctypes: [User]\n")
	s := mcpTStart(t)

	tools := s.ListTools()
	for _, name := range []string{"whoami", "check_permission"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if p, _ := tool.Tool.InputSchema.Properties["site"].(map[string]any); p == nil || !contains(tool.Tool.InputSchema.Required, "site", false) {
			t.Errorf("%s schema has no required site: %+v", name, tool.Tool.InputSchema)
		}
	}
	mcpTErr(t, s, "whoami", nil, "site is required")
	mcpTErr(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}, "site is required")

	before := len(prod.Requests())
	mcpTErr(t, s, "whoami", map[string]interface{}{"site": "Prod"}, `DocType "User" is denied by sites.Prod.mcp.deny_doctypes`)
	if len(prod.Requests()) != before {
		t.Error("the refused whoami reached Prod")
	}
	out := mcpTObj(t, mcpTOK(t, s, "whoami", map[string]interface{}{"site": "dev"}))
	if out["user"] != "dev@dev.example" || out["site"] != "dev" || out["server"].(map[string]interface{})["frappe"] != "16.36.1" {
		t.Errorf("dev whoami = %v", out)
	}

	// Lifting the policy takes effect on the next call (it is read per call);
	// the answer is Prod's own, from its own credentials and cache entry.
	writeCfg("")
	out = mcpTObj(t, mcpTOK(t, s, "whoami", map[string]interface{}{"site": "Prod"}))
	if out["user"] != "auditor@prod.example" || out["site"] != "Prod" || out["auth"] != "oauth" || out["server"].(map[string]interface{})["frappe"] != "15.2.0" {
		t.Errorf("Prod whoami = %v", out)
	}
	if out["url"] != prod.URL {
		t.Errorf("url = %v", out["url"])
	}

	// check_permission asks the site it was given.
	pOut := mcpTObj(t, mcpTOK(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "perm_type": "write", "site": "Prod"}))
	dOut := mcpTObj(t, mcpTOK(t, s, "check_permission", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "perm_type": "write", "site": "dev"}))
	if pOut["allowed"] != false || dOut["allowed"] != true {
		t.Errorf("Prod %v, dev %v: each site answers for itself", pOut, dOut)
	}
	// Each site only ever got its own credentials.
	mcpTAuth(t, prod, "Bearer ")
	mcpTAuth(t, dev, "token ")
}
