package sitesetup

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// The wizards build a config.SiteConfig; writing it and reading it back must
// be lossless for awkward names and secrets, and config.Load must find it.
func TestSiteConfigRoundTrip(t *testing.T) {
	t.Setenv("FFC_URL", "")
	t.Setenv("FFC_API_KEY", "")
	t.Setenv("FFC_API_SECRET", "")

	sites := map[string]config.SiteConfig{
		"apikey": {URL: "https://a.example", APIKey: "k:#'\"", APISecret: "s e c\xff\xfe"},
		"password": {URL: "http://localhost:8000", Username: "user@example.com",
			Password: ` p"a's:s #w0rd: {x} `},
		"oauth": {URL: "https://o.example", OAuthClientID: "cid", OAuthClientSecret: "csec",
			AccessToken: "at", RefreshToken: "rt", TokenExpiry: 1893456000},
	}
	for _, name := range []string{"Prod", "erp.example.com", "#dev", "null", "~", "dev:"} {
		for kind, site := range sites {
			t.Run(name+"/"+kind, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "sub", "config.yaml")
				st := Store{Path: path}
				if err := st.Init(name, site); err != nil {
					t.Fatalf("Init: %v", err)
				}
				if err := st.Add("other", config.SiteConfig{URL: "https://other.example", APIKey: "k", APISecret: "s"}); err != nil {
					t.Fatalf("Add: %v", err)
				}

				cfg, err := config.Read(path)
				if err != nil {
					t.Fatalf("config.Read: %v", err)
				}
				if cfg.DefaultSite != name {
					t.Errorf("default_site = %q, want %q", cfg.DefaultSite, name)
				}
				if got := cfg.Sites[name]; got != site {
					t.Errorf("site round-trip:\n got %+v\nwant %+v", got, site)
				}

				loaded, err := config.Load("", path)
				if err != nil {
					t.Fatalf("config.Load default: %v", err)
				}
				want := site
				want.Name = name
				if *loaded != want {
					t.Errorf("Load = %+v, want %+v", *loaded, want)
				}

				fi, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				// Windows has no Unix permission bits; Go reports 0666/0777 there.
				if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
					t.Errorf("config mode = %v, want 0600", fi.Mode().Perm())
				}
				if fi, _ := os.Stat(filepath.Dir(path)); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
					t.Errorf("config dir mode = %v, want 0700", fi.Mode().Perm())
				}
			})
		}
	}
}

func TestAddSiteKeepsDefaultAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# my header\n# second line\n\n# about default\ndefault_site: dev\nsites:\n  dev:\n    url: https://dev.example\n    api_key: k\n    api_secret: s\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Store{Path: path}).Add("Prod", config.SiteConfig{URL: "https://p.example", Username: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), "# my header\n# second line\n") {
		t.Errorf("header lost:\n%s", raw)
	}
	cfg, err := config.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultSite != "dev" || len(cfg.Sites) != 2 {
		t.Errorf("got default %q with %d sites, want dev with 2", cfg.DefaultSite, len(cfg.Sites))
	}
}

// Every write through the store drops the cache of the names it changed,
// and only after the write succeeded; a refused write drops nothing.
func TestStoreLifecycleDropsCaches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var dropped []string
	st := Store{Path: path, DropCache: func(name string) { dropped = append(dropped, name) }}
	expectDropped := func(step string, want ...string) {
		t.Helper()
		if !reflect.DeepEqual(dropped, want) {
			t.Errorf("%s: dropped %q, want %q", step, dropped, want)
		}
		dropped = nil
	}
	site := config.SiteConfig{URL: "https://a.example", APIKey: "k", APISecret: "s"}

	if err := st.Init("a", site); err != nil {
		t.Fatal(err)
	}
	expectDropped("init", "a")
	if err := st.Add("b", site); err != nil {
		t.Fatal(err)
	}
	expectDropped("add", "b")

	if err := st.SetDefault("b"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDefault("nope"); err == nil || !strings.Contains(err.Error(), "available: a, b") {
		t.Errorf("SetDefault(nope) = %v", err)
	}
	before, _ := os.ReadFile(path)
	if err := st.SetDefault("b"); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("SetDefault to the current default rewrote the file")
	}
	expectDropped("set default")

	if err := st.Rename("b", "c"); err != nil {
		t.Fatal(err)
	}
	expectDropped("rename", "b", "c")
	if err := st.Rename("missing", "d"); err == nil {
		t.Error("Rename of a missing site: want error")
	}
	expectDropped("refused rename")

	if err := st.SetURL("c", "https://c.example"); err != nil {
		t.Fatal(err)
	}
	expectDropped("set url", "c")

	cfg, err := config.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultSite != "c" || cfg.Sites["c"].URL != "https://c.example" || cfg.Sites["c"].APISecret != "s" {
		t.Errorf("config = default %q, sites %+v", cfg.DefaultSite, cfg.Sites)
	}

	r, err := st.Remove("c")
	if err != nil || r != (Removed{WasDefault: true, NewDefault: "a"}) {
		t.Errorf("Remove(c) = %+v, %v", r, err)
	}
	expectDropped("remove", "c")
	r, err = st.Remove("a")
	if err != nil || r != (Removed{WasDefault: true}) {
		t.Errorf("Remove(a) = %+v, %v", r, err)
	}
	expectDropped("remove last", "a")
	if _, err := st.Remove("a"); err == nil {
		t.Error("Remove of a missing site: want error")
	}
	expectDropped("refused remove")
}

// A Store without DropCache still writes.
func TestStoreWithoutDropCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := (Store{Path: path}).Init("a", config.SiteConfig{URL: "https://a.example", APIKey: "k", APISecret: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (Store{Path: path}).Remove("a"); err != nil {
		t.Fatal(err)
	}
	if cfg, err := config.Read(path); err != nil || len(cfg.Sites) != 0 {
		t.Fatalf("after remove: %+v, %v", cfg, err)
	}
}

// AddOrInit writes what Init writes into a missing file, and adds to a file
// that exists (another process may have created it since the caller looked).
func TestAddOrInit(t *testing.T) {
	site := config.SiteConfig{URL: "https://a.example", APIKey: "k", APISecret: "s"}
	dir := t.TempDir()
	initPath, bothPath := filepath.Join(dir, "init.yaml"), filepath.Join(dir, "sub", "both.yaml")
	if err := (Store{Path: initPath}).Init("prod", site); err != nil {
		t.Fatal(err)
	}
	var dropped []string
	st := Store{Path: bothPath, DropCache: func(n string) { dropped = append(dropped, n) }}
	if err := st.AddOrInit("prod", site); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(initPath)
	got, _ := os.ReadFile(bothPath)
	if string(got) != string(want) {
		t.Errorf("missing file:\n%s\nwant (as Init):\n%s", got, want)
	}

	// The file now exists: the next site is added, the default stays.
	if err := st.AddOrInit("staging", config.SiteConfig{URL: "https://b.example", APIKey: "k2", APISecret: "s2"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(bothPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultSite != "prod" || len(cfg.Sites) != 2 {
		t.Errorf("after the second add: default %q, sites %v", cfg.DefaultSite, cfg.Sites)
	}
	if !reflect.DeepEqual(dropped, []string{"prod", "staging"}) {
		t.Errorf("dropped caches = %v", dropped)
	}
}
