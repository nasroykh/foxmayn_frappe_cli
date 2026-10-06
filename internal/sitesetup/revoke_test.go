package sitesetup

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

const revokePath = "/api/method/frappe.integrations.oauth2.revoke_token"

func TestRevokeToken(t *testing.T) {
	ctx := context.Background()

	t.Run("refresh token first", func(t *testing.T) {
		site := frappetest.New(t)
		ok, err := RevokeToken(ctx, config.SiteConfig{URL: site.URL, OAuthClientID: frappetest.OAuthClientID,
			AccessToken: frappetest.Token, RefreshToken: frappetest.RefreshToken}, time.Second)
		if !ok || err != nil {
			t.Fatalf("RevokeToken = %v, %v", ok, err)
		}
		reqs := site.RequestsTo(http.MethodPost, revokePath)
		form, _ := url.ParseQuery(reqs[0].Body)
		if form.Get("token_type_hint") != "refresh_token" || form.Get("client_id") != frappetest.OAuthClientID ||
			reqs[0].Header.Get("Authorization") != "" {
			t.Errorf("revoke request = %+v", reqs[0])
		}
		if got := site.Revoked(); len(got) != 1 || got[0] != frappetest.RefreshToken {
			t.Errorf("revoked = %v", got)
		}
	})

	t.Run("access token without refresh token", func(t *testing.T) {
		site := frappetest.New(t)
		ok, err := RevokeToken(ctx, config.SiteConfig{URL: site.URL, OAuthClientID: frappetest.OAuthClientID,
			AccessToken: frappetest.Token}, time.Second)
		if !ok || err != nil {
			t.Fatalf("RevokeToken = %v, %v", ok, err)
		}
		if got := site.Revoked(); len(got) != 1 || got[0] != frappetest.Token {
			t.Errorf("revoked = %v", got)
		}
	})

	t.Run("nothing to revoke", func(t *testing.T) {
		site := frappetest.New(t)
		ok, err := RevokeToken(ctx, config.SiteConfig{URL: site.URL, APIKey: "k", APISecret: "s"}, time.Second)
		if ok || err != nil || len(site.Requests()) != 0 {
			t.Errorf("RevokeToken = %v, %v; %d requests", ok, err, len(site.Requests()))
		}
	})

	t.Run("timeout", func(t *testing.T) {
		site := frappetest.New(t)
		site.Handle("POST "+revokePath, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}))
		ok, err := RevokeToken(ctx, config.SiteConfig{URL: site.URL, OAuthClientID: "c", RefreshToken: "r"}, 200*time.Millisecond)
		if !ok || err == nil || !strings.Contains(err.Error(), "did not answer within 200ms") {
			t.Errorf("RevokeToken = %v, %v", ok, err)
		}
	})
}
