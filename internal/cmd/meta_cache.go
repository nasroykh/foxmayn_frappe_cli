package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitecache"
)

// The local metadata cache sits next to server.json in the site's cache
// directory (sitecache.Dir): the DocType list, the report list and compact
// schemas. Shell completion reads it and never fills it; list-doctypes,
// list-reports, get-schema and `ffc cache warm` fill it. Documents are never
// cached.
//
// TTLs: the DocType and report lists change when an app is installed or
// someone creates a DocType or report, which is rare, and a stale list only
// offers a wrong name to a Tab press (the command then fails on the site),
// so they live a day like server.json. A schema is what a user reads before
// writing documents, and Customize Form changes it at once on the site, so
// it lives an hour: long enough that a session of get-schema calls costs
// one fetch, short enough that a customisation shows the same day without
// --refresh.
const (
	doctypeCacheTTL = 24 * time.Hour
	reportCacheTTL  = 24 * time.Hour
	schemaCacheTTL  = time.Hour

	// maxSchemaCacheFiles bounds the schema directory; writing one more
	// evicts the least recently used.
	maxSchemaCacheFiles = 100
	// maxCacheFileBytes is the most a cache read takes from one file. A
	// compact Sales Invoice schema is about 100 KiB.
	maxCacheFileBytes = 8 << 20

	doctypesCacheFile = "doctypes.json"
	reportsCacheFile  = "reports.json"
	schemaCacheDir    = "schema"
)

// cacheItem is one entry of a cached list: a DocType, or a report with the
// DocType it reports on (the MCP policy needs it to filter report names).
type cacheItem struct {
	Name       string `json:"name"`
	RefDoctype string `json:"ref_doctype,omitempty"`
}

// listCache is doctypes.json or reports.json.
type listCache struct {
	URL       string      `json:"url"`
	FetchedAt time.Time   `json:"fetched_at"`
	Items     []cacheItem `json:"items"`
}

// schemaCache is one DocType's schema: the compact view get-schema prints
// with --json, the rows of its table, and the warnings of a partial fetch,
// so a cache hit prints exactly what the fetch printed.
type schemaCache struct {
	URL       string                   `json:"url"`
	FetchedAt time.Time                `json:"fetched_at"`
	Doctype   string                   `json:"doctype"`
	Schema    map[string]interface{}   `json:"schema"`
	Rows      []map[string]interface{} `json:"rows"`
	Warnings  []string                 `json:"warnings,omitempty"`
}

func siteURL(cfg *config.SiteConfig) string { return strings.TrimRight(cfg.URL, "/") }

// fresh reports whether an entry fetched at from url is usable now for a
// site at cfg: same URL, not older than ttl, not dated in the future.
func fresh(cfg *config.SiteConfig, url string, at, now time.Time, ttl time.Duration) bool {
	age := now.Sub(at)
	return url == siteURL(cfg) && !at.IsZero() && age >= 0 && age <= ttl
}

// readCacheFile decodes a cache file with UseNumber (numbers stay as the
// site sent them). Any problem (missing, too large, damaged, trailing data)
// is a miss.
func readCacheFile(path string, v interface{}) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || st.Size() > maxCacheFileBytes {
		return false
	}
	b, err := io.ReadAll(io.LimitReader(f, maxCacheFileBytes+1))
	if err != nil || len(b) > maxCacheFileBytes {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(v) != nil {
		return false
	}
	_, err = dec.Token()
	return err == io.EOF
}

// writeCacheFile stores v at path: atomic, file 0600, directories 0700.
// Commands ignore the error, as for server.json: a cache that cannot be
// written only costs the next call a request. `ffc cache warm` reports it.
func writeCacheFile(path string, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := config.WriteFileAtomic(path, b, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0o700) // a directory an older run made wider
	return nil
}

func metaCachePath(cfg *config.SiteConfig, name string) (string, error) {
	dir, err := sitecache.Dir(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// schemaCachePath is schema/<name>.json, the DocType name made safe for a
// path the way site names are (a DocType name may hold "/" or "..").
func schemaCachePath(cfg *config.SiteConfig, doctype string) (string, error) {
	return metaCachePath(cfg, filepath.Join(schemaCacheDir, sitecache.DirName(doctype)+".json"))
}

func listCacheFile(kind string) (string, time.Duration) {
	if kind == "Report" {
		return reportsCacheFile, reportCacheTTL
	}
	return doctypesCacheFile, doctypeCacheTTL
}

// readListCache returns the cached DocTypes (kind "DocType") or reports
// (kind "Report") of a site, or nil when there is no fresh list.
func readListCache(cfg *config.SiteConfig, kind string, now time.Time) []cacheItem {
	file, ttl := listCacheFile(kind)
	path, err := metaCachePath(cfg, file)
	if err != nil {
		return nil
	}
	var lc listCache
	if !readCacheFile(path, &lc) || !fresh(cfg, lc.URL, lc.FetchedAt, now, ttl) {
		return nil
	}
	return lc.Items
}

// writeListCache stores a complete list of DocTypes or reports, read from
// rows of the list API (name, and ref_doctype for reports).
func writeListCache(cfg *config.SiteConfig, kind string, rows []map[string]interface{}) error {
	items := make([]cacheItem, 0, len(rows))
	for _, r := range rows {
		name, _ := r["name"].(string)
		if name == "" {
			continue
		}
		ref, _ := r["ref_doctype"].(string)
		items = append(items, cacheItem{Name: name, RefDoctype: ref})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	file, _ := listCacheFile(kind)
	path, err := metaCachePath(cfg, file)
	if err != nil {
		return err
	}
	return writeCacheFile(path, listCache{URL: siteURL(cfg), FetchedAt: time.Now().UTC(), Items: items})
}

// listCacher is the listDocsTo hook of list-doctypes and list-reports: a
// complete list without a module filter replaces the cached one. A filtered
// list is not the site's list, so it is not cached.
func listCacher(kind, module string) func(*config.SiteConfig, []map[string]interface{}) {
	if module != "" {
		return nil
	}
	return func(cfg *config.SiteConfig, rows []map[string]interface{}) { _ = writeListCache(cfg, kind, rows) }
}

// readSchemaCache returns the cached schema of doctype, or nil.
func readSchemaCache(cfg *config.SiteConfig, doctype string, now time.Time) *schemaCache {
	path, err := schemaCachePath(cfg, doctype)
	if err != nil {
		return nil
	}
	var sc schemaCache
	if !readCacheFile(path, &sc) || sc.Doctype != doctype || sc.Schema == nil ||
		!fresh(cfg, sc.URL, sc.FetchedAt, now, schemaCacheTTL) {
		return nil
	}
	return &sc
}

// writeSchemaCache stores a fetched schema (its compact view and table rows;
// the full definition is not kept) and evicts the least recently used
// schemas beyond maxSchemaCacheFiles.
func writeSchemaCache(cfg *config.SiteConfig, doctype string, compact map[string]interface{}, rows []map[string]interface{}, warnings []string) error {
	path, err := schemaCachePath(cfg, doctype)
	if err != nil {
		return err
	}
	sc := schemaCache{URL: siteURL(cfg), FetchedAt: time.Now().UTC(), Doctype: doctype,
		Schema: compact, Rows: rows, Warnings: warnings}
	if err := writeCacheFile(path, sc); err != nil {
		return err
	}
	evictSchemas(filepath.Dir(path), maxSchemaCacheFiles)
	return nil
}

// touchSchemaCache marks a schema as just used, so eviction drops the least
// recently used one. Only get-schema calls it on a hit; completion never
// writes to the cache, not even a time stamp. The fetched_at inside the file,
// not the file time, decides whether the entry is fresh.
func touchSchemaCache(cfg *config.SiteConfig, doctype string) {
	if path, err := schemaCachePath(cfg, doctype); err == nil {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
	}
}

// evictSchemas removes the least recently used schema files of dir (by file
// time: written, or touched by a get-schema hit) until at most keep remain.
func evictSchemas(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
			if info, err := e.Info(); err == nil {
				files = append(files, file{e.Name(), info.ModTime()})
			}
		}
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files[:len(files)-keep] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}

// cacheEntry describes one cache file for `ffc cache status`.
type cacheEntry struct {
	Kind      string    `json:"kind"` // server, doctypes, reports, schema
	Name      string    `json:"name,omitempty"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
	AgeSec    int64     `json:"age_seconds"`
	Bytes     int64     `json:"bytes"`
	Fresh     bool      `json:"fresh"`
}

// cacheEntries lists the cache files of a site. A file that cannot be read
// is listed as not fresh, so status shows what clear would remove.
func cacheEntries(cfg *config.SiteConfig, now time.Time) []cacheEntry {
	dir, err := sitecache.Dir(cfg)
	if err != nil {
		return nil
	}
	stamp := func(path string) (string, time.Time, string) {
		var v struct {
			URL       string    `json:"url"`
			FetchedAt time.Time `json:"fetched_at"`
			Doctype   string    `json:"doctype"`
		}
		readCacheFile(path, &v)
		return v.URL, v.FetchedAt, v.Doctype
	}
	entry := func(path, kind string, ttl time.Duration) (cacheEntry, bool) {
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			return cacheEntry{}, false
		}
		url, at, dt := stamp(path)
		e := cacheEntry{Kind: kind, Name: dt, Bytes: st.Size(), Fresh: fresh(cfg, url, at, now, ttl)}
		if !at.IsZero() {
			e.FetchedAt, e.AgeSec = at, int64(now.Sub(at)/time.Second)
		}
		return e, true
	}
	var out []cacheEntry
	for _, f := range []struct {
		file, kind string
		ttl        time.Duration
	}{
		{"server.json", "server", serverCacheTTL},
		{doctypesCacheFile, "doctypes", doctypeCacheTTL},
		{reportsCacheFile, "reports", reportCacheTTL},
	} {
		if e, ok := entry(filepath.Join(dir, f.file), f.kind, f.ttl); ok {
			out = append(out, e)
		}
	}
	files, _ := os.ReadDir(filepath.Join(dir, schemaCacheDir))
	var schemas []cacheEntry
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		if e, ok := entry(filepath.Join(dir, schemaCacheDir, f.Name()), "schema", schemaCacheTTL); ok {
			if e.Name == "" {
				e.Name = strings.TrimSuffix(f.Name(), ".json")
			}
			schemas = append(schemas, e)
		}
	}
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].Name < schemas[j].Name })
	return append(out, schemas...)
}
