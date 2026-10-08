package services

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

// AppVersion is the desktop app's version. Release builds set it with ldflags
// (desktop-release.yml via APP_VERSION); a local build stays 0.0.0-dev and
// never gets an update offer.
var AppVersion = "0.0.0-dev"

// Environment describes the machine the app runs on.
type Environment struct {
	AppVersion string `json:"appVersion"`
	// OS is runtime.GOOS: "windows" or "darwin" (Linux is not a target yet).
	OS           string  `json:"os"`
	ConfigPath   string  `json:"configPath"`
	ConfigExists bool    `json:"configExists"`
	FFC          FFCInfo `json:"ffc"`
	// InstallCommand installs ffc by hand: the README's one-liner, with the
	// script pinned to a release tag.
	InstallCommand string  `json:"installCommand"`
	WSL            WSLInfo `json:"wsl"`
}

// AppService reports the environment, installs ffc and opens things with
// the operating system.
type AppService struct {
	host       Host
	goos       string
	configPath string
	ffc        *FFCLocator
	wsl        *wslDetector
	// install installs ffc (over the binary at over, when set) and returns its
	// path (ffcInstaller.installAt).
	install func(ctx context.Context, over string, log func(string)) (string, error)
	// version is the running app version and releasesURL the release list the
	// update check reads (tests point it at a fake).
	version     string
	releasesURL string

	wslOnce sync.Once
	wslInfo WSLInfo

	installMu sync.Mutex
}

// NewAppService builds the service for this machine. configPath is the ffc
// config file (config.DefaultConfigPath).
func NewAppService(host Host, configPath string, ffc *FFCLocator) *AppService {
	home, _ := os.UserHomeDir()
	return &AppService{
		install:     newFFCInstaller(runtime.GOOS, home).installAt,
		version:     AppVersion,
		releasesURL: release.ReleasesURL,
		host:        host,
		goos:        runtime.GOOS,
		configPath:  configPath,
		ffc:         ffc,
		wsl:         newWSLDetector(runtime.GOOS),
	}
}

// NewFFCLocator finds the ffc binary of this machine; AppService and
// AssistantsService share one.
func NewFFCLocator() *FFCLocator {
	home, _ := os.UserHomeDir()
	return newFFCLocator(runtime.GOOS, home)
}

// DefaultConfigPath is the config file the ffc CLI uses by default.
func DefaultConfigPath() (string, error) {
	return config.DefaultConfigPath()
}

// ConfigPath is the config file the app manages: $FFC_CONFIG (made
// absolute) when set, as the CLI reads it, else the CLI's default. A macOS
// app started from Finder does not see variables set in a shell profile.
func ConfigPath() (string, error) {
	return configPathFrom(os.Getenv, DefaultConfigPath)
}

func configPathFrom(getenv func(string) string, def func() (string, error)) (string, error) {
	if v := strings.TrimSpace(getenv("FFC_CONFIG")); v != "" {
		return filepath.Abs(v)
	}
	return def()
}

// Environment returns the app and ffc versions, the config path and the
// WSL detection (run once per app start).
func (s *AppService) Environment() Environment {
	s.wslOnce.Do(func() { s.wslInfo = s.wsl.detect() })
	return Environment{
		AppVersion:     AppVersion,
		OS:             s.goos,
		ConfigPath:     s.configPath,
		ConfigExists:   isRegularFile(s.configPath),
		FFC:            s.ffc.Info(),
		InstallCommand: installCommand(s.goos),
		WSL:            s.wslInfo,
	}
}

// RefreshFFC looks for the ffc binary again.
func (s *AppService) RefreshFFC() FFCInfo {
	return s.ffc.Refresh()
}

// updateTarget is the ffc binary InstallFFC replaces in place: the one the
// app found, when FFCInfo.Updatable, so an update never leaves the old binary
// first on PATH. "" (no ffc, a development build, anything the app must not
// overwrite) installs into the installer's own folder.
func (s *AppService) updateTarget() string {
	return s.ffc.Info().target
}

// InstallFFC downloads the latest ffc release from GitHub, verifies its
// signature and checksum, installs it for this user (see ffcInstaller) over
// the ffc already found when that is a release build (updateTarget), sends
// each step as an "installer:log" event, then looks for the binary again.
// The UI asks the user first. Cancelling the call stops the download.
func (s *AppService) InstallFFC(ctx context.Context) (FFCInfo, error) {
	if !s.installMu.TryLock() {
		return FFCInfo{}, &Error{Code: CodeUnavailable, Message: "The installer is already running."}
	}
	defer s.installMu.Unlock()

	// A package manager's ffc is updated through it: neither replaced nor
	// shadowed by a second copy in the installer's folder.
	info := s.ffc.Refresh()
	if ctx.Err() != nil {
		return info, newError(CodeCancelled, "The installation was cancelled.", nil)
	}
	if info.Manager != "" {
		return info, &Error{Code: CodeUnavailable, Message: "ffc was installed with " + info.Manager + ". Update it in a terminal with: " + info.UpgradeCommand}
	}

	_, err := s.install(ctx, s.updateTarget(), func(line string) {
		s.host.Emit(EventInstallerLog, InstallerLine{Line: line})
	})
	info = s.ffc.Refresh()
	switch {
	case ctx.Err() != nil:
		return info, newError(CodeCancelled, "The installation was cancelled.", nil)
	case err != nil:
		return info, newError(CodeFailed, "ffc could not be installed.", err)
	case !info.Found:
		return info, &Error{Code: CodeFailed, Message: "ffc was installed, but it was not found where it should be. Restart the app and try again."}
	}
	return info, nil
}

// OpenWebsite opens an http(s) URL in the default browser.
func (s *AppService) OpenWebsite(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return invalid("url", "Only web addresses can be opened.")
	}
	if err := s.host.OpenURL(u.String()); err != nil {
		return newError(CodeFailed, "The browser could not be opened.", err)
	}
	return nil
}

// SetWindowTheme matches the window background to the theme the page applied
// (the in-app choice, not only the system theme) and shows the window if it
// is still hidden. The page calls it on start and on every theme change.
func (s *AppService) SetWindowTheme(dark bool) {
	s.host.SetWindowTheme(dark)
}

// OpenConfigFolder opens the folder that holds config.yaml.
func (s *AppService) OpenConfigFolder() error {
	return s.openFolder(filepath.Dir(s.configPath))
}

// OpenFFCFolder opens the folder that holds the ffc binary.
func (s *AppService) OpenFFCFolder() error {
	info := s.ffc.Info()
	if !info.Found {
		return &Error{Code: CodeFFCMissing, Message: "ffc is not installed."}
	}
	return s.openFolder(filepath.Dir(info.Path))
}

func (s *AppService) openFolder(dir string) error {
	fi, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return &Error{Code: CodeNotFound, Message: "The folder does not exist yet: " + dir}
	}
	if err != nil || !fi.IsDir() {
		return newError(CodeFailed, "The folder could not be opened.", err)
	}
	if err := s.host.OpenFile(dir); err != nil {
		return newError(CodeFailed, "The folder could not be opened.", err)
	}
	return nil
}
