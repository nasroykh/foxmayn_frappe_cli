package frappetest_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func TestFakeOAuthExpiryAndRefresh(t *testing.T) {
	s := frappetest.New(t)
	const path = "/api/method/frappe.auth.get_logged_user"
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	s.ExpireTokenAfter(frappetest.Token, 2)
	for i := 0; i < 2; i++ {
		if r := fkDo(t, s, "GET", path, "", bearer(frappetest.Token)); r.Status != 200 {
			t.Fatalf("request %d: %d", i, r.Status)
		}
	}
	// Like Frappe's validate_auth: an expired token is AuthenticationError.
	if r := fkDo(t, s, "GET", path, "", bearer(frappetest.Token)); r.Status != 401 || r.Body["exc_type"] != "AuthenticationError" {
		t.Fatalf("expired: %d %s", r.Status, r.Raw)
	}

	token := func(form url.Values) resp {
		return fkDo(t, s, "POST", "/api/method/frappe.integrations.oauth2.get_token", form.Encode(), func(r *http.Request) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		})
	}
	grant := url.Values{"grant_type": {"refresh_token"}, "client_id": {frappetest.OAuthClientID}, "refresh_token": {frappetest.RefreshToken}}
	r := token(grant)
	if r.Status != 200 || r.Body["access_token"] != frappetest.Token+"-1" || r.Body["refresh_token"] != frappetest.RefreshToken+"-1" {
		t.Fatalf("refresh: %d %s", r.Status, r.Raw)
	}
	if r := fkDo(t, s, "GET", path, "", bearer(frappetest.Token+"-1")); r.Status != 200 {
		t.Fatalf("new token: %d", r.Status)
	}
	// The old refresh token stays active, as in Frappe.
	if r := token(grant); r.Status != 200 || s.Refreshes() != 2 {
		t.Fatalf("second refresh: %d %s", r.Status, r.Raw)
	}

	bad := url.Values{"grant_type": {"refresh_token"}, "client_id": {"nope"}, "refresh_token": {frappetest.RefreshToken}}
	if r := token(bad); r.Status != 401 || r.Body["error"] != "invalid_client" {
		t.Fatalf("bad client: %d %s", r.Status, r.Raw)
	}
	s.FailRefresh(true)
	if r := token(grant); r.Status != 400 || r.Body["error"] != "invalid_grant" {
		t.Fatalf("FailRefresh: %d %s", r.Status, r.Raw)
	}
	if s.Refreshes() != 2 {
		t.Errorf("refreshes = %d", s.Refreshes())
	}
}

func form(r *http.Request)     { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }
func jsonBody(r *http.Request) { r.Header.Set("Content-Type", "application/json") }

func TestFakeOAuthRegistration(t *testing.T) {
	s := frappetest.New(t)
	const register = "/api/method/frappe.integrations.oauth2.register_client"
	md := fkDo(t, s, "GET", "/.well-known/oauth-authorization-server", "", nil)
	if md.Status != 200 || md.Body["registration_endpoint"] != s.URL+register {
		t.Fatalf("metadata: %d %s", md.Status, md.Raw)
	}

	const body = `{"client_name":"ffc","redirect_uris":["http://127.0.0.1:53682/callback"],"grant_types":["authorization_code","refresh_token"],"token_endpoint_auth_method":"none","scope":"openid all"}`
	r := fkDo(t, s, "POST", register, body, jsonBody)
	id, _ := r.Body["client_id"].(string)
	if r.Status != 201 || id == "" || r.Body["client_secret"] != nil || r.Body["scope"] != "openid all" {
		t.Fatalf("register: %d %s", r.Status, r.Raw)
	}
	if regs := s.Registrations(); len(regs) != 1 || regs[0].ClientID != id || regs[0].Metadata["client_name"] != "ffc" {
		t.Fatalf("registrations = %+v", regs)
	}

	// The registered client exchanges a code for its own redirect URI only.
	code := url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "code": {frappetest.AuthCode},
		"code_verifier": {"v"}, "redirect_uri": {"http://127.0.0.1:53682/callback"}}
	tok := func(v url.Values) resp {
		return fkDo(t, s, "POST", "/api/method/frappe.integrations.oauth2.get_token", v.Encode(), form)
	}
	if r := tok(code); r.Status != 200 || r.Body["refresh_token"] == nil {
		t.Fatalf("code exchange: %d %s", r.Status, r.Raw)
	}
	code.Set("redirect_uri", "http://127.0.0.1:1/callback")
	if r := tok(code); r.Status != 400 {
		t.Fatalf("other redirect: %d %s", r.Status, r.Raw)
	}

	// Frappe's validation: https or loopback IP only, supported grants only.
	for _, bad := range []string{
		`{"client_name":"x","redirect_uris":["http://localhost:1/callback"]}`,
		`{"client_name":"x","redirect_uris":["https://a.example/cb"],"grant_types":["client_credentials"]}`,
		`{"redirect_uris":["https://a.example/cb"]}`,
	} {
		if r := fkDo(t, s, "POST", register, bad, jsonBody); r.Status != 400 || r.Body["error"] != "invalid_client_metadata" {
			t.Errorf("%s: %d %s", bad, r.Status, r.Raw)
		}
	}

	s.FailRegistration(http.StatusTooManyRequests)
	if r := fkDo(t, s, "POST", register, body, jsonBody); r.Status != 429 || r.Body["exc_type"] != "RateLimitExceededError" {
		t.Errorf("rate limit: %d %s", r.Status, r.Raw)
	}
	s.FailRegistration(0)
	s.SetDynamicRegistration(false)
	if r := fkDo(t, s, "POST", register, body, jsonBody); r.Status != 404 {
		t.Errorf("disabled: %d %s", r.Status, r.Raw)
	}
	if md := fkDo(t, s, "GET", "/.well-known/oauth-authorization-server", "", nil); md.Status != 200 || md.Body["registration_endpoint"] != nil {
		t.Errorf("metadata while disabled: %d %s", md.Status, md.Raw)
	}
	s.SetAuthServerMetadata(false)
	if md := fkDo(t, s, "GET", "/.well-known/oauth-authorization-server", "", nil); md.Status != 404 {
		t.Errorf("metadata off: %d", md.Status)
	}
	if n := len(s.Registrations()); n != 1 {
		t.Errorf("registrations = %d, want 1", n)
	}
}

func TestFakeOAuthRevoke(t *testing.T) {
	s := frappetest.New(t)
	const revoke = "/api/method/frappe.integrations.oauth2.revoke_token"
	rev := func(v url.Values) resp { return fkDo(t, s, "POST", revoke, v.Encode(), form) }

	if r := rev(url.Values{"client_id": {"nope"}, "token": {frappetest.RefreshToken}, "token_type_hint": {"refresh_token"}}); r.Status != 404 {
		t.Fatalf("unknown client: %d %s", r.Status, r.Raw)
	}
	if r := rev(url.Values{"client_id": {frappetest.OAuthClientID}}); r.Status != 400 {
		t.Fatalf("no token: %d %s", r.Status, r.Raw)
	}
	r := rev(url.Values{"client_id": {frappetest.OAuthClientID}, "token": {frappetest.RefreshToken}, "token_type_hint": {"refresh_token"}})
	if r.Status != 200 || len(r.Body) != 0 {
		t.Fatalf("revoke: %d %s", r.Status, r.Raw)
	}
	if got := s.Revoked(); len(got) != 1 || got[0] != frappetest.RefreshToken {
		t.Fatalf("revoked = %v", got)
	}
	// The record is revoked: its refresh token no longer refreshes and its
	// access token no longer authenticates.
	grant := url.Values{"grant_type": {"refresh_token"}, "client_id": {frappetest.OAuthClientID}, "refresh_token": {frappetest.RefreshToken}}
	if r := fkDo(t, s, "POST", "/api/method/frappe.integrations.oauth2.get_token", grant.Encode(), form); r.Status != 403 || r.Body["exc_type"] != "PermissionError" {
		t.Errorf("refresh after revoke: %d %s", r.Status, r.Raw)
	}
	if r := fkDo(t, s, "GET", "/api/method/frappe.auth.get_logged_user", "", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+frappetest.Token)
	}); r.Status != 401 {
		t.Errorf("access token after revoke: %d", r.Status)
	}
	// An unknown token is still a 200, as RFC 7009 asks.
	if r := rev(url.Values{"client_id": {frappetest.OAuthClientID}, "token": {"unknown"}, "token_type_hint": {"refresh_token"}}); r.Status != 200 {
		t.Errorf("unknown token: %d", r.Status)
	}
	if n := len(s.Revoked()); n != 1 {
		t.Errorf("revoked %d tokens, want 1", n)
	}
}
