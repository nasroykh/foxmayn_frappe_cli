package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const (
	metadataPath = "/.well-known/oauth-authorization-server"
	registerPath = "/api/method/frappe.integrations.oauth2.register_client"
)

// fakeBrowser replaces the browser for one test: it records each
// authorization URL and answers it like a user who approves, by calling the
// redirect URI with the fake's AuthCode and the request's state.
type fakeBrowser struct {
	mu   sync.Mutex
	urls []*url.URL
}

func stubBrowser(t *testing.T) *fakeBrowser {
	t.Helper()
	b := &fakeBrowser{}
	old := openBrowserFn
	t.Cleanup(func() { openBrowserFn = old })
	openBrowserFn = func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		b.mu.Lock()
		b.urls = append(b.urls, u)
		b.mu.Unlock()
		q := u.Query()
		cb := q.Get("redirect_uri") + "?" + url.Values{"code": {frappetest.AuthCode}, "state": {q.Get("state")}}.Encode()
		go func() {
			if resp, err := http.Get(cb); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	return b
}

func (b *fakeBrowser) opened() []*url.URL {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*url.URL(nil), b.urls...)
}

// codeTokens are the tokens the fake issues for the first code exchange.
var codeTokens = []string{frappetest.Token + "-code-1", frappetest.RefreshToken + "-code-1"}

func TestSiteAddOAuthRegistersClient(t *testing.T) {
	site := frappetest.New(t)
	cfg := fakeConfig(t, site, "apikey")
	browser := stubBrowser(t)

	r := runFFC(t, cfg, "", "site", "add", "--oauth", "--name", "reg", "--url", site.URL)
	wantCode(t, r, 0)

	regs := site.Registrations()
	if len(regs) != 1 {
		t.Fatalf("registrations = %d, want 1", len(regs))
	}
	md := regs[0].Metadata
	opened := browser.opened()
	if len(opened) != 1 {
		t.Fatalf("browser opened %d times", len(opened))
	}
	q := opened[0].Query()
	redirect := q.Get("redirect_uri")
	if uris, _ := md["redirect_uris"].([]interface{}); len(uris) != 1 || uris[0] != redirect || !strings.HasPrefix(redirect, "http://127.0.0.1:") {
		t.Errorf("registered redirect_uris %v, login used %q", md["redirect_uris"], redirect)
	}
	if md["token_endpoint_auth_method"] != "none" || md["scope"] != oauthScope ||
		!strings.HasPrefix(md["client_name"].(string), "ffc") {
		t.Errorf("registration = %v", md)
	}
	if g, _ := md["grant_types"].([]interface{}); len(g) != 2 || g[0] != "authorization_code" || g[1] != "refresh_token" {
		t.Errorf("grant_types = %v", md["grant_types"])
	}
	if q.Get("client_id") != regs[0].ClientID || q.Get("scope") != oauthScope || q.Get("code_challenge_method") != "S256" {
		t.Errorf("authorization URL = %s", opened[0])
	}

	saved, err := config.Load("reg", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.OAuthClientID != regs[0].ClientID || saved.OAuthClientSecret != "" ||
		saved.AccessToken != codeTokens[0] || saved.RefreshToken != codeTokens[1] || saved.IsTokenExpired() {
		t.Errorf("saved site = %+v", saved)
	}
	if !strings.Contains(r.Stderr, "Registered OAuth client "+regs[0].ClientID) {
		t.Errorf("stderr = %q", r.Stderr)
	}
	for _, tok := range codeTokens {
		if strings.Contains(r.Stdout+r.Stderr, tok) {
			t.Errorf("output shows a token")
		}
	}

	// The saved site works: the token authenticates.
	if r := runFFC(t, cfg, "", "--site", "reg", "ping"); r.Err != nil {
		t.Errorf("ping with the new site: %v (%s)", r.Err, r.Stderr)
	}
}

func TestInitOAuthClientIDSkipsRegistration(t *testing.T) {
	site := frappetest.New(t)
	stubBrowser(t)
	path := filepath.Join(t.TempDir(), "config.yaml")

	r := runFFC(t, path, "", "init", "--oauth", "--client-id", frappetest.OAuthClientID, "--name", "p", "--url", site.URL)
	wantCode(t, r, 0)
	if n := len(site.Registrations()); n != 0 {
		t.Errorf("registered %d clients with --client-id", n)
	}
	if n := len(site.RequestsTo(http.MethodGet, metadataPath)); n != 0 {
		t.Errorf("metadata read %d times with --client-id", n)
	}
	saved := mustRead(t, path).Sites["p"]
	if saved.OAuthClientID != frappetest.OAuthClientID || saved.RefreshToken != codeTokens[1] {
		t.Errorf("saved site = %+v", saved)
	}
}

func TestClientIDNeedsOAuth(t *testing.T) {
	site := frappetest.New(t)
	cfg := fakeConfig(t, site, "apikey")
	r := runFFC(t, cfg, frappetest.APISecret+"\n", "site", "add", "--client-id", "x", "--name", "n", "--url", site.URL,
		"--api-key", frappetest.APIKey, "--api-secret-stdin")
	wantCode(t, r, 2)
	if !strings.Contains(r.Stderr, "--client-id needs --oauth") {
		t.Errorf("stderr = %q", r.Stderr)
	}
	r = runFFC(t, cfg, "", "site", "add", "--oauth", "--name", "n", "--url", site.URL, "--api-key", "k")
	wantCode(t, r, 2)
}

// Without a terminal there is nobody to ask for a client ID: a site that
// offers no registration is a usage error naming --client-id, and nothing
// is written or opened.
func TestOAuthSetupWithoutRegistration(t *testing.T) {
	for name, off := range map[string]func(*frappetest.Site){
		"registration off":  func(s *frappetest.Site) { s.SetDynamicRegistration(false) },
		"no metadata (v15)": func(s *frappetest.Site) { s.SetAuthServerMetadata(false) },
	} {
		t.Run(name, func(t *testing.T) {
			site := frappetest.New(t)
			off(site)
			cfg := fakeConfig(t, site, "apikey")
			before, _ := os.ReadFile(cfg)
			browser := stubBrowser(t)

			r := runFFC(t, cfg, "", "site", "add", "--oauth", "--name", "reg", "--url", site.URL)
			wantCode(t, r, 2)
			if !strings.Contains(r.Stderr, "--client-id") || !strings.Contains(r.Stderr, "OAuth Settings") {
				t.Errorf("stderr = %q", r.Stderr)
			}
			if after, _ := os.ReadFile(cfg); string(after) != string(before) {
				t.Error("config changed")
			}
			if len(browser.opened()) != 0 || len(site.Registrations()) != 0 || len(site.RequestsTo(http.MethodPost, registerPath)) != 0 {
				t.Error("the flow went on without a client")
			}
		})
	}
}

func TestOAuthSetupRegistrationFails(t *testing.T) {
	cases := []struct {
		status, code int
		want         string
	}{
		{http.StatusTooManyRequests, exitNetwork, "5 per 10 minutes"},
		{http.StatusBadRequest, 6, "refused by the test"},
	}
	for _, c := range cases {
		site := frappetest.New(t)
		site.FailRegistration(c.status)
		cfg := fakeConfig(t, site, "apikey")
		browser := stubBrowser(t)
		r := runFFC(t, cfg, "", "site", "add", "--oauth", "--name", "reg", "--url", site.URL)
		wantCode(t, r, c.code)
		if !strings.Contains(r.Stderr, c.want) || !strings.Contains(r.Stderr, "--client-id") {
			t.Errorf("%d: stderr = %q", c.status, r.Stderr)
		}
		if n := len(site.RequestsTo(http.MethodPost, registerPath)); n != 1 {
			t.Errorf("%d: registration attempted %d times, want 1", c.status, n)
		}
		if len(browser.opened()) != 0 {
			t.Errorf("%d: browser opened", c.status)
		}
	}
}

// A site cannot reach the terminal through what it names: a client_id with
// control characters is refused, and the user name is printed sanitised.
func TestOAuthSetupSanitizesSiteValues(t *testing.T) {
	const esc = "\x1b]0;pwned\x07"
	t.Run("client_id", func(t *testing.T) {
		site := frappetest.New(t)
		site.Handle("POST "+registerPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"client_id":"abc\u001b]0;pwned\u0007"}`))
		}))
		cfg := fakeConfig(t, site, "apikey")
		browser := stubBrowser(t)
		r := runFFC(t, cfg, "", "site", "add", "--oauth", "--name", "reg", "--url", site.URL)
		if r.Err == nil || !strings.Contains(r.Stderr, "invalid client_id") || strings.ContainsAny(r.Stdout+r.Stderr, "\x1b\x07") {
			t.Errorf("err %v, stderr %q", r.Err, r.Stderr)
		}
		if len(browser.opened()) != 0 {
			t.Error("browser opened with an invalid client")
		}
	})
	t.Run("user", func(t *testing.T) {
		site := frappetest.New(t)
		site.HandleMethod("frappe.auth.get_logged_user", func(*http.Request, map[string]interface{}) (interface{}, error) {
			return "admin@example.com" + esc, nil
		})
		cfg := fakeConfig(t, site, "apikey")
		stubBrowser(t)
		r := runFFC(t, cfg, "", "site", "add", "--oauth", "--name", "reg", "--url", site.URL)
		wantCode(t, r, 0)
		if !strings.Contains(r.Stderr, "Logged in as admin@example.com") || strings.ContainsAny(r.Stdout+r.Stderr, "\x1b\x07") {
			t.Errorf("stderr = %q", r.Stderr)
		}
	})
}

// resolveOAuthApp is the decision the wizard and the flags share; the
// wizard prompts on a *noRegistrationError, so these cases are its fallback.
func TestResolveOAuthApp(t *testing.T) {
	ctx := context.Background()
	const redirect = "http://127.0.0.1:53682/callback"

	t.Run("manual wins", func(t *testing.T) {
		site := frappetest.New(t)
		app, err := resolveOAuthApp(ctx, site.URL, redirect, oauthApp{ID: "mine", Secret: "s"})
		if err != nil || app != (oauthApp{ID: "mine", Secret: "s"}) || len(site.Requests()) != 0 {
			t.Errorf("app %+v, err %v, %d requests", app, err, len(site.Requests()))
		}
	})
	t.Run("registers once", func(t *testing.T) {
		site := frappetest.New(t)
		app, err := resolveOAuthApp(ctx, site.URL, redirect, oauthApp{})
		regs := site.Registrations()
		if err != nil || !app.Registered || len(regs) != 1 || app.ID != regs[0].ClientID || app.Secret != "" {
			t.Errorf("app %+v, err %v, registrations %+v", app, err, regs)
		}
	})
	fallback := []struct {
		name        string
		setup       func(*frappetest.Site)
		unsupported bool
		want        string
	}{
		{"registration off", func(s *frappetest.Site) { s.SetDynamicRegistration(false) }, true, "Enable Dynamic Client Registration"},
		{"no metadata", func(s *frappetest.Site) { s.SetAuthServerMetadata(false) }, true, "Show Auth Server Metadata"},
		{"metadata is HTML", func(s *frappetest.Site) { s.Handle("GET "+metadataPath, frappetest.HTMLPage(http.StatusOK)) }, true, "no OAuth server metadata"},
		{"rate limited", func(s *frappetest.Site) { s.FailRegistration(http.StatusTooManyRequests) }, false, "5 per 10 minutes"},
		{"refused", func(s *frappetest.Site) { s.FailRegistration(http.StatusBadRequest) }, false, "refused by the test"},
		{"metadata 502", func(s *frappetest.Site) { s.Handle("GET "+metadataPath, frappetest.HTMLPage(http.StatusBadGateway)) }, false, "could not read"},
	}
	for _, c := range fallback {
		t.Run(c.name, func(t *testing.T) {
			site := frappetest.New(t)
			c.setup(site)
			_, err := resolveOAuthApp(ctx, site.URL, redirect, oauthApp{})
			var nr *noRegistrationError
			if !errors.As(err, &nr) || nr.unsupported != c.unsupported || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v (unsupported %v)", err, nr != nil && nr.unsupported)
			}
			if n := len(site.RequestsTo(http.MethodPost, registerPath)); n > 1 {
				t.Errorf("registration attempted %d times", n)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		_, err := resolveOAuthApp(ctx, srv.URL, redirect, oauthApp{})
		var nr *noRegistrationError
		var te *client.TransportError
		if !errors.As(err, &nr) || nr.unsupported || !errors.As(err, &te) {
			t.Errorf("err = %v", err)
		}
	})
}

// The OAuth setup under --debug=body: the tokens and the code never reach
// the output, and the trace shows the registration.
func TestOAuthSetupDebugHidesSecrets(t *testing.T) {
	site := frappetest.New(t)
	cfg := fakeConfig(t, site, "apikey")
	stubBrowser(t)

	r := runFFC(t, cfg, "", "--debug=body", "site", "add", "--oauth", "--name", "reg", "--url", site.URL)
	wantCode(t, r, 0)
	if !strings.Contains(r.Stderr, registerPath) || !strings.Contains(r.Stderr, "get_token") {
		t.Fatalf("trace misses the requests:\n%s", r.Stderr)
	}
	exchange := site.RequestsTo(http.MethodPost, "/api/method/frappe.integrations.oauth2.get_token")
	if len(exchange) != 1 {
		t.Fatalf("token requests = %d", len(exchange))
	}
	form, _ := url.ParseQuery(exchange[0].Body)
	verifier := form.Get("code_verifier")
	if len(verifier) < 43 {
		t.Fatalf("code_verifier %d characters", len(verifier))
	}
	for _, secret := range append(codeTokens, frappetest.AuthCode, verifier) {
		if strings.Contains(r.Stdout+r.Stderr, secret) {
			t.Errorf("output shows %q", secret)
		}
	}
}

// A hand-made confidential client: --client-id with its secret from
// FFC_OAUTH_CLIENT_SECRET. The secret goes to the token endpoint and into the
// config, never to the output, --debug=body included.
func TestOAuthClientSecretFromEnv(t *testing.T) {
	const secret = "confidential-client-secret-1234"
	t.Setenv("FFC_OAUTH_CLIENT_SECRET", secret)
	site := frappetest.New(t)
	cfg := fakeConfig(t, site, "apikey")
	stubBrowser(t)

	r := runFFC(t, cfg, "", "--debug=body", "site", "add", "--oauth", "--client-id", frappetest.OAuthClientID, "--name", "conf", "--url", site.URL)
	wantCode(t, r, 0)
	exchange := site.RequestsTo(http.MethodPost, "/api/method/frappe.integrations.oauth2.get_token")
	if len(exchange) != 1 {
		t.Fatalf("token requests = %d", len(exchange))
	}
	if form, _ := url.ParseQuery(exchange[0].Body); form.Get("client_secret") != secret || form.Get("client_id") != frappetest.OAuthClientID {
		t.Errorf("token form client_id %q, client_secret sent %v", form.Get("client_id"), form.Get("client_secret") == secret)
	}
	saved, err := config.Load("conf", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.OAuthClientSecret != secret || saved.OAuthClientID != frappetest.OAuthClientID {
		t.Errorf("saved client %q, secret saved %v", saved.OAuthClientID, saved.OAuthClientSecret == secret)
	}
	if !strings.Contains(r.Stderr, "get_token") {
		t.Fatalf("trace misses the token request:\n%s", r.Stderr)
	}
	if strings.Contains(r.Stdout+r.Stderr, secret) {
		t.Error("output shows the client secret")
	}
	if strings.Contains(r.Stderr, "FFC_OAUTH_CLIENT_SECRET is ignored") {
		t.Error("warned although --client-id was given")
	}
}

// Without --client-id the secret has no client to go with: it is ignored,
// with a warning, and the registered public client is used.
func TestOAuthClientSecretWithoutClientIDWarns(t *testing.T) {
	t.Setenv("FFC_OAUTH_CLIENT_SECRET", "unused-secret")
	site := frappetest.New(t)
	cfg := fakeConfig(t, site, "apikey")
	stubBrowser(t)

	r := runFFC(t, cfg, "", "site", "add", "--oauth", "--name", "reg", "--url", site.URL)
	wantCode(t, r, 0)
	if !strings.Contains(r.Stderr, "warning: FFC_OAUTH_CLIENT_SECRET is ignored without --client-id") {
		t.Errorf("stderr = %q", r.Stderr)
	}
	saved, err := config.Load("reg", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.OAuthClientSecret != "" || len(site.Registrations()) != 1 {
		t.Errorf("secret saved %v, registrations %d", saved.OAuthClientSecret != "", len(site.Registrations()))
	}
	form, _ := url.ParseQuery(site.RequestsTo(http.MethodPost, "/api/method/frappe.integrations.oauth2.get_token")[0].Body)
	if form.Has("client_secret") {
		t.Error("the ignored secret was sent")
	}
}
