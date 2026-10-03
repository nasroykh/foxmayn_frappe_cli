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
	cfg, err := config.Load(siteName, configPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.Name == "" || !cfg.IsOAuth() || !cfg.IsTokenExpired() {
		return cfg, nil
	}
	if cfg.RefreshToken == "" {
		fmt.Fprintf(os.Stderr, "warning: the OAuth token for site %q has expired and there is no refresh token; run 'ffc site add --oauth' again\n", cfg.Name)
		return cfg, nil
	}
	refreshed, err := refreshOAuth(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: refreshing the OAuth token for site %q failed: %v\n", cfg.Name, err)
		return cfg, nil
	}
	return refreshed, nil
}

// refreshOAuth refreshes cfg's access token and persists it. The read, the
// refresh and the write all happen under the config lock: if another ffc
// process (e.g. a detached MCP server) refreshed while we waited, its fresh
// token is reused instead of spending the refresh token a second time.
func refreshOAuth(ctx context.Context, cfg *config.SiteConfig) (*config.SiteConfig, error) {
	path, err := resolveCfgPath()
	if err != nil {
		return nil, err
	}
	out := *cfg
	// The refresh runs under the config lock, so it must finish well before
	// another process may consider the lock stale, whatever --timeout says.
	ctx, cancel := context.WithTimeout(ctx, config.MaxLockHold)
	defer cancel()
	err = config.Edit(path, func(f *config.File) error {
		cur, ok := f.Site(cfg.Name)
		if !ok {
			return fmt.Errorf("site %q not found in %s", cfg.Name, path)
		}
		if cur.AccessToken != "" && !cur.IsTokenExpired() {
			out.AccessToken, out.RefreshToken, out.TokenExpiry = cur.AccessToken, cur.RefreshToken, cur.TokenExpiry
			return config.ErrUnchanged
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

// newClient loads the selected site and returns a client for it.
func newClient(ctx context.Context) (*client.FrappeClient, error) {
	cfg, err := loadSite(ctx)
	if err != nil {
		return nil, err
	}
	return client.New(ctx, cfg)
}
