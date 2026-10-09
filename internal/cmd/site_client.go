package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// loadSite loads the selected site (global --site / --config) and, for an
// OAuth site whose access token has expired, refreshes it first. It runs only
// for commands that talk to a site, so local commands (site, config, help,
// completion) never touch the network.
func loadSite(ctx context.Context) (*config.SiteConfig, error) {
	cfg, err := loadSiteConfig()
	if err != nil {
		return nil, err
	}
	path, err := resolveCfgPath()
	if err != nil {
		return nil, err
	}
	return refreshSite(ctx, path, cfg), nil
}

// loadSiteConfig loads the selected site without touching the network.
func loadSiteConfig() (*config.SiteConfig, error) {
	cfg, err := config.Load(siteName, configPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// refreshSite returns cfg with a fresh access token when cfg is an OAuth
// site whose token has expired. A failure is a warning: the request then
// gets a 401.
func refreshSite(ctx context.Context, path string, cfg *config.SiteConfig) *config.SiteConfig {
	if cfg.Name == "" || !cfg.IsOAuth() || !cfg.IsTokenExpired() {
		return cfg
	}
	if cfg.RefreshToken == "" {
		fmt.Fprintf(os.Stderr, "warning: the OAuth token for site %q has expired and there is no refresh token; run 'ffc site add --oauth' again\n", cfg.Name)
		return cfg
	}
	refreshed, err := refreshOAuth(ctx, path, cfg, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: refreshing the OAuth token for site %q failed: %v\n", cfg.Name, err)
		return cfg
	}
	return refreshed
}

// refreshOAuth refreshes cfg's access token and persists it in the config
// file at path, rotated refresh token included. The read, the refresh and the write all happen under the
// config lock: if another ffc process (e.g. a detached MCP server) refreshed
// while we waited, its fresh token is reused instead of spending the refresh
// token a second time. rejected is a token the site refused (a 401 during a
// run, see tokenRefresher): the stored token is reused only when it differs
// from it, even if it has not expired on paper. "" (refreshSite) reuses any
// unexpired stored token.
func refreshOAuth(ctx context.Context, path string, cfg *config.SiteConfig, rejected string) (*config.SiteConfig, error) {
	out := *cfg
	// The refresh runs under the config lock, so it must finish well before
	// another process may consider the lock stale, whatever --timeout says.
	ctx, cancel := context.WithTimeout(ctx, config.MaxLockHold)
	defer cancel()
	err := config.Edit(path, func(f *config.File) error {
		cur, ok := f.Site(cfg.Name)
		if !ok {
			return fmt.Errorf("site %q not found in %s", cfg.Name, path)
		}
		if cur.AccessToken != "" && cur.AccessToken != rejected && !cur.IsTokenExpired() {
			out.AccessToken, out.RefreshToken, out.TokenExpiry = cur.AccessToken, cur.RefreshToken, cur.TokenExpiry
			return config.ErrUnchanged
		}
		if cur.RefreshToken == "" {
			return fmt.Errorf("site %q: %w", cfg.Name, client.ErrNoRefreshToken)
		}
		tokens, err := client.RefreshOAuthToken(ctx, cur.URL, cur.OAuthClientID, cur.OAuthClientSecret, cur.RefreshToken)
		if err != nil {
			return err
		}
		out.AccessToken, out.TokenExpiry = tokens.AccessToken, tokens.ExpiresAt
		if tokens.RefreshToken != "" {
			out.RefreshToken = tokens.RefreshToken
		}
		return f.SetSiteTokens(cfg.Name, tokens.AccessToken, tokens.RefreshToken, tokens.ExpiresAt)
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// tokenRefresher returns the refresher an OAuth client of site cfg uses when
// the site rejects its access token in the middle of a run (an access token
// lives about an hour; a bulk run, --all or --paginate can outlast it). It
// is refreshOAuth with the rejected token, so concurrent ffc processes still
// refresh once. nil when cfg is not an OAuth site of the config file (FFC_*
// environment credentials have nothing to refresh or persist).
func tokenRefresher(path string, cfg *config.SiteConfig) client.TokenRefresher {
	if cfg.Name == "" || !cfg.IsOAuth() {
		return nil
	}
	return func(ctx context.Context, rejected string) (string, error) {
		out, err := refreshOAuth(ctx, path, cfg, rejected)
		if err != nil {
			return "", err
		}
		return out.AccessToken, nil
	}
}

// newSiteClient builds the client for a loaded site; an OAuth client
// refreshes its token on a 401 (tokenRefresher).
func newSiteClient(ctx context.Context, path string, cfg *config.SiteConfig) (*client.FrappeClient, error) {
	c, err := client.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	c.SetTokenRefresher(tokenRefresher(path, cfg))
	return c, nil
}

// newClient loads the selected site and returns a client for it.
func newClient(ctx context.Context) (*client.FrappeClient, error) {
	c, _, err := newClientCfg(ctx)
	return c, err
}

// newClientCfg is newClient that also returns the site's config.
func newClientCfg(ctx context.Context) (*client.FrappeClient, *config.SiteConfig, error) {
	cfg, err := loadSite(ctx)
	if err != nil {
		return nil, nil, err
	}
	path, err := resolveCfgPath()
	if err != nil {
		return nil, nil, err
	}
	c, err := newSiteClient(ctx, path, cfg)
	if err != nil {
		return nil, nil, err
	}
	return c, cfg, nil
}
