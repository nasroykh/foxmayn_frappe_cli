//go:build contract

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// contractOAuthApp is the app_name of the fixture OAuth Client; teardown
// removes only clients with this name.
const contractOAuthApp = "ffc contract test (safe to delete)"

// TestContractOAuthExpiry pins what the token refresh during a run relies
// on (T2.11): Frappe refuses an expired bearer token in validate_auth with
// 401 AuthenticationError before the method runs (a write is not applied),
// the fake answers the same, and the refresh_token grant returns a new
// access token and a rotated refresh token, after which the request
// succeeds once.
func TestContractOAuthExpiry(t *testing.T) {
	sc := contractSite(t)
	ctx := contractCtx(t)
	admin, err := client.New(ctx, sc)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(admin.CloseQuietly)
	teardownContractOAuth(t, admin)
	t.Cleanup(func() { teardownContractOAuth(t, admin) })

	oc, err := admin.CreateDoc(ctx, "OAuth Client", map[string]interface{}{
		"app_name": contractOAuthApp, "scopes": "all openid", "skip_authorization": 1,
		"default_redirect_uri": "http://127.0.0.1/callback", "redirect_uris": "http://127.0.0.1/callback",
		"grant_type": "Authorization Code", "response_type": "Code",
	})
	if err != nil {
		t.Fatalf("OAuth Client: %v", err)
	}
	clientID := fmt.Sprint(oc["name"])
	access, refresh := contractRandom(t), contractRandom(t)
	if _, err := admin.CreateDoc(ctx, "OAuth Bearer Token", map[string]interface{}{
		"client": clientID, "user": "Administrator", "scopes": "all openid", "status": "Active",
		"access_token": access, "refresh_token": refresh, "expires_in": 1,
	}); err != nil {
		t.Fatalf("OAuth Bearer Token: %v", err)
	}
	time.Sleep(2 * time.Second)

	// The real site and the fake refuse an expired token alike.
	expired, err := client.New(ctx, &config.SiteConfig{URL: sc.URL, AccessToken: access})
	if err != nil {
		t.Fatal(err)
	}
	fake := frappetest.New(t)
	fake.ExpireToken(frappetest.Token)
	fc, _ := client.New(ctx, &config.SiteConfig{URL: fake.URL, AccessToken: frappetest.Token})
	marker := "ffc contract oauth " + access[:8]
	for name, c := range map[string]*client.FrappeClient{"real": expired, "fake": fc} {
		_, err := c.CreateDoc(ctx, "ToDo", map[string]interface{}{"description": marker})
		var e *client.APIError
		if !errors.As(err, &e) || e.Status != http.StatusUnauthorized || e.ExcType != "AuthenticationError" {
			t.Errorf("%s: expired token: %v", name, err)
		}
	}
	if n, err := admin.GetCount(ctx, "ToDo", `{"description":"`+marker+`"}`); err != nil || n != 0 {
		t.Fatalf("the refused write was applied: %d, %v", n, err)
	}

	// With a refresher the same write succeeds once, after one refresh.
	c, _ := client.New(ctx, &config.SiteConfig{URL: sc.URL, AccessToken: access})
	var calls int
	var tokens *client.OAuthTokens
	c.SetTokenRefresher(func(ctx context.Context, rejected string) (string, error) {
		calls++
		if rejected != access {
			t.Errorf("rejected token is not the expired one")
		}
		tokens, err = client.RefreshOAuthToken(ctx, sc.URL, clientID, "", refresh)
		if err != nil {
			return "", err
		}
		return tokens.AccessToken, nil
	})
	if _, err := c.CreateDoc(ctx, "ToDo", map[string]interface{}{"description": marker}); err != nil {
		t.Fatalf("write after refresh: %v", err)
	}
	if calls != 1 || tokens == nil || tokens.AccessToken == access || tokens.RefreshToken == "" || tokens.RefreshToken == refresh {
		t.Errorf("refresh: calls %d, new access %v, rotated refresh %v", calls,
			tokens != nil && tokens.AccessToken != access, tokens != nil && tokens.RefreshToken != refresh)
	}
	if n, err := admin.GetCount(ctx, "ToDo", `{"description":"`+marker+`"}`); err != nil || n != 1 {
		t.Errorf("ToDo created %d times (%v), want 1", n, err)
	}
	rows, _ := admin.GetList(ctx, "ToDo", client.ListOptions{Filters: `{"description":"` + marker + `"}`, Limit: -1})
	for _, r := range rows {
		_ = admin.DeleteDoc(ctx, "ToDo", fmt.Sprint(r["name"]))
	}
}

// TestContractOAuthRevoke pins what site remove relies on: revoke_token
// (oauth2.py:188) with the client_id and token_type_hint=refresh_token in a
// form body and no Authorization header answers 200 and revokes the whole
// OAuth Bearer Token record (the refresh token no longer refreshes, the
// access token no longer authenticates); an unknown token is a 200 too. The
// fake answers the same.
func TestContractOAuthRevoke(t *testing.T) {
	sc := contractSite(t)
	ctx := contractCtx(t)
	admin, err := client.New(ctx, sc)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(admin.CloseQuietly)
	teardownContractOAuth(t, admin)
	t.Cleanup(func() { teardownContractOAuth(t, admin) })

	oc, err := admin.CreateDoc(ctx, "OAuth Client", map[string]interface{}{
		"app_name": contractOAuthApp, "scopes": "all openid", "skip_authorization": 1,
		"default_redirect_uri": "http://127.0.0.1/callback", "redirect_uris": "http://127.0.0.1/callback",
		"grant_type": "Authorization Code", "response_type": "Code",
	})
	if err != nil {
		t.Fatalf("OAuth Client: %v", err)
	}
	clientID := fmt.Sprint(oc["name"])
	access, refresh := contractRandom(t), contractRandom(t)
	if _, err := admin.CreateDoc(ctx, "OAuth Bearer Token", map[string]interface{}{
		"client": clientID, "user": "Administrator", "scopes": "all openid", "status": "Active",
		"access_token": access, "refresh_token": refresh, "expires_in": 3600,
	}); err != nil {
		t.Fatalf("OAuth Bearer Token: %v", err)
	}
	if _, err := client.GetOAuthUser(ctx, sc.URL, access); err != nil {
		t.Fatalf("access token before revoke: %v", err)
	}

	fake := frappetest.New(t)
	sites := []struct{ name, url, clientID, access, refresh string }{
		{"real", sc.URL, clientID, access, refresh},
		{"fake", fake.URL, frappetest.OAuthClientID, frappetest.Token, frappetest.RefreshToken},
	}
	for _, s := range sites {
		if err := client.RevokeOAuthToken(ctx, s.url, s.clientID, "", s.refresh, "refresh_token"); err != nil {
			t.Fatalf("%s: revoke: %v", s.name, err)
		}
		_, err := client.RefreshOAuthToken(ctx, s.url, s.clientID, "", s.refresh)
		var e *client.APIError
		if !errors.As(err, &e) || e.Status != http.StatusBadRequest {
			t.Errorf("%s: refresh after revoke: %v", s.name, err)
		}
		if _, err := client.GetOAuthUser(ctx, s.url, s.access); !errors.As(err, &e) || e.Status != http.StatusUnauthorized {
			t.Errorf("%s: access token after revoke: %v", s.name, err)
		}
		if err := client.RevokeOAuthToken(ctx, s.url, s.clientID, "", contractRandom(t), "refresh_token"); err != nil {
			t.Errorf("%s: revoking an unknown token: %v", s.name, err)
		}
	}
	tok, err := admin.GetDoc(ctx, "OAuth Bearer Token", access)
	if err != nil || tok["status"] != "Revoked" {
		t.Errorf("bearer token status = %v (%v), want Revoked", tok["status"], err)
	}
}

// TestContractOAuthRegistration pins dynamic client registration (Frappe
// v16, oauth2.py:324-434, integrations/utils.py:206-274): the metadata
// advertises register_client only while enabled, and a public client
// registered for a loopback redirect URI gets no secret, exactly that URI
// (pydantic must not rewrite it: Frappe compares redirect_uri as a string),
// the requested scopes and no skip_authorization. It skips where
// registration is not offered (v15, or disabled in OAuth Settings). Each
// run registers one client (newer Frappe allows 5 per 10 minutes per IP).
func TestContractOAuthRegistration(t *testing.T) {
	sc := contractSite(t)
	ctx := contractCtx(t)
	md, err := client.DiscoverOAuthServer(ctx, sc.URL)
	if err != nil || md.RegistrationEndpoint == "" {
		t.Skipf("the site offers no dynamic client registration (metadata: %v)", err)
	}
	admin, err := client.New(ctx, sc)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(admin.CloseQuietly)
	teardownContractOAuth(t, admin)
	t.Cleanup(func() { teardownContractOAuth(t, admin) })

	const redirect = "http://127.0.0.1:53682/callback"
	req := oauthClientMetadata(redirect)
	req.ClientName = contractOAuthApp // so teardown removes it
	reg, err := client.RegisterOAuthClient(ctx, sc.URL, md.RegistrationEndpoint, req)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if reg.ClientSecret != "" {
		t.Error("a public client got a client_secret")
	}
	doc, err := admin.GetDoc(ctx, "OAuth Client", reg.ClientID)
	if err != nil {
		t.Fatalf("registered client: %v", err)
	}
	if doc["redirect_uris"] != redirect || doc["default_redirect_uri"] != redirect || doc["scopes"] != oauthScope ||
		doc["token_endpoint_auth_method"] != "None" || fmt.Sprint(doc["skip_authorization"]) != "0" {
		t.Errorf("registered client = redirect %v / %v, scopes %v, auth %v, skip %v", doc["redirect_uris"],
			doc["default_redirect_uri"], doc["scopes"], doc["token_endpoint_auth_method"], doc["skip_authorization"])
	}

	// The fake registers the same way.
	fake := frappetest.New(t)
	fmd, err := client.DiscoverOAuthServer(ctx, fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	freg, err := client.RegisterOAuthClient(ctx, fake.URL, fmd.RegistrationEndpoint, req)
	if err != nil || freg.ClientSecret != "" {
		t.Errorf("fake: %+v, %v", freg, err)
	}
}

func contractRandom(t *testing.T) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// teardownContractOAuth removes the fixture OAuth Clients and their tokens.
func teardownContractOAuth(t *testing.T, c *client.FrappeClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	clients, err := c.GetList(ctx, "OAuth Client", client.ListOptions{Filters: `{"app_name":"` + contractOAuthApp + `"}`, Limit: -1})
	if err != nil {
		return
	}
	for _, oc := range clients {
		name := fmt.Sprint(oc["name"])
		toks, _ := c.GetList(ctx, "OAuth Bearer Token", client.ListOptions{Filters: `{"client":"` + name + `"}`, Limit: -1})
		for _, tk := range toks {
			if err := c.DeleteDoc(ctx, "OAuth Bearer Token", fmt.Sprint(tk["name"])); err != nil {
				t.Logf("teardown: OAuth Bearer Token: %v", err)
			}
		}
		if err := c.DeleteDoc(ctx, "OAuth Client", name); err != nil {
			t.Logf("teardown: OAuth Client %s: %v", name, err)
		}
	}
}
