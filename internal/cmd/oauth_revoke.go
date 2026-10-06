package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// revokeTimeout bounds the revocation in site remove. It runs before the
// config lock is taken, and removal never waits longer than this for it.
var revokeTimeout = 10 * time.Second

// revokeSiteToken revokes the OAuth token of site on its server
// (frappe.integrations.oauth2.revoke_token): the refresh token, which
// revokes its whole OAuth Bearer Token record, or the access token when
// there is no refresh token. It reports false when the site has no token.
// It never prompts; site remove decides what a failure means (a warning).
func revokeSiteToken(ctx context.Context, site config.SiteConfig) (bool, error) {
	token, hint := site.RefreshToken, "refresh_token"
	if token == "" {
		token, hint = site.AccessToken, "access_token"
	}
	if token == "" || site.URL == "" {
		return false, nil
	}
	rctx, cancel := context.WithTimeout(ctx, revokeTimeout)
	defer cancel()
	err := client.RevokeOAuthToken(rctx, site.URL, site.OAuthClientID, site.OAuthClientSecret, token, hint)
	if err != nil && ctx.Err() == nil && errors.Is(rctx.Err(), context.DeadlineExceeded) {
		// The transport error would suggest --timeout, which does not apply.
		return true, fmt.Errorf("the site did not answer within %s", revokeTimeout)
	}
	return true, err
}
