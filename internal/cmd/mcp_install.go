package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/mcpinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// mcp install flags
var (
	miClient   string
	miName     string
	miReadOnly bool
	miPrint    bool
	miYes      bool
)

// Seams for tests: the client environment (home and config dirs, the claude
// CLI) and the path of the running binary.
var (
	mcpInstallEnv        = mcpinstall.DefaultEnv
	mcpInstallExecutable = installedExecutable
)

var mcpInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Add ffc as an MCP server to Claude Code, Claude Desktop, Cursor, VS Code or Codex",
	Long: `Add (or replace) the stdio MCP server entry that runs ffc in an AI client's
user-level config, so every project sees it.

The entry runs this ffc binary by its absolute path with "mcp", plus
"--site NAME" when --site is given (otherwise the server follows
'ffc site use') and "--read-only" when set. A config file other than the
default (--config or FFC_CONFIG) is added as "--config <absolute path>".
The default entry name, frappe,
is the one the old Node installer used, so its entry is replaced.

  claude-code     runs: claude mcp add-json --scope user <name> '<json>'
                  (Claude Code owns ~/.claude.json; ffc never edits it)
  claude-desktop  <config dir>/Claude/claude_desktop_config.json
  cursor          ~/.cursor/mcp.json
  vscode          <config dir>/Code/User/mcp.json (default profile)
  codex           ~/.codex/config.toml ($CODEX_HOME/config.toml when set)

<config dir> is %APPDATA% on Windows, ~/Library/Application Support on macOS
and $XDG_CONFIG_HOME or ~/.config on Linux.

The change is shown as a diff (the command, for claude-code) and confirmed
before anything is written. The old file is kept as
<file>.ffc-<YYYYMMDD-HHMMSS>.bak. Comments and formatting are kept; a file
that does not parse is refused and left untouched.

Examples:
  ffc mcp install --client claude-desktop
  ffc mcp install --client cursor --site prod --read-only
  ffc mcp install --client codex --print
  ffc mcp install --client vscode --name frappe-prod --site prod --yes
`,
	Args: cobra.NoArgs,
	RunE: runMCPInstall,
}

// installResult is the machine-readable result.
type installResult struct {
	Client  string   `json:"client"`
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Backup  string   `json:"backup"`
	Changed bool     `json:"changed"`
	Applied bool     `json:"applied"`
	Server  []string `json:"server"`
	Command []string `json:"command"`
}

func runMCPInstall(cmd *cobra.Command, _ []string) error {
	if miClient == "" {
		return usageErrorf("--client is required: one of %s", strings.Join(mcpinstall.Clients, ", "))
	}
	if !slices.Contains(mcpinstall.Clients, miClient) {
		return usageErrorf("--client %q: want one of %s (ChatGPT needs a remote server and is not supported)",
			miClient, strings.Join(mcpinstall.Clients, ", "))
	}
	if err := mcpinstall.ValidName(miName); err != nil {
		return &usageError{fmt.Errorf("--name: %w", err)}
	}

	exe, err := mcpInstallExecutable()
	if err != nil {
		return err
	}
	args := []string{"mcp"}
	// A config other than the default one (--config or FFC_CONFIG) goes into
	// the entry: the server would otherwise read the default config, where
	// the site may be missing or be another site of the same name.
	if configPath != "" {
		abs, err := filepath.Abs(configPath)
		if err != nil {
			return &usageError{fmt.Errorf("--config %q: %w", configPath, err)}
		}
		if def, err := config.DefaultConfigPath(); err != nil || !samePath(abs, def) {
			args = append(args, "--config", abs)
		}
	}
	// Only an explicit --site is pinned (FFC_SITE is not): without it the
	// server follows 'ffc site use'.
	if cmd.Flags().Changed("site") {
		cfg, err := loadSiteConfig()
		if err != nil {
			return &usageError{fmt.Errorf("--site %q: %w", siteName, err)}
		}
		if cfg.Name == "" {
			return usageErrorf("--site %q: the site must be saved in the config file", siteName)
		}
		args = append(args, "--site", cfg.Name) // the exact config key
	}
	if miReadOnly {
		args = append(args, "--read-only")
	}

	env, err := mcpInstallEnv()
	if err != nil {
		return err
	}
	ch, err := mcpinstall.Plan(miClient, mcpinstall.Server{Name: miName, Command: exe, Args: args}, env)
	if err != nil {
		if errors.Is(err, mcpinstall.ErrInvalid) {
			return &usageError{err}
		}
		return fmt.Errorf("mcp install: %w", err)
	}

	human := !machineOutput()
	res := installResult{
		Client:  ch.Client,
		Name:    ch.Name,
		Path:    ch.Path,
		Changed: ch.Changed(),
		Server:  append([]string{exe}, args...),
		Command: ch.CommandLines(),
	}
	if !ch.Changed() {
		if !human {
			return printResult(res)
		}
		output.PrintSuccess(fmt.Sprintf("The %q entry in %s is already up to date", ch.Name, ch.Path))
		return nil
	}

	if human || (!miPrint && !miYes) {
		// --print makes the diff or the command the output; otherwise it is
		// shown before the question (on stderr, also with --json).
		var w io.Writer = os.Stderr
		if miPrint {
			w = os.Stdout
		}
		printInstallPlan(w, ch)
	}
	if miPrint {
		if !human {
			return printResult(res)
		}
		return nil
	}
	// Refuse what Apply would refuse before asking: claude missing or a
	// batch shim, a read-only file.
	if err := ch.Check(); err != nil {
		if errors.Is(err, mcpinstall.ErrClaudeNotFound) || errors.Is(err, mcpinstall.ErrClaudeBatch) {
			return &usageError{err}
		}
		return fmt.Errorf("mcp install: %w", err)
	}
	if !miYes {
		prompt := fmt.Sprintf("Write %s?", ch.Path)
		if ch.Client == mcpinstall.ClaudeCode {
			prompt = "Run the claude command above?"
		}
		if err := confirm(prompt); err != nil {
			return err
		}
	}

	backup, err := ch.Apply()
	if err != nil {
		return fmt.Errorf("mcp install: %w", err)
	}
	res.Backup, res.Applied = backup, true
	if !human {
		return printResult(res)
	}
	if ch.Client == mcpinstall.ClaudeCode {
		output.PrintSuccess(fmt.Sprintf("Added %q to Claude Code's user config", ch.Name))
	} else {
		output.PrintSuccess(fmt.Sprintf("Wrote the %q entry to %s", ch.Name, ch.Path))
	}
	if backup != "" {
		fmt.Fprintf(os.Stderr, "Backup: %s\n", backup)
	}
	fmt.Fprintln(os.Stderr, ch.Hint)
	return nil
}

// printInstallPlan shows what Apply would do: the explanation on stderr,
// the diff or the commands on w.
func printInstallPlan(w io.Writer, ch *mcpinstall.Change) {
	if ch.Client == mcpinstall.ClaudeCode {
		fmt.Fprintf(os.Stderr, "Claude Code keeps user-scope MCP servers in %s and rewrites that file itself,\n"+
			"so ffc runs the claude CLI instead (no diff, no backup):\n", ch.Path)
		if ch.Replaces {
			fmt.Fprintf(os.Stderr, "(replaces the existing %q entry)\n", ch.Name)
		}
		for _, l := range ch.CommandLines() {
			fmt.Fprintln(w, text.Sanitize(l))
		}
		return
	}
	switch {
	case ch.Old == nil:
		fmt.Fprintf(os.Stderr, "%s does not exist yet; it will be created.\n", ch.Path)
	case ch.Replaces:
		fmt.Fprintf(os.Stderr, "Replacing the %q entry in %s:\n", ch.Name, ch.Path)
	default:
		fmt.Fprintf(os.Stderr, "Adding the %q entry to %s:\n", ch.Name, ch.Path)
	}
	fmt.Fprint(w, text.Sanitize(ch.Diff()))
}

// installedExecutable is the absolute path of the running ffc, with
// symlinks resolved. A temporary 'go run' build is refused: the entry would
// point at a file Go deletes.
func installedExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding the ffc binary: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if isGoRunBuild(exe, os.TempDir(), os.Getenv("GOTMPDIR")) {
		return "", usageErrorf("%s is a temporary 'go run' build; install ffc first (see the README) and run 'ffc mcp install' with the installed binary", exe)
	}
	return exe, nil
}

// samePath compares two cleaned absolute paths, ignoring case on Windows.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// isGoRunBuild reports whether exe looks like a go run build: a go-build
// directory under one of the temp dirs.
func isGoRunBuild(exe string, tmpDirs ...string) bool {
	if !strings.Contains(exe, "go-build") {
		return false
	}
	for _, dir := range tmpDirs {
		if dir == "" {
			continue
		}
		dirs := []string{dir}
		if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
			dirs = append(dirs, real)
		}
		for _, d := range dirs {
			rel, err := filepath.Rel(d, exe)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && strings.Contains(rel, "go-build") {
				return true
			}
		}
	}
	return false
}

func init() {
	mcpInstallCmd.Flags().StringVar(&miClient, "client", "", "AI client: "+strings.Join(mcpinstall.Clients, ", ")+" (required)")
	mcpInstallCmd.Flags().StringVar(&miName, "name", mcpinstall.DefaultName, "Name of the server entry")
	mcpInstallCmd.Flags().BoolVar(&miReadOnly, "read-only", false, "Install the server with --read-only (only read tools)")
	mcpInstallCmd.Flags().BoolVar(&miPrint, "print", false, "Print the diff (or the claude command) and change nothing")
	mcpInstallCmd.Flags().BoolVarP(&miYes, "yes", "y", false, "Write without asking")
	mcpInstallCmd.MarkFlagsMutuallyExclusive("print", "yes")
	mcpCmd.AddCommand(mcpInstallCmd)
}
