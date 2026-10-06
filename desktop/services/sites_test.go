package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitecache"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitesetup"
)

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestAddWithAPIKeyCreatesConfigThenAdds(t *testing.T) {
	ctx := context.Background()
	fake := frappetest.New(t)
	s, _, path := newSites(t)

	got, err := s.AddWithAPIKey(ctx, APIKeyRequest{Name: " Main ", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Main" || got.URL != fake.URL || got.Auth != AuthAPIKey || !got.IsDefault || got.User == "" || got.Replaced {
		t.Errorf("first add = %+v", got)
	}
	// No file before: Init wrote it, 0600 on Unix.
	if fi, err := os.Stat(path); err != nil || (os.PathSeparator == '/' && fi.Mode().Perm() != 0o600) {
		t.Fatalf("config file: %v %v", fi, err)
	}

	second, err := s.AddWithPassword(ctx, PasswordRequest{Name: "dev", URL: fake.URL, Username: frappetest.Username, Password: frappetest.Password})
	if err != nil {
		t.Fatal(err)
	}
	if second.IsDefault || second.User != frappetest.Username {
		t.Errorf("second add = %+v", second)
	}
	if fake.Logins() != 1 || fake.Logouts() != 1 {
		t.Errorf("password check: %d logins, %d logouts", fake.Logins(), fake.Logouts())
	}

	cfg, err := config.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultSite != "Main" || len(cfg.Sites) != 2 || cfg.Sites["Main"].APISecret != frappetest.APISecret || cfg.Sites["dev"].Password != frappetest.Password {
		t.Errorf("config = %+v", cfg)
	}

	// The same name again is refused unless replacing.
	_, err = s.AddWithAPIKey(ctx, APIKeyRequest{Name: "dev", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if code(err) != CodeExists {
		t.Fatalf("existing name: %v", err)
	}
	rep, err := s.AddWithAPIKey(ctx, APIKeyRequest{Name: "dev", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret, Replace: true})
	if err != nil || !rep.Replaced {
		t.Fatalf("replace: %+v %v", rep, err)
	}
}

func TestAddRefusesBadInputAndCredentials(t *testing.T) {
	ctx := context.Background()
	fake := frappetest.New(t)
	s, _, path := newSites(t)

	cases := []struct {
		req   APIKeyRequest
		code  string
		field string
	}{
		{APIKeyRequest{Name: "", URL: fake.URL, APIKey: "k", APISecret: "s"}, CodeInvalid, "name"},
		{APIKeyRequest{Name: "a", URL: "", APIKey: "k", APISecret: "s"}, CodeInvalid, "url"},
		{APIKeyRequest{Name: "a", URL: fake.URL, APIKey: "", APISecret: "s"}, CodeInvalid, "apiKey"},
		{APIKeyRequest{Name: "a", URL: fake.URL, APIKey: "k", APISecret: " "}, CodeInvalid, "apiSecret"},
		{APIKeyRequest{Name: "a", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: "wrong"}, CodeAuth, ""},
		{APIKeyRequest{Name: "a", URL: "http://127.0.0.1:1", APIKey: "k", APISecret: "s"}, CodeNetwork, ""},
	}
	for _, c := range cases {
		_, err := s.AddWithAPIKey(ctx, c.req)
		var e *Error
		if !errors.As(err, &e) || e.Code != c.code || e.Field != c.field {
			t.Errorf("%+v: err = %#v", c.req, err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused add wrote the config: %v", err)
	}
}

func TestValidate(t *testing.T) {
	s, _, _ := newSites(t)
	v := s.Validate("Prod", "erp.example.com/app/home")
	if !v.OK || v.URL != "https://erp.example.com" || v.PlainHTTP || v.Exists {
		t.Errorf("valid = %+v", v)
	}
	v = s.Validate("", "http://localhost:8000")
	if v.OK || v.NameError == "" || v.URL != "http://localhost:8000" || !v.PlainHTTP {
		t.Errorf("no name = %+v", v)
	}
	if v := s.Validate("x", ""); v.OK || v.URLError == "" {
		t.Errorf("no url = %+v", v)
	}
}

func TestListNeverCarriesSecrets(t *testing.T) {
	ctx := context.Background()
	fake := frappetest.New(t)
	s, _, path := newSites(t)

	if l, err := s.List(); err != nil || l.ConfigExists || len(l.Sites) != 0 {
		t.Fatalf("empty list = %+v, %v", l, err)
	}
	if err := (sitesetup.Store{Path: path}).Init("oauth", config.SiteConfig{URL: fake.URL, OAuthClientID: frappetest.OAuthClientID,
		AccessToken: frappetest.Token, RefreshToken: frappetest.RefreshToken, TokenExpiry: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWithAPIKey(ctx, APIKeyRequest{Name: "key", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWithPassword(ctx, PasswordRequest{Name: "pw", URL: fake.URL, Username: frappetest.Username, Password: frappetest.Password}); err != nil {
		t.Fatal(err)
	}

	l, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	auths := map[string]string{}
	for _, site := range l.Sites {
		auths[site.Name] = site.Auth
	}
	if auths["oauth"] != AuthOAuth || auths["key"] != AuthAPIKey || auths["pw"] != AuthPassword || l.DefaultSite != "oauth" || !l.Sites[1].IsDefault {
		t.Errorf("list = %+v", l)
	}
	if !l.Sites[0].PlainHTTP || l.Sites[0].LastCheck == nil || !l.Sites[0].LastCheck.OK {
		t.Errorf("key site = %+v", l.Sites[0])
	}
	js, _ := json.Marshal(l)
	for _, secret := range []string{frappetest.APISecret, frappetest.Password, frappetest.Token, frappetest.RefreshToken} {
		if strings.Contains(string(js), secret) {
			t.Errorf("List JSON carries a secret %q: %s", secret, js)
		}
	}
}

func TestSetDefaultRenameChangeURL(t *testing.T) {
	ctx := context.Background()
	fake := frappetest.New(t)
	other := frappetest.New(t)
	s, _, path := newSites(t)
	for _, n := range []string{"a", "b"} {
		if _, err := s.AddWithAPIKey(ctx, APIKeyRequest{Name: n, URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetDefault("b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("b", "c"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("a", "c"); code(err) != CodeExists {
		t.Errorf("rename onto an existing name: %v", err)
	}
	if err := s.Rename("a", " "); code(err) != CodeInvalid {
		t.Errorf("rename to blank: %v", err)
	}
	newURL, err := s.ChangeURL(ctx, "c", other.URL)
	if err != nil || newURL != other.URL {
		t.Fatalf("ChangeURL = %q, %v", newURL, err)
	}
	cfg, _ := config.Read(path)
	if cfg.DefaultSite != "c" || cfg.Sites["c"].URL != other.URL || cfg.Sites["a"].URL != fake.URL {
		t.Errorf("config = %+v", cfg)
	}
	// A URL whose site refuses the credentials is not saved.
	refusing := frappetest.New(t)
	refusing.Handle("GET /api/method/frappe.auth.get_logged_user", frappetest.ErrorHandler(frappetest.Permission("Not permitted")))
	if _, err := s.ChangeURL(ctx, "a", refusing.URL); err == nil {
		t.Error("ChangeURL to a refusing site succeeded")
	}
	if cfg, _ := config.Read(path); cfg.Sites["a"].URL != fake.URL {
		t.Errorf("refused URL was saved: %s", cfg.Sites["a"].URL)
	}
}

func TestChangeURLRefusesOAuth(t *testing.T) {
	fake := frappetest.New(t)
	s, _, path := newSites(t)
	if err := (sitesetup.Store{Path: path}).Init("o", config.SiteConfig{URL: fake.URL, AccessToken: frappetest.Token}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeURL(context.Background(), "o", "https://elsewhere.example.com"); code(err) != CodeUnavailable {
		t.Errorf("ChangeURL on OAuth: %v", err)
	}
}

func TestRemoveRevokesOAuthAndWarnsOnFailure(t *testing.T) {
	ctx := context.Background()
	fake := frappetest.New(t)
	s, _, path := newSites(t)
	store := sitesetup.Store{Path: path}
	oauth := config.SiteConfig{URL: fake.URL, OAuthClientID: frappetest.OAuthClientID, AccessToken: frappetest.Token, RefreshToken: frappetest.RefreshToken}
	if err := store.Init("o", oauth); err != nil {
		t.Fatal(err)
	}
	if err := store.Add("k", config.SiteConfig{URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); err != nil {
		t.Fatal(err)
	}

	res, err := s.Remove(ctx, "o")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Revoked || res.RevokeError != "" || !res.WasDefault || res.NewDefault != "k" {
		t.Errorf("remove = %+v", res)
	}
	if got := fake.Revoked(); len(got) != 1 || got[0] != frappetest.RefreshToken {
		t.Errorf("revoked = %v", got)
	}

	// An unreachable server: removed anyway, with the error reported.
	if err := store.Add("gone", config.SiteConfig{URL: "http://127.0.0.1:1", OAuthClientID: "x", AccessToken: "t", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	res, err = s.Remove(ctx, "gone")
	if err != nil || res.Revoked || res.RevokeError == "" {
		t.Errorf("remove unreachable = %+v, %v", res, err)
	}
	if cfg, _ := config.Read(path); len(cfg.Sites) != 1 {
		t.Errorf("sites left = %v", cfg.Sites)
	}
	// An API-key site sends nothing.
	before := len(fake.Requests())
	if _, err := s.Remove(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Requests()) != before {
		t.Error("removing an API-key site made requests")
	}
	if _, err := s.Remove(ctx, "k"); code(err) != CodeNotFound {
		t.Errorf("remove missing: %v", err)
	}
}

func TestCheckRefreshesExpiredOAuthToken(t *testing.T) {
	ctx := context.Background()
	fake := frappetest.New(t)
	s, _, path := newSites(t)
	if err := (sitesetup.Store{Path: path}).Init("o", config.SiteConfig{URL: fake.URL, OAuthClientID: frappetest.OAuthClientID,
		AccessToken: frappetest.Token, RefreshToken: frappetest.RefreshToken, TokenExpiry: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	fake.ExpireToken(frappetest.Token)

	res, err := s.Check(ctx, "o")
	if err != nil || !res.OK || res.User == "" {
		t.Fatalf("Check = %+v, %v", res, err)
	}
	if fake.Refreshes() != 1 {
		t.Errorf("refreshes = %d", fake.Refreshes())
	}
	cfg, _ := config.Read(path)
	if site := cfg.Sites["o"]; site.AccessToken == frappetest.Token || site.IsTokenExpired() {
		t.Errorf("refreshed token not saved: %+v", site)
	}
	if l, _ := s.List(); l.Sites[0].LastCheck == nil || !l.Sites[0].LastCheck.OK {
		t.Errorf("check not remembered: %+v", l.Sites[0])
	}

	// A refused refresh is an expired sign-in, not a failed call.
	fake.FailRefresh(true)
	if err := config.Edit(path, func(f *config.File) error {
		return f.SetSiteTokens("o", "stale", "stale-refresh", time.Now().Add(-time.Minute).Unix())
	}); err != nil {
		t.Fatal(err)
	}
	res, err = s.Check(ctx, "o")
	if err != nil || res.OK || res.Code != CodeAuth {
		t.Errorf("Check with refused refresh = %+v, %v", res, err)
	}

	// A refresh that fails for a passing reason is not an expired sign-in.
	s.refresh = func(context.Context, string, string, string, string) (*client.OAuthTokens, error) {
		return nil, &client.APIError{Status: http.StatusServiceUnavailable}
	}
	res, err = s.Check(ctx, "o")
	if err != nil || res.OK || res.Code == CodeAuth || strings.Contains(res.Message, "expired") {
		t.Errorf("Check with a 503 from the token endpoint = %+v, %v", res, err)
	}
	s.refresh = func(context.Context, string, string, string, string) (*client.OAuthTokens, error) {
		return nil, fmt.Errorf("refreshing: %w", context.DeadlineExceeded)
	}
	if res, _ = s.Check(ctx, "o"); res.Code != CodeNetwork {
		t.Errorf("Check with a timed-out refresh = %+v", res)
	}
}

func TestCheckReportsRefusedKey(t *testing.T) {
	fake := frappetest.New(t)
	s, _, path := newSites(t)
	if err := (sitesetup.Store{Path: path}).Init("k", config.SiteConfig{URL: fake.URL, APIKey: frappetest.APIKey, APISecret: "revoked"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Check(context.Background(), "k")
	if err != nil || res.OK || res.Code != CodeAuth {
		t.Errorf("Check = %+v, %v", res, err)
	}
}

func TestSignInWithBrowser(t *testing.T) {
	ctx := context.Background()
	fake := frappetest.New(t)
	s, h, path := newSites(t)
	h.openURL = approvingBrowser

	got, err := s.SignInWithBrowser(ctx, BrowserSignInRequest{Name: "prod", URL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	if got.Auth != AuthOAuth || !got.Registered || got.User == "" || !got.IsDefault {
		t.Errorf("signed in = %+v", got)
	}
	cfg, _ := config.Read(path)
	if site := cfg.Sites["prod"]; site.AccessToken == "" || site.RefreshToken == "" || site.OAuthClientID == "" {
		t.Errorf("stored site = %+v", site)
	}
	var steps []string
	var authURL string
	for _, d := range h.named(EventSignInProgress) {
		p := d.(SignInProgress)
		steps = append(steps, p.Step)
		if p.AuthURL != "" {
			authURL = p.AuthURL
		}
	}
	if strings.Join(steps, ",") != "starting,registering,browser,finishing,finishing,saving,done" || !strings.Contains(authURL, "/api/method/frappe.integrations.oauth2.authorize") {
		t.Errorf("progress = %v, auth URL %q", steps, authURL)
	}
	for _, d := range h.named(EventSignInProgress) {
		if js, _ := json.Marshal(d); strings.Contains(string(js), frappetest.Token) {
			t.Errorf("progress carries a token: %s", js)
		}
	}
	if err := s.ReopenSignInPage(); code(err) != CodeUnavailable {
		t.Errorf("reopen after the sign-in: %v", err)
	}
}

func TestSignInWithManualClient(t *testing.T) {
	fake := frappetest.New(t)
	fake.SetDynamicRegistration(false)
	s, h, path := newSites(t)
	h.openURL = approvingBrowser

	_, err := s.SignInWithBrowser(context.Background(), BrowserSignInRequest{Name: "v15", URL: fake.URL})
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeNoRegistration || !e.Unsupported || !strings.HasPrefix(e.RedirectURI, "http://127.0.0.1:") {
		t.Fatalf("no registration: %#v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("a failed sign-in wrote the config")
	}

	got, err := s.SignInWithBrowser(context.Background(), BrowserSignInRequest{Name: "v15", URL: fake.URL, ClientID: frappetest.OAuthClientID, ClientSecret: "shh"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Read(path)
	if site := cfg.Sites["v15"]; got.Registered || site.OAuthClientID != frappetest.OAuthClientID || site.OAuthClientSecret != "shh" {
		t.Errorf("manual client: %+v / %+v", got, site)
	}
	if len(fake.Registrations()) != 0 {
		t.Error("a manual client was registered")
	}
}

func TestSignInCancel(t *testing.T) {
	fake := frappetest.New(t)
	s, h, path := newSites(t)
	opened := make(chan struct{}, 1)
	h.openURL = func(string) error { opened <- struct{}{}; return nil } // the user never approves

	errc := make(chan error, 1)
	go func() {
		_, err := s.SignInWithBrowser(context.Background(), BrowserSignInRequest{Name: "p", URL: fake.URL})
		errc <- err
	}()
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("the browser was never opened")
	}
	if err := s.ReopenSignInPage(); err != nil {
		t.Errorf("reopen while waiting: %v", err)
	}
	s.CancelSignIn()
	select {
	case err := <-errc:
		if code(err) != CodeCancelled {
			t.Errorf("cancelled sign-in: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CancelSignIn did not stop the sign-in")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("a cancelled sign-in wrote the config")
	}
}

// A second sign-in replaces the first and gets its callback port, so a
// redirect URI registered on a hand-made client still matches.
func TestSignInReplacedKeepsPort(t *testing.T) {
	fake := frappetest.New(t)
	s, h, _ := newSites(t)
	opened := make(chan string, 2)
	h.openURL = func(raw string) error { opened <- raw; return nil } // nobody approves
	redirect := func(raw string) string {
		u, _ := url.Parse(raw)
		return u.Query().Get("redirect_uri")
	}
	wait := func(what string) string {
		select {
		case u := <-opened:
			return u
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the browser was never opened", what)
		}
		return ""
	}

	first := make(chan error, 1)
	go func() {
		_, err := s.SignInWithBrowser(context.Background(), BrowserSignInRequest{Name: "p", URL: fake.URL, Attempt: "a1"})
		first <- err
	}()
	uri1 := redirect(wait("first"))

	second := make(chan error, 1)
	go func() {
		_, err := s.SignInWithBrowser(context.Background(), BrowserSignInRequest{Name: "p", URL: fake.URL, Attempt: "a2"})
		second <- err
	}()
	if err := <-first; code(err) != CodeCancelled {
		t.Errorf("replaced sign-in: %v", err)
	}
	uri2 := redirect(wait("second"))
	if uri1 == "" || uri1 != uri2 {
		t.Errorf("redirect URIs differ: %q then %q", uri1, uri2)
	}
	s.CancelSignIn()
	if err := <-second; code(err) != CodeCancelled {
		t.Errorf("second sign-in: %v", err)
	}

	// Every event names its attempt, and the second attempt's events come
	// after the first one's browser step.
	var seen []string
	for _, d := range h.named(EventSignInProgress) {
		p := d.(SignInProgress)
		if p.Attempt != "a1" && p.Attempt != "a2" {
			t.Errorf("event without its attempt: %+v", p)
		}
		seen = append(seen, p.Attempt+":"+p.Step)
	}
	if got := strings.Join(seen, ","); !strings.HasPrefix(got, "a1:starting,a1:registering,a1:browser,a2:starting") || !strings.HasSuffix(got, "a2:browser") {
		t.Errorf("events = %s", got)
	}
}

func TestSignInDeniedInBrowser(t *testing.T) {
	fake := frappetest.New(t)
	s, h, _ := newSites(t)
	h.openURL = func(raw string) error {
		u, _ := url.Parse(raw)
		q := u.Query()
		go func() {
			if resp, err := http.Get(q.Get("redirect_uri") + "?error=access_denied&state=" + q.Get("state")); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	if _, err := s.SignInWithBrowser(context.Background(), BrowserSignInRequest{Name: "p", URL: fake.URL}); err == nil || code(err) == CodeCancelled {
		t.Errorf("denied sign-in: %v", err)
	}
}

func TestWatcherEmitsConfigChanged(t *testing.T) {
	fake := frappetest.New(t)
	s, h, _ := newSites(t)
	s.pollEvery = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWatching(ctx)

	wait := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for len(h.named(EventConfigChanged)) < n {
			if time.Now().After(deadline) {
				t.Fatalf("got %d config:changed events, want %d", len(h.named(EventConfigChanged)), n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if _, err := s.AddWithAPIKey(context.Background(), APIKeyRequest{Name: "a", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); err != nil {
		t.Fatal(err)
	}
	wait(1)
	if ev := h.named(EventConfigChanged)[0].(ConfigChanged); !ev.Exists || ev.Path != s.path {
		t.Errorf("event = %+v", ev)
	}
	time.Sleep(30 * time.Millisecond) // a later write lands in another mtime tick on coarse clocks
	if _, err := s.AddWithAPIKey(context.Background(), APIKeyRequest{Name: "bb", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); err != nil {
		t.Fatal(err)
	}
	wait(2)
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	wait(3)
	if ev := h.named(EventConfigChanged)[2].(ConfigChanged); ev.Exists {
		t.Errorf("removal event = %+v", ev)
	}
	if err := s.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveDropsTheSiteCache(t *testing.T) {
	base := t.TempDir()
	old := sitecache.UserCacheDir
	sitecache.UserCacheDir = func() (string, error) { return base, nil }
	t.Cleanup(func() { sitecache.UserCacheDir = old })

	fake := frappetest.New(t)
	s, _, path := newSites(t)
	store := sitesetup.Store{Path: path}
	key := config.SiteConfig{URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}
	if err := store.Init("gone", key); err != nil {
		t.Fatal(err)
	}
	if err := store.Add("kept", key); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{}
	for _, name := range []string{"gone", "kept"} {
		cfg := key
		cfg.Name = name
		dir, err := sitecache.Dir(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "server.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		dirs[name] = dir
	}

	if _, err := s.Remove(context.Background(), "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs["gone"]); !os.IsNotExist(err) {
		t.Errorf("removed site's cache survived: %v", err)
	}
	if _, err := os.Stat(dirs["kept"]); err != nil {
		t.Errorf("other site's cache was dropped: %v", err)
	}
}
