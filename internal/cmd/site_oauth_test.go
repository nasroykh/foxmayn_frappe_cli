package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// oauthConfig writes a config whose only site "t" holds an expired OAuth
// access token, with refreshToken ("" for none).
func oauthConfig(t *testing.T, site *frappetest.Site, refreshToken string) string {
	t.Helper()
	body := fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    oauth_client_id: cid\n    access_token: expired-token\n    token_expiry: 1\n", site.URL)
	if refreshToken != "" {
		body += fmt.Sprintf("    refresh_token: %q\n", refreshToken)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const tokenPath = "/api/method/frappe.integrations.oauth2.get_token"

func TestOAuthRefreshPersistsNewToken(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "a"})
	site.Handle("POST "+tokenPath, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"r2","expires_in":3600,"token_type":"Bearer"}`, frappetest.Token)
	}))
	cfg := oauthConfig(t, site, "r1")

	r := runFFC(t, cfg, "", "--json", "get-doc", "-d", "ToDo", "-n", "a")
	if r.Err != nil {
		t.Fatalf("get-doc: %v\nstderr: %s", r.Err, r.Stderr)
	}
	reqs := site.RequestsTo(http.MethodPost, tokenPath)
	if len(reqs) != 1 || !strings.Contains(reqs[0].Body, "grant_type=refresh_token") || !strings.Contains(reqs[0].Body, "refresh_token=r1") {
		t.Fatalf("token requests = %+v", reqs)
	}
	got := site.RequestsTo(http.MethodGet, "/api/resource/ToDo/a")
	if len(got) != 1 || got[0].Header.Get("Authorization") != "Bearer "+frappetest.Token {
		t.Errorf("document request did not use the refreshed token: %+v", got)
	}

	saved, err := config.Load("t", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != frappetest.Token || saved.RefreshToken != "r2" || saved.IsTokenExpired() {
		t.Errorf("saved tokens = %q / %q expired=%v", saved.AccessToken, saved.RefreshToken, saved.IsTokenExpired())
	}

	// A second run reuses the saved token instead of refreshing again.
	if r := runFFC(t, cfg, "", "--json", "get-doc", "-d", "ToDo", "-n", "a"); r.Err != nil {
		t.Fatal(r.Err)
	}
	if n := len(site.RequestsTo(http.MethodPost, tokenPath)); n != 1 {
		t.Errorf("token refreshed %d times, want 1", n)
	}
}

func TestOAuthRefreshFailureWarnsAndProceeds(t *testing.T) {
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "a"})
	site.Handle("POST "+tokenPath, frappetest.ErrorHandler(&frappetest.Error{Status: http.StatusUnauthorized, ExcType: "AuthenticationError", Message: "invalid_grant"}))
	cfg := oauthConfig(t, site, "r1")

	r := runFFC(t, cfg, "", "--json", "get-doc", "-d", "ToDo", "-n", "a")
	if r.Err == nil {
		t.Fatal("want an error: the expired token is rejected")
	}
	if !strings.Contains(r.Stderr, "refreshing the OAuth token") {
		t.Errorf("stderr = %q, want the refresh warning", r.Stderr)
	}
	if saved, _ := config.Load("t", cfg); saved.AccessToken != "expired-token" || saved.RefreshToken != "r1" {
		t.Errorf("config changed after a failed refresh: %+v", saved)
	}
}

func TestOAuthExpiredWithoutRefreshTokenWarns(t *testing.T) {
	site := frappetest.New(t)
	r := runFFC(t, oauthConfig(t, site, ""), "", "--json", "ping")
	if !strings.Contains(r.Stderr, "no refresh token") {
		t.Errorf("stderr = %q, want the no-refresh-token warning", r.Stderr)
	}
	if n := len(site.RequestsTo(http.MethodPost, tokenPath)); n != 0 {
		t.Errorf("token endpoint called %d times", n)
	}
}

func TestSiteUseAndRemove(t *testing.T) {
	site := frappetest.New(t)
	cfg := fakeConfig(t, site, "apikey")

	if r := runFFC(t, cfg, "", "site", "use", "other"); r.Err != nil {
		t.Fatalf("site use: %v", r.Err)
	}
	c, err := config.Read(cfg)
	if err != nil || c.DefaultSite != "other" {
		t.Fatalf("default_site = %q, %v; want other", c.DefaultSite, err)
	}
	if r := runFFC(t, cfg, "", "site", "use", "nope"); r.Err == nil {
		t.Error("site use with an unknown site: want error")
	}

	// remove asks for confirmation; without a terminal it must not delete.
	r := runFFC(t, cfg, "", "site", "remove", "t")
	if r.Err == nil {
		t.Error("site remove without a terminal: want error")
	}
	if c, _ := config.Read(cfg); len(c.Sites) != 2 {
		t.Errorf("sites = %d after a refused remove, want 2", len(c.Sites))
	}
}
