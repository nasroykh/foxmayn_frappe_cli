package services

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// AppVersion is the desktop app's version (build/config.yml info.version).
var AppVersion = "0.1.0"

// Environment describes the machine the app runs on.
type Environment struct {
	AppVersion string `json:"appVersion"`
	// OS is runtime.GOOS: "windows" or "darwin" (Linux is not a target yet).
	OS           string  `json:"os"`
	ConfigPath   string  `json:"configPath"`
	ConfigExists bool    `json:"configExists"`
	FFC          FFCInfo `json:"ffc"`
	// InstallCommand installs ffc by hand (the README's one-liner).
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
	installer  func(ctx context.Context, argv []string, line func(string)) error

	wslOnce sync.Once
	wslInfo WSLInfo

	installMu sync.Mutex
}

// NewAppService builds the service for this machine. configPath is the ffc
// config file (config.DefaultConfigPath).
func NewAppService(host Host, configPath string, ffc *FFCLocator) *AppService {
	return &AppService{
		host:       host,
		goos:       runtime.GOOS,
		configPath: configPath,
		ffc:        ffc,
		wsl:        newWSLDetector(runtime.GOOS),
		installer:  runInstaller,
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

// InstallFFC runs the official installer (install.ps1 on Windows, install.sh
// elsewhere), sends each line it prints as an "installer:log" event, then
// looks for the binary again. The UI asks the user first. Cancelling the
// call stops the installer.
func (s *AppService) InstallFFC(ctx context.Context) (FFCInfo, error) {
	if !s.installMu.TryLock() {
		return FFCInfo{}, &Error{Code: CodeUnavailable, Message: "The installer is already running."}
	}
	defer s.installMu.Unlock()

	err := s.installer(ctx, installArgv(s.goos), func(line string) {
		s.host.Emit(EventInstallerLog, InstallerLine{Line: line})
	})
	info := s.ffc.Refresh()
	switch {
	case ctx.Err() != nil:
		return info, newError(CodeCancelled, "The installation was cancelled.", nil)
	case err != nil:
		return info, newError(CodeFailed, "The installer did not finish.", err)
	case !info.Found:
		return info, &Error{Code: CodeFailed, Message: "The installer finished, but ffc was not found where it installs. Restart the app and try again."}
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
