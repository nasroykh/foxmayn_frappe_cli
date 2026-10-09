package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// mcpTJunkGlobals points every flag global `ffc mcp` reads at values that
// cannot work, so a server built from options that still works never read
// them. decoy is the config file the globals name.
func mcpTJunkGlobals(t *testing.T, decoy string) {
	t.Helper()
	prevCfg, prevSite, prevList, prevAll := configPath, siteName, mcpSiteList, mcpAllSites
	prevFlags, prevRO, prevSets := mcpFlags, mcpReadOnly, mcpToolsets
	t.Cleanup(func() {
		configPath, siteName, mcpSiteList, mcpAllSites = prevCfg, prevSite, prevList, prevAll
		mcpFlags, mcpReadOnly, mcpToolsets = prevFlags, prevRO, prevSets
	})
	configPath, siteName, mcpSiteList, mcpAllSites = decoy, "no-such-site", []string{"x", "y"}, true
	mcpFlags.AllowTools, mcpReadOnly, mcpToolsets = []string{"ping"}, true, []string{"admin"}
}

// A server built from options reads none of the flag globals: the config
// path, site, policy and tool sets all come from the options.
func TestMCPServerFromOptionsIgnoresGlobals(t *testing.T) {
	s := cmdTSite(t)
	s.Add("ToDo", map[string]interface{}{"name": "TD-1", "description": "a"})
	cfg := fakeConfig(t, s, "apikey")
	mcpTJunkGlobals(t, filepath.Join(t.TempDir(), "missing.yaml"))

	srv, warnings, closeEnv, err := startMCP(context.Background(), mcpOptions{configPath: cfg, site: "other"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeEnv)
	if len(warnings) != 0 {
		t.Errorf("warnings = %q", warnings)
	}
	tools := srv.ListTools()
	if _, ok := tools["get_doc"]; !ok {
		t.Error("get_doc missing: the options' tool sets were not used")
	}
	if _, ok := tools["update_doc"]; !ok {
		t.Error("update_doc missing: the global --read-only leaked into the server")
	}
	mcpTOK(t, srv, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"})
	mcpTOK(t, srv, "update_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "data": map[string]interface{}{"description": "b"}})
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfg), auditFileName)); err != nil {
		t.Errorf("the audit log is not next to the options' config: %v", err)
	}
}

// An OAuth refresh in the middle of an MCP call writes the new tokens to
// the options' config file, never to the file the globals name.
func TestMCPOAuthRefreshUsesOptionsConfig(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	decoy := fakeConfig(t, s, "oauth")
	mcpTJunkGlobals(t, decoy)
	decoyBefore, err := os.ReadFile(decoy)
	if err != nil {
		t.Fatal(err)
	}

	srv, _, closeEnv, err := startMCP(context.Background(), mcpOptions{configPath: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeEnv)
	s.ExpireToken(frappetest.Token)
	mcpTOK(t, srv, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"})
	if s.Refreshes() != 1 {
		t.Fatalf("refreshes = %d, want 1", s.Refreshes())
	}
	if got := oauthTSite(t, cfg).AccessToken; got != frappetest.Token+"-1" {
		t.Errorf("options config token = %q, want the refreshed one", got)
	}
	decoyAfter, err := os.ReadFile(decoy)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoyAfter) != string(decoyBefore) {
		t.Error("the config named by the globals was written")
	}
}
