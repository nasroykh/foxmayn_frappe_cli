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
