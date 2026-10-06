package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitecache"
)

// serverCacheTTL is how long the installed apps of a site are remembered. An
// upgrade changes them rarely, and a command that needs them (whoami,
// doctor, anything that must know the Frappe major version) should not pay
// a request for it each time.
const serverCacheTTL = 24 * time.Hour

func serverCachePath(cfg *config.SiteConfig) (string, error) {
	dir, err := sitecache.Dir(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.json"), nil
}

// readServerCache returns the cached apps of a site, or nil when there are
// none worth using: no file, a damaged one, one older than the TTL (or dated
// in the future), or one read from another URL than the site has now.
func readServerCache(cfg *config.SiteConfig, now time.Time) *client.ServerInfo {
	path, err := serverCachePath(cfg)
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var info client.ServerInfo
	if json.Unmarshal(b, &info) != nil || len(info.Apps) == 0 || info.URL != strings.TrimRight(cfg.URL, "/") {
		return nil
	}
	if age := now.Sub(info.FetchedAt); age < 0 || age > serverCacheTTL {
		return nil
	}
	return &info
}

// writeServerCache stores info. The directory is 0700 and the file 0600
// (the cache says which versions a site runs, which tells an attacker which
// bugs it has), and the write is atomic. A failure is ignored: a cache that
// cannot be written only costs the next call a request.
func writeServerCache(cfg *config.SiteConfig, info *client.ServerInfo) {
	path, err := serverCachePath(cfg)
	if err != nil {
		return
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	if config.WriteFileAtomic(path, b, 0o600) != nil {
		return
	}
	_ = os.Chmod(filepath.Dir(path), 0o700) // a directory an older run made wider
}

// invalidateServerCache forgets the cached apps of a site.
func invalidateServerCache(cfg *config.SiteConfig) {
	if path, err := serverCachePath(cfg); err == nil {
		_ = os.Remove(path)
	}
}

// serverChanged reports whether err suggests that the server is no longer
// what the cache describes: a method that is gone (404), a server error, or
// no answer at all. The cache is then dropped, so the next call reads it
// afresh.
func serverChanged(err error) bool {
	var api *client.APIError
	var tr *client.TransportError
	switch {
	case errors.As(err, &api):
		return api.Status == http.StatusNotFound || api.Status >= 500
	case errors.As(err, &tr):
		return true
	}
	return false
}

// serverInfo returns the installed apps of the site cfg describes: from the
// cache while it is fresh, from the site otherwise (and then cached).
// refresh skips the cache. cached tells which it was. A failing request
// drops the cache when it suggests that the server changed.
func serverInfo(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, refresh bool) (info *client.ServerInfo, cached bool, err error) {
	if !refresh {
		if info = readServerCache(cfg, time.Now()); info != nil {
			return info, true, nil
		}
	}
	if info, err = c.ServerVersions(ctx); err != nil {
		if serverChanged(err) {
			invalidateServerCache(cfg)
		}
		return nil, false, err
	}
	writeServerCache(cfg, info)
	return info, false, nil
}
