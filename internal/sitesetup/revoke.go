package sitesetup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// RevokeToken revokes the OAuth token of site on its server
// (frappe.integrations.oauth2.revoke_token): the refresh token, which
// revokes its whole OAuth Bearer Token record, or the access token when
// there is no refresh token. It reports false when the site has no token.
// timeout bounds the request (site remove runs it before taking the config
// lock and never waits longer). The caller decides what a failure means
// (site remove: a warning).
func RevokeToken(ctx context.Context, site config.SiteConfig, timeout time.Duration) (bool, error) {
	token, hint := site.RefreshToken, "refresh_token"
	if token == "" {
		token, hint = site.AccessToken, "access_token"
	}
	if token == "" || site.URL == "" {
		return false, nil
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := client.RevokeOAuthToken(rctx, site.URL, site.OAuthClientID, site.OAuthClientSecret, token, hint)
	if err != nil && ctx.Err() == nil && errors.Is(rctx.Err(), context.DeadlineExceeded) {
		// The transport error would suggest --timeout, which does not apply.
		return true, fmt.Errorf("the site did not answer within %s", timeout)
	}
	return true, err
}
