package sitesetup

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// approve is a browser whose user approves: it calls the redirect URI with
// the fake's authorization code and the request's state, as Frappe's
// consent page would. It records the URL it was given.
func approve(t *testing.T, opened *[]*url.URL) func(string) error {
	t.Helper()
	return func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		*opened = append(*opened, u)
		q := u.Query()
		cb := q.Get("redirect_uri") + "?" + url.Values{"code": {frappetest.AuthCode}, "state": {q.Get("state")}}.Encode()
		go func() {
			if resp, err := http.Get(cb); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

// The whole OAuth setup through the package API, as an app would run it:
// bind the callback server, register a client for its redirect URI, log in
// through a browser and get the site entry to store.
func TestOAuthFlowEndToEnd(t *testing.T) {
	ctx := context.Background()
	site := frappetest.New(t)

	flow, err := StartOAuthFlow()
	if err != nil {
		t.Fatal(err)
	}
	defer flow.Close()
	if !strings.HasPrefix(flow.RedirectURI(), "http://127.0.0.1:") || flow.Timeout != 5*time.Minute {
		t.Fatalf("redirect URI %q, timeout %s", flow.RedirectURI(), flow.Timeout)
	}

	app, err := ResolveOAuthApp(ctx, site.URL, flow.RedirectURI(), OAuthApp{})
	if err != nil || !app.Registered {
		t.Fatalf("ResolveOAuthApp = %+v, %v", app, err)
	}
	regs := site.Registrations()
	if uris, _ := regs[0].Metadata["redirect_uris"].([]interface{}); len(uris) != 1 || uris[0] != flow.RedirectURI() {
		t.Errorf("registered redirect_uris %v", regs[0].Metadata["redirect_uris"])
	}

	var opened []*url.URL
	var steps []string
	sc, user, err := flow.Login(ctx, site.URL, app, LoginHooks{
		OpenBrowser: approve(t, &opened),
		Step: func(title string, run func()) error {
			steps = append(steps, title)
			run()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if len(opened) != 1 {
		t.Fatalf("browser opened %d times", len(opened))
	}
	q := opened[0].Query()
	if opened[0].Path != "/api/method/frappe.integrations.oauth2.authorize" || q.Get("client_id") != app.ID ||
		q.Get("redirect_uri") != flow.RedirectURI() || q.Get("scope") != OAuthScope || q.Get("response_type") != "code" ||
		q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || q.Get("state") == "" {
		t.Errorf("authorization URL = %s", opened[0])
	}
	form, _ := url.ParseQuery(site.RequestsTo(http.MethodPost, "/api/method/frappe.integrations.oauth2.get_token")[0].Body)
	if form.Get("code") != frappetest.AuthCode || form.Get("redirect_uri") != flow.RedirectURI() || generateCodeChallenge(form.Get("code_verifier")) != q.Get("code_challenge") {
		t.Errorf("token request = %v", form)
	}
	if want := []string{"Exchanging authorization code for tokens...", "Fetching user info..."}; !reflect.DeepEqual(steps, want) {
		t.Errorf("steps = %q", steps)
	}

	if sc.URL != site.URL || sc.OAuthClientID != app.ID || sc.OAuthClientSecret != "" ||
		sc.AccessToken != frappetest.Token+"-code-1" || sc.RefreshToken != frappetest.RefreshToken+"-code-1" || sc.IsTokenExpired() {
		t.Errorf("site = %+v", sc)
	}
	if user == "" {
		t.Error("no user")
	}
	// The site entry works as it is.
	if got, err := Verify(ctx, sc); err != nil || got != user {
		t.Errorf("Verify(new site) = %q, %v; login said %q", got, err, user)
	}
	// Login closed the callback server. (Not probed over HTTP: another test
	// binary may hold the same preferred port by now.)
	open := false
	flow.cs.closeOnce.Do(func() { open = true })
	if open {
		t.Error("Login left the callback server running")
	}
}

// Without Step the network calls run directly; the user name is stripped
// of terminal controls.
func TestOAuthLoginWithoutStepSanitizesUser(t *testing.T) {
	site := frappetest.New(t)
	site.HandleMethod("frappe.auth.get_logged_user", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return "admin@example.com\x1b]0;pwned\x07", nil
	})
	flow, err := StartOAuthFlow()
	if err != nil {
		t.Fatal(err)
	}
	var opened []*url.URL
	_, user, err := flow.Login(context.Background(), site.URL, OAuthApp{ID: frappetest.OAuthClientID}, LoginHooks{OpenBrowser: approve(t, &opened)})
	if err != nil || user != "admin@example.com]0;pwned" {
		t.Errorf("Login = %q, %v", user, err)
	}
}

func TestOAuthLoginEnds(t *testing.T) {
	site := frappetest.New(t)
	app := OAuthApp{ID: frappetest.OAuthClientID}
	start := func(t *testing.T) *OAuthFlow {
		t.Helper()
		flow, err := StartOAuthFlow()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(flow.Close)
		return flow
	}
	nothing := func(string) error { return nil }

	t.Run("browser fails", func(t *testing.T) {
		boom := errors.New("no browser")
		_, _, err := start(t).Login(context.Background(), site.URL, app, LoginHooks{OpenBrowser: func(string) error { return boom }})
		if !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cancelled while waiting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		_, _, err := start(t).Login(ctx, site.URL, app, LoginHooks{OpenBrowser: func(string) error { cancel(); return nil }})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		flow := start(t)
		flow.Timeout = 50 * time.Millisecond
		_, _, err := flow.Login(context.Background(), site.URL, app, LoginHooks{OpenBrowser: nothing})
		if err == nil || !strings.HasPrefix(err.Error(), "authorization: timed out") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("denied", func(t *testing.T) {
		_, _, err := start(t).Login(context.Background(), site.URL, app, LoginHooks{OpenBrowser: func(raw string) error {
			u, _ := url.Parse(raw)
			q := u.Query()
			go func() {
				cb := q.Get("redirect_uri") + "?" + url.Values{"error": {"access_denied"}, "state": {q.Get("state")}}.Encode()
				if resp, err := http.Get(cb); err == nil {
					resp.Body.Close()
				}
			}()
			return nil
		}})
		if err == nil || err.Error() != "authorization: authorization denied: access_denied" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("step refuses", func(t *testing.T) {
		stop := errors.New("spinner aborted")
		var opened []*url.URL
		_, _, err := start(t).Login(context.Background(), site.URL, app, LoginHooks{
			OpenBrowser: approve(t, &opened),
			Step:        func(string, func()) error { return stop },
		})
		if !errors.Is(err, stop) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("exchange refused", func(t *testing.T) {
		var opened []*url.URL
		_, _, err := start(t).Login(context.Background(), site.URL, OAuthApp{ID: "unknown-client"}, LoginHooks{OpenBrowser: approve(t, &opened)})
		var ae *client.APIError
		if err == nil || !strings.HasPrefix(err.Error(), "token exchange: ") || !errors.As(err, &ae) {
			t.Errorf("err = %v", err)
		}
	})
}
