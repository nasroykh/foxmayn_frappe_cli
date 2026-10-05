package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// serverCacheTTL is how long the installed apps of a site are remembered. An
// upgrade changes them rarely, and a command that needs them (whoami,
// doctor, anything that must know the Frappe major version) should not pay
// a request for it each time.
const serverCacheTTL = 24 * time.Hour

// userCacheDir is where the cache lives. A variable so tests can point it at
// a temporary directory on every platform (os.UserCacheDir reads XDG_CACHE_HOME
// on Linux only).
var userCacheDir = os.UserCacheDir

// serverCacheDir returns ffc's cache directory for a site and the
// credentials it signs in with: <user cache dir>/ffc/<site>/<credential>.
//
// <site> is the site's name made safe for a path plus a short hash of the
// exact name. A name may hold "/", "..", a colon or anything else a config
// key can, and two names that differ only in such characters ("a/b", "a_b")
// must not share a directory. A site that has no name (it comes from FFC_*
// variables alone) is keyed by its URL.
//
// <credential> (credentialID) separates what one login may see from what
// another may: the DocType and report lists and the schemas depend on the
// user's permissions, so FFC_API_KEY on a named site, two env-only key pairs
// on one URL, or a site re-added with other credentials never share entries.
func serverCacheDir(cfg *config.SiteConfig) (string, error) {
	root, err := siteCacheRoot(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, credentialID(cfg)), nil
}

// siteCacheRoot is <user cache dir>/ffc/<site>: the cache of every
// credential of one site, which site remove, rename, edit and cache clear
// delete.
func siteCacheRoot(cfg *config.SiteConfig) (string, error) {
	base, err := userCacheDir()
	if err != nil {
		return "", err
	}
	key := cfg.Name
	if key == "" {
		// A password in the URL must not end up in a directory name.
		key = "env:" + redactedURL(cfg.URL)
	}
	return filepath.Join(base, "ffc", cacheDirName(key)), nil
}

// credentialID names the identity a site signs in as, in the order
// client.New picks the method: the OAuth client, the API key or the
// username, hashed. It never uses a secret (token, API secret, password),
// so it stays the same when a token is refreshed. An OAuth site authorised
// again as another user keeps its client id; that goes through site add,
// which drops the site's cache.
func credentialID(cfg *config.SiteConfig) string {
	var kind, id string
	switch {
	case cfg.AccessToken != "":
		kind, id = "oauth", cfg.OAuthClientID
	case cfg.APIKey != "" && cfg.APISecret != "":
		kind, id = "key", cfg.APIKey
	case cfg.IsSessionAuth():
		kind, id = "user", cfg.Username
	default:
		return "none"
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + id))
	return kind + "-" + hex.EncodeToString(sum[:8])
}

// cacheDirName turns a site name into one safe path element.
func cacheDirName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 40 {
			break
		}
	}
	safe := strings.Trim(b.String(), ".") // "." and ".." are not names, and a leading dot hides the directory
	if safe == "" {
		safe = "site"
	}
	sum := sha256.Sum256([]byte(name))
	return safe + "-" + hex.EncodeToString(sum[:4])
}

func serverCachePath(cfg *config.SiteConfig) (string, error) {
	dir, err := serverCacheDir(cfg)
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
