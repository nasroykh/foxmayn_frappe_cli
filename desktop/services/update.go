package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

const (
	updateTimeout   = 15 * time.Second
	desktopTagStart = "desktop-v"
	releasePagePath = "/nasroykh/foxmayn_frappe_cli/releases/"
	releasePageBase = "https://github.com/nasroykh/foxmayn_frappe_cli/releases/"
)

// UpdateInfo is the answer of CheckForUpdate.
type UpdateInfo struct {
	Available bool `json:"available"`
	// Current is the running version (AppVersion).
	Current string `json:"current"`
	// Latest is the newest version the app may offer ("" when none was found).
	Latest string `json:"latest"`
	// URL is the release page on GitHub, always under this repository's
	// releases.
	URL string `json:"url"`
	// PublishedAt is the release's publish time (RFC 3339), or "".
	PublishedAt string `json:"publishedAt"`
	// FFC compares the installed ffc with the newest ffc release.
	FFC FFCUpdate `json:"ffc"`
}

// FFCUpdate says whether a newer ffc release than the installed one exists.
// InstallFFC installs it.
type FFCUpdate struct {
	Available bool `json:"available"`
	// Current is the installed ffc's version ("" when ffc is not installed).
	Current string `json:"current"`
	// Latest is the newest ffc release ("" when none was found or the
	// installed ffc is not updatable: not a release build, or a file the app
	// must not replace).
	Latest string `json:"latest"`
}

// updateRelease is the part of GitHub's release JSON the check reads.
type updateRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
}

// CheckForUpdate asks GitHub for the newest desktop release (tag
// desktop-v<semver>, not a draft) and compares it with this app's version.
// A prerelease counts only while the running version is 0.x or itself a
// prerelease. An app version that does not parse (a development build) is
// never offered an app update. From the same list it compares the installed
// ffc, when FFCInfo.Updatable (InstallFFC replaces it in place; anything else
// is left to its own `ffc update` notice), with the ffc release InstallFFC
// would install (newestFFC); a development build of the app still checks
// ffc. Nothing is fetched when
// neither version is a release. It sends no credentials; cancelling ctx
// stops it.
func (s *AppService) CheckForUpdate(ctx context.Context) (UpdateInfo, error) {
	info := UpdateInfo{Current: s.version}
	cur, appOK := releaseVersion(s.version)
	installed := s.ffc.Info()
	ffcCur, ffcOK := releaseVersion(installed.Version)
	ffcOK = ffcOK && installed.Updatable
	info.FFC.Current = installed.Version
	if !appOK && !ffcOK {
		return info, nil
	}

	rels, err := s.fetchReleases(ctx)
	if err != nil {
		return info, err
	}
	if ffcOK {
		info.FFC = newestFFC(rels, ffcCur, installed.Version)
	}
	if !appOK {
		return info, nil
	}
	var best *updateRelease
	var bestVer semver
	for i := range rels {
		r := &rels[i]
		v, ok := desktopVersion(r.TagName)
		if r.Draft || !ok {
			continue
		}
		if (r.Prerelease || v.pre != "") && cur.major != 0 && cur.pre == "" {
			continue
		}
		if best == nil || v.compare(bestVer) > 0 {
			best, bestVer = r, v
		}
	}
	if best == nil {
		return info, nil
	}
	info.Latest = bestVer.String()
	info.URL = releasePageURL(best)
	if t, err := time.Parse(time.RFC3339, best.PublishedAt); err == nil {
		info.PublishedAt = t.UTC().Format(time.RFC3339)
	}
	info.Available = bestVer.compare(cur) > 0
	return info, nil
}

// newestFFC compares the installed ffc (cur, shown as current) with the ffc
// release InstallFFC and `ffc update` would install: the first entry of the
// list that release.Latest takes (not a draft, not a prerelease, a v<digit>
// tag). Picking it the same way means the offer always names what gets
// installed; it is offered only when newer, so it is never a downgrade.
func newestFFC(rels []updateRelease, cur semver, current string) FFCUpdate {
	out := FFCUpdate{Current: current}
	for i := range rels {
		r := &rels[i]
		if r.Draft || r.Prerelease || !release.IsCLITag(r.TagName) {
			continue
		}
		if v, ok := parseSemver(r.TagName); ok {
			out.Latest = v.String()
			out.Available = v.compare(cur) > 0
		}
		break
	}
	return out
}

// releaseVersion parses a release version; a development build ("dev",
// "0.0.0-dev") is not one.
func releaseVersion(s string) (semver, bool) {
	v, ok := parseSemver(s)
	return v, ok && !v.isDevBuild()
}

func (s *AppService) fetchReleases(ctx context.Context) ([]updateRelease, error) {
	resp, err := client.NewHTTPClient(updateTimeout).R().
		SetContext(ctx).
		SetHeader("Accept", "application/vnd.github+json").
		Get(s.releasesURL)
	switch {
	case ctx.Err() != nil:
		return nil, newError(CodeCancelled, "The update check was cancelled.", nil)
	case err != nil:
		return nil, newError(CodeNetwork, "GitHub could not be reached. Check your internet connection.", err)
	}
	switch code := resp.StatusCode(); {
	case code == http.StatusForbidden || code == http.StatusTooManyRequests:
		return nil, newError(CodeUnavailable, "GitHub is limiting requests right now. Try again later.", fmt.Errorf("GitHub API returned HTTP %d", code))
	case code != http.StatusOK:
		return nil, newError(CodeFailed, "GitHub could not list the releases.", fmt.Errorf("GitHub API returned HTTP %d", code))
	}
	var rels []updateRelease
	if err := json.Unmarshal(resp.Body(), &rels); err != nil {
		return nil, newError(CodeFailed, "GitHub's answer could not be read.", err)
	}
	return rels, nil
}

// releasePageURL is the release's html_url when it is a page under this
// repository's releases on github.com, else the tag's page.
func releasePageURL(r *updateRelease) string {
	if u, err := url.Parse(r.HTMLURL); err == nil &&
		u.Scheme == "https" && u.Host == "github.com" && u.User == nil &&
		strings.HasPrefix(u.Path, releasePagePath) && len(u.Path) > len(releasePagePath) {
		return u.String()
	}
	return releasePageBase + "tag/" + url.PathEscape(r.TagName)
}

// desktopVersion returns the version of a desktop release tag.
func desktopVersion(tag string) (semver, bool) {
	rest, ok := strings.CutPrefix(tag, desktopTagStart)
	if !ok {
		return semver{}, false
	}
	return parseSemver(rest)
}

// semver is a parsed semantic version (build metadata is dropped). The
// CLI's own comparison (internal/cmd newerThan) reads anything leniently and
// lives in the cobra package; the update notice must refuse what is not a
// version, so it has its own strict one.
type semver struct {
	major, minor, patch uint64
	pre                 string
}

var semverRE = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// parseSemver parses "1.2.3", "v1.2.3", "1.2.3-rc.1" and "1.2.3+build". It
// accepts nothing else: a version that does not parse is never compared.
func parseSemver(s string) (semver, bool) {
	m := semverRE.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var n [3]uint64
	for i := range n {
		v, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return semver{}, false
		}
		n[i] = v
	}
	return semver{n[0], n[1], n[2], m[4]}, true
}

func (v semver) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if v.pre != "" {
		s += "-" + v.pre
	}
	return s
}

// isDevBuild is the "0.0.0-dev" placeholder of a build without a release
// version.
func (v semver) isDevBuild() bool {
	return v.major == 0 && v.minor == 0 && v.patch == 0 && strings.HasPrefix(v.pre, "dev")
}

// compare orders versions by semver precedence: -1, 0 or 1.
func (v semver) compare(o semver) int {
	for _, p := range [][2]uint64{{v.major, o.major}, {v.minor, o.minor}, {v.patch, o.patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.pre == o.pre:
		return 0
	case v.pre == "":
		return 1
	case o.pre == "":
		return -1
	}
	a, b := strings.Split(v.pre, "."), strings.Split(o.pre, ".")
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePreIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// comparePreIdent compares two dot-separated prerelease identifiers:
// numbers by value and below text, text in ASCII order.
func comparePreIdent(a, b string) int {
	an, aerr := strconv.ParseUint(a, 10, 64)
	bn, berr := strconv.ParseUint(b, 10, 64)
	switch {
	case aerr == nil && berr == nil:
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
		return 0
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}
