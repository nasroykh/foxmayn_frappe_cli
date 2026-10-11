package services

import (
	"context"
	"errors"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// hostOnly hides every dialog of a host: it has only the Host methods.
type hostOnly struct{ Host }

func errKey(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Key
	}
	return ""
}

func TestChangeURLAsksBeforeSendingTheSignIn(t *testing.T) {
	ctx := context.Background()
	fake, other := frappetest.New(t), frappetest.New(t)
	s, h, path := newSites(t)
	if _, err := s.AddWithAPIKey(ctx, APIKeyRequest{Name: "a", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}); err != nil {
		t.Fatal(err)
	}

	// Declined: nothing is sent to the new address and nothing changes.
	h.siteNo = true
	if _, err := s.ChangeURL(ctx, "a", other.URL); code(err) != CodeCancelled || errKey(err) != "policy.declined" {
		t.Fatalf("declined ChangeURL = %v", err)
	}
	if n := len(other.Requests()); n != 0 {
		t.Fatalf("%d requests reached the new address after a decline", n)
	}
	if want := "address a " + fake.URL + " -> " + other.URL; len(h.siteAsks) != 1 || h.siteAsks[0] != want {
		t.Fatalf("asked %q, want %q", h.siteAsks, want)
	}

	// The same address asks nothing.
	h.siteNo = false
	if _, err := s.ChangeURL(ctx, "a", fake.URL); err != nil || len(h.siteAsks) != 1 {
		t.Fatalf("same address: %v, asked %q", err, h.siteAsks)
	}

	// A host that cannot ask cannot send the sign-in elsewhere.
	s.host = hostOnly{h}
	if _, err := s.ChangeURL(ctx, "a", other.URL); errKey(err) != "policy.cannotAskSite" {
		t.Fatalf("ChangeURL without a dialog = %v", err)
	}
	if cfg, _ := config.Read(path); cfg.Sites["a"].URL != fake.URL {
		t.Fatalf("address changed to %q", cfg.Sites["a"].URL)
	}
}

func TestReplaceSiteAsks(t *testing.T) {
	ctx := context.Background()
	fake, other := frappetest.New(t), frappetest.New(t)
	s, h, path := newSites(t)
	req := APIKeyRequest{Name: "a", URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret}
	if _, err := s.AddWithAPIKey(ctx, req); err != nil {
		t.Fatal(err)
	}
	if len(h.siteAsks) != 0 {
		t.Fatalf("a new site asked %q", h.siteAsks)
	}

	h.siteNo = true
	req.URL, req.Replace = other.URL, true
	if _, err := s.AddWithAPIKey(ctx, req); errKey(err) != "policy.declined" {
		t.Fatalf("declined replace = %v", err)
	}
	if n := len(other.Requests()); n != 0 {
		t.Fatalf("%d requests reached the new address after a decline", n)
	}
	if cfg, _ := config.Read(path); cfg.Sites["a"].URL != fake.URL {
		t.Fatalf("site replaced after a decline: %q", cfg.Sites["a"].URL)
	}

	h.siteNo = false
	if _, err := s.AddWithAPIKey(ctx, req); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := config.Read(path); cfg.Sites["a"].URL != other.URL || len(h.siteAsks) != 2 {
		t.Fatalf("replace: url %q, asked %q", cfg.Sites["a"].URL, h.siteAsks)
	}
}

func TestLocalOnlyOffAsks(t *testing.T) {
	g := newAssistantRig(t)
	h := g.a.host.(*fakeHost)
	if _, err := g.a.SaveSiteSettings(SiteSettings{Site: "prod", LocalOnly: true}); err != nil {
		t.Fatal(err)
	}
	// Turning it on, or saving while off, asks nothing.
	if _, err := g.a.SaveSiteSettings(SiteSettings{Site: "prod", LocalOnly: true, Instructions: "x"}); err != nil || len(h.siteAsks) != 0 {
		t.Fatalf("saving with local-only on: %v, asked %q", err, h.siteAsks)
	}

	h.siteNo = true
	if _, err := g.a.SaveSiteSettings(SiteSettings{Site: "prod"}); errKey(err) != "policy.declined" {
		t.Fatalf("declined local-only off = %v", err)
	}
	if ss, _ := g.a.GetSiteSettings("prod"); !ss.LocalOnly || ss.Instructions != "x" {
		t.Fatalf("settings changed after a decline: %+v", ss)
	}

	h.siteNo = false
	if ss, err := g.a.SaveSiteSettings(SiteSettings{Site: "prod"}); err != nil || ss.LocalOnly {
		t.Fatalf("local-only off = %+v, %v", ss, err)
	}
	if len(h.siteAsks) != 2 || h.siteAsks[1] != "localOff prod" {
		t.Fatalf("asked %q", h.siteAsks)
	}
	if _, err := g.a.SaveSiteSettings(SiteSettings{Site: "prod"}); err != nil || len(h.siteAsks) != 2 {
		t.Fatalf("saving while off: %v, asked %q", err, h.siteAsks)
	}
}
