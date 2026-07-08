package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/go-resty/resty/v2"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

// releaseVersionRE matches a clean release version (vX.Y.Z or X.Y.Z).
var releaseVersionRE = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// isDevBuild reports whether v is anything other than a clean release tag. The
// Makefile injects `git describe --tags --always --dirty` for local builds
// (e.g. "abc1234", "v0.1.0-3-gdeadbee", "v0.1.0-dirty"), which must not be
// nagged about updates or treated as outdated (L29).
func isDevBuild(v string) bool {
	return !releaseVersionRE.MatchString(strings.TrimSpace(v))
}

var (
	upCheckOnly bool
	upYes       bool
)

const githubReleasesAPI = "https://api.github.com/repos/nasroykh/foxmayn_frappe_cli/releases/latest"

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update ffc to the latest version",
	Long: `Check GitHub for the latest ffc release and replace the binary in place.

Works regardless of how ffc was installed (curl, powershell, go install).

Examples:
  ffc update           # Check and update (asks for confirmation)
  ffc update --check   # Only check, do not install
  ffc update --yes     # Update without asking for confirmation
`,
	RunE: runUpdate,
}

func runUpdate(_ *cobra.Command, _ []string) error {
	current := version.Version

	// Fetch latest release from GitHub.
	var release githubRelease
	var fetchErr error
	_ = spinner.New().
		Title("Checking for updates…").
		Action(func() {
			resp, err := resty.New().SetTimeout(30*time.Second).R().
				SetResult(&release).
				SetHeader("Accept", "application/vnd.github+json").
				Get(githubReleasesAPI)
			if err != nil {
				fetchErr = fmt.Errorf("fetching release info: %w", err)
				return
			}
			if resp.StatusCode() != 200 {
				fetchErr = fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode())
			}
		}).
		Run()
	if fetchErr != nil {
		return fetchErr
	}

	latest := release.TagName // e.g. "v0.2.0"
	if latest == "" {
		return fmt.Errorf("no releases found on GitHub")
	}

	isDev := isDevBuild(current)
	upToDate := !isDev && !newerThan(current, latest)

	if upToDate {
		output.PrintSuccess(fmt.Sprintf("Already up to date (%s)", current))
		return nil
	}

	if isDev {
		fmt.Fprintf(os.Stderr, "Running a dev build. Latest release: %s\n", latest)
	} else {
		fmt.Fprintf(os.Stderr, "Update available: %s → %s\n", current, latest)
	}

	if upCheckOnly {
		return nil
	}

	// Find the matching release asset for this OS/arch, plus the checksums file.
	target := releaseAssetName(latest)
	var downloadURL, checksumsURL string
	for _, a := range release.Assets {
		switch a.Name {
		case target:
			downloadURL = a.BrowserDownloadURL
		case "checksums.txt":
			checksumsURL = a.BrowserDownloadURL
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no asset found for %s/%s (expected %q)", runtime.GOOS, runtime.GOARCH, target)
	}

	// Confirm before downloading.
	if !upYes {
		var confirmed bool
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Install ffc %s?", latest)).
					Value(&confirmed),
			),
		).WithKeyMap(escQuitKeyMap()).Run()
		// A real prompt error (e.g. no TTY) is distinct from a user cancel (L4).
		if err != nil && !errors.Is(err, huh.ErrUserAborted) {
			return err
		}
		if err != nil || !confirmed {
			fmt.Fprintln(os.Stderr, "Update cancelled.")
			return nil
		}
	}

	// Download archive and replace binary.
	var installErr error
	_ = spinner.New().
		Title(fmt.Sprintf("Downloading ffc %s…", latest)).
		Action(func() {
			installErr = downloadAndInstall(downloadURL, checksumsURL, target)
		}).
		Run()
	if installErr != nil {
		return installErr
	}

	output.PrintSuccess(fmt.Sprintf("Updated to ffc %s", latest))
	return nil
}

// downloadAndInstall fetches the release archive, verifies its SHA-256 against
// the release's checksums.txt, and replaces the running binary.
func downloadAndInstall(downloadURL, checksumsURL, assetName string) error {
	resp, err := resty.New().SetTimeout(5 * time.Minute).R().Get(downloadURL)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	if resp.StatusCode() != 200 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode())
	}

	// Verify the download against the published checksum before trusting it —
	// TLS alone doesn't protect against a compromised release (H1).
	if err := verifyChecksum(resp.Body(), checksumsURL, assetName); err != nil {
		return err
	}

	binName := runningBinaryName()
	var binData []byte
	if runtime.GOOS == "windows" {
		binData, err = extractFromZip(resp.Body(), binName)
	} else {
		binData, err = extractFromTarGz(resp.Body(), binName)
	}
	if err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}

	return replaceBinary(binData)
}

// verifyChecksum computes the SHA-256 of archive and compares it against the
// entry for assetName in the release's checksums.txt (H1).
func verifyChecksum(archive []byte, checksumsURL, assetName string) error {
	if checksumsURL == "" {
		return fmt.Errorf("release has no checksums.txt — refusing to install an unverified binary")
	}
	resp, err := resty.New().SetTimeout(30 * time.Second).R().Get(checksumsURL)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}
	if resp.StatusCode() != 200 {
		return fmt.Errorf("fetching checksums: HTTP %d", resp.StatusCode())
	}

	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(resp.Body()), "\n") {
		// checksums.txt lines are "<hex-sha256>  <filename>".
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			if !strings.EqualFold(fields[0], got) {
				return fmt.Errorf("checksum mismatch for %s:\n  expected %s\n  got      %s", assetName, fields[0], got)
			}
			return nil
		}
	}
	return fmt.Errorf("no checksum entry for %s in checksums.txt", assetName)
}

// replaceBinary writes newData to a temp file then atomically swaps it with
// the currently running binary.
func replaceBinary(newData []byte) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolving symlinks: %w", err)
	}

	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, "ffc-update-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("no write permission to %s — try running with sudo", dir)
		}
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, writeErr := tmp.Write(newData); writeErr != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing update: %w", writeErr)
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("setting permissions: %w", err)
	}

	return swapExecutable(exePath, tmpPath)
}

// swapExecutable replaces current with replacement.
// On Windows, the running binary cannot be overwritten directly, so we
// rename the old binary out of the way first (the OS still holds it open),
// then rename the new binary into place.
func swapExecutable(current, replacement string) error {
	if runtime.GOOS == "windows" {
		old := current + ".old"
		os.Remove(old) // remove leftover from any previous update
		if err := os.Rename(current, old); err != nil {
			os.Remove(replacement)
			return fmt.Errorf("moving old binary: %w", err)
		}
		if err := os.Rename(replacement, current); err != nil {
			_ = os.Rename(old, current) // try to restore
			return fmt.Errorf("installing new binary: %w", err)
		}
		return nil
	}
	// Unix: os.Rename is atomic on the same filesystem.
	if err := os.Rename(replacement, current); err != nil {
		os.Remove(replacement)
		return fmt.Errorf("replacing binary: %w", err)
	}
	return nil
}

// releaseAssetName returns the GoReleaser archive filename for the current
// platform. GoReleaser strips the leading "v" from the tag for .Version.
//
// Example: "v0.2.0" → "ffc_0.2.0_linux_amd64.tar.gz"
func releaseAssetName(tagVersion string) string {
	ver := strings.TrimPrefix(tagVersion, "v")
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("ffc_%s_%s_%s.%s", ver, runtime.GOOS, runtime.GOARCH, ext)
}

// runningBinaryName returns the expected binary filename inside the archive.
func runningBinaryName() string {
	if runtime.GOOS == "windows" {
		return "ffc.exe"
	}
	return "ffc"
}

func extractFromTarGz(data []byte, name string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decompressing gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if filepath.Base(hdr.Name) == name {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%q not found in archive", name)
}

func extractFromZip(data []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("opening zip: %w", err)
	}
	for _, f := range r.File {
		if filepath.Base(f.Name) == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%q not found in zip", name)
}

// newerThan reports whether latest is a higher semver than current.
// Both may or may not carry a leading "v".
func newerThan(current, latest string) bool {
	curBase, curPre := splitPreRelease(strings.TrimPrefix(current, "v"))
	latBase, latPre := splitPreRelease(strings.TrimPrefix(latest, "v"))
	cur := parseSemver(curBase)
	lat := parseSemver(latBase)
	for i := 0; i < 3; i++ {
		if lat[i] > cur[i] {
			return true
		}
		if lat[i] < cur[i] {
			return false
		}
	}
	// Same numeric version: a final release is newer than a pre-release, so
	// v1.6.0 supersedes v1.6.0-rc1 (L30). We never offer a pre-release as an
	// "update" over a final release.
	return curPre != "" && latPre == ""
}

// splitPreRelease separates "1.6.0-rc1" into ("1.6.0", "rc1").
func splitPreRelease(s string) (base, pre string) {
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func parseSemver(s string) []int {
	parts := strings.SplitN(s, ".", 3)
	out := make([]int, 3)
	for i, p := range parts {
		if i >= 3 {
			break
		}
		// Strip pre-release suffix (e.g. "1-alpha" → "1").
		if idx := strings.IndexAny(p, "-+"); idx >= 0 {
			p = p[:idx]
		}
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

func init() {
	updateCmd.Flags().BoolVar(&upCheckOnly, "check", false, "Only check for updates, do not install")
	updateCmd.Flags().BoolVarP(&upYes, "yes", "y", false, "Skip confirmation prompt")
	rootCmd.AddCommand(updateCmd)
}
