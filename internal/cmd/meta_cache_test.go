package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func metaTSite() *config.SiteConfig {
	return &config.SiteConfig{Name: "prod", URL: "http://erp.example/"}
}

func TestListCacheTTLAndURLBinding(t *testing.T) {
	cacheTEnv(t)
	cfg := metaTSite()
	if err := writeListCache(cfg, "DocType", []map[string]interface{}{{"name": "ToDo"}, {"name": "Note"}, {"name": ""}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	got := readListCache(cfg, "DocType", now)
	if len(got) != 2 || got[0].Name != "Note" || got[1].Name != "ToDo" {
		t.Fatalf("read %+v, want Note and ToDo sorted", got)
	}
	if readListCache(cfg, "Report", now) != nil {
		t.Error("the report list must be separate from the DocType list")
	}
	if readListCache(cfg, "DocType", now.Add(doctypeCacheTTL-time.Minute)) == nil {
		t.Error("an entry inside the TTL must be used")
	}
	if readListCache(cfg, "DocType", now.Add(doctypeCacheTTL+time.Minute)) != nil {
		t.Error("an entry past the TTL must be ignored")
	}
	if readListCache(cfg, "DocType", now.Add(-time.Hour)) != nil {
		t.Error("an entry dated in the future must be ignored")
	}
	moved := *cfg
	moved.URL = "http://other.example"
	if readListCache(&moved, "DocType", now) != nil {
		t.Error("an entry read from another URL must be ignored")
	}

	if err := writeListCache(cfg, "Report", []map[string]interface{}{{"name": "Gross Profit", "ref_doctype": "Sales Invoice"}}); err != nil {
		t.Fatal(err)
	}
	if r := readListCache(cfg, "Report", time.Now()); len(r) != 1 || r[0].RefDoctype != "Sales Invoice" {
		t.Errorf("reports %+v", r)
	}
}

func TestMetaCacheIgnoresDamage(t *testing.T) {
	cacheTEnv(t)
	cfg := metaTSite()
	path, _ := metaCachePath(cfg, doctypesCacheFile)
	good, _ := json.Marshal(listCache{URL: siteURL(cfg), FetchedAt: time.Now(), Items: []cacheItem{{Name: "ToDo"}}})
	for name, body := range map[string]string{
		"not JSON":      "{nope",
		"trailing data": string(good) + `{"x":1}`,
		"no date":       `{"url":"http://erp.example","items":[{"name":"ToDo"}]}`,
		"truncated":     string(good[:len(good)-3]),
	} {
		if err := config.WriteFileAtomic(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if readListCache(cfg, "DocType", time.Now()) != nil {
			t.Errorf("%s: a damaged file must be ignored", name)
		}
	}
	// A file over the size bound is not read.
	big := make([]byte, maxCacheFileBytes+10)
	copy(big, good)
	for i := len(good); i < len(big); i++ {
		big[i] = ' '
	}
	_ = config.WriteFileAtomic(path, big, 0o600)
	if readListCache(cfg, "DocType", time.Now()) != nil {
		t.Error("an oversized file must be ignored")
	}
	_ = config.WriteFileAtomic(path, good, 0o600)
	if readListCache(cfg, "DocType", time.Now()) == nil {
		t.Error("the good file must be read")
	}
}

func TestSchemaCacheRoundTripAndPermissions(t *testing.T) {
	cacheTEnv(t)
	cfg := metaTSite()
	compact := map[string]interface{}{"name": "A/../B", "fields": []interface{}{map[string]interface{}{"fieldname": "x", "length": json.Number("140")}}}
	rows := []map[string]interface{}{{"fieldname": "x", "default": json.Number("1.0")}}
	if err := writeSchemaCache(cfg, "A/../B", compact, rows, []string{"partial"}); err != nil {
		t.Fatal(err)
	}
	sc := readSchemaCache(cfg, "A/../B", time.Now())
	if sc == nil || sc.Warnings[0] != "partial" {
		t.Fatalf("read %+v", sc)
	}
	// Numbers keep their literal (1.0 stays 1.0).
	if sc.Rows[0]["default"] != json.Number("1.0") {
		t.Errorf("default %#v, want json.Number 1.0", sc.Rows[0]["default"])
	}
	if readSchemaCache(cfg, "A/../C", time.Now()) != nil {
		t.Error("another DocType's schema was returned")
	}
	if readSchemaCache(cfg, "A/../B", time.Now().Add(schemaCacheTTL+time.Minute)) != nil {
		t.Error("an expired schema must be ignored")
	}
	path, _ := schemaCachePath(cfg, "A/../B")
	dir, _ := serverCacheDir(cfg)
	if filepath.Dir(filepath.Dir(path)) != dir {
		t.Errorf("schema path %s escapes %s/schema", path, dir)
	}
	// A file whose doctype field names another DocType (a hash collision, a
	// copied file) is not used.
	b, _ := os.ReadFile(path)
	other, _ := schemaCachePath(cfg, "Other")
	_ = config.WriteFileAtomic(other, b, 0o600)
	if readSchemaCache(cfg, "Other", time.Now()) != nil {
		t.Error("a file for another DocType must be ignored")
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{path, filepath.Dir(path), dir} {
			st, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			want := os.FileMode(0o700)
			if !st.IsDir() {
				want = 0o600
			}
			if st.Mode().Perm() != want {
				t.Errorf("%s mode %v, want %v", p, st.Mode().Perm(), want)
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ffc-tmp") {
			t.Errorf("temp file %s left behind", e.Name())
		}
	}
}

func TestSchemaCacheEvictsOldest(t *testing.T) {
	cacheTEnv(t)
	cfg := metaTSite()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < maxSchemaCacheFiles; i++ {
		dt := fmt.Sprintf("DT %03d", i)
		if err := writeSchemaCache(cfg, dt, map[string]interface{}{"name": dt}, nil, nil); err != nil {
			t.Fatal(err)
		}
		p, _ := schemaCachePath(cfg, dt)
		mod := base.Add(time.Duration(i) * time.Second)
		_ = os.Chtimes(p, mod, mod)
	}
	if err := writeSchemaCache(cfg, "Newest", map[string]interface{}{"name": "Newest"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	dir, _ := serverCacheDir(cfg)
	entries, _ := os.ReadDir(filepath.Join(dir, schemaCacheDir))
	if len(entries) != maxSchemaCacheFiles {
		t.Fatalf("%d schema files, want %d", len(entries), maxSchemaCacheFiles)
	}
	if readSchemaCache(cfg, "DT 000", time.Now()) != nil {
		t.Error("the oldest schema must be evicted")
	}
	if readSchemaCache(cfg, "DT 001", time.Now()) == nil || readSchemaCache(cfg, "Newest", time.Now()) == nil {
		t.Error("only the oldest schema may be evicted")
	}
}

// TestGetSchemaCache: a fresh cache answers get-schema with no request (no
// login on a password site) and the same output; --refresh and --full fetch.
func TestGetSchemaCache(t *testing.T) {
	cacheTEnv(t)
	s := cmdTSchemaSite(t) // no Custom Field/Property Setter: two warnings
	cfg := fakeConfig(t, s, "password")

	type out struct{ stdout, stderr string }
	run := func(args ...string) out {
		r := cmdTOK(t, runFFC(t, cfg, "", args...))
		return out{r.Stdout, r.Stderr}
	}
	for _, args := range [][]string{
		{"--json", "get-schema", "-d", "Ticket"},
		{"get-schema", "-d", "Ticket"},
		{"--output", "yaml", "get-schema", "-d", "Ticket", "--keys", "name,fields"},
	} {
		fresh := run(append(args, "--refresh")...)
		n := len(s.Requests())
		cached := run(args...)
		if got := len(s.Requests()) - n; got != 0 {
			t.Errorf("%v: a cache hit sent %d requests", args, got)
		}
		if fresh != cached {
			t.Errorf("%v: output differs\nfresh:  %+v\ncached: %+v", args, fresh, cached)
		}
		if !strings.Contains(cached.stderr, "custom fields could not be merged") {
			t.Errorf("%v: the warnings must be replayed: %q", args, cached.stderr)
		}
	}
	// The first call (with --refresh) logged in and out; the hits did not.
	if n := len(s.RequestsTo("POST", "/api/method/login")); n != 3 {
		t.Errorf("%d logins, want one per --refresh run", n)
	}

	n := len(s.Requests())
	run("--json", "get-schema", "-d", "Ticket", "--refresh")
	if got := len(s.RequestsTo("GET", "/api/resource/DocType/Ticket")); got == 0 || len(s.Requests()) == n {
		t.Error("--refresh must fetch")
	}
	n = len(s.Requests())
	full := run("--json", "get-schema", "-d", "Ticket", "--full")
	if len(s.Requests()) == n || !strings.Contains(full.stdout, "creation_noise") {
		t.Error("--full must fetch the full definition")
	}
	// --debug says the answer came from the cache.
	dbg := run("--debug", "get-schema", "-d", "Ticket")
	if !strings.Contains(dbg.stderr, `schema of "Ticket" from the local cache`) {
		t.Errorf("--debug: %q", dbg.stderr)
	}
	// Another URL for the same site name is another server.
	moved := frappetest.New(t)
	moved.Add("DocType", map[string]interface{}{"name": "Ticket", "fields": []interface{}{}})
	b, _ := os.ReadFile(cfg)
	_ = os.WriteFile(cfg, []byte(strings.ReplaceAll(string(b), s.URL, moved.URL)), 0o600)
	run("get-schema", "-d", "Ticket")
	if len(moved.RequestsTo("GET", "/api/resource/DocType/Ticket")) != 1 {
		t.Error("a cache of another URL must not answer")
	}
}

// TestListCommandsFillTheCache: only a complete, unfiltered list is cached.
func TestListCommandsFillTheCache(t *testing.T) {
	cacheTEnv(t)
	s := complTSite(t)
	cfgPath := fakeConfig(t, s, "apikey")
	site, _ := config.Load("t", cfgPath)
	cached := func() int { return len(readListCache(site, "DocType", time.Now())) }
	clear := func() {
		p, _ := metaCachePath(site, doctypesCacheFile)
		_ = os.Remove(p)
	}

	cmdTOK(t, runFFC(t, cfgPath, "", "list-doctypes", "--limit", "2"))
	if cached() != 0 {
		t.Error("a list cut by --limit must not be cached")
	}
	cmdTOK(t, runFFC(t, cfgPath, "", "list-doctypes", "--module", "Accounts", "--limit", "0"))
	if cached() != 0 {
		t.Error("a list filtered by --module must not be cached")
	}
	cmdTOK(t, runFFC(t, cfgPath, "", "list-doctypes")) // 5 rows < limit 50
	if cached() != 5 {
		t.Errorf("a list shorter than the limit is complete: %d cached", cached())
	}
	clear()
	cmdTOK(t, runFFC(t, cfgPath, "", "--output", "ndjson", "list-doctypes", "--all", "--page-size", "2"))
	if cached() != 5 {
		t.Errorf("--all: %d cached, want 5", cached())
	}
	clear()
	cmdTOK(t, runFFC(t, cfgPath, "", "list-doctypes", "--limit", "0"))
	if cached() != 5 {
		t.Errorf("--limit 0: %d cached", cached())
	}
	// A failed --all run caches nothing.
	clear()
	s.Handle("GET /api/resource/DocType", frappetest.ErrorHandler(frappetest.Validation("boom")))
	if r := runFFC(t, cfgPath, "", "list-doctypes", "--all"); r.Err == nil {
		t.Fatal("expected a failure")
	}
	if cached() != 0 {
		t.Error("a failed list must not be cached")
	}
}

func TestCacheCommands(t *testing.T) {
	base := cacheTEnv(t)
	s := complTSite(t)
	cfgPath := fakeConfig(t, s, "apikey")

	r := cmdTOK(t, runFFC(t, cfgPath, "", "--json", "cache", "status"))
	var st struct {
		Site    string       `json:"site"`
		Entries []cacheEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &st); err != nil || st.Site != "t" || len(st.Entries) != 0 {
		t.Fatalf("empty status: %s (%v)", r.Stdout, err)
	}

	r = cmdTOK(t, runFFC(t, cfgPath, "", "--json", "cache", "warm", "--doctypes", "Ticket, ToDo"))
	var w struct {
		Doctypes, Reports int
		Schemas           []string
	}
	if err := json.Unmarshal([]byte(r.Stdout), &w); err != nil || w.Doctypes != 5 || w.Reports != 2 || len(w.Schemas) != 2 {
		t.Fatalf("warm: %s (%v)", r.Stdout, err)
	}
	if n := len(s.RequestsTo("GET", "/api/resource/DocType/ToDo")); n != 1 {
		t.Errorf("ToDo schema fetched %d times", n)
	}

	r = cmdTOK(t, runFFC(t, cfgPath, "", "--json", "cache", "status"))
	if err := json.Unmarshal([]byte(r.Stdout), &st); err != nil || len(st.Entries) != 4 {
		t.Fatalf("status after warm: %s (%v)", r.Stdout, err)
	}
	kinds := []string{}
	for _, e := range st.Entries {
		kinds = append(kinds, e.Kind+":"+e.Name)
		if !e.Fresh || e.Bytes == 0 {
			t.Errorf("entry %+v", e)
		}
	}
	if strings.Join(kinds, ",") != "doctypes:,reports:,schema:Ticket,schema:ToDo" {
		t.Errorf("entries %v", kinds)
	}
	r = cmdTOK(t, runFFC(t, cfgPath, "", "cache", "status"))
	if !strings.Contains(r.Stdout, "Ticket") || !strings.Contains(r.Stdout, "fresh") {
		t.Errorf("table status: %s", r.Stdout)
	}

	// A DocType that does not exist fails warm (not found), after the lists
	// were cached.
	r = runFFC(t, cfgPath, "", "cache", "warm", "--doctypes", "Nope")
	if r.Err == nil || r.Code != exitNotFound || !strings.Contains(r.Stderr, "Nope") {
		t.Errorf("warm with an unknown DocType: code %d err %v", r.Code, r.Err)
	}

	// The other site's cache is separate; clear removes only the selected one.
	cmdTOK(t, runFFC(t, cfgPath, "", "--site", "other", "cache", "warm"))
	r = cmdTOK(t, runFFC(t, cfgPath, "", "--json", "cache", "clear"))
	if !strings.Contains(r.Stdout, `"removed": 4`) {
		t.Errorf("clear: %s", r.Stdout)
	}
	site, _ := config.Load("t", cfgPath)
	other, _ := config.Load("other", cfgPath)
	if readListCache(site, "DocType", time.Now()) != nil || readListCache(other, "DocType", time.Now()) == nil {
		t.Error("clear must remove the selected site's cache only")
	}
	cmdTOK(t, runFFC(t, cfgPath, "", "cache", "clear", "--all-sites"))
	if _, err := os.Stat(filepath.Join(base, "ffc")); !os.IsNotExist(err) {
		t.Errorf("--all-sites left %s: %v", filepath.Join(base, "ffc"), err)
	}
}
