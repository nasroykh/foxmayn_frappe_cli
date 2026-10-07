package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

const relBase = "https://github.com/nasroykh/foxmayn_frappe_cli/releases/tag/"

// updateService is an AppService running version cur against a fake release
// list answered by h.
func updateService(t *testing.T, cur string, h http.HandlerFunc) *AppService {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := NewAppService(&fakeHost{}, "/x/config.yaml", fakeLocator("darwin", "/Users/me", "", nil))
	s.version = cur
	s.releasesURL = srv.URL
	return s
}

func listJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// mixed is a list in GitHub's order (not semver order): CLI tags, a draft,
// prereleases, tags that do not parse and a hostile html_url.
const mixed = `[
 {"tag_name":"v9.9.9","html_url":"` + relBase + `v9.9.9"},
 {"tag_name":"desktop-v0.2.0","html_url":"` + relBase + `desktop-v0.2.0","published_at":"2026-10-01T10:00:00Z"},
 {"tag_name":"desktop-v0.4.0","draft":true,"html_url":"` + relBase + `desktop-v0.4.0"},
 {"tag_name":"desktop-v0.3.0-rc.1","prerelease":true,"html_url":"` + relBase + `desktop-v0.3.0-rc.1"},
 {"tag_name":"desktop-vbeta","html_url":"` + relBase + `desktop-vbeta"},
 {"tag_name":"desktop-v1.2","html_url":"` + relBase + `desktop-v1.2"},
 {"tag_name":"desktop-v01.0.0","html_url":"` + relBase + `desktop-v01.0.0"},
 {"tag_name":"desktop-v0.10.0","html_url":"https://evil.example/releases/x","published_at":"not a time"},
 {"tag_name":"desktop-v0.9.0","html_url":"` + relBase + `desktop-v0.9.0"}
]`

func TestCheckForUpdatePicksHighestDesktopSemver(t *testing.T) {
	s := updateService(t, "0.1.0", listJSON(mixed))
	got, err := s.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 0.10.0 beats 0.9.0 numerically even though it is listed after 0.2.0;
	// the draft 0.4.0 and the CLI v9.9.9 are skipped.
	if !got.Available || got.Latest != "0.10.0" || got.Current != "0.1.0" {
		t.Fatalf("got %+v", got)
	}
	// The html_url is not under this repository's releases: fall back to the tag page.
	if got.URL != relBase+"desktop-v0.10.0" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.PublishedAt != "" {
		t.Errorf("PublishedAt = %q, want empty for an unparsable time", got.PublishedAt)
	}
}

func TestCheckForUpdateURLAndDate(t *testing.T) {
	s := updateService(t, "0.1.0", listJSON(`[{"tag_name":"desktop-v0.2.0","html_url":"`+relBase+`desktop-v0.2.0","published_at":"2026-10-01T12:00:00+02:00"}]`))
	got, err := s.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != relBase+"desktop-v0.2.0" || got.PublishedAt != "2026-10-01T10:00:00Z" {
		t.Errorf("got %+v", got)
	}

	for _, bad := range []string{
		"http://github.com/nasroykh/foxmayn_frappe_cli/releases/tag/x",
		"https://github.com.evil.example/nasroykh/foxmayn_frappe_cli/releases/tag/x",
		"https://github.com/other/repo/releases/tag/x",
		"https://github.com/nasroykh/foxmayn_frappe_cli/issues/1",
		"https://github.com/nasroykh/foxmayn_frappe_cli/releases/",
		"https://user@github.com/nasroykh/foxmayn_frappe_cli/releases/tag/x",
		"javascript:alert(1)",
		"",
	} {
		r := &updateRelease{TagName: "desktop-v0.2.0", HTMLURL: bad}
		if u := releasePageURL(r); u != relBase+"desktop-v0.2.0" {
			t.Errorf("html_url %q gave %q", bad, u)
		}
	}
}

func TestCheckForUpdateUpToDate(t *testing.T) {
	list := listJSON(`[{"tag_name":"desktop-v0.2.0"},{"tag_name":"desktop-v0.1.0"}]`)
	for _, cur := range []string{"0.2.0", "v0.2.0", "0.3.0", "0.2.0+build.5"} {
		got, err := updateService(t, cur, list).CheckForUpdate(context.Background())
		if err != nil || got.Available {
			t.Errorf("current %s: %+v, %v", cur, got, err)
		}
	}
	// No desktop release at all.
	got, err := updateService(t, "0.1.0", listJSON(`[{"tag_name":"v1.11.0"}]`)).CheckForUpdate(context.Background())
	if err != nil || got.Available || got.Latest != "" {
		t.Errorf("no desktop release: %+v, %v", got, err)
	}
}

func TestCheckForUpdatePrereleaseRule(t *testing.T) {
	list := listJSON(`[
	 {"tag_name":"desktop-v1.1.0-rc.1","prerelease":true},
	 {"tag_name":"desktop-v1.0.1-beta.2"},
	 {"tag_name":"desktop-v1.0.0"}]`)
	cases := []struct {
		cur    string
		avail  bool
		latest string
	}{
		{"0.9.0", true, "1.1.0-rc.1"}, // 0.x: prereleases count
		{"1.0.0", false, "1.0.0"},     // stable 1.x: they do not
		{"0.9.0-rc.1", true, "1.1.0-rc.1"},
		{"1.0.0-rc.1", true, "1.1.0-rc.1"}, // itself a prerelease
		{"1.1.0-rc.1", false, "1.1.0-rc.1"},
		{"0.1.0", true, "1.1.0-rc.1"},
	}
	for _, c := range cases {
		got, err := updateService(t, c.cur, list).CheckForUpdate(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", c.cur, err)
		}
		if got.Available != c.avail || got.Latest != c.latest {
			t.Errorf("current %s: available=%v latest=%q, want %v %q", c.cur, got.Available, got.Latest, c.avail, c.latest)
		}
	}
	// A flagged prerelease with a plain tag counts as one on 1.x.
	got, err := updateService(t, "1.0.0", listJSON(`[{"tag_name":"desktop-v1.2.0","prerelease":true}]`)).CheckForUpdate(context.Background())
	if err != nil || got.Available || got.Latest != "" {
		t.Errorf("flagged prerelease on 1.x: %+v, %v", got, err)
	}
}

func TestCheckForUpdateDevBuildNeverOffers(t *testing.T) {
	var hits atomic.Int32
	for _, cur := range []string{"0.0.0-dev", "dev", "", "unknown", "1.2", "1.2.3.4", "v", "01.2.3"} {
		s := updateService(t, cur, func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			_, _ = w.Write([]byte(`[{"tag_name":"desktop-v9.0.0"}]`))
		})
		got, err := s.CheckForUpdate(context.Background())
		if err != nil || got.Available || got.Latest != "" || got.Current != cur {
			t.Errorf("current %q: %+v, %v", cur, got, err)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("a version that never gets an update made %d requests", hits.Load())
	}
}

func TestCheckForUpdateHTTPErrors(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusForbidden, CodeUnavailable},
		{http.StatusTooManyRequests, CodeUnavailable},
		{http.StatusInternalServerError, CodeFailed},
		{http.StatusNotFound, CodeFailed},
	}
	for _, c := range cases {
		s := updateService(t, "0.1.0", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"message":"nope"}`, c.status)
		})
		_, err := s.CheckForUpdate(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Code != c.code || !strings.Contains(e.Detail, "HTTP") {
			t.Errorf("status %d: %v", c.status, err)
		}
	}

	_, err := updateService(t, "0.1.0", listJSON(`{"not":"a list"}`)).CheckForUpdate(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeFailed {
		t.Errorf("bad JSON: %v", err)
	}

	s := updateService(t, "0.1.0", listJSON(`[]`))
	s.releasesURL = "http://127.0.0.1:1/"
	if _, err := s.CheckForUpdate(context.Background()); !errors.As(err, &e) || e.Code != CodeNetwork {
		t.Errorf("unreachable: %v", err)
	}
}

func TestCheckForUpdateCancel(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := updateService(t, "0.1.0", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.CheckForUpdate(ctx)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeCancelled {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the check did not stop after cancel")
	}
}

func TestSemverCompare(t *testing.T) {
	order := []string{
		"0.0.1", "0.1.0-alpha", "0.1.0-alpha.1", "0.1.0-alpha.beta", "0.1.0-beta", "0.1.0-beta.2",
		"0.1.0-beta.11", "0.1.0-rc.1", "0.1.0", "0.1.1", "0.2.0", "0.10.0", "1.0.0-rc.1", "1.0.0", "2.0.0",
	}
	for i := range order {
		for j := range order {
			a, ok1 := parseSemver(order[i])
			b, ok2 := parseSemver(order[j])
			if !ok1 || !ok2 {
				t.Fatalf("parse %q %q", order[i], order[j])
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := a.compare(b); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	a, _ := parseSemver("v1.2.3+one")
	b, _ := parseSemver("1.2.3+two")
	if a.compare(b) != 0 || a.String() != "1.2.3" {
		t.Errorf("build metadata must not matter: %v %v", a, b)
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "1.2.x", "-1.2.3", "1.2.3-", "1.2.3-a..b", "01.2.3", "99999999999999999999.0.0", "v v1.2.3"} {
		if _, ok := parseSemver(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

// ffcService is updateService with an ffc of version ffcVer found on PATH.
func ffcService(t *testing.T, app, ffcVer string, h http.HandlerFunc) *AppService {
	t.Helper()
	s := updateService(t, app, h)
	s.ffc = fakeLocator("darwin", "/Users/me", "/usr/local/bin/ffc", nil)
	s.ffc.version = func(string) (string, error) { return ffcVer, nil }
	s.ffc.resolve = func(p string) (string, error) { return p, nil }
	return s
}

func TestCheckForUpdateFFC(t *testing.T) {
	// GitHub's order (newest first); the desktop release, the draft and the
	// prerelease never count, whatever their numbers.
	list := listJSON(`[
 {"tag_name":"desktop-v9.0.0"},
 {"tag_name":"v2.0.0","draft":true},
 {"tag_name":"v1.13.0-rc1","prerelease":true},
 {"tag_name":"v1.12.1"},
 {"tag_name":"v1.12.0"}
]`)
	for _, c := range []struct {
		installed string
		available bool
	}{{"1.12.0", true}, {"v1.11.0", true}, {"1.12.1", false}, {"1.13.0", false}, {"1.12.1-rc1", true}} {
		got, err := ffcService(t, "0.1.0", c.installed, list).CheckForUpdate(context.Background())
		if err != nil || got.FFC.Available != c.available || got.FFC.Latest != "1.12.1" || got.FFC.Current != c.installed {
			t.Errorf("ffc %s: %+v, %v", c.installed, got.FFC, err)
		}
		// The desktop answer is unchanged by the ffc one.
		if !got.Available || got.Latest != "9.0.0" {
			t.Errorf("ffc %s: desktop part %+v", c.installed, got)
		}
	}

	// The release named is the one the installer takes (release.Latest: the
	// first final v<digit> entry), so offer and install never differ. A
	// backport listed first is never offered as an "update" (no downgrade).
	backport := `[{"tag_name":"v1.11.5"},{"tag_name":"v1.12.0"}]`
	got, err := ffcService(t, "0.1.0", "1.12.0", listJSON(backport)).CheckForUpdate(context.Background())
	if err != nil || got.FFC.Available || got.FFC.Latest != "1.11.5" {
		t.Errorf("backport first: %+v, %v", got.FFC, err)
	}
	srv := httptest.NewServer(listJSON(backport))
	defer srv.Close()
	if r, err := release.Latest(context.Background(), srv.URL, 5*time.Second); err != nil || r.TagName != "v"+got.FFC.Latest {
		t.Errorf("installer picks %v, %v; the check named %s", r, err, got.FFC.Latest)
	}

	// A development build of the app still checks ffc; the desktop part stays empty.
	got, err = ffcService(t, "0.0.0-dev", "1.12.0", list).CheckForUpdate(context.Background())
	if err != nil || !got.FFC.Available || got.Available || got.Latest != "" {
		t.Errorf("dev app: %+v, %v", got, err)
	}

	// An ffc built from source is never offered a release; with a dev app too, nothing is asked.
	var hits atomic.Int32
	got, err = ffcService(t, "0.0.0-dev", "dev", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`[{"tag_name":"v9.0.0"}]`))
	}).CheckForUpdate(context.Background())
	if err != nil || got.FFC.Available || got.FFC.Latest != "" || got.FFC.Current != "dev" || hits.Load() != 0 {
		t.Errorf("dev ffc: %+v, %v, %d requests", got.FFC, err, hits.Load())
	}

	// An ffc the app would not replace (here a symlink to a multi-call
	// binary) is left to its own update notice: no offer, no request.
	s := ffcService(t, "0.0.0-dev", "1.12.0", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`[{"tag_name":"v9.0.0"}]`))
	})
	s.ffc.resolve = func(string) (string, error) { return "/usr/bin/busybox", nil }
	got, err = s.CheckForUpdate(context.Background())
	if err != nil || got.FFC.Available || got.FFC.Latest != "" || hits.Load() != 0 {
		t.Errorf("not updatable: %+v, %v, %d requests", got.FFC, err, hits.Load())
	}

	// No ffc release in the list.
	got, err = ffcService(t, "0.1.0", "1.12.0", listJSON(`[{"tag_name":"desktop-v0.1.0"}]`)).CheckForUpdate(context.Background())
	if err != nil || got.FFC.Available || got.FFC.Latest != "" {
		t.Errorf("no ffc release: %+v, %v", got.FFC, err)
	}
}
