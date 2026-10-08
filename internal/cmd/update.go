package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	upVersion   string
	upRollback  bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update ffc to the latest version",
	Long: `Check GitHub for the latest ffc release and replace the binary in place.

Works for the install scripts (curl, powershell) and go install. A copy that
Homebrew, Scoop or winget installed is updated through that package manager:
ffc update then only says which command to run.

Every update keeps the replaced binary next to it (ffc.prev, ffc.prev.exe on
Windows); --rollback swaps the two back. A version from --version passes the
same signature and checksum checks as the latest one.

Examples:
  ffc update                    # Check and update (asks for confirmation)
  ffc update --check            # Only check, do not install
  ffc update --yes              # Update without asking for confirmation
  ffc update --version v1.15.0  # Install that release (also an older one)
  ffc update --rollback         # Go back to the binary the last update replaced
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
	if upRollback {
		if upVersion != "" || upCheckOnly {
			return usageErrorf("--rollback cannot be combined with --version or --check")
		}
		return runRollback(ctx)
	}
	pinned := ""
	if upVersion != "" {
		tag, err := versionTag(upVersion)
		if err != nil {
			return err
		}
		pinned = tag
	}
	current := version.Version

	// Fetch the latest (or the named) release from GitHub.
	var rel *release.Release
	var fetchErr error
	if err := runSpinner("Checking for updates…", func() {
		if pinned != "" {
			rel, fetchErr = release.Tagged(ctx, releasesBase, pinned, 30*time.Second)
		} else {
			rel, fetchErr = release.Latest(ctx, releasesURL, 30*time.Second)
		}
	}); err != nil {
		return err
	}
	if fetchErr != nil {
		return fetchErr
	}

	latest := rel.TagName // e.g. "v0.2.0"
	kind := classifyUpdate(current, latest)
	if pinned != "" {
		kind = classifyPin(current, latest)
	}
	switch {
	case kind == kindUpToDate && pinned != "":
		output.PrintSuccess(fmt.Sprintf("Already running %s", current))
		return nil
	case kind == kindUpToDate:
		output.PrintSuccess(fmt.Sprintf("Already up to date (%s)", current))
		return nil
	case pinned != "":
		fmt.Fprintf(os.Stderr, "Release %s found (you run %s)\n", latest, current)
	case kind == kindDev:
		fmt.Fprintf(os.Stderr, "Running a dev build. Latest release: %s\n", latest)
	case kind == kindDowngrade:
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

// pinnedVersionRE matches a --version value: a release or a pre-release.
var pinnedVersionRE = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)

// versionTag turns a --version value into its tag ("1.2.3" → "v1.2.3").
func versionTag(v string) (string, error) {
	v = strings.TrimSpace(v)
	if !pinnedVersionRE.MatchString(v) {
		return "", usageErrorf("--version %q: want a release version such as v1.15.0", v)
	}
	return "v" + strings.TrimPrefix(v, "v"), nil
}

// classifyPin compares the running version with a release the user named:
// the same version is up to date, else an update or a downgrade.
func classifyPin(current, target string) updateKind {
	current = strings.TrimSpace(current)
	switch {
	case !numericBaseRE.MatchString(current):
		return kindDev
	case strings.TrimPrefix(current, "v") == strings.TrimPrefix(target, "v"):
		return kindUpToDate
	case newerThan(current, target):
		return kindUpdate
	}
	return kindDowngrade
}

// runRollback swaps the running binary with the one the last update replaced
// (prevPath). The swap keeps the running one as the new previous binary, so a
// second rollback undoes the first.
func runRollback(ctx context.Context) error {
	if m := managedInstall(); m != nil {
		return fmt.Errorf("ffc was installed with %s; change versions with it (%s)", m.Name, m.Command)
	}
	exe, err := executablePath()
	if err != nil {
		return err
	}
	prev := prevPath(exe)
	data, err := readPrevious(prev)
	if err != nil {
		return err
	}
	label := "the previous ffc"
	if v := binaryVersion(ctx, prev); v != "" {
		label = "ffc " + v
	}
	fmt.Fprintf(os.Stderr, "Roll back: %s → %s (%s)\n", version.Version, label, prev)
	if !upYes {
		if err := confirm(fmt.Sprintf("Roll back to %s?", label)); err != nil {
			return err
		}
	}
	if err := replaceBinary(data); err != nil {
		return err
	}
	output.PrintSuccess(fmt.Sprintf("Rolled back to %s (ffc update --rollback again undoes it)", label))
	return nil
}

// prevPath is where an update keeps the binary it replaced.
// Windows keeps the .exe extension, so the copy still runs (ffc.prev.exe).
func prevPath(exe string) string {
	if ext := filepath.Ext(exe); strings.EqualFold(ext, ".exe") {
		return strings.TrimSuffix(exe, ext) + ".prev" + ext
	}
	return exe + ".prev"
}

// readPrevious reads the kept binary, refusing a missing, empty or oversized
// file (the bounds of an extracted release binary).
func readPrevious(prev string) ([]byte, error) {
	f, err := os.Open(prev)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no previous version to roll back to: %s does not exist (each ffc update from this version on keeps one)", prev)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, release.MaxBinaryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) > release.MaxBinaryBytes {
		return nil, fmt.Errorf("%s is not a usable ffc binary (%d bytes)", prev, len(data))
	}
	return data, nil
}

var versionOutputRE = regexp.MustCompile(`^ffc version (\S+)`)

// binaryVersion runs path --version, or returns "" when it does not answer
// like ffc within a few seconds.
func binaryVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, path, "--version")
	c.Env = append(os.Environ(), "FFC_NO_UPDATE_CHECK=1")
	out, err := c.Output()
	if err != nil {
		return ""
	}
	if m := versionOutputRE.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

// keepPrevious copies the binary at exe to prevPath(exe) through a temp file,
// replacing an older copy, so --rollback can restore it.
func keepPrevious(exe string) error {
	src, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(exe), "ffc-prev-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, src)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), prevPath(exe))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
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

// releasesURL and releasesBase are release.ReleasesURL and release.APIBase;
// tests point them at a fake.
var (
	releasesURL  = release.ReleasesURL
	releasesBase = release.APIBase
)

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

	// A failure here costs only the rollback, not the update.
	if err := keepPrevious(exePath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not keep the current binary for --rollback: %v\n", err)
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
	updateCmd.Flags().StringVar(&upVersion, "version", "", "Install this release (e.g. v1.15.0) instead of the latest")
	updateCmd.Flags().BoolVar(&upRollback, "rollback", false, "Swap back to the binary the last update replaced")
	rootCmd.AddCommand(updateCmd)
}
