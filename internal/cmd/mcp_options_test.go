package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
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
	mcpFlags.Confirm, mcpFlags.DenyDoctypes, mcpFlags.AllowMethods = config.ConfirmAlways, []string{"ToDo"}, []string{"none.*"}
}

// A server built from options reads none of the flag globals: the config
// path, site, policy and tool sets all come from the options.
func TestMCPServerFromOptionsIgnoresGlobals(t *testing.T) {
	s := cmdTSite(t)
	s.Add("ToDo", map[string]interface{}{"name": "TD-1", "description": "a"})
	cfg := fakeConfig(t, s, "apikey")
	decoy := fakeConfig(t, s, "apikey")
	mcpTJunkGlobals(t, decoy)

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
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(cfg), auditFileName))
	if err != nil {
		t.Fatalf("the audit log is not next to the options' config: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !strings.Contains(line, `"site":"other"`) {
			t.Errorf("audit line not for the options' site: %s", line)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(decoy), auditFileName)); err == nil {
		t.Error("an audit file was written next to the config named by the globals")
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

// The refreshSite path: the token is already expired in the options' config
// when the site is loaded, so the refresh happens before the request.
func TestMCPOAuthExpiredAtLoadUsesOptionsConfig(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	decoy := fakeConfig(t, s, "oauth")
	for _, p := range []string{cfg, decoy} {
		if err := config.Edit(p, func(f *config.File) error {
			return f.SetSiteTokens("t", frappetest.Token, frappetest.RefreshToken, time.Now().Add(-time.Hour).Unix())
		}); err != nil {
			t.Fatal(err)
		}
	}
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

// A confirmation state issued by one server is no answer to another: the
// replayed call is asked or refused again, never executed. A server with no
// confirmer refuses too.
func TestMCPConfirmStateNotReplayableAcrossEnvs(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "apikey")
	o := mcpOptions{configPath: cfg, policy: config.MCPPolicy{Confirm: config.ConfirmAlways}}
	envA, closeA, err := newMCPEnv(o, []string{"t"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeA)
	envB, closeB, err := newMCPEnv(o, []string{"t"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeB)

	req := mcp.CallToolRequest{}
	req.Params.Name = "delete_doc"
	req.Params.Arguments = map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	req.Params.RequestState = envA.confirm.newState("t", req, time.Now().Add(time.Minute))
	req.Params.InputResponses = mcp.InputResponses{confirmID: mcp.NewElicitationInputResponse(mcp.ElicitationResult{
		ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept, Content: map[string]interface{}{"confirm": true}},
	})}
	site := &config.SiteConfig{Name: "t"}
	policy := newMCPPolicy(site, o.policy)
	sc, err := scopeOf(req)
	if err != nil {
		t.Fatal(err)
	}

	for name, cf := range map[string]*confirmer{"other env": envB.confirm, "no confirmer": nil} {
		var rec auditRecord
		res := policy.confirm(context.Background(), cf, req, sc, &rec)
		if res == nil || rec.Confirm == confirmYes {
			t.Errorf("%s: the call went ahead (confirm %q)", name, rec.Confirm)
		}
	}
	// The issuing env still accepts it once.
	var rec auditRecord
	if res := policy.confirm(context.Background(), envA.confirm, req, sc, &rec); res != nil || rec.Confirm != confirmYes {
		t.Errorf("own state refused: %v %q", res, rec.Confirm)
	}
}
