package services

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/desktopinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

const (
	updateTimeout   = 15 * time.Second
	desktopTagStart = desktopinstall.TagPrefix
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
	// InstallCommand installs Latest from a terminal (desktopInstallCommand),
	// set when an update is available and Latest has no prerelease suffix.
	InstallCommand string `json:"installCommand"`
	// CanInstall is true when InstallUpdate can replace this copy of the app
	// with Latest (a local check: an installed copy in a writable place).
	CanInstall bool `json:"canInstall"`
	// InstallBlocked is the error key (errors.<key>) saying why an available
	// update cannot be installed from the app; "" when it can, or when the
	// update is a prerelease the app only links to.
	InstallBlocked string `json:"installBlocked"`
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
	// must not replace; CheckFFCUpdate also compares those).
	Latest string `json:"latest"`
	// Manager and Command are set by CheckFFCUpdate when a package manager
	// installed ffc: the update is that manager's command, never InstallFFC.
	Manager string `json:"manager,omitempty"`
	Command string `json:"command,omitempty"`
}

// CheckForUpdate asks GitHub for the newest desktop release (tag
// desktop-v<semver>, not a draft) and compares it with this app's version.
// A prerelease counts only while the running version is 0.x or itself a
// prerelease, but a newer release that InstallUpdate can install (no "-"
// suffix) is offered in place of a still newer rc or beta. An app version that does not parse (a development build) is
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
	best, bestVer, ok := desktopinstall.Newest(rels, cur)
	// An update InstallUpdate can install wins over a newer rc or beta: the
	// version offered is the version Find installs, never a different one.
	if ib, iv, iok := desktopinstall.NewestInstallable(rels, cur); iok && iv.Compare(cur) > 0 {
		best, bestVer, ok = ib, iv, true
	}
	if !ok {
		return info, nil
	}
	info.Latest = bestVer.String()
	info.URL = releasePageURL(best)
	if t, err := time.Parse(time.RFC3339, best.PublishedAt); err == nil {
		info.PublishedAt = t.UTC().Format(time.RFC3339)
	}
	info.Available = bestVer.Compare(cur) > 0
	if info.Available && bestVer.Pre == "" {
		info.InstallCommand = desktopInstallCommand(s.goos, info.Latest)
		exe, err := s.exe()
		if err == nil {
			err = s.eligible(s.goos, cur, exe)
		}
		info.CanInstall = err == nil
		if err != nil {
			info.InstallBlocked = blockedKey(err)
		}
	}
	return info, nil
}

// CheckFFCUpdate compares the installed ffc with the newest ffc release,
// for the ffc helper tab's own button. Unlike CheckForUpdate it also compares
// an ffc the app would not replace (a package manager's copy: Manager and
// Command say how to update it). Nothing is fetched when no working release
// build of ffc is installed. It sends no credentials; cancelling ctx stops it.
func (s *AppService) CheckFFCUpdate(ctx context.Context) (FFCUpdate, error) {
	installed := s.ffc.Info()
	out := FFCUpdate{Current: installed.Version, Manager: installed.Manager, Command: installed.UpgradeCommand}
	cur, ok := releaseVersion(installed.Version)
	if !installed.Found || installed.Error != "" || !ok {
		return out, nil
	}
	rels, err := s.fetchReleases(ctx)
	if err != nil {
		return out, err
	}
	u := newestFFC(rels, cur, installed.Version)
	u.Manager, u.Command = out.Manager, out.Command
	return u, nil
}

// desktopInstallCommand runs install-desktop.sh (macOS) or install-desktop.ps1
// (Windows) from the release's own tag, never from main. The scripts install
// the newest desktop-v<X.Y.Z>, which is version or a later one. A download
// by curl or PowerShell is not quarantined or marked as from the internet,
// so the unsigned app opens without the Gatekeeper or SmartScreen screens a
// browser download meets.
func desktopInstallCommand(goos, version string) string {
	base := "https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/" + desktopTagStart + version
	if goos == "windows" {
		return `powershell -NoProfile -ExecutionPolicy Bypass -Command "irm ` + base + `/install-desktop.ps1 | iex"`
	}
	return "curl -fsSL " + base + "/install-desktop.sh | sh"
}

// newestFFC compares the installed ffc (cur, shown as current) with the ffc
// release InstallFFC and `ffc update` would install: the first entry of the
// list that release.Latest takes (not a draft, not a prerelease, a v<digit>
// tag). Picking it the same way means the offer always names what gets
// installed; it is offered only when newer, so it is never a downgrade.
func newestFFC(rels []release.Release, cur desktopinstall.Version, current string) FFCUpdate {
	out := FFCUpdate{Current: current}
	for i := range rels {
		r := &rels[i]
		if r.Draft || r.Prerelease || !release.IsCLITag(r.TagName) {
			continue
		}
		if v, ok := desktopinstall.ParseVersion(r.TagName); ok {
			out.Latest = v.String()
			out.Available = v.Compare(cur) > 0
		}
		break
	}
	return out
}

// releaseVersion parses a release version; a development build ("dev",
// "0.0.0-dev") is not one.
func releaseVersion(s string) (desktopinstall.Version, bool) {
	v, ok := desktopinstall.ParseVersion(s)
	return v, ok && !v.IsDevBuild()
}

// blockedKey is the error key of the reason Eligible refused.
func blockedKey(err error) string {
	switch {
	case errors.Is(err, desktopinstall.ErrUnsupported):
		return "update.unsupported"
	case errors.Is(err, desktopinstall.ErrNotWritable):
		return "update.notWritable"
	default:
		return "update.notInstalled"
	}
}

func (s *AppService) fetchReleases(ctx context.Context) ([]release.Release, error) {
	rels, err := release.List(ctx, s.releasesURL, updateTimeout)
	if err == nil {
		return rels, nil
	}
	var se *release.StatusError
	var ue *url.Error
	switch {
	case ctx.Err() != nil:
		return nil, newError(CodeCancelled, "The update check was cancelled.", nil)
	case errors.As(err, &se) && (se.Code == 403 || se.Code == 429):
		return nil, newError(CodeUnavailable, "GitHub is limiting requests right now. Try again later.", fmt.Errorf("GitHub API returned HTTP %d", se.Code))
	case errors.As(err, &se):
		return nil, newError(CodeFailed, "GitHub could not list the releases.", fmt.Errorf("GitHub API returned HTTP %d", se.Code))
	case errors.As(err, &ue):
		return nil, newError(CodeNetwork, "GitHub could not be reached. Check your internet connection.", err)
	}
	return nil, newError(CodeFailed, "GitHub's answer could not be read.", err)
}

// releasePageURL is the release's html_url when it is a page under this
// repository's releases on github.com, else the tag's page.
func releasePageURL(r *release.Release) string {
	if u, err := url.Parse(r.HTMLURL); err == nil &&
		u.Scheme == "https" && u.Host == "github.com" && u.User == nil &&
		strings.HasPrefix(u.Path, releasePagePath) && len(u.Path) > len(releasePagePath) {
		return u.String()
	}
	return releasePageBase + "tag/" + url.PathEscape(r.TagName)
}
