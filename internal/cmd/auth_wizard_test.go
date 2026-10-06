package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func TestCheckNameOnce(t *testing.T) {
	calls := 0
	check := checkNameOnce(func(string) error { calls++; return nil })
	_ = check("a")
	_ = check("a")
	_ = check("b")
	if calls != 2 {
		t.Fatalf("check called %d times, want 2 (once per distinct name)", calls)
	}
}

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
				if err := writeInitConfig(path, name, site); err != nil {
					t.Fatalf("writeInitConfig: %v", err)
				}
				if err := addSiteToConfig(path, "other", config.SiteConfig{URL: "https://other.example", APIKey: "k", APISecret: "s"}); err != nil {
					t.Fatalf("addSiteToConfig: %v", err)
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
				if authLabel(*loaded) == "none" {
					t.Errorf("authLabel(%s) = none", kind)
				}

				st, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				// Windows has no Unix permission bits; Go reports 0666/0777 there.
				if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
					t.Errorf("config mode = %v, want 0600", st.Mode().Perm())
				}
				if st, _ := os.Stat(filepath.Dir(path)); runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
					t.Errorf("config dir mode = %v, want 0700", st.Mode().Perm())
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
	if err := addSiteToConfig(path, "Prod", config.SiteConfig{URL: "https://p.example", Username: "u", Password: "p"}); err != nil {
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

func TestSetConfigValuesOnNullDefault(t *testing.T) {
	t.Setenv("FFC_URL", "")
	t.Setenv("FFC_API_KEY", "")
	t.Setenv("FFC_API_SECRET", "")
	for _, dflt := range []string{"default_site: ~", "default_site:", "default_site: null", `default_site: ""`} {
		t.Run(dflt, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			content := dflt + "\nnumber_format: ~\nsites:\n  dev:\n    url: https://dev.example\n    api_key: k\n    api_secret: s\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			err := setConfigValues(path, []configValue{{"default_site", "dev"}, {"number_format", "us"}})
			if err != nil {
				t.Fatalf("setConfigValues: %v", err)
			}
			site, err := config.Load("", path)
			if err != nil {
				t.Fatalf("config.Load after set: %v", err)
			}
			if site.Name != "dev" {
				t.Errorf("default site = %q, want dev", site.Name)
			}
			cfg, _ := config.Read(path)
			if cfg.NumberFormat != "us" {
				t.Errorf("number_format = %q, want us", cfg.NumberFormat)
			}
		})
	}
}

func TestSetConfigValuesValidatesAndSkipsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "# keep me\ndefault_site:   \"dev\"   # inline\nsites:\n    dev: {url: 'https://dev.example', api_key: k, api_secret: s}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setConfigValues(path, []configValue{{"default_site", "nope"}}); err == nil ||
		!strings.Contains(err.Error(), "available: dev") {
		t.Errorf("unknown site: err = %v, want 'not found ... available: dev'", err)
	}
	if err := setConfigValues(path, []configValue{{"default_site", "dev"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != content {
		t.Errorf("unchanged value rewrote the file:\n%s", raw)
	}
}

func TestAuthLabel(t *testing.T) {
	tests := []struct {
		site config.SiteConfig
		want string
	}{
		{config.SiteConfig{AccessToken: "t", APIKey: "k", APISecret: "s"}, "OAuth 2.0"},
		{config.SiteConfig{APIKey: "k", APISecret: "s", Username: "u", Password: "p"}, "API Key"},
		{config.SiteConfig{APIKey: "k", Username: "u", Password: "p"}, "Username/Password"},
		{config.SiteConfig{APIKey: "k"}, "none"},
		{config.SiteConfig{Username: "u"}, "none"},
		{config.SiteConfig{OAuthClientID: "c"}, "none"},
	}
	for _, tt := range tests {
		if got := authLabel(tt.site); got != tt.want {
			t.Errorf("authLabel(%+v) = %q, want %q", tt.site, got, tt.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"dev":             "dev",
		"erp.example.com": "erp.example.com",
		"#dev":            "'#dev'",
		"it's":            `'it'\''s'`,
	}
	for in, want := range tests {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
