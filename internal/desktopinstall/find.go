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
// only while the app itself is 0.x or a prerelease. It is the rule of the
// update check (what to tell the person about); ok is false when nothing
// qualifies, and the result may be older than cur. What an install may take
// is NewestInstallable.
func Newest(rels []release.Release, cur Version) (*release.Release, Version, bool) {
	return newest(rels, cur, false)
}

// NewestInstallable is Newest without the releases that carry a "-" suffix
// (rc, beta), which are linked to and never installed. Find uses it, and the
// update check offers it as the installable update, so the version shown is
// the version installed. A GitHub-flagged prerelease with a plain number
// still follows Newest's rule: only while cur is 0.x or a prerelease.
func NewestInstallable(rels []release.Release, cur Version) (*release.Release, Version, bool) {
	return newest(rels, cur, true)
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

// list fetches the release list, mapping the failures to this package's
// errors.
func (in *Installer) list(ctx context.Context) ([]release.Release, error) {
	if !supported(in.GOOS) {
		return nil, ErrUnsupported
	}
	rels, err := release.List(ctx, in.ReleasesURL, listTimeout)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		var se *release.StatusError
		if errors.As(err, &se) && (se.Code == 403 || se.Code == 429) {
			return nil, fmt.Errorf("%w: %w", ErrRateLimited, err)
		}
		return nil, fmt.Errorf("%w: listing releases: %w", ErrNetwork, err)
	}
	return rels, nil
}

// Latest returns the plan for the newest release to install on a machine
// that has none (or whose version is unknown). A release with a "-" suffix
// (rc, beta) is never planned; a GitHub "prerelease" with a plain 0.x number
// is.
func (in *Installer) Latest(ctx context.Context) (Plan, error) {
	rels, err := in.list(ctx)
	if err != nil {
		return Plan{}, err
	}
	// Version{} is 0.0.0: flagged prereleases count, "-" suffixes do not.
	rel, v, ok := newest(rels, Version{}, true)
	if !ok {
		return Plan{}, ErrNoRelease
	}
	return in.plan(rel, v)
}

// Find is the plan for an installed version cur, picked by
// NewestInstallable(rels, cur): a stable cur is never moved to a flagged
// prerelease, a "-" suffix is never installed, and a stable release is still
// found when a newer suffixed one exists. ErrNotNewer (with the plan still
// returned) when the pick is not newer than cur, so there is no downgrade and
// no reinstall.
func (in *Installer) Find(ctx context.Context, cur Version) (Plan, error) {
	rels, err := in.list(ctx)
	if err != nil {
		return Plan{}, err
	}
	rel, v, ok := NewestInstallable(rels, cur)
	if !ok {
		return Plan{}, ErrNoRelease
	}
	p, err := in.plan(rel, v)
	if err != nil {
		return p, err
	}
	if v.Compare(cur) <= 0 {
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
