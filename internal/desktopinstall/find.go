package desktopinstall

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

const (
	releasesPage = "https://github.com/nasroykh/foxmayn_frappe_cli/releases/"
	// defaultDownloadBase is where release assets live; a Plan's URLs are
	// built from it, never taken from the API.
	defaultDownloadBase = releasesPage + "download/"
	releasePagePath     = "/nasroykh/foxmayn_frappe_cli/releases/"

	appName     = "foxmayn-frappe-desktop"
	sumsName    = "checksums.txt"
	sigName     = "checksums.txt.sig"
	listTimeout = 30 * time.Second
)

// DefaultAllowedHosts are the hosts a download may be redirected to.
var DefaultAllowedHosts = []string{"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"}

// RunFunc runs a program with an argument vector (never a shell) and returns
// its combined output; a non-zero exit is an error.
type RunFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// Plan is the release to install and every file it needs. All URLs were built
// from the repository, the tag and fixed file names, and equal the ones the
// release API lists.
type Plan struct {
	Tag      string
	Version  Version
	Asset    string // file name of the setup or disk image
	AssetURL string
	SumsURL  string
	SigURL   string
	PageURL  string // the release page, for people
}

// Installer finds, downloads and installs desktop releases. Use New; the
// exported fields are seams for tests (a fake GitHub, a recorded Run).
type Installer struct {
	GOOS         string
	ReleasesURL  string
	AllowedHosts []string // hosts a download may be redirected to (host names, no port)
	Run          RunFunc  // runs hdiutil, ditto, codesign, plutil, xattr and the setup
	Log          func(string)
	// DownloadBase replaces the GitHub download prefix and Transport is the
	// HTTP transport of the big download (nil = default). Both are for tests.
	DownloadBase string
	Transport    http.RoundTripper
}

// New returns an Installer for goos with GitHub's real endpoints.
func New(goos string) *Installer {
	return &Installer{
		GOOS:         goos,
		ReleasesURL:  release.ReleasesURL,
		AllowedHosts: append([]string(nil), DefaultAllowedHosts...),
		Run:          ExecRun,
		DownloadBase: defaultDownloadBase,
	}
}

func (in *Installer) log(s string) {
	if in.Log != nil {
		in.Log(s)
	}
}

func supported(goos string) bool { return goos == "windows" || goos == "darwin" }

// AssetName is the setup (windows) or disk image (darwin) of version v.
func AssetName(goos string, v Version) (string, error) {
	switch goos {
	case "windows":
		return fmt.Sprintf("%s-%s-windows-amd64-setup.exe", appName, v), nil
	case "darwin":
		return fmt.Sprintf("%s-%s-macos-universal.dmg", appName, v), nil
	}
	return "", ErrUnsupported
}

// Newest picks the newest desktop release for an app at version cur: no
// drafts, no CLI tags, and a prerelease (GitHub-flagged or with a "-" suffix)
// only while the app itself is 0.x or a prerelease. It is the one pick rule
// for the update check, the in-app install and the CLI. ok is false when
// nothing qualifies; the result may be older than cur.
func Newest(rels []release.Release, cur Version) (*release.Release, Version, bool) {
	return newest(rels, cur, false)
}

func newest(rels []release.Release, cur Version, stableOnly bool) (*release.Release, Version, bool) {
	var best *release.Release
	var bestVer Version
	for i := range rels {
		r := &rels[i]
		v, ok := TagVersion(r.TagName)
		if r.Draft || !ok {
			continue
		}
		if stableOnly && v.Pre != "" {
			continue
		}
		if (r.Prerelease || v.Pre != "") && cur.Major != 0 && cur.Pre == "" {
			continue
		}
		if best == nil || v.Compare(bestVer) > 0 {
			best, bestVer = r, v
		}
	}
	return best, bestVer, best != nil
}

// Latest returns the plan for the newest installable release, whatever is
// installed. A release with a "-" suffix (rc, beta) is never planned; a
// GitHub "prerelease" with a plain 0.x number is.
func (in *Installer) Latest(ctx context.Context) (Plan, error) {
	if !supported(in.GOOS) {
		return Plan{}, ErrUnsupported
	}
	rels, err := release.List(ctx, in.ReleasesURL, listTimeout)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return Plan{}, cerr
		}
		var se *release.StatusError
		if errors.As(err, &se) && (se.Code == 403 || se.Code == 429) {
			return Plan{}, fmt.Errorf("%w: %w", ErrRateLimited, err)
		}
		return Plan{}, fmt.Errorf("%w: listing releases: %w", ErrNetwork, err)
	}
	// Version{} is 0.0.0: flagged prereleases count, "-" suffixes do not.
	rel, v, ok := newest(rels, Version{}, true)
	if !ok {
		return Plan{}, ErrNoRelease
	}
	return in.plan(rel, v)
}

// Find is Latest for an installed version cur: ErrNotNewer (with the plan
// still returned) when the newest release is not newer than cur, so there is
// no downgrade and no reinstall.
func (in *Installer) Find(ctx context.Context, cur Version) (Plan, error) {
	p, err := in.Latest(ctx)
	if err != nil {
		return p, err
	}
	if p.Version.Compare(cur) <= 0 {
		return p, ErrNotNewer
	}
	return p, nil
}

// plan builds the URLs and checks the API lists exactly those.
func (in *Installer) plan(rel *release.Release, v Version) (Plan, error) {
	asset, err := AssetName(in.GOOS, v)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Tag: rel.TagName, Version: v, Asset: asset, PageURL: pageURL(rel)}
	p.AssetURL, p.SumsURL, p.SigURL = in.urls(p.Tag, asset)
	for _, w := range []struct{ name, url string }{
		{p.Asset, p.AssetURL}, {sumsName, p.SumsURL}, {sigName, p.SigURL},
	} {
		got, found := "", false
		for _, a := range rel.Assets {
			if a.Name == w.name {
				got, found = a.BrowserDownloadURL, true
				break
			}
		}
		if !found {
			return Plan{}, fmt.Errorf("%w: %s has no %s", ErrAsset, rel.TagName, w.name)
		}
		if got != w.url {
			return Plan{}, fmt.Errorf("%w: %s lists %s at %q, expected %q", ErrAsset, rel.TagName, w.name, got, w.url)
		}
	}
	return p, nil
}

func (in *Installer) base() string {
	if in.DownloadBase != "" {
		return in.DownloadBase
	}
	return defaultDownloadBase
}

// urls builds the three download URLs of tag.
func (in *Installer) urls(tag, asset string) (assetURL, sumsURL, sigURL string) {
	b := in.base() + url.PathEscape(tag) + "/"
	return b + url.PathEscape(asset), b + sumsName, b + sigName
}

// pageURL is the release's html_url when it is a page under this
// repository's releases on github.com, else the tag's page.
func pageURL(r *release.Release) string {
	if u, err := url.Parse(r.HTMLURL); err == nil &&
		u.Scheme == "https" && u.Host == "github.com" && u.User == nil &&
		strings.HasPrefix(u.Path, releasePagePath) && len(u.Path) > len(releasePagePath) {
		return u.String()
	}
	return releasesPage + "tag/" + url.PathEscape(r.TagName)
}
