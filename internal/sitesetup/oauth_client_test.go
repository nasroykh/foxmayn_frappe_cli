package sitesetup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const (
	metadataPath = "/.well-known/oauth-authorization-server"
	registerPath = "/api/method/frappe.integrations.oauth2.register_client"
)

// ResolveOAuthApp is the decision the wizard and the flags share; the
// wizard prompts on a *NoRegistrationError, so these cases are its fallback.
func TestResolveOAuthApp(t *testing.T) {
	ctx := context.Background()
	const redirect = "http://127.0.0.1:53682/callback"

	t.Run("manual wins", func(t *testing.T) {
		site := frappetest.New(t)
		app, err := ResolveOAuthApp(ctx, site.URL, redirect, OAuthApp{ID: "mine", Secret: "s"})
		if err != nil || app != (OAuthApp{ID: "mine", Secret: "s"}) || len(site.Requests()) != 0 {
			t.Errorf("app %+v, err %v, %d requests", app, err, len(site.Requests()))
		}
	})
	t.Run("registers once", func(t *testing.T) {
		site := frappetest.New(t)
		app, err := ResolveOAuthApp(ctx, site.URL, redirect, OAuthApp{})
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
		{"no metadata", func(s *frappetest.Site) { s.SetAuthServerMetadata(false) }, true, "registration may still be on"},
		{"metadata is HTML", func(s *frappetest.Site) { s.Handle("GET "+metadataPath, frappetest.HTMLPage(http.StatusOK)) }, true, "is not metadata"},
		{"metadata 403 (WAF)", func(s *frappetest.Site) { s.Handle("GET "+metadataPath, frappetest.HTMLPage(http.StatusForbidden)) }, true, "refused the OAuth server metadata request (HTTP 403)"},
		{"rate limited", func(s *frappetest.Site) { s.FailRegistration(http.StatusTooManyRequests) }, false, "5 per 10 minutes"},
		{"refused", func(s *frappetest.Site) { s.FailRegistration(http.StatusBadRequest) }, false, "refused by the test"},
		{"metadata 502", func(s *frappetest.Site) { s.Handle("GET "+metadataPath, frappetest.HTMLPage(http.StatusBadGateway)) }, false, "could not read"},
	}
	for _, c := range fallback {
		t.Run(c.name, func(t *testing.T) {
			site := frappetest.New(t)
			c.setup(site)
			_, err := ResolveOAuthApp(ctx, site.URL, redirect, OAuthApp{})
			var nr *NoRegistrationError
			if !errors.As(err, &nr) || nr.Unsupported != c.unsupported || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v (unsupported %v)", err, nr != nil && nr.Unsupported)
			}
			if n := len(site.RequestsTo(http.MethodPost, registerPath)); n > 1 {
				t.Errorf("registration attempted %d times", n)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		_, err := ResolveOAuthApp(ctx, srv.URL, redirect, OAuthApp{})
		var nr *NoRegistrationError
		var te *client.TransportError
		if !errors.As(err, &nr) || nr.Unsupported || !errors.As(err, &te) {
			t.Errorf("err = %v", err)
		}
	})
}
