package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// cacheTEnv points the user cache directory at a fresh directory.
func cacheTEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := userCacheDir
	userCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userCacheDir = old })
	return dir
}

func cacheTSite(t *testing.T) (*frappetest.Site, *config.SiteConfig, *client.FrappeClient) {
	t.Helper()
	s := frappetest.New(t)
	cfg := &config.SiteConfig{Name: "prod", URL: s.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}
	c, err := client.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, cfg, c
}

func versionCalls(s *frappetest.Site) int {
	return len(s.RequestsTo("GET", "/api/method/frappe.utils.change_log.get_versions"))
}

func TestCacheDirNameIsSafe(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{
		"prod", "Prod", "erp.example.com", "a/b", "a_b", "a\\b", "../../etc/passwd", "..", ".", "", ".hidden",
		"site with spaces", "päivä", "a:b", strings.Repeat("x", 500), "nul\x00byte", "C:\\Users\\x",
	} {
		got := cacheDirName(name)
		if strings.ContainsAny(got, `/\:`+"\x00") || got == "." || got == ".." || strings.HasPrefix(got, ".") || got == "" {
			t.Errorf("cacheDirName(%q) = %q is not a safe path element", name, got)
		}
		if len(got) > 60 {
			t.Errorf("cacheDirName(%q) is %d bytes long", name, len(got))
		}
		if other, dup := seen[got]; dup {
			t.Errorf("%q and %q share the directory %q", name, other, got)
		}
		seen[got] = name
		if cacheDirName(name) != got {
			t.Errorf("cacheDirName(%q) is not stable", name)
		}
	}
}

func TestServerCacheStaysInsideTheCacheDir(t *testing.T) {
	base := cacheTEnv(t)
	for _, name := range []string{"../../escape", "/abs/path", "a/../../b"} {
		dir, err := serverCacheDir(&config.SiteConfig{Name: name, URL: "http://x"})
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(filepath.Join(base, "ffc"), dir)
		if err != nil || rel == ".." || strings.Contains(rel, string(filepath.Separator)) {
			t.Errorf("site %q gives %s, outside ffc/<one element>", name, dir)
		}
	}
	// A site with no name (FFC_* variables) is keyed by its URL.
	a, _ := serverCacheDir(&config.SiteConfig{URL: "http://a"})
	b, _ := serverCacheDir(&config.SiteConfig{URL: "http://b"})
	if a == b {
		t.Error("two unnamed sites must not share a cache")
	}
}

func TestServerCacheWriteAndPermissions(t *testing.T) {
	cacheTEnv(t)
	s, cfg, c := cacheTSite(t)
	info, cached, err := serverInfo(context.Background(), c, cfg, false)
	if err != nil || cached || info.FrappeMajor() != 16 {
		t.Fatalf("first call: %+v cached=%v err=%v", info, cached, err)
	}
	path, _ := serverCachePath(cfg)
	if runtime.GOOS != "windows" {
		fst, err := os.Stat(path)
		if err != nil || fst.Mode().Perm() != 0o600 {
			t.Fatalf("cache file: %v %v, want 0600", fst, err)
		}
		dst, _ := os.Stat(filepath.Dir(path))
		if dst.Mode().Perm() != 0o700 {
			t.Errorf("cache dir mode %v, want 0700", dst.Mode().Perm())
		}
	}
	// No temp file is left behind by the atomic write.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("the cache dir holds %d entries, want only server.json", len(entries))
	}

	info, cached, err = serverInfo(context.Background(), c, cfg, false)
	if err != nil || !cached || info.FrappeVersion() != "16.36.1" {
		t.Fatalf("second call: %+v cached=%v err=%v", info, cached, err)
	}
	if n := versionCalls(s); n != 1 {
		t.Errorf("get_versions called %d times, want 1", n)
	}
}

func TestServerCacheWidensNoDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	cacheTEnv(t)
	_, cfg, c := cacheTSite(t)
	path, _ := serverCachePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := serverInfo(context.Background(), c, cfg, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Dir(path)); st.Mode().Perm() != 0o700 {
		t.Errorf("a wider directory must be tightened to 0700, is %v", st.Mode().Perm())
	}
}

func TestServerCacheTTL(t *testing.T) {
	cacheTEnv(t)
	_, cfg, c := cacheTSite(t)
	if _, _, err := serverInfo(context.Background(), c, cfg, false); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if readServerCache(cfg, now.Add(serverCacheTTL-time.Minute)) == nil {
		t.Error("an entry just inside the TTL must be used")
	}
	if readServerCache(cfg, now.Add(serverCacheTTL+time.Minute)) != nil {
		t.Error("an entry past the TTL must be ignored")
	}
	if readServerCache(cfg, now.Add(-time.Hour)) != nil {
		t.Error("an entry dated in the future must be ignored")
	}
}

func TestServerCacheStaleIsRefetched(t *testing.T) {
	cacheTEnv(t)
	s, cfg, c := cacheTSite(t)
	path, _ := serverCachePath(cfg)
	stale := &client.ServerInfo{URL: s.URL, FetchedAt: time.Now().Add(-48 * time.Hour),
		Apps: map[string]client.AppVersion{"frappe": {Version: "14.0.0"}}}
	writeServerCache(cfg, stale)
	info, cached, err := serverInfo(context.Background(), c, cfg, false)
	if err != nil || cached || info.FrappeVersion() != "16.36.1" {
		t.Fatalf("stale: %+v cached=%v err=%v", info, cached, err)
	}
	b, _ := os.ReadFile(path)
	var stored client.ServerInfo
	_ = json.Unmarshal(b, &stored)
	if stored.FrappeVersion() != "16.36.1" {
		t.Error("the refetched value must replace the stale one")
	}
}

func TestServerCacheRefresh(t *testing.T) {
	cacheTEnv(t)
	s, cfg, c := cacheTSite(t)
	if _, _, err := serverInfo(context.Background(), c, cfg, false); err != nil {
		t.Fatal(err)
	}
	s.SetApps(map[string]frappetest.App{"frappe": {Title: "Frappe", Version: "17.0.0"}})
	if info, cached, _ := serverInfo(context.Background(), c, cfg, false); !cached || info.FrappeMajor() != 16 {
		t.Fatalf("without --refresh the cache answers: %+v cached=%v", info, cached)
	}
	info, cached, err := serverInfo(context.Background(), c, cfg, true)
	if err != nil || cached || info.FrappeMajor() != 17 {
		t.Fatalf("--refresh: %+v cached=%v err=%v", info, cached, err)
	}
	if readServerCache(cfg, time.Now()).FrappeMajor() != 17 {
		t.Error("--refresh must store what it read")
	}
}

func TestServerCacheIgnoresOtherURLAndDamage(t *testing.T) {
	cacheTEnv(t)
	s, cfg, c := cacheTSite(t)
	writeServerCache(cfg, &client.ServerInfo{URL: "http://elsewhere.example", FetchedAt: time.Now(),
		Apps: map[string]client.AppVersion{"frappe": {Version: "14.0.0"}}})
	if info, cached, err := serverInfo(context.Background(), c, cfg, false); err != nil || cached || info.FrappeMajor() != 16 {
		t.Fatalf("an entry of another URL must not be used: %+v cached=%v err=%v", info, cached, err)
	}
	path, _ := serverCachePath(cfg)
	for _, junk := range []string{"", "{", "null", `{"url":"` + s.URL + `","apps":{}}`, "\x00\x01"} {
		if err := os.WriteFile(path, []byte(junk), 0o600); err != nil {
			t.Fatal(err)
		}
		if readServerCache(cfg, time.Now()) != nil {
			t.Errorf("a damaged cache %q must be a miss", junk)
		}
		if _, _, err := serverInfo(context.Background(), c, cfg, false); err != nil {
			t.Errorf("a damaged cache %q must not fail the call: %v", junk, err)
		}
	}
}

func TestServerCacheDroppedWhenTheServerChanged(t *testing.T) {
	cacheTEnv(t)
	s, cfg, c := cacheTSite(t)
	if _, _, err := serverInfo(context.Background(), c, cfg, false); err != nil {
		t.Fatal(err)
	}
	path, _ := serverCachePath(cfg)
	// A 404 on the method: the server is no longer what was cached.
	s.HandleMethod("frappe.utils.change_log.get_versions", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.NotFound("gone")
	})
	if _, _, err := serverInfo(context.Background(), c, cfg, true); err == nil {
		t.Fatal("want the 404")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the cache must be dropped after a 404: %v", err)
	}

}

// A permission error says nothing about the server: the cache stays.
func TestServerCacheKeptOnPermissionError(t *testing.T) {
	cacheTEnv(t)
	s, cfg, c := cacheTSite(t)
	if _, _, err := serverInfo(context.Background(), c, cfg, false); err != nil {
		t.Fatal(err)
	}
	s.HandleMethod("frappe.utils.change_log.get_versions", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Permission("no")
	})
	if _, _, err := serverInfo(context.Background(), c, cfg, true); err == nil {
		t.Fatal("want the 403")
	}
	path, _ := serverCachePath(cfg)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a 403 must keep the cache: %v", err)
	}
}

func TestServerChanged(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&client.APIError{Status: 404}, true},
		{&client.APIError{Status: 500}, true},
		{&client.APIError{Status: 503}, true},
		{&client.TransportError{Err: os.ErrDeadlineExceeded}, true},
		{&client.APIError{Status: 403}, false},
		{&client.APIError{Status: 401}, false},
		{os.ErrNotExist, false},
		{nil, false},
	} {
		if got := serverChanged(c.err); got != c.want {
			t.Errorf("serverChanged(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
