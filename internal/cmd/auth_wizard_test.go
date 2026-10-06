package cmd

import (
	"os"
	"path/filepath"
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
		// The three kinds the setup writes (sitesetup's store round trip).
		{config.SiteConfig{URL: "https://a.example", APIKey: "k", APISecret: "s"}, "API Key"},
		{config.SiteConfig{URL: "http://localhost:8000", Username: "user@example.com", Password: "p"}, "Username/Password"},
		{config.SiteConfig{URL: "https://o.example", OAuthClientID: "cid", OAuthClientSecret: "csec",
			AccessToken: "at", RefreshToken: "rt", TokenExpiry: 1893456000}, "OAuth 2.0"},
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
