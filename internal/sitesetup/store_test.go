package sitesetup

import (
	"os"
	"path/filepath"
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
