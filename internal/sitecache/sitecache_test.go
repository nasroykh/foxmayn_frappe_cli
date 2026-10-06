package sitecache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// cacheTEnv points the user cache directory at a fresh directory.
func cacheTEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := UserCacheDir
	UserCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { UserCacheDir = old })
	return dir
}

func TestCacheDirNameIsSafe(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{
		"prod", "Prod", "erp.example.com", "a/b", "a_b", "a\\b", "../../etc/passwd", "..", ".", "", ".hidden",
		"site with spaces", "päivä", "a:b", strings.Repeat("x", 500), "nul\x00byte", "C:\\Users\\x",
	} {
		got := DirName(name)
		if strings.ContainsAny(got, `/\:`+"\x00") || got == "." || got == ".." || strings.HasPrefix(got, ".") || got == "" {
			t.Errorf("DirName(%q) = %q is not a safe path element", name, got)
		}
		if len(got) > 60 {
			t.Errorf("DirName(%q) is %d bytes long", name, len(got))
		}
		if other, dup := seen[got]; dup {
			t.Errorf("%q and %q share the directory %q", name, other, got)
		}
		seen[got] = name
		if DirName(name) != got {
			t.Errorf("DirName(%q) is not stable", name)
		}
	}
}

func TestServerCacheStaysInsideTheCacheDir(t *testing.T) {
	base := cacheTEnv(t)
	for _, name := range []string{"../../escape", "/abs/path", "a/../../b"} {
		dir, err := Dir(&config.SiteConfig{Name: name, URL: "http://x"})
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(filepath.Join(base, "ffc"), dir)
		parts := strings.Split(rel, string(filepath.Separator))
		if err != nil || len(parts) != 2 || parts[0] == ".." || parts[1] != "none" {
			t.Errorf("site %q gives %s, not ffc/<site>/<credential>", name, dir)
		}
	}
	// A site with no name (FFC_* variables) is keyed by its URL.
	a, _ := Dir(&config.SiteConfig{URL: "http://a"})
	b, _ := Dir(&config.SiteConfig{URL: "http://b"})
	if a == b {
		t.Error("two unnamed sites must not share a cache")
	}
}

func TestDropRemovesEveryCredentialOfOneSite(t *testing.T) {
	cacheTEnv(t)
	mk := func(cfg *config.SiteConfig) string {
		dir, err := Dir(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	a := mk(&config.SiteConfig{Name: "a", APIKey: "k", APISecret: "s"})
	a2 := mk(&config.SiteConfig{Name: "a", Username: "u", Password: "p"})
	b := mk(&config.SiteConfig{Name: "b"})
	Drop("a")
	Drop("")
	for _, d := range []string{a, a2} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s survived Drop (%v)", d, err)
		}
	}
	if _, err := os.Stat(b); err != nil {
		t.Errorf("another site's cache was dropped: %v", err)
	}
}
