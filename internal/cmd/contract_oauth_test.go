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
