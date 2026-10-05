package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadKeepsSiteNameCaseAndDots(t *testing.T) {
	t.Setenv("FFC_URL", "")
	t.Setenv("FFC_API_KEY", "")
	t.Setenv("FFC_API_SECRET", "")
	p := writeTemp(t, `default_site: Prod
sites:
  Prod:
    url: https://a.example
    api_key: k
    api_secret: s
  erp.example.com:
    url: https://b.example
    api_key: k2
    api_secret: s2
`)
	s, err := Load("", p)
	if err != nil || s.Name != "Prod" || s.URL != "https://a.example" {
		t.Fatalf("Load default Prod = %+v, %v", s, err)
	}
	s, err = Load("erp.example.com", p)
	if err != nil || s.URL != "https://b.example" {
		t.Fatalf("Load dotted = %+v, %v", s, err)
	}
	// Exact match first; otherwise a unique case-insensitive match.
	if s, err := Load("prod", p); err != nil || s.Name != "Prod" {
		t.Fatalf("Load prod = %+v, %v; want the Prod site", s, err)
	}
	ambiguous := writeTemp(t, `sites:
  Dev:
    url: https://a.example
    api_key: k
    api_secret: s
  DEV:
    url: https://b.example
    api_key: k
    api_secret: s
`)
	if s, err := Load("DEV", ambiguous); err != nil || s.URL != "https://b.example" {
		t.Fatalf("exact match = %+v, %v", s, err)
	}
	if _, err := Load("dev", ambiguous); err == nil {
		t.Fatal("an ambiguous case-insensitive match must fail")
	}
}

func TestLoadExplicitMissingConfigIsError(t *testing.T) {
	t.Setenv("FFC_URL", "https://env.example")
	if _, err := Load("", filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("explicit --config that does not exist must fail, not fall back to env")
	}
}

func TestEnvOverrides(t *testing.T) {
	p := writeTemp(t, `default_site: dev
sites:
  dev:
    url: https://stored.example
    access_token: tok
`)
	t.Run("url alone never redirects stored credentials", func(t *testing.T) {
		t.Setenv("FFC_URL", "https://evil.example")
		t.Setenv("FFC_API_KEY", "")
		t.Setenv("FFC_API_SECRET", "")
		if s, err := Load("", p); err == nil || !strings.Contains(err.Error(), "FFC_URL") {
			t.Fatalf("got %+v, %v; want an FFC_URL error", s, err)
		}
	})
	t.Run("url equal to the stored one is fine", func(t *testing.T) {
		t.Setenv("FFC_URL", "https://stored.example")
		t.Setenv("FFC_API_KEY", "")
		t.Setenv("FFC_API_SECRET", "")
		if s, err := Load("", p); err != nil || s.AccessToken != "tok" {
			t.Fatalf("got %+v, %v", s, err)
		}
	})
	t.Run("site lookup falls back to a unique case-insensitive match", func(t *testing.T) {
		t.Setenv("FFC_URL", "")
		t.Setenv("FFC_API_KEY", "")
		t.Setenv("FFC_API_SECRET", "")
		s, err := Load("DEV", p)
		if err != nil || s.Name != "dev" {
			t.Fatalf("got %+v, %v", s, err)
		}
	})
	t.Run("env key pair replaces stored auth", func(t *testing.T) {
		t.Setenv("FFC_URL", "https://other.example")
		t.Setenv("FFC_API_KEY", "k")
		t.Setenv("FFC_API_SECRET", "s")
		s, err := Load("", p)
		if err != nil || s.URL != "https://other.example" || s.AccessToken != "" || s.APIKey != "k" {
			t.Fatalf("got %+v, %v", s, err)
		}
	})
}

func TestEditPreservesHeaderAndFixesNullDefault(t *testing.T) {
	p := writeTemp(t, `# line1
# line2

# about default
default_site: ~
sites:
  dev:
    url: https://a.example
`)
	if err := Edit(p, func(f *File) error { f.Set("default_site", "dev"); return nil }); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	out := string(raw)
	for _, want := range []string{"# line1", "# line2", "# about default", "default_site: dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "!!null") {
		t.Errorf("null tag leaked:\n%s", out)
	}
	if s, err := Load("", p); err != nil || s.Name != "dev" {
		t.Fatalf("reload: %+v %v", s, err)
	}
	// Windows has no Unix permission bits; Go reports 0666/0777 there.
	if st, _ := os.Stat(p); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", st.Mode().Perm())
	}
}

func TestPutSiteRoundTripsTrickyValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.yaml")
	names := []string{"#dev", "dev:", "null", "~", "!x", "Prod", "erp.example.com", "[x]"}
	secret := "p@ss: #\"'\\\n\xff\xfe"
	err := Overwrite(p, func(f *File) error {
		for _, n := range names {
			if err := f.PutSite(n, SiteConfig{URL: "https://x.example", Username: "u", Password: secret}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultSite != "#dev" {
		t.Errorf("default = %q, want first site", cfg.DefaultSite)
	}
	for _, n := range names {
		s, ok := cfg.Sites[n]
		if !ok {
			t.Errorf("site %q lost", n)
			continue
		}
		if s.Password != strings.ToValidUTF8(secret, "\uFFFD") && s.Password != secret {
			t.Errorf("site %q password = %q", n, s.Password)
		}
	}
	// Windows has no Unix permission bits; Go reports 0666/0777 there.
	if st, _ := os.Stat(filepath.Dir(p)); runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Errorf("dir perm = %v, want 0700", st.Mode().Perm())
	}
}

func TestRemoveSiteMovesDefault(t *testing.T) {
	p := writeTemp(t, "default_site: a\nsites:\n  a: {url: x}\n  b: {url: y}\n")
	if err := Edit(p, func(f *File) error { return f.RemoveSite("a") }); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Read(p)
	if cfg.DefaultSite != "b" || len(cfg.Sites) != 1 {
		t.Fatalf("got %+v", cfg)
	}
	if err := Edit(p, func(f *File) error { return f.RemoveSite("zzz") }); err == nil {
		t.Fatal("removing a missing site must fail")
	}
}

func TestSetSiteTokensKeepsOtherKeys(t *testing.T) {
	p := writeTemp(t, "default_site: a\nsites:\n  a:\n    url: x  # my site\n    oauth_client_id: cid\n")
	if err := Edit(p, func(f *File) error { return f.SetSiteTokens("a", "AT", "RT", 42) }); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Read(p)
	s := cfg.Sites["a"]
	if s.AccessToken != "AT" || s.RefreshToken != "RT" || s.TokenExpiry != 42 || s.OAuthClientID != "cid" {
		t.Fatalf("got %+v", s)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "# my site") {
		t.Errorf("comment lost:\n%s", raw)
	}
}

func TestEditFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	os.WriteFile(target, []byte("default_site: a\n"), 0o600)
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unsupported")
	}
	if err := Edit(link, func(f *File) error { f.Set("number_format", "us"); return nil }); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a regular file")
	}
	raw, _ := os.ReadFile(target)
	if !strings.Contains(string(raw), "number_format: us") {
		t.Fatalf("target not updated: %s", raw)
	}
}

func TestEditUnchangedSkipsWrite(t *testing.T) {
	p := writeTemp(t, "default_site:   a   # keep my formatting\n")
	if err := Edit(p, func(*File) error { return ErrUnchanged }); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != "default_site:   a   # keep my formatting\n" {
		t.Fatalf("file rewritten: %q", raw)
	}
	if _, err := os.Stat(p + ".lock"); !os.IsNotExist(err) {
		t.Fatal("lock file left behind")
	}
}

func TestLockReleaseKeepsAnotherHoldersLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	lock := path + ".lock"

	release1, err := lockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The first holder stalls past lockStale; a second process breaks the lock.
	old := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	release2, err := lockFile(path)
	if err != nil {
		t.Fatalf("breaking a stale lock: %v", err)
	}

	release1() // the stalled holder finishes late
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("first holder removed the second holder's lock: %v", err)
	}
	release2()
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("lock still present after release: %v", err)
	}
}

func TestRenameSite(t *testing.T) {
	// A key parsed as a number must load as a string name after the rename.
	p := writeTemp(t, "default_site: 8000\nsites:\n  8000: {url: x}\n  Prod: {url: y}\n")
	if err := Edit(p, func(f *File) error { return f.RenameSite("8000", "dev") }); err != nil {
		t.Fatal(err)
	}
	cfg, err := Read(p)
	if err != nil || cfg.DefaultSite != "dev" || cfg.Sites["dev"].URL != "x" {
		t.Fatalf("got %+v, %v", cfg, err)
	}
	// A numeric new name round-trips too.
	if err := Edit(p, func(f *File) error { return f.RenameSite("dev", "9000") }); err != nil {
		t.Fatal(err)
	}
	if cfg, err := Read(p); err != nil || cfg.Sites["9000"].URL != "x" || cfg.DefaultSite != "9000" {
		t.Fatalf("got %+v, %v", cfg, err)
	}

	// A name differing only in case from another site is taken.
	if err := Edit(p, func(f *File) error { return f.RenameSite("9000", "prod") }); err == nil {
		t.Fatal("rename to a case variant of another site must fail")
	}
	// Changing the case of the site itself is allowed.
	if err := Edit(p, func(f *File) error { return f.RenameSite("Prod", "PROD") }); err != nil {
		t.Fatal(err)
	}

	// default_site that resolves case-insensitively follows the rename.
	p = writeTemp(t, "default_site: prod\nsites:\n  Prod: {url: y}\n  dev: {url: x}\n")
	if err := Edit(p, func(f *File) error { return f.RenameSite("Prod", "live") }); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := Read(p); cfg.DefaultSite != "live" {
		t.Fatalf("default_site = %q, want live", cfg.DefaultSite)
	}
}

func TestMCPPolicy(t *testing.T) {
	t.Setenv("FFC_URL", "")
	t.Setenv("FFC_API_KEY", "")
	t.Setenv("FFC_API_SECRET", "")
	p := writeTemp(t, `default_site: dev
sites:
  dev:
    url: https://a.example
    access_token: tok
    mcp:
      read_only: true
      allow_doctypes: [ToDo, User]
      deny_methods: ["frappe.x.*"]
`)
	s, err := Load("", p)
	if err != nil || s.MCP == nil || !s.MCP.ReadOnly || len(s.MCP.AllowDoctypes) != 2 || s.MCP.DenyMethods[0] != "frappe.x.*" {
		t.Fatalf("got %+v, %v", s.MCP, err)
	}

	t.Run("env credentials keep the policy", func(t *testing.T) {
		t.Setenv("FFC_API_KEY", "k")
		t.Setenv("FFC_API_SECRET", "s")
		if s, err := Load("", p); err != nil || s.MCP == nil || !s.MCP.ReadOnly {
			t.Fatalf("got %+v, %v", s, err)
		}
	})

	t.Run("a misspelt key is an error", func(t *testing.T) {
		bad := writeTemp(t, "default_site: dev\nsites:\n  dev:\n    url: https://a.example\n    mcp:\n      readonly: true\n")
		if _, err := Load("", bad); err == nil || !strings.Contains(err.Error(), `unknown mcp policy key "readonly"`) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("replacing a site's credentials keeps its policy", func(t *testing.T) {
		err := Edit(p, func(f *File) error {
			return f.PutSite("dev", SiteConfig{URL: "https://a.example", APIKey: "k2", APISecret: "s2"})
		})
		if err != nil {
			t.Fatal(err)
		}
		s, err := Load("", p)
		if err != nil || s.APIKey != "k2" || s.AccessToken != "" || s.MCP == nil || !s.MCP.ReadOnly {
			t.Fatalf("got %+v, %v", s, err)
		}
	})
}
