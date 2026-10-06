package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const (
	revokePath   = "/api/method/frappe.integrations.oauth2.revoke_token"
	registerPath = "/api/method/frappe.integrations.oauth2.register_client"
)

func testClientMetadata() OAuthClientMetadata {
	return OAuthClientMetadata{
		ClientName: "ffc (test)", RedirectURIs: []string{"http://127.0.0.1:53682/callback"},
		GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
		TokenEndpointAuthMethod: "none", Scope: "openid all",
	}
}

func TestDiscoverAndRegisterOAuthClient(t *testing.T) {
	s := frappetest.New(t)
	ctx := context.Background()
	md, err := DiscoverOAuthServer(ctx, s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if md.RegistrationEndpoint != s.URL+registerPath {
		t.Fatalf("registration_endpoint = %q", md.RegistrationEndpoint)
	}
	reg, err := RegisterOAuthClient(ctx, s.URL, md.RegistrationEndpoint, testClientMetadata())
	if err != nil {
		t.Fatal(err)
	}
	regs := s.Registrations()
	if len(regs) != 1 || reg.ClientID != regs[0].ClientID || reg.ClientSecret != "" {
		t.Fatalf("registered %+v, fake has %+v", reg, regs)
	}
	got := regs[0].Metadata
	if got["token_endpoint_auth_method"] != "none" || got["scope"] != "openid all" || got["client_name"] != "ffc (test)" {
		t.Errorf("registration body = %v", got)
	}
	if uris, _ := got["redirect_uris"].([]interface{}); len(uris) != 1 || uris[0] != "http://127.0.0.1:53682/callback" {
		t.Errorf("redirect_uris = %v", got["redirect_uris"])
	}
	if r := s.RequestsTo(http.MethodPost, registerPath); len(r) != 1 || !strings.HasPrefix(r[0].Header.Get("Content-Type"), "application/json") {
		t.Errorf("register requests = %+v", r)
	}

	// The endpoint's path is posted to the configured site URL; another
	// host is refused before anything is sent.
	if _, err := RegisterOAuthClient(ctx, s.URL, "https://evil.example"+registerPath, testClientMetadata()); err == nil || !strings.Contains(err.Error(), "not on the site's host") {
		t.Errorf("other host: %v", err)
	}
	if _, err := RegisterOAuthClient(ctx, s.URL, registerPath, testClientMetadata()); err != nil {
		t.Errorf("relative endpoint: %v", err)
	}
	if n := len(s.RequestsTo(http.MethodPost, registerPath)); n != 2 {
		t.Errorf("register requests = %d, want 2", n)
	}
}

func TestDiscoverOAuthServerMissing(t *testing.T) {
	s := frappetest.New(t)
	s.SetAuthServerMetadata(false)
	_, err := DiscoverOAuthServer(context.Background(), s.URL)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Fatalf("metadata off: %v", err)
	}

	// A login proxy answering 200 with HTML is not metadata either.
	s.Handle("GET /.well-known/oauth-authorization-server", frappetest.HTMLPage(http.StatusOK))
	if _, err := DiscoverOAuthServer(context.Background(), s.URL); err == nil {
		t.Fatal("HTML page: want an error")
	}
}

func TestRegisterOAuthClientErrors(t *testing.T) {
	s := frappetest.New(t)
	ctx := context.Background()
	ep := s.URL + registerPath
	var ae *APIError

	s.FailRegistration(http.StatusTooManyRequests)
	if _, err := RegisterOAuthClient(ctx, s.URL, ep, testClientMetadata()); !errors.As(err, &ae) || ae.Status != http.StatusTooManyRequests {
		t.Errorf("429: %v", err)
	}
	s.FailRegistration(0)
	bad := testClientMetadata()
	bad.RedirectURIs = []string{"http://localhost:1/callback"}
	_, err := RegisterOAuthClient(ctx, s.URL, ep, bad)
	if !errors.As(err, &ae) || ae.Status != http.StatusBadRequest || !strings.Contains(err.Error(), "redirect_uris must be https") {
		t.Errorf("invalid metadata: %v", err)
	}
	s.SetDynamicRegistration(false)
	if _, err := RegisterOAuthClient(ctx, s.URL, ep, testClientMetadata()); !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Errorf("disabled: %v", err)
	}
	// Every attempt was a single POST: registration is never retried.
	if n := len(s.RequestsTo(http.MethodPost, registerPath)); n != 3 {
		t.Errorf("register requests = %d, want 3", n)
	}
}

func TestRegisterOAuthClientRefusesBadClientID(t *testing.T) {
	s := frappetest.New(t)
	for name, id := range map[string]string{
		"escape":  "abc\x1b]0;pwned\x07",
		"newline": "abc\ndef",
		"spaces":  " abc",
		"long":    strings.Repeat("a", 141),
		"unicode": "ab‮cd",
	} {
		b, _ := json.Marshal(map[string]string{"client_id": id})
		body := string(b)
		s.Handle("POST "+registerPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(body))
		}))
		_, err := RegisterOAuthClient(context.Background(), s.URL, registerPath, testClientMetadata())
		if err == nil || !strings.Contains(err.Error(), "invalid client_id") || strings.ContainsAny(err.Error(), "\x1b\n‮") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if !validClientID(strings.Repeat("a", 140)) || !validClientID("3f2a9c81bd") {
		t.Error("valid IDs refused")
	}
}

func TestRevokeOAuthToken(t *testing.T) {
	s := frappetest.New(t)
	ctx := context.Background()
	if err := RevokeOAuthToken(ctx, s.URL, frappetest.OAuthClientID, "", frappetest.RefreshToken, "refresh_token"); err != nil {
		t.Fatal(err)
	}
	reqs := s.RequestsTo(http.MethodPost, revokePath)
	if len(reqs) != 1 || len(reqs[0].Query) != 0 {
		t.Fatalf("revoke requests = %+v", reqs)
	}
	form, _ := url.ParseQuery(reqs[0].Body)
	if form.Get("token") != frappetest.RefreshToken || form.Get("token_type_hint") != "refresh_token" ||
		form.Get("client_id") != frappetest.OAuthClientID || form.Has("client_secret") || reqs[0].Header.Get("Authorization") != "" {
		t.Errorf("revoke form = %v, Authorization %q", form, reqs[0].Header.Get("Authorization"))
	}
	if got := s.Revoked(); len(got) != 1 || got[0] != frappetest.RefreshToken {
		t.Errorf("revoked = %v", got)
	}

	// A refusal is an error that never repeats the token.
	s.Handle("POST "+revokePath, frappetest.ErrorHandler(&frappetest.Error{Status: http.StatusInternalServerError, ExcType: "Exception", Message: "boom"}))
	err := RevokeOAuthToken(ctx, s.URL, frappetest.OAuthClientID, "", "secret-refresh", "refresh_token")
	if err == nil || strings.Contains(err.Error(), "secret-refresh") {
		t.Errorf("server error: %v", err)
	}
	if n := len(s.RequestsTo(http.MethodPost, revokePath)); n != 2 {
		t.Errorf("revoke requests = %d, want 2 (no retry)", n)
	}
}
