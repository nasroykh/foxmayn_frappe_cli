package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
)

// installScriptTag pins the install scripts of the copyable command to a
// release tag, so it never runs whatever is on main. The scripts themselves
// install the latest release.
const installScriptTag = "v1.11.0"

// installCommand is the command the user can copy to install ffc by hand.
func installCommand(goos string) string {
	base := "https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/" + installScriptTag
	if goos == "windows" {
		return `powershell -NoProfile -ExecutionPolicy Bypass -Command "irm ` + base + `/install.ps1 | iex"`
	}
	return "curl -fsSL " + base + "/install.sh | sh"
}

// ffcInstaller installs the latest ffc release in-process: it downloads the
// archive for this platform, verifies the release signature and the
// archive's SHA-256 (internal/release, the code behind `ffc update`), and
// writes the binary where the install scripts put it and FFCLocator looks:
// %LOCALAPPDATA%\Programs\ffc\ffc.exe on Windows, ~/.local/bin/ffc on macOS.
type ffcInstaller struct {
	goos, goarch string
	home         string
	getenv       func(string) string
	latest       func(ctx context.Context) (*release.Release, error)
	download     func(ctx context.Context, t release.Target) ([]byte, error)
	// addToPath adds dir to the user's PATH (Windows); it reports whether it
	// changed anything.
	addToPath func(dir string) (bool, error)
	// shellPath is the PATH of the user's login shell (macOS), which a GUI
	// app does not inherit.
	shellPath func() (string, error)
}

func newFFCInstaller(goos, home string) *ffcInstaller {
	return &ffcInstaller{
		goos:   goos,
		goarch: runtime.GOARCH,
		home:   home,
		getenv: os.Getenv,
		latest: func(ctx context.Context) (*release.Release, error) {
			return release.Latest(ctx, release.LatestURL, 30*time.Second)
		},
		download:  release.Download,
		addToPath: addUserPath,
		shellPath: loginShellPath,
	}
}

// dir is the folder ffc is installed into.
func (i *ffcInstaller) dir() (string, error) {
	if i.goos == "windows" {
		if d := i.getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Programs", "ffc"), nil
		}
		if i.home != "" {
			return filepath.Join(i.home, "AppData", "Local", "Programs", "ffc"), nil
		}
		return "", errors.New("neither LOCALAPPDATA nor the home folder is known")
	}
	if i.home == "" {
		return "", errors.New("the home folder is not known")
	}
	return filepath.Join(i.home, ".local", "bin"), nil
}

// install downloads, verifies and installs ffc, calling log with a line for
// each step. It returns the installed binary's path. Cancelling ctx stops the
// download; nothing is written after ctx ends.
func (i *ffcInstaller) install(ctx context.Context, log func(string)) (string, error) {
	path, err := i.run(ctx, log)
	if err != nil && ctx.Err() == nil {
		log("error: " + err.Error())
	}
	return path, err
}

func (i *ffcInstaller) run(ctx context.Context, log func(string)) (string, error) {
	dir, err := i.dir()
	if err != nil {
		return "", err
	}
	log(fmt.Sprintf("Detecting platform... %s/%s", i.goos, i.goarch))
	log("Fetching the latest release from GitHub...")
	rel, err := i.latest(ctx)
	if err != nil {
		return "", err
	}
	target, err := rel.Target(i.goos, i.goarch)
	if err != nil {
		return "", err
	}
	log(fmt.Sprintf("Downloading ffc %s (%s)...", rel.TagName, target.AssetName))
	bin, err := i.download(ctx, target)
	if err != nil {
		return "", err
	}
	log("Verified the release signature and the SHA-256 checksum.")
	if err := ctx.Err(); err != nil {
		return "", err
	}

	path := filepath.Join(dir, release.BinaryName(i.goos))
	if err := writeBinary(path, bin, i.goos == "windows"); err != nil {
		return "", err
	}
	log("Installed ffc " + rel.TagName + " to " + path)

	if i.goos == "windows" {
		switch added, err := i.addToPath(dir); {
		case err != nil:
			log("Could not add " + dir + " to your PATH (" + err.Error() + "). Assistants use the full path, so they work anyway.")
		case added:
			log("Added " + dir + " to your PATH. New terminals will see it.")
		}
	} else if p, err := i.shellPath(); err != nil || !pathHas(p, dir, false) {
		log(dir + " is not on your PATH. Assistants use the full path, so they work anyway; to run ffc in a terminal, add it to your shell profile:")
		log(`  echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zprofile`)
	}
	return path, nil
}

// writeBinary writes data to path through a temp file in the same folder,
// synced before the rename, and executable. A running ffc.exe cannot be
// overwritten but can be renamed, so on Windows it is moved aside first, as
// `ffc update` and install.ps1 do.
func writeBinary(path string, data []byte, windows bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".ffc-install-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("writing ffc: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("writing ffc: %w", err))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("writing ffc: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("making ffc executable: %w", err)
	}

	if windows {
		if _, err := os.Stat(path); err == nil {
			old := path + ".old"
			if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
				old = fmt.Sprintf("%s.old-%d", path, time.Now().UnixNano())
			}
			if err := os.Rename(path, old); err != nil {
				os.Remove(tmpPath)
				return fmt.Errorf("moving the old ffc aside: %w", err)
			}
			if err := os.Rename(tmpPath, path); err != nil {
				_ = os.Rename(old, path)
				os.Remove(tmpPath)
				return fmt.Errorf("installing ffc: %w", err)
			}
			// Old copies still running fail to delete and go next time.
			matches, _ := filepath.Glob(path + ".old*")
			for _, m := range matches {
				os.Remove(m)
			}
			return nil
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("installing ffc: %w", err)
	}
	return nil
}

// pathHas reports whether the PATH list p has dir as a whole entry.
func pathHas(p, dir string, windows bool) bool {
	sep, trim := ":", "/"
	if windows {
		sep, trim = ";", `\`
	}
	want := strings.TrimRight(dir, trim)
	for _, e := range strings.Split(p, sep) {
		e = strings.TrimRight(e, trim)
		if e == want || (windows && strings.EqualFold(e, want)) {
			return true
		}
	}
	return false
}

// loginShellPath asks the user's login shell for its PATH (at most 5 s).
func loginShellPath() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-c", `printf %s "$PATH"`)
	hideWindow(cmd)
	out, err := cmd.Output()
	return string(out), err
}
