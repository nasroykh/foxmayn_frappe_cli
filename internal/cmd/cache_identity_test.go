package cmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// TestCacheIsPerCredential: what one login cached is never served to
// another on the same site name and URL (FFC_API_KEY on a named site, two
// env-only key pairs, a site re-added with other credentials), and the
// path holds no secret.
func TestCacheIsPerCredential(t *testing.T) {
	cacheTEnv(t)
	url := "http://erp.example"
	cfgs := map[string]*config.SiteConfig{
		"key A":       {Name: "prod", URL: url, APIKey: "keyA", APISecret: "secretA"},
		"key B":       {Name: "prod", URL: url, APIKey: "keyB", APISecret: "secretA"},
		"user":        {Name: "prod", URL: url, Username: "a@x", Password: "pw-secret"},
		"other user":  {Name: "prod", URL: url, Username: "b@x", Password: "pw-secret"},
		"oauth":       {Name: "prod", URL: url, OAuthClientID: "cid", AccessToken: "tok-secret"},
		"env key A":   {URL: url, APIKey: "keyA", APISecret: "secretA"},
		"env key B":   {URL: url, APIKey: "keyB", APISecret: "secretB"},
		"no password": {Name: "prod", URL: url, Username: "a@x"},
	}
	seen := map[string]string{}
	for label, cfg := range cfgs {
		dir, err := serverCacheDir(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"secret", "tok-secret", "keyA", "keyB", "a@x"} {
			if strings.Contains(dir, secret) {
				t.Errorf("%s: cache path %s holds %q", label, dir, secret)
			}
		}
		if other, dup := seen[dir]; dup {
			t.Errorf("%s and %s share %s", label, other, dir)
		}
		seen[dir] = label
		if err := writeListCache(cfg, "DocType", []map[string]interface{}{{"name": label}}); err != nil {
			t.Fatal(err)
		}
	}
	for label, cfg := range cfgs {
		if got := readListCache(cfg, "DocType", time.Now()); len(got) != 1 || got[0].Name != label {
			t.Errorf("%s reads %+v", label, got)
		}
	}
	// A refreshed OAuth token is the same identity.
	refreshed := *cfgs["oauth"]
	refreshed.AccessToken, refreshed.RefreshToken = "new-token", "new-refresh"
	if a, b := credentialID(cfgs["oauth"]), credentialID(&refreshed); a != b {
		t.Errorf("token refresh changed the cache identity: %s → %s", a, b)
	}
}

// TestCacheCommandsEnvOnlySite: a site defined by FFC_* variables alone is
// named by its URL (password hidden), not by an empty name.
func TestCacheCommandsEnvOnlySite(t *testing.T) {
	cacheTEnv(t)
	home := t.TempDir() // no config file at the default path
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	s := complTSite(t)
	url := strings.Replace(s.URL, "http://", "http://u:hunter2@", 1)
	cliEnv = map[string]string{"FFC_URL": url, "FFC_API_KEY": frappetest.APIKey, "FFC_API_SECRET": frappetest.APISecret}
	t.Cleanup(func() { cliEnv = nil })

	r := cmdTOK(t, runFFC(t, "", "", "cache", "warm"))
	if !strings.Contains(r.Stdout, "of http://u:xxxxx@") || strings.Contains(r.Stdout+r.Stderr, "hunter2") {
		t.Errorf("warm: %q", r.Stdout)
	}
	r = cmdTOK(t, runFFC(t, "", "", "cache", "status"))
	if !strings.Contains(r.Stdout, "Cache of http://u:xxxxx@") || !strings.Contains(r.Stdout, "doctypes") {
		t.Errorf("status: %q", r.Stdout)
	}
	r = cmdTOK(t, runFFC(t, "", "", "--json", "cache", "status"))
	if !strings.Contains(r.Stdout, `"url": "http://u:xxxxx@`) || strings.Contains(r.Stdout, "hunter2") {
		t.Errorf("status --json: %s", r.Stdout)
	}
	r = cmdTOK(t, runFFC(t, "", "", "cache", "clear"))
	if !strings.Contains(r.Stdout, "of http://u:xxxxx@") || !strings.Contains(r.Stdout, "Removed 2 ") {
		t.Errorf("clear: %q", r.Stdout)
	}
}

// TestSiteCommandsDropTheCache: site remove, rename, edit and add (over an
// existing name) delete the site's cache, for every login; other sites keep
// theirs.
func TestSiteCommandsDropTheCache(t *testing.T) {
	cacheTEnv(t)
	s := complTSite(t)
	cfgPath := fakeConfig(t, s, "apikey")
	fill := func(name string) string {
		t.Helper()
		for _, cfg := range []*config.SiteConfig{
			{Name: name, URL: s.URL, APIKey: "k1", APISecret: "s"},
			{Name: name, URL: s.URL, Username: "u", Password: "p"},
		} {
			if err := writeListCache(cfg, "DocType", []map[string]interface{}{{"name": "ToDo"}}); err != nil {
				t.Fatal(err)
			}
		}
		root, _ := siteCacheRoot(&config.SiteConfig{Name: name})
		return root
	}
	gone := func(what, root string) {
		t.Helper()
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Errorf("%s left %s (%v)", what, root, err)
		}
	}

	otherRoot := fill("other")
	root := fill("t")
	cmdTOK(t, runFFC(t, cfgPath, "", "site", "edit", "t", "--url", s.URL+"/"))
	gone("site edit", root)

	root, stale := fill("t"), fill("renamed")
	cmdTOK(t, runFFC(t, cfgPath, "", "site", "rename", "t", "renamed"))
	gone("site rename (old name)", root)
	gone("site rename (an earlier site's cache under the new name)", stale)

	root = fill("renamed")
	cmdTOK(t, runFFC(t, cfgPath, "", "site", "remove", "renamed", "--yes"))
	gone("site remove", root)

	root = fill("again")
	if err := siteStore(cfgPath).Add("again", config.SiteConfig{URL: s.URL, APIKey: "k2", APISecret: "s2"}); err != nil {
		t.Fatal(err)
	}
	gone("site add", root)

	if _, err := os.Stat(otherRoot); err != nil {
		t.Errorf("another site's cache was removed: %v", err)
	}
}

// TestCLICacheIsPerCredential: the same site name and URL configured with
// another login (removed and re-added, another config file) fetches its own
// schema and completes nothing from the other login's lists.
func TestCLICacheIsPerCredential(t *testing.T) {
	cacheTEnv(t)
	s := complTSite(t)
	keyCfg := fakeConfig(t, s, "apikey")
	userCfg := fakeConfig(t, s, "password")

	cmdTOK(t, runFFC(t, keyCfg, "", "get-schema", "-d", "Ticket"))
	cmdTOK(t, runFFC(t, keyCfg, "", "list-doctypes", "--limit", "0"))
	if vals, _ := complT(t, keyCfg, "list-docs", "-d", "Ti"); len(vals) != 1 {
		t.Fatalf("the API-key login completes %q", vals)
	}

	if vals, _ := complT(t, userCfg, "list-docs", "-d", "Ti"); len(vals) != 0 {
		t.Errorf("the password login sees the API-key login's list: %q", vals)
	}
	n := len(s.RequestsTo("GET", "/api/resource/DocType/Ticket"))
	cmdTOK(t, runFFC(t, userCfg, "", "get-schema", "-d", "Ticket"))
	if len(s.RequestsTo("GET", "/api/resource/DocType/Ticket")) != n+1 {
		t.Error("the password login was served the API-key login's schema")
	}
}
