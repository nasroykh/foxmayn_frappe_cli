package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitesetup"
)

func errKey(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Key
	}
	return ""
}

func TestSameServer(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		want     bool
	}{
		{"identical", "https://erp.example.com", "https://erp.example.com", true},
		{"host case", "https://erp.example.com", "https://ERP.Example.com", true},
		{"http to https", "http://erp.example.com", "https://erp.example.com", true},
		{"https to http", "https://erp.example.com", "http://erp.example.com", false},
		{"other host", "https://erp.example.com", "https://erp.example.org", false},
		{"subdomain", "https://erp.example.com", "https://a.erp.example.com", false},
		{"explicit https port", "https://erp.example.com", "https://erp.example.com:443", true},
		{"explicit http port", "http://erp.example.com:80", "http://erp.example.com", true},
		{"other port", "https://erp.example.com", "https://erp.example.com:8443", false},
		{"port on both", "http://localhost:8000", "http://localhost:8000", true},
		{"different port on both", "http://localhost:8000", "http://localhost:8001", false},
		{"http port moves to https", "http://erp.example.com:8000", "https://erp.example.com", false},
		{"unparsable", "https://erp.example.com", "https://[bad", false},
		{"no host", "", "https://erp.example.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SameServer(c.old, c.new); got != c.want {
				t.Errorf("SameServer(%q, %q) = %v, want %v", c.old, c.new, got, c.want)
			}
		})
	}
}

// authHeaders lists the Authorization header of every request a fake got.
func authHeaders(site *frappetest.Site) []string {
	var out []string
	for _, r := range site.Requests() {
		if h := r.Header.Get("Authorization"); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// A site on a server with an old key, as a config written earlier holds it.
func seedAPIKeySite(t *testing.T, path, url string) {
	t.Helper()
	site := config.SiteConfig{URL: url, APIKey: "old-key", APISecret: "old-secret", MCP: &config.MCPPolicy{ReadOnly: true, Confirm: "always"}}
	if err := (sitesetup.Store{Path: path}).Init("a", site); err != nil {
		t.Fatal(err)
	}
}

func TestChangeURLSameServerKeepsTheSignIn(t *testing.T) {
	ctx := context.Background()
	fake := frappetest.New(t)
	s, _, path := newSites(t)
	if _, err := s.AddWithAPIKey(ctx, APIKeyRequest{Name: "a", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); err != nil {
		t.Fatal(err)
	}
	if need, err := s.NeedsSignIn("a", fake.URL); err != nil || need {
		t.Fatalf("NeedsSignIn same server = %v, %v", need, err)
	}
	// A sign-in given for the same server is ignored: the saved one is used.
	got, err := s.ChangeURL(ctx, "a", fake.URL, NewSignIn{APIKey: "other", APISecret: "other"})
	if err != nil || got != fake.URL {
		t.Fatalf("ChangeURL = %q, %v", got, err)
	}
	cfg, _ := config.Read(path)
	if site := cfg.Sites["a"]; site.APIKey != frappetest.APIKey || site.APISecret != frappetest.APISecret {
		t.Fatalf("sign-in changed: %+v", site)
	}
	for _, h := range authHeaders(fake) {
		if strings.Contains(h, "other") {
			t.Fatalf("the typed sign-in was sent to the same server: %q", h)
		}
	}
}

func TestChangeURLOtherServerNeedsASignIn(t *testing.T) {
	ctx := context.Background()
	fake, other := frappetest.New(t), frappetest.New(t)
	s, _, path := newSites(t)
	seedAPIKeySite(t, path, fake.URL)

	// This fake and the other one are both 127.0.0.1, on different ports.
	if need, err := s.NeedsSignIn("a", other.URL); err != nil || !need {
		t.Fatalf("NeedsSignIn other server = %v, %v", need, err)
	}
	// Without a sign-in, or with half of one, nothing is sent anywhere.
	for _, sign := range []NewSignIn{{}, {APIKey: "k"}, {APISecret: "s"}, {Username: frappetest.Username, Password: frappetest.Password}} {
		if _, err := s.ChangeURL(ctx, "a", other.URL, sign); code(err) != CodeInvalid || errKey(err) != "site.newServer" {
			t.Fatalf("ChangeURL %+v = %v", sign, err)
		}
	}
	if n := len(other.Requests()); n != 0 {
		t.Fatalf("%d requests reached the new server without a sign-in", n)
	}
	if cfg, _ := config.Read(path); cfg.Sites["a"].URL != fake.URL || cfg.Sites["a"].APIKey != "old-key" {
		t.Fatalf("config changed: %+v", cfg.Sites["a"])
	}

	// With a sign-in: verified there with the new key, saved with the new
	// address, and the site's safety rules stay.
	got, err := s.ChangeURL(ctx, "a", other.URL, NewSignIn{APIKey: " " + frappetest.APIKey + " ", APISecret: frappetest.APISecret})
	if err != nil || got != other.URL {
		t.Fatalf("ChangeURL = %q, %v", got, err)
	}
	want := "token " + frappetest.APIKey + ":" + frappetest.APISecret
	hs := authHeaders(other)
	if len(hs) == 0 {
		t.Fatal("the new server was not asked")
	}
	for _, h := range hs {
		if h != want {
			t.Fatalf("new server saw %q, want %q", h, want)
		}
	}
	for _, r := range fake.Requests() {
		if strings.Contains(r.Header.Get("Authorization"), frappetest.APIKey) {
			t.Fatalf("the new key reached the old server")
		}
	}
	cfg, _ := config.Read(path)
	site := cfg.Sites["a"]
	if site.URL != other.URL || site.APIKey != frappetest.APIKey || site.APISecret != frappetest.APISecret {
		t.Fatalf("config = %+v", site)
	}
	if site.MCP == nil || !site.MCP.ReadOnly || site.MCP.Confirm != "always" {
		t.Fatalf("the mcp block was lost: %+v", site.MCP)
	}
	if listed, err := s.List(); err != nil || len(listed.Sites) != 1 || listed.Sites[0].URL != other.URL {
		t.Fatalf("List = %+v, %v", listed, err)
	}
}

// The new server refuses the typed sign-in: nothing is saved.
func TestChangeURLOtherServerRefusingSignIn(t *testing.T) {
	ctx := context.Background()
	fake, other := frappetest.New(t), frappetest.New(t)
	s, _, path := newSites(t)
	seedAPIKeySite(t, path, fake.URL)
	if _, err := s.ChangeURL(ctx, "a", other.URL, NewSignIn{APIKey: "wrong", APISecret: "wrong"}); err == nil {
		t.Fatal("ChangeURL with a wrong key succeeded")
	}
	if cfg, _ := config.Read(path); cfg.Sites["a"].URL != fake.URL || cfg.Sites["a"].APIKey != "old-key" {
		t.Fatalf("config changed: %+v", cfg.Sites["a"])
	}
}

func TestChangeURLOtherServerPasswordSite(t *testing.T) {
	ctx := context.Background()
	fake, other := frappetest.New(t), frappetest.New(t)
	s, _, path := newSites(t)
	if err := (sitesetup.Store{Path: path}).Init("p", config.SiteConfig{URL: fake.URL, Username: "old", Password: "old"}); err != nil {
		t.Fatal(err)
	}
	// An API key does not stand in for a password.
	if _, err := s.ChangeURL(ctx, "p", other.URL, NewSignIn{APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); errKey(err) != "site.newServer" {
		t.Fatalf("API key for a password site = %v", err)
	}
	if n := len(other.Requests()); n != 0 {
		t.Fatalf("%d requests reached the new server", n)
	}
	if _, err := s.ChangeURL(ctx, "p", other.URL, NewSignIn{Username: frappetest.Username, Password: frappetest.Password}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Read(path)
	if site := cfg.Sites["p"]; site.URL != other.URL || site.Username != frappetest.Username || site.Password != frappetest.Password || site.APIKey != "" {
		t.Fatalf("config = %+v", site)
	}
	if other.Logins() == 0 {
		t.Fatal("the new server saw no login")
	}
}

func TestNeedsSignIn(t *testing.T) {
	s, _, path := newSites(t)
	seedAPIKeySite(t, path, "https://erp.example.com")
	for url, want := range map[string]bool{
		"https://erp.example.com":      false,
		"https://ERP.example.com:443/": false,
		"https://erp.example.org":      true,
		"https://erp.example.com:8443": true,
		"not a url":                    false, // ChangeURL reports it
	} {
		if got, err := s.NeedsSignIn("a", url); err != nil || got != want {
			t.Errorf("NeedsSignIn(%q) = %v, %v; want %v", url, got, err, want)
		}
	}
	if _, err := s.NeedsSignIn("nope", "https://erp.example.org"); code(err) != CodeNotFound {
		t.Errorf("unknown site: %v", err)
	}
}

// Replacing a saved site asks nothing here: the screen confirms first.
func TestReplaceSiteNeedsNoDialog(t *testing.T) {
	ctx := context.Background()
	fake, other := frappetest.New(t), frappetest.New(t)
	s, _, path := newSites(t)
	req := APIKeyRequest{Name: "a", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}
	if _, err := s.AddWithAPIKey(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWithAPIKey(ctx, APIKeyRequest{Name: "a", URL: other.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); code(err) != CodeExists {
		t.Fatalf("replace without the flag = %v", err)
	}
	req.URL, req.Replace = other.URL, true
	if _, err := s.AddWithAPIKey(ctx, req); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Read(path); cfg.Sites["a"].URL != other.URL {
		t.Fatalf("site not replaced: %q", cfg.Sites["a"].URL)
	}
}

// Turning Local models only off saves without a question: the screen asks.
func TestLocalOnlyOffSavesDirectly(t *testing.T) {
	g := newAssistantRig(t)
	h := g.a.host.(*fakeHost)
	if _, err := g.a.SaveSiteSettings(SiteSettings{Site: "prod", LocalOnly: true, Instructions: "x"}); err != nil {
		t.Fatal(err)
	}
	ss, err := g.a.SaveSiteSettings(SiteSettings{Site: "prod", Instructions: "x"})
	if err != nil || ss.LocalOnly {
		t.Fatalf("local-only off = %+v, %v", ss, err)
	}
	if got, _ := g.a.GetSiteSettings("prod"); got.LocalOnly || got.Instructions != "x" {
		t.Fatalf("stored %+v", got)
	}
	if len(h.opened) != 0 || len(h.files) != 0 || len(h.events) != 0 {
		t.Fatalf("host used: opened %v, files %v, events %v", h.opened, h.files, h.events)
	}
}
