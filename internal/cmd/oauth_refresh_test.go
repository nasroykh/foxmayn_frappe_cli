package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// oauthTSite reads site "t" back from the config at path.
func oauthTSite(t *testing.T, path string) *config.SiteConfig {
	t.Helper()
	cfg, err := config.Load("t", path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// oauthTOtherProcess refreshes site "t" the way another ffc process would:
// it spends the stored refresh token and writes the new tokens to path.
func oauthTOtherProcess(t *testing.T, s *frappetest.Site, path string) string {
	t.Helper()
	cur := oauthTSite(t, path)
	tok, err := client.RefreshOAuthToken(context.Background(), s.URL, frappetest.OAuthClientID, "", cur.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Edit(path, func(f *config.File) error {
		return f.SetSiteTokens("t", tok.AccessToken, tok.RefreshToken, tok.ExpiresAt)
	}); err != nil {
		t.Fatal(err)
	}
	return tok.AccessToken
}

// oauthTBearers returns the access tokens sent to method path, in order.
func oauthTBearers(s *frappetest.Site, method, path string) []string {
	var out []string
	for _, r := range s.RequestsTo(method, path) {
		out = append(out, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	return out
}

// A bulk run with 10 workers whose token expires halfway refreshes once,
// every item succeeds, and the rotated tokens are saved.
func TestCmdOAuthRefreshBulk(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	s.ExpireTokenAfter(frappetest.Token, 5)

	var items []string
	for i := 0; i < 30; i++ {
		items = append(items, fmt.Sprintf(`{"description":"b%d"}`, i))
	}
	before := s.Count("ToDo")
	r := cmdTOK(t, cmdTExec(t, cfg, "", "--json", "bulk-create", "-d", "ToDo", "--concurrency", "10",
		"--data", "["+strings.Join(items, ",")+"]"))
	if got := s.Count("ToDo") - before; got != 30 {
		t.Fatalf("created %d, want 30\n%s", got, r.Stdout)
	}
	if n := s.Refreshes(); n != 1 {
		t.Errorf("refreshes = %d, want 1", n)
	}
	site := oauthTSite(t, cfg)
	if site.AccessToken != frappetest.Token+"-1" || site.RefreshToken != frappetest.RefreshToken+"-1" ||
		site.TokenExpiry < time.Now().Unix()+3000 {
		t.Errorf("saved tokens: access %q refresh %q expiry %d", site.AccessToken, site.RefreshToken, site.TokenExpiry)
	}
	// The other site and the file's other keys are untouched.
	if other, err := config.Load("other", cfg); err != nil || other.APIKey != frappetest.APIKey {
		t.Errorf("other: %+v %v", other, err)
	}
	raw, _ := os.ReadFile(cfg)
	if !strings.Contains(string(raw), "number_format: us") {
		t.Errorf("config lost a key:\n%s", raw)
	}
}

// list-docs --all keeps paging across a refresh.
func TestCmdOAuthRefreshListAll(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	s.ExpireTokenAfter(frappetest.Token, 1)
	r := cmdTOK(t, cmdTExec(t, cfg, "", "--json", "list-docs", "-d", "ToDo", "--all", "--page-size", "1"))
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &rows); err != nil || len(rows) != 3 {
		t.Fatalf("rows %v (%v)", rows, err)
	}
	if s.Refreshes() != 1 {
		t.Errorf("refreshes = %d, want 1", s.Refreshes())
	}
}

// A refresh that fails leaves the 401, with a hint, and the config as it was.
func TestCmdOAuthRefreshFails(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	s.ExpireToken(frappetest.Token)
	s.FailRefresh(true)
	r := cmdTExec(t, cfg, "", "--json", "get-doc", "-d", "ToDo", "-n", "TD-1")
	if r.Code != exitAuth {
		t.Fatalf("exit %d, want %d: %v", r.Code, exitAuth, r.Err)
	}
	cmdTFail(t, r, "access token was rejected and refreshing it failed", "invalid_grant", "ffc site add --oauth")
	if got := oauthTSite(t, cfg); got.AccessToken != frappetest.Token || got.RefreshToken != frappetest.RefreshToken {
		t.Errorf("config changed: %+v", got)
	}
	// The original request was sent once and not repeated.
	if n := len(s.RequestsTo("GET", "/api/resource/ToDo/TD-1")); n != 1 {
		t.Errorf("get-doc sent %d times", n)
	}
}

// A site without a refresh token reports that, still as a 401.
func TestCmdOAuthNoRefreshToken(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	raw, _ := os.ReadFile(cfg)
	stripped := strings.Replace(string(raw), "    refresh_token: \""+frappetest.RefreshToken+"\"\n", "", 1)
	if stripped == string(raw) {
		t.Fatal("no refresh_token line in the config")
	}
	if err := os.WriteFile(cfg, []byte(stripped), 0o600); err != nil {
		t.Fatal(err)
	}
	s.ExpireToken(frappetest.Token)
	r := cmdTExec(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "TD-1")
	if r.Code != exitAuth {
		t.Fatalf("exit %d: %v", r.Code, r.Err)
	}
	cmdTFail(t, r, "no refresh token is stored")
	if s.Refreshes() != 0 {
		t.Error("refreshed without a refresh token")
	}
}

// When another process refreshed first, the rejected client takes the token
// it saved instead of spending the refresh token again.
func TestOAuthRefreshTakesOtherProcessToken(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	prevCfg, prevSite := configPath, siteName
	t.Cleanup(func() { configPath, siteName = prevCfg, prevSite })
	configPath, siteName = cfg, ""

	c, _, err := newClientCfg(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other := oauthTOtherProcess(t, s, cfg)
	s.ExpireToken(frappetest.Token)
	if _, err := c.GetDoc(context.Background(), "ToDo", "TD-1"); err != nil {
		t.Fatal(err)
	}
	if n := s.Refreshes(); n != 1 {
		t.Errorf("refreshes = %d, want 1 (the other process's)", n)
	}
	if got := oauthTBearers(s, "GET", "/api/resource/ToDo/TD-1"); len(got) != 2 || got[0] != frappetest.Token || got[1] != other {
		t.Errorf("bearers = %v", got)
	}
}

// The MCP server: a call whose token is rejected refreshes and succeeds; a
// later call uses the saved token; a refresh by another process is picked
// up without a refresh of its own.
func TestMCPOAuthRefresh(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	prevCfg, prevSite, prevList, prevAll := configPath, siteName, mcpSiteList, mcpAllSites
	t.Cleanup(func() { configPath, siteName, mcpSiteList, mcpAllSites = prevCfg, prevSite, prevList, prevAll })
	configPath, siteName, mcpSiteList, mcpAllSites = cfg, "", nil, false
	srv := mcpTStart(t)

	args := map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}
	mcpTOK(t, srv, "get_doc", args)

	// Expiry in the middle of one call: bulk_create crosses it.
	s.ExpireTokenAfter(frappetest.Token, 2)
	mcpTOK(t, srv, "bulk_create", map[string]interface{}{"doctype": "ToDo", "data": []interface{}{
		map[string]interface{}{"description": "a"}, map[string]interface{}{"description": "b"},
		map[string]interface{}{"description": "c"}, map[string]interface{}{"description": "d"}}})
	if s.Refreshes() != 1 || s.Count("ToDo") != 7 {
		t.Fatalf("refreshes %d, ToDo count %d; want 1, 7", s.Refreshes(), s.Count("ToDo"))
	}
	if got := oauthTSite(t, cfg).AccessToken; got != frappetest.Token+"-1" {
		t.Fatalf("saved token %q", got)
	}
	mcpTOK(t, srv, "get_doc", args)

	// Another process refreshes and the MCP server's token expires.
	other := oauthTOtherProcess(t, s, cfg)
	s.ExpireToken(frappetest.Token + "-1")
	mcpTOK(t, srv, "get_doc", args)
	if s.Refreshes() != 2 {
		t.Errorf("refreshes = %d, want 2 (no refresh of its own)", s.Refreshes())
	}
	got := oauthTBearers(s, "GET", "/api/resource/ToDo/TD-1")
	if last := got[len(got)-1]; last != other {
		t.Errorf("last get_doc used %q, want %q (all: %v)", last, other, got)
	}
}

// --debug=body around a refresh shows neither the old, the new nor the
// refresh tokens.
func TestDebugOAuthRefreshHidesTokens(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	s.ExpireToken(frappetest.Token)
	r := cmdTOK(t, debugTRun(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "TD-1", "--debug=body"))
	if s.Refreshes() != 1 {
		t.Fatalf("refreshes = %d", s.Refreshes())
	}
	for _, secret := range []string{frappetest.Token, frappetest.RefreshToken} {
		if strings.Contains(r.Stderr, secret) {
			t.Errorf("trace contains %q:\n%s", secret, r.Stderr)
		}
	}
	for _, want := range []string{"/api/method/frappe.integrations.oauth2.get_token", "< 401", "< 200"} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("trace lacks %q:\n%s", want, r.Stderr)
		}
	}
}
