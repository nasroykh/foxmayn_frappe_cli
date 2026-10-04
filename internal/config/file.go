package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// File is an editable view of config.yaml. It works on the yaml.Node tree, not
// on the Config struct, so comments, key order and unknown keys survive a
// rewrite. All values are written through yaml.v3 encoding, so names and
// secrets containing ':', '#', quotes or non-UTF-8 bytes can never corrupt the
// file.
type File struct {
	doc  *yaml.Node // DocumentNode; head comments live here
	root *yaml.Node // top-level MappingNode
}

// ErrUnchanged can be returned by an Edit callback to skip the write.
var ErrUnchanged = errors.New("config unchanged")

// Edit loads the config file at path (or an empty document if it does not
// exist), applies fn, and writes the result back atomically with mode 0600.
// The whole read-modify-write runs under an exclusive lock file, so a token
// refresh in a long-running `ffc mcp` server and a concurrent `ffc site add`
// can no longer silently revert each other's changes.
func Edit(path string, fn func(f *File) error) error {
	return edit(path, false, fn)
}

// Overwrite is like Edit but starts from an empty document, discarding the
// existing content (used by `ffc init`, which creates a fresh config).
func Overwrite(path string, fn func(f *File) error) error {
	return edit(path, true, fn)
}

func edit(path string, fresh bool, fn func(f *File) error) error {
	path, err := resolveSymlink(path)
	if err != nil {
		return err
	}
	unlock, err := lockFile(path)
	if err != nil {
		return err
	}
	defer unlock()

	f := &File{}
	if !fresh {
		raw, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return fmt.Errorf("reading config: %w", err)
		default:
			if f, err = parseFile(raw); err != nil {
				return err
			}
		}
	}
	if f.doc == nil {
		f.root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		f.doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{f.root}}
	}

	if err := fn(f); err != nil {
		if errors.Is(err, ErrUnchanged) {
			return nil
		}
		return err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	// Encoding the DocumentNode (not just the mapping) keeps the file's
	// leading comment block, even when the first key has its own comment.
	if err := enc.Encode(f.doc); err != nil {
		return fmt.Errorf("serialising config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("serialising config: %w", err)
	}
	if err := WriteFileAtomic(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

func parseFile(raw []byte) (*File, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if doc.Kind == 0 { // empty or comment-only file
		return &File{}, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parsing config: top level must be a mapping")
	}
	return &File{doc: &doc, root: doc.Content[0]}, nil
}

// Get returns the string value of a top-level key ("" when absent).
func (f *File) Get(key string) string {
	if v := mapValue(f.root, key); v != nil && v.Kind == yaml.ScalarNode && v.Tag != "!!null" {
		return v.Value
	}
	return ""
}

// Set sets a top-level string key such as default_site or number_format.
func (f *File) Set(key, value string) {
	setScalar(f.root, key, value, "!!str")
}

// HasSite reports whether a site with exactly this name exists.
func (f *File) HasSite(name string) bool {
	sites := mapValue(f.root, "sites")
	return sites != nil && mapValue(sites, name) != nil
}

// SiteNames returns the configured site names in file order.
func (f *File) SiteNames() []string {
	sites := mapValue(f.root, "sites")
	if sites == nil || sites.Kind != yaml.MappingNode {
		return nil
	}
	names := make([]string, 0, len(sites.Content)/2)
	for i := 0; i+1 < len(sites.Content); i += 2 {
		names = append(names, sites.Content[i].Value)
	}
	return names
}

// Site decodes the named site entry.
func (f *File) Site(name string) (SiteConfig, bool) {
	var s SiteConfig
	node := mapValue(mapValue(f.root, "sites"), name)
	if node == nil || node.Decode(&s) != nil {
		return SiteConfig{}, false
	}
	s.Name = name
	return s, true
}

// PutSite adds or replaces a site entry. When no default site is set yet, the
// new site becomes the default so the config is immediately usable.
func (f *File) PutSite(name string, site SiteConfig) error {
	var node yaml.Node
	if err := node.Encode(site); err != nil {
		return fmt.Errorf("encoding site %q: %w", name, err)
	}
	sites := mapValue(f.root, "sites")
	if sites == nil || sites.Kind != yaml.MappingNode {
		// A missing, empty (`sites:`) or null sites value cannot take children.
		sites = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setNode(f.root, "sites", sites)
	}
	sites.Style = 0 // `sites: {}` left behind by a removal would stay in flow style
	setNode(sites, name, &node)
	if f.Get("default_site") == "" {
		f.Set("default_site", name)
	}
	return nil
}

// RemoveSite deletes a site entry. If it was the default, default_site is
// cleared (or moved to the first remaining site).
func (f *File) RemoveSite(name string) error {
	sites := mapValue(f.root, "sites")
	if sites == nil || !deleteKey(sites, name) {
		return fmt.Errorf("site %q not found in config", name)
	}
	if f.Get("default_site") == name {
		next := ""
		if names := f.SiteNames(); len(names) > 0 {
			next = names[0]
		}
		f.Set("default_site", next)
	}
	return nil
}

// RenameSite renames a site entry in place (position and comments are kept)
// and keeps default_site pointing at it when oldName was the default.
func (f *File) RenameSite(oldName, newName string) error {
	sites := mapValue(f.root, "sites")
	if sites == nil || sites.Kind != yaml.MappingNode || mapValue(sites, oldName) == nil {
		return fmt.Errorf("site %q not found in config", oldName)
	}
	// Site lookup falls back to a case-insensitive match, so a name that
	// differs from another site only in case would make it ambiguous.
	defaultSite, defaultExact := f.Get("default_site"), false
	for i := 0; i+1 < len(sites.Content); i += 2 {
		key := sites.Content[i].Value
		if key != oldName && strings.EqualFold(key, newName) {
			return fmt.Errorf("site %q already exists", key)
		}
		defaultExact = defaultExact || key == defaultSite
	}
	for i := 0; i+1 < len(sites.Content); i += 2 {
		if k := sites.Content[i]; k.Value == oldName {
			// A key parsed as a number (8000:) keeps its !!int tag otherwise,
			// and the file no longer loads.
			k.Value, k.Tag, k.Style = newName, "!!str", 0
			break
		}
	}
	// default_site follows the site it resolves to: an exact match, or the
	// case-insensitive fallback config.Load uses.
	if defaultSite == oldName || !defaultExact && strings.EqualFold(defaultSite, oldName) {
		f.Set("default_site", newName)
	}
	return nil
}

// SetSiteURL changes the url of an existing site, leaving its other keys (and
// comments) untouched.
func (f *File) SetSiteURL(name, url string) error {
	site := mapValue(mapValue(f.root, "sites"), name)
	if site == nil || site.Kind != yaml.MappingNode {
		return fmt.Errorf("site %q not found in config", name)
	}
	setScalar(site, "url", url, "!!str")
	return nil
}

// SetSiteTokens stores refreshed OAuth tokens on an existing site, leaving its
// other keys (and comments) untouched.
func (f *File) SetSiteTokens(name, accessToken, refreshToken string, expiry int64) error {
	sites := mapValue(f.root, "sites")
	var site *yaml.Node
	if sites != nil {
		site = mapValue(sites, name)
	}
	if site == nil || site.Kind != yaml.MappingNode {
		return fmt.Errorf("site %q not found in config", name)
	}
	setScalar(site, "access_token", accessToken, "!!str")
	if refreshToken != "" {
		setScalar(site, "refresh_token", refreshToken, "!!str")
	}
	setScalar(site, "token_expiry", strconv.FormatInt(expiry, 10), "!!int")
	return nil
}

// ─── yaml.Node helpers ───────────────────────────────────────────────────────

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setNode(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			// Keep the comments attached to the old value.
			value.HeadComment = m.Content[i+1].HeadComment
			value.LineComment = m.Content[i+1].LineComment
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// setScalar replaces the whole value node (kind, tag and style), so a key that
// previously held `~`/null or a quoted style cannot end up as `!!null dev`.
func setScalar(m *yaml.Node, key, value, tag string) {
	setNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
}

func deleteKey(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// ─── Files ───────────────────────────────────────────────────────────────────

// WriteFileAtomic writes data to a temp file in the same directory, syncs it
// and renames it over path, so a crash can never leave a truncated file. The
// directory is created 0700 when missing and the file gets mode perm.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ffc-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// resolveSymlink follows a symlinked config file (e.g. one managed by a
// dotfiles repo) so the atomic rename replaces its target, not the link.
func resolveSymlink(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	switch {
	case err == nil:
		return real, nil
	case errors.Is(err, fs.ErrNotExist):
		return path, nil
	default:
		return "", fmt.Errorf("resolving config path: %w", err)
	}
}

// MaxLockHold bounds any network call made while holding the config lock (an
// OAuth refresh). It must stay well below lockStale, or a slow holder could
// have its lock broken and two processes would both write the file.
const MaxLockHold = 30 * time.Second

const (
	lockWait  = 45 * time.Second
	lockStale = 3 * MaxLockHold
)

// lockFile takes an exclusive, cross-process lock on path by creating
// path+".lock" with O_EXCL. A lock older than lockStale is assumed to belong
// to a crashed process and is broken. The returned func releases the lock.
func lockFile(path string) (func(), error) {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// The token lets release remove only its own lock, never one a
			// later process took after breaking ours as stale.
			token := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
			_, werr := f.WriteString(token)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(lock)
				return nil, fmt.Errorf("locking config: %w", werr)
			}
			return func() {
				if b, err := os.ReadFile(lock); err == nil && string(b) == token {
					os.Remove(lock)
				}
			}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("locking config: %w", err)
		}
		if st, statErr := os.Stat(lock); statErr == nil && time.Since(st.ModTime()) > lockStale {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("config is locked by another ffc process (remove %s if no other ffc is running)", lock)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
