package desktopinstall

import (
	"context"
	"errors"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

func relsFor(tags ...string) []release.Release {
	var out []release.Release
	for _, tg := range tags {
		r := release.Release{TagName: tg}
		if len(tg) > 0 && tg[0] == '!' { // "!tag" = flagged prerelease
			r.TagName, r.Prerelease = tg[1:], true
		}
		out = append(out, r)
	}
	return out
}

// The pick rule carried over from the desktop's TestCheckForUpdatePrereleaseRule.
func TestNewestPrereleaseRule(t *testing.T) {
	list := relsFor("!desktop-v1.1.0-rc.1", "desktop-v1.0.1-beta.2", "desktop-v1.0.0")
	for _, c := range []struct{ cur, want string }{
		{"0.9.0", "1.1.0-rc.1"}, // 0.x: prereleases count
		{"1.0.0", "1.0.0"},      // stable 1.x: they do not
		{"0.9.0-rc.1", "1.1.0-rc.1"},
		{"1.0.0-rc.1", "1.1.0-rc.1"}, // itself a prerelease
		{"1.1.0-rc.1", "1.1.0-rc.1"},
		{"0.1.0", "1.1.0-rc.1"},
	} {
		cur, _ := ParseVersion(c.cur)
		_, v, ok := Newest(list, cur)
		if !ok || v.String() != c.want {
			t.Errorf("current %s: %v %v, want %s", c.cur, v, ok, c.want)
		}
	}
	// A flagged prerelease with a plain tag counts as one on 1.x.
	cur, _ := ParseVersion("1.0.0")
	if _, v, ok := Newest(relsFor("!desktop-v1.2.0"), cur); ok {
		t.Errorf("flagged prerelease on 1.x picked: %v", v)
	}
	// Drafts and CLI tags never count; the highest wins whatever the order.
	list = relsFor("v9.0.0", "desktop-v0.3.0", "desktop-v0.10.0", "desktop-v0.9.0")
	list[1].Draft = true
	cur, _ = ParseVersion("0.1.0")
	if r, v, ok := Newest(list, cur); !ok || r.TagName != "desktop-v0.10.0" || v.String() != "0.10.0" {
		t.Errorf("highest: %v %v %v", r, v, ok)
	}
	if _, _, ok := Newest(relsFor("v1.0.0"), cur); ok {
		t.Error("a CLI tag was picked")
	}
}

func TestLatestAndFind(t *testing.T) {
	for _, goos := range []string{"windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			f, in := newFake(t, goos)
			f.rels = append(f.rels, release.Release{TagName: "v9.9.9"})
			f.publish("desktop-v1.5.0", []byte("draft"), "draft")
			f.publish("desktop-v1.1.0-rc.1", []byte("rc"), "prerelease")
			f.publish("desktop-v0.10.0", []byte("payload"))
			f.publish("desktop-v0.9.0", []byte("old"))

			p, err := in.Latest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantAsset := "foxmayn-frappe-desktop-0.10.0-windows-amd64-setup.exe"
			if goos == "darwin" {
				wantAsset = "foxmayn-frappe-desktop-0.10.0-macos-universal.dmg"
			}
			base := f.srv.URL + "/download/desktop-v0.10.0/"
			if p.Tag != "desktop-v0.10.0" || p.Version.String() != "0.10.0" || p.Asset != wantAsset ||
				p.AssetURL != base+wantAsset || p.SumsURL != base+"checksums.txt" || p.SigURL != base+"checksums.txt.sig" ||
				p.PageURL != "https://github.com/nasroykh/foxmayn_frappe_cli/releases/tag/desktop-v0.10.0" {
				t.Errorf("plan = %+v", p)
			}

			for _, c := range []struct {
				cur  string
				want error
			}{{"0.9.0", nil}, {"0.10.0", ErrNotNewer}, {"0.11.0", ErrNotNewer}, {"2.0.0", ErrNotNewer}} {
				cur, _ := ParseVersion(c.cur)
				p, err := in.Find(context.Background(), cur)
				if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
					t.Errorf("Find(%s) = %v, want %v", c.cur, err, c.want)
				}
				if p.Tag != "desktop-v0.10.0" {
					t.Errorf("Find(%s) plan = %+v", c.cur, p)
				}
			}
		})
	}
}

func TestLatestNeverPlansSuffixButPlans0xPrerelease(t *testing.T) {
	f, in := newFake(t, "windows")
	f.publish("desktop-v1.1.0-rc.1", []byte("rc"), "prerelease")
	if _, err := in.Latest(context.Background()); !errors.Is(err, ErrNoRelease) {
		t.Errorf("only an rc: %v", err)
	}
	f.publish("desktop-v0.8.0", []byte("x"), "prerelease") // flagged, plain number: installable (Q1)
	p, err := in.Latest(context.Background())
	if err != nil || p.Version.String() != "0.8.0" {
		t.Errorf("0.x prerelease: %+v %v", p, err)
	}
}

func TestLatestAssetChecks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(f *fake, r *release.Release)
	}{
		{"missing signature", func(f *fake, r *release.Release) { r.Assets = r.Assets[:2] }},
		{"missing setup", func(f *fake, r *release.Release) { r.Assets = r.Assets[1:] }},
		{"renamed asset", func(f *fake, r *release.Release) { r.Assets[0].Name = "evil.exe" }},
		{"other url", func(f *fake, r *release.Release) { r.Assets[0].BrowserDownloadURL = "https://evil.example/x.exe" }},
		{"other tag in url", func(f *fake, r *release.Release) {
			r.Assets[1].BrowserDownloadURL = f.srv.URL + "/download/desktop-v0.1.0/checksums.txt"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, in := newFake(t, "windows")
			f.publish("desktop-v0.10.0", []byte("p"))
			tc.mutate(f, &f.rels[0])
			if _, err := in.Latest(context.Background()); !errors.Is(err, ErrAsset) {
				t.Errorf("err = %v, want ErrAsset", err)
			}
		})
	}
}

func TestLatestHTTPErrors(t *testing.T) {
	for _, c := range []struct {
		status int
		want   error
	}{{403, ErrRateLimited}, {429, ErrRateLimited}, {500, ErrNetwork}, {404, ErrNetwork}} {
		f, in := newFake(t, "darwin")
		f.status = c.status
		_, err := in.Latest(context.Background())
		var se *release.StatusError
		if !errors.Is(err, c.want) || !errors.As(err, &se) || se.Code != c.status {
			t.Errorf("status %d: %v", c.status, err)
		}
	}
	// No answer at all.
	in := New("windows")
	in.ReleasesURL = "http://127.0.0.1:1/releases"
	if _, err := in.Latest(context.Background()); !errors.Is(err, ErrNetwork) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestUnsupportedOSMakesNoRequest(t *testing.T) {
	f, in := newFake(t, "linux")
	if _, err := in.Latest(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Latest: %v", err)
	}
	if _, err := in.Find(context.Background(), Version{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Find: %v", err)
	}
	if _, err := in.Download(context.Background(), Plan{}, t.TempDir(), nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Download: %v", err)
	}
	if err := in.Apply(context.Background(), Target{GOOS: "linux", Path: "x"}, "y", Version{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Apply: %v", err)
	}
	if n := len(f.hits); n != 0 {
		t.Errorf("%d requests on an unsupported OS", n)
	}
}
