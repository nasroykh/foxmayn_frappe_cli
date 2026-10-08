package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/release"
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

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update ffc to the latest version",
	Long: `Check GitHub for the latest ffc release and replace the binary in place.

Works for the install scripts (curl, powershell) and go install. A copy that
Homebrew, Scoop or winget installed is updated through that package manager:
ffc update then only says which command to run.

Examples:
  ffc update           # Check and update (asks for confirmation)
  ffc update --check   # Only check, do not install
  ffc update --yes     # Update without asking for confirmation
`,
	RunE: runUpdate,
}

// updateKind classifies how latest relates to the running version.
type updateKind int

const (
	kindUpToDate updateKind = iota
	kindUpdate
	kindDowngrade
	kindDev // unparseable dev build (e.g. a bare commit hash)
)

// gitDescribeRE matches the suffixes `git describe --dirty` adds to a build
// that is ahead of its tag or has local edits.
var gitDescribeRE = regexp.MustCompile(`-\d+-g[0-9a-f]+|-dirty`)

var numericBaseRE = regexp.MustCompile(`^v?\d+\.\d+\.\d+`)

// classifyUpdate compares the numeric base of both versions so a pre-release
// or git-describe build ahead of the latest stable is never offered a silent
// downgrade (D23).
func classifyUpdate(current, latest string) updateKind {
	current = strings.TrimSpace(current)
	if !numericBaseRE.MatchString(current) {
		return kindDev
	}
	curBase, curPre := splitPreRelease(strings.TrimPrefix(current, "v"))
	latBase, _ := splitPreRelease(strings.TrimPrefix(latest, "v"))
	cur, lat := parseSemver(curBase), parseSemver(latBase)
	for i := 0; i < 3; i++ {
		if lat[i] > cur[i] {
			return kindUpdate
		}
		if lat[i] < cur[i] {
			return kindDowngrade
		}
	}
	switch {
	case curPre == "":
		return kindUpToDate
	case gitDescribeRE.MatchString(current):
		return kindDowngrade // ahead of (or edited on top of) this release
	default:
		return kindUpdate // rc of this version: the final supersedes it
	}
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	current := version.Version

	// Fetch latest release from GitHub.
	var rel *release.Release
	var fetchErr error
	if err := runSpinner("Checking for updates…", func() {
		rel, fetchErr = release.Latest(ctx, releasesURL, 30*time.Second)
	}); err != nil {
		return err
	}
	if fetchErr != nil {
		return fetchErr
	}

	latest := rel.TagName // e.g. "v0.2.0"
	kind := classifyUpdate(current, latest)
	switch kind {
	case kindUpToDate:
		output.PrintSuccess(fmt.Sprintf("Already up to date (%s)", current))
		return nil
	case kindDev:
		fmt.Fprintf(os.Stderr, "Running a dev build. Latest release: %s\n", latest)
	case kindDowngrade:
		fmt.Fprintf(os.Stderr, "Downgrade: you run %s, which is newer than the latest release %s\n", current, latest)
	default:
		fmt.Fprintf(os.Stderr, "Update available: %s → %s\n", current, latest)
	}

	if upCheckOnly {
		return nil
	}
	// The package manager must keep knowing which version it installed.
	if m := managedInstall(); m != nil {
		return fmt.Errorf("ffc was installed with %s; update it with: %s", m.Name, m.Command)
	}

	// Find the matching release asset for this OS/arch, plus the checksums
	// file and its signature.
	target, err := rel.Target(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	// Confirm before downloading. --yes also confirms a downgrade; the notice
	// above is always printed.
	if !upYes {
		title := fmt.Sprintf("Install ffc %s?", latest)
		if kind == kindDowngrade {
			title = fmt.Sprintf("Downgrade to ffc %s?", latest)
		}
		// Decline or Esc exits non-zero; no TTY fails with a --yes hint.
		if err := confirm(title); err != nil {
			return err
		}
	}

	// Download archive and replace binary.
	var installErr error
	if err := runSpinner(fmt.Sprintf("Downloading ffc %s…", latest), func() {
		installErr = downloadAndInstall(ctx, target)
	}); err != nil {
		return err
	}
	if installErr != nil {
		return installErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	output.PrintSuccess(fmt.Sprintf("Updated to ffc %s", latest))
	return nil
}

// downloadAndInstall fetches and verifies the release archive (signed
// checksums.txt, then SHA-256) and replaces the running binary.
func downloadAndInstall(ctx context.Context, target release.Target) error {
	binData, err := release.Download(ctx, target)
	if err != nil {
		return err
	}
	// Do not touch the installed binary once the user has interrupted.
	if err := ctx.Err(); err != nil {
		return err
	}
	return replaceBinary(binData)
}

// releasesURL is release.ReleasesURL; tests point it at a fake.
var releasesURL = release.ReleasesURL

// executablePath is the running binary with symlinks resolved: the file
// ffc update replaces. Tests replace it.
var executablePath = func() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks: %w", err)
	}
	return exePath, nil
}

// managedInstall returns the package manager that installed the running
// binary, or nil (also when its path cannot be found).
func managedInstall() *release.Manager {
	p, err := executablePath()
	if err != nil {
		return nil
	}
	return release.ManagedBy(p)
}

// updateCommand is the command that updates this ffc: ffc update, or the
// package manager's own.
func updateCommand() string {
	if m := managedInstall(); m != nil {
		return m.Command
	}
	return "ffc update"
}

// replaceBinary writes newData to a temp file then atomically swaps it with
// the currently running binary.
func replaceBinary(newData []byte) error {
	exePath, err := executablePath()
	if err != nil {
		return err
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
	// Sync before rename: a rename can become durable before the data, and a
	// power loss would then leave a zero-length binary (D21).
	if syncErr := tmp.Sync(); syncErr != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing update: %w", syncErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("writing update: %w", closeErr)
	}

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
		// A previous .old may still be a running image (e.g. a detached MCP
		// server) and so cannot be removed; use a unique name then (D22).
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			old = fmt.Sprintf("%s.old-%d", current, time.Now().UnixNano())
		}
		if err := os.Rename(current, old); err != nil {
			os.Remove(replacement)
			return fmt.Errorf("moving old binary: %w", err)
		}
		if err := os.Rename(replacement, current); err != nil {
			_ = os.Rename(old, current) // try to restore
			return fmt.Errorf("installing new binary: %w", err)
		}
		cleanupStaleOld(current)
		return nil
	}
	// Unix: os.Rename is atomic on the same filesystem.
	if err := os.Rename(replacement, current); err != nil {
		os.Remove(replacement)
		return fmt.Errorf("replacing binary: %w", err)
	}
	return nil
}

// cleanupStaleOld best-effort removes leftover ffc.exe.old* files from earlier
// updates. Ones still in use fail to delete and are retried next time (D22).
func cleanupStaleOld(current string) {
	matches, _ := filepath.Glob(current + ".old*")
	for _, m := range matches {
		os.Remove(m)
	}
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
