package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/desktopinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/spf13/cobra"
)

// Seams for tests: the host OS, the installer (tests aim it at a fake
// release server and a recorded Run), the home directory and the lookup of
// the installed copy (the real one reads /Applications and the registry).
var (
	desktopGOOS         = runtime.GOOS
	newDesktopInstaller = desktopinstall.New
	desktopHome         = os.UserHomeDir
	desktopLocate       = func(in *desktopinstall.Installer, home string) (desktopinstall.Target, error) {
		return in.LocateInstalled(home, os.Getenv)
	}
)

const desktopName = "Foxmayn Frappe Desktop"

var deskYes bool

var desktopCmd = &cobra.Command{
	Use:   "desktop",
	Short: "Check for and install the Foxmayn Frappe Desktop app (Windows, macOS)",
	Long: `Check for and install the Foxmayn Frappe Desktop app.

Works on Windows and macOS only. The app is installed per user, with the same
checks the app runs when it updates itself: the release's checksums.txt carries
a valid signature from a release key built into ffc, and the download matches
its SHA-256 checksum.`,
}

var desktopStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the installed desktop app and the latest release",
	Long: `Show whether Foxmayn Frappe Desktop is installed, its version, and the newest
desktop release. Exits 0 whether or not an update exists: scripts read
update_available.

Examples:
  ffc desktop status
  ffc desktop status --json
  ffc desktop status --jq .update_available
`,
	Args: cobra.NoArgs,
	RunE: runDesktopStatus,
}

var desktopInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install or update the desktop app",
	Long: `Install Foxmayn Frappe Desktop, or update it to the newest release.

Windows runs the per-user setup silently. macOS copies the app from the disk
image into /Applications, or into ~/Applications when /Applications is not
writable (an existing copy is updated where it is). The previous copy stays in
place until the new one passes its checks.

Nothing happens when the installed version is already the newest, and ffc never
downgrades. ffc does not quit or open the app: restart it to use the new version.

Examples:
  ffc desktop install             # asks first
  ffc desktop install --yes       # no question
  ffc desktop install --dry-run   # show what would be installed
`,
	Args: cobra.NoArgs,
	RunE: runDesktopInstall,
}

func init() {
	desktopInstallCmd.Flags().BoolVarP(&deskYes, "yes", "y", false, "Skip the confirmation")
	addDryRun(desktopInstallCmd, false)
	desktopCmd.AddCommand(desktopStatusCmd, desktopInstallCmd)
	rootCmd.AddCommand(desktopCmd)
}

// desktopStatus is the result of `ffc desktop status`.
type desktopStatus struct {
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version"`
	Path             string `json:"path"`
	Latest           string `json:"latest"`
	UpdateAvailable  bool   `json:"update_available"`
	ReleaseURL       string `json:"release_url"`
}

// desktopPlanView is what `install --dry-run` would do.
type desktopPlanView struct {
	Action string `json:"action"` // install | update
	From   string `json:"from"`
	To     string `json:"to"`
	Asset  string `json:"asset"`
	Path   string `json:"path"`
	DryRun bool   `json:"dry_run"`
}

// desktopResult is the outcome of `ffc desktop install`.
type desktopResult struct {
	Action        string `json:"action"` // install | update | none (already the newest)
	Version       string `json:"version"`
	Path          string `json:"path"`
	RestartNeeded bool   `json:"restart_needed"`
}

// desktopError maps the desktopinstall classes to exit codes (CLAUDE.md,
// exit codes). A cancelled context stays as it is: classify makes it 130.
func desktopError(err error) error {
	switch {
	case err == nil, errors.Is(err, context.Canceled), errors.Is(err, errAborted):
		return err
	case errors.Is(err, desktopinstall.ErrUnsupported):
		return &usageError{err}
	case errors.Is(err, desktopinstall.ErrNetwork), errors.Is(err, desktopinstall.ErrRateLimited):
		return &codeError{code: exitNetwork, msg: err.Error()}
	case errors.Is(err, desktopinstall.ErrNotWritable):
		return &codeError{code: exitPermission, msg: err.Error()}
	}
	// ErrVerify, ErrAsset, ErrInstall, ErrNoRelease and the rest: exit 1.
	return err
}

// desktopSetup refuses other systems before any network call, builds the
// installer and finds the installed copy.
func desktopSetup(cmd *cobra.Command) (*desktopinstall.Installer, desktopinstall.Target, error) {
	if desktopGOOS != "windows" && desktopGOOS != "darwin" {
		return nil, desktopinstall.Target{}, usageErrorf("%s is only available for Windows and macOS", desktopName)
	}
	in := newDesktopInstaller(desktopGOOS)
	// Steps go to stderr; the spinner shows its own title instead.
	in.Log = func(s string) {
		if !quiet && !spinnerEnabled() {
			fmt.Fprintln(os.Stderr, s)
		}
	}
	home, _ := desktopHome() // "" only loses the ~/Applications fallback
	t, err := desktopLocate(in, home)
	if err != nil {
		return nil, t, desktopError(fmt.Errorf("locating the installed app: %w", err))
	}
	return in, t, nil
}

func cmdContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// installedVersion is the version of an installed copy; ok is false when it
// is not installed or the system did not say.
func installedVersion(t desktopinstall.Target) (desktopinstall.Version, bool) {
	if !t.Installed || t.Version == "" {
		return desktopinstall.Version{}, false
	}
	return desktopinstall.ParseVersion(t.Version)
}

func runDesktopStatus(cmd *cobra.Command, _ []string) error {
	ctx := cmdContext(cmd)
	in, t, err := desktopSetup(cmd)
	if err != nil {
		return err
	}
	var plan desktopinstall.Plan
	var planErr error
	if err := runSpinner("Checking for the latest release…", func() { plan, planErr = in.Latest(ctx) }); err != nil {
		return err
	}
	if planErr != nil {
		return desktopError(planErr)
	}
	st := desktopStatus{
		Installed:        t.Installed,
		InstalledVersion: t.Version,
		Path:             t.Path,
		Latest:           plan.Version.String(),
		ReleaseURL:       plan.PageURL,
	}
	if t.Installed {
		// An installed copy whose version is unknown cannot be shown to be
		// current, so it counts as outdated.
		cur, ok := installedVersion(t)
		st.UpdateAvailable = !ok || plan.Version.Compare(cur) > 0
	}
	fields := []string{"installed", "installed_version", "path", "latest", "update_available", "release_url"}
	return render(st, fields, func() error {
		output.PrintDocTable(map[string]interface{}{
			"installed":         st.Installed,
			"installed_version": st.InstalledVersion,
			"path":              st.Path,
			"latest":            st.Latest,
			"update_available":  st.UpdateAvailable,
			"release_url":       st.ReleaseURL,
		}, fields)
		return nil
	})
}

func runDesktopInstall(cmd *cobra.Command, _ []string) error {
	ctx := cmdContext(cmd)
	in, t, err := desktopSetup(cmd)
	if err != nil {
		return err
	}

	cur, haveCur := installedVersion(t)
	var plan desktopinstall.Plan
	var planErr error
	if err := runSpinner("Checking for the latest release…", func() {
		if haveCur {
			plan, planErr = in.Find(ctx, cur)
		} else {
			plan, planErr = in.Latest(ctx)
		}
	}); err != nil {
		return err
	}
	if errors.Is(planErr, desktopinstall.ErrNotNewer) {
		if !machineOutput() {
			output.PrintSuccess(fmt.Sprintf("%s %s is up to date", desktopName, t.Version))
		}
		return printDesktopResult(desktopResult{Action: "none", Version: t.Version, Path: t.Path})
	}
	if planErr != nil {
		return desktopError(planErr)
	}

	action := "install"
	if t.Installed {
		action = "update"
	}
	to := plan.Version.String()

	if dryRunOn(cmd) {
		return render(desktopPlanView{Action: action, From: t.Version, To: to, Asset: plan.Asset, Path: t.Path, DryRun: true},
			[]string{"action", "from", "to", "asset", "path", "dry_run"}, func() error {
				output.PrintDocTable(map[string]interface{}{
					"action": action, "from": t.Version, "to": to, "asset": plan.Asset, "path": t.Path,
				}, []string{"action", "from", "to", "asset", "path"})
				return nil
			})
	}

	if !deskYes {
		title := fmt.Sprintf("Install %s %s?", desktopName, to)
		if action == "update" {
			title = fmt.Sprintf("Update %s from %s to %s?", desktopName, orUnknown(t.Version), to)
		}
		if err := confirm(title); err != nil {
			return err
		}
	}

	dir, err := desktopinstall.NewTempDir()
	if err != nil {
		return fmt.Errorf("creating a private download folder: %w", err)
	}
	defer os.RemoveAll(dir)

	var path string
	var dlErr error
	if err := runSpinner(fmt.Sprintf("Downloading %s %s…", desktopName, to), func() {
		path, dlErr = in.Download(ctx, plan, dir, nil)
	}); err != nil {
		return err
	}
	if dlErr != nil {
		return desktopError(dlErr)
	}

	var applyErr error
	if err := runSpinner(fmt.Sprintf("Installing %s %s…", desktopName, to), func() {
		applyErr = in.Apply(ctx, t, path, plan.Version)
	}); err != nil {
		return err
	}
	if applyErr != nil {
		return desktopError(applyErr)
	}
	desktopinstall.CleanupOld(t) // best effort; a running Windows exe stays until next time

	res := desktopResult{Action: action, Version: to, Path: t.Path, RestartNeeded: t.Installed}
	if !machineOutput() {
		output.PrintSuccess(fmt.Sprintf("%s %s: %s %s", map[string]string{"install": "Installed", "update": "Updated"}[action], desktopName, to, t.Path))
	}
	if res.RestartNeeded {
		fmt.Fprintf(os.Stderr, "If %s is open, restart it to use %s.\n", desktopName, to)
	}
	return printDesktopResult(res)
}

func orUnknown(v string) string {
	if v == "" {
		return "an unknown version"
	}
	return v
}

func printDesktopResult(r desktopResult) error {
	fields := []string{"action", "version", "path", "restart_needed"}
	return render(r, fields, func() error {
		output.PrintDocTable(map[string]interface{}{
			"action": r.Action, "version": r.Version, "path": r.Path, "restart_needed": r.RestartNeeded,
		}, fields)
		return nil
	})
}
