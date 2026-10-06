package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const revokePath = "/api/method/frappe.integrations.oauth2.revoke_token"

func TestSiteRemoveRevokesToken(t *testing.T) {
	site := frappetest.New(t)
	cfg := fakeConfig(t, site, "oauth")

	r := runFFC(t, cfg, "", "site", "remove", "t", "--yes")
	wantCode(t, r, 0)
	if got := site.Revoked(); len(got) != 1 || got[0] != frappetest.RefreshToken {
		t.Fatalf("revoked = %v", got)
	}
	reqs := site.RequestsTo(http.MethodPost, revokePath)
	form, _ := url.ParseQuery(reqs[0].Body)
	if form.Get("client_id") != frappetest.OAuthClientID || form.Get("token_type_hint") != "refresh_token" ||
		len(reqs[0].Query) != 0 || reqs[0].Header.Get("Authorization") != "" {
		t.Errorf("revoke request = %+v", reqs[0])
	}
	if _, ok := mustRead(t, cfg).Sites["t"]; ok {
		t.Error("site still in the config")
	}
	if !strings.Contains(r.Stderr, "revoked") || strings.Contains(r.Stderr+r.Stdout, frappetest.RefreshToken) {
		t.Errorf("stderr = %q", r.Stderr)
	}

	// An API-key site has nothing to revoke and sends nothing.
	wantCode(t, runFFC(t, cfg, "", "site", "remove", "other", "--yes"), 0)
	if n := len(site.RequestsTo(http.MethodPost, revokePath)); n != 1 {
		t.Errorf("revoke requests = %d, want 1", n)
	}
}

// Without a refresh token the access token is revoked by name.
func TestSiteRemoveRevokesAccessToken(t *testing.T) {
	site := frappetest.New(t)
	body := "default_site: t\nsites:\n  t:\n    url: \"" + site.URL + "\"\n    oauth_client_id: \"" + frappetest.OAuthClientID +
		"\"\n    access_token: \"" + frappetest.Token + "\"\n"
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wantCode(t, runFFC(t, cfg, "", "site", "remove", "t", "--yes"), 0)
	reqs := site.RequestsTo(http.MethodPost, revokePath)
	if len(reqs) != 1 {
		t.Fatalf("revoke requests = %d", len(reqs))
	}
	if form, _ := url.ParseQuery(reqs[0].Body); form.Get("token_type_hint") != "access_token" || form.Get("token") != frappetest.Token {
		t.Errorf("revoke form = %v", form)
	}
	if got := site.Revoked(); len(got) != 1 || got[0] != frappetest.Token {
		t.Errorf("revoked = %v", got)
	}
}

func TestSiteRemoveRevokeFailureStillRemoves(t *testing.T) {
	old := revokeTimeout
	revokeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { revokeTimeout = old })

	cases := map[string]func(*frappetest.Site) string{
		"server error": func(s *frappetest.Site) string {
			s.Handle("POST "+revokePath, frappetest.ErrorHandler(&frappetest.Error{Status: 500, ExcType: "Exception", Message: "boom"}))
			return s.URL
		},
		"timeout": func(s *frappetest.Site) string {
			s.Handle("POST "+revokePath, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}))
			return s.URL
		},
		"unreachable": func(*frappetest.Site) string {
			srv := httptest.NewServer(http.NotFoundHandler())
			srv.Close()
			return srv.URL
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			site := frappetest.New(t)
			target := setup(site)
			cfg := fakeConfig(t, &frappetest.Site{URL: target}, "oauth")
			start := time.Now()
			r := runFFC(t, cfg, "", "site", "remove", "t", "--yes")
			wantCode(t, r, 0)
			if d := time.Since(start); d > 3*time.Second {
				t.Errorf("removal took %s", d)
			}
			if _, ok := mustRead(t, cfg).Sites["t"]; ok {
				t.Error("site still in the config")
			}
			if !strings.Contains(r.Stderr, "warning: could not revoke") || strings.Contains(r.Stderr, frappetest.RefreshToken) {
				t.Errorf("stderr = %q", r.Stderr)
			}
			if name == "timeout" && !strings.Contains(r.Stderr, "did not answer within 300ms") {
				t.Errorf("timeout warning = %q", r.Stderr)
			}
		})
	}
}

// The revocation under --debug=body never shows the tokens.
func TestSiteRemoveDebugHidesTokens(t *testing.T) {
	site := frappetest.New(t)
	cfg := fakeConfig(t, site, "oauth")
	r := runFFC(t, cfg, "", "--debug=body", "site", "remove", "t", "--yes")
	wantCode(t, r, 0)
	if !strings.Contains(r.Stderr, revokePath) || !strings.Contains(r.Stderr, "token=") {
		t.Fatalf("trace misses the request:\n%s", r.Stderr)
	}
	for _, secret := range []string{frappetest.RefreshToken, frappetest.Token} {
		if strings.Contains(r.Stdout+r.Stderr, secret) {
			t.Errorf("output shows %q", secret)
		}
	}
	if got := site.Revoked(); len(got) != 1 {
		t.Errorf("revoked = %v", got)
	}
}
