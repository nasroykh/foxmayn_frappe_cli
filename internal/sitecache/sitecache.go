// Package sitecache is the layout of ffc's local per-site cache in the user
// cache directory, and its removal. It never prompts and imports no UI, so
// the CLI and the desktop app share it.
package sitecache

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// UserCacheDir is where the cache lives. A variable so tests can point it at
// a temporary directory on every platform (os.UserCacheDir reads XDG_CACHE_HOME
// on Linux only).
var UserCacheDir = os.UserCacheDir

// Dir returns ffc's cache directory for a site and the
// credentials it signs in with: <user cache dir>/ffc/<site>/<credential>.
//
// <site> is the site's name made safe for a path plus a short hash of the
// exact name. A name may hold "/", "..", a colon or anything else a config
// key can, and two names that differ only in such characters ("a/b", "a_b")
// must not share a directory. A site that has no name (it comes from FFC_*
// variables alone) is keyed by its URL.
//
// <credential> (CredentialID) separates what one login may see from what
// another may: the DocType and report lists and the schemas depend on the
// user's permissions, so FFC_API_KEY on a named site, two env-only key pairs
// on one URL, or a site re-added with other credentials never share entries.
func Dir(cfg *config.SiteConfig) (string, error) {
	root, err := Root(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, CredentialID(cfg)), nil
}

// Root is <user cache dir>/ffc/<site>: the cache of every
// credential of one site, which site remove, rename, edit and cache clear
// delete.
func Root(cfg *config.SiteConfig) (string, error) {
	base, err := UserCacheDir()
	if err != nil {
		return "", err
	}
	key := cfg.Name
	if key == "" {
		// A password in the URL must not end up in a directory name.
		key = "env:" + redactedURL(cfg.URL)
	}
	return filepath.Join(base, "ffc", DirName(key)), nil
}

// CredentialID names the identity a site signs in as, in the order
// client.New picks the method: the OAuth client, the API key or the
// username, hashed. It never uses a secret (token, API secret, password),
// so it stays the same when a token is refreshed. An OAuth site authorised
// again as another user keeps its client id; that goes through site add,
// which drops the site's cache.
func CredentialID(cfg *config.SiteConfig) string {
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

// Drop deletes the cache of the site named name, for every
// credential: the site was removed, renamed, re-added or pointed at another
// server. A failure is ignored; the URL and credential binding already keep
// a stale entry from being used.
func Drop(name string) {
	if name == "" {
		return
	}
	if root, err := Root(&config.SiteConfig{Name: name}); err == nil {
		_ = os.RemoveAll(root)
	}
}

// DirName turns a site name into one safe path element.
func DirName(name string) string {
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

// redactedURL hides a password in the URL's userinfo.
func redactedURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Redacted()
	}
	return raw
}
