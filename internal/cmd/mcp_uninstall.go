package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/mcpinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// mcp uninstall flags
var (
	muClient string
	muName   string
	muPrint  bool
	muYes    bool
)

var mcpUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the ffc MCP server entry from Claude Code, Claude Desktop, Cursor, VS Code or Codex",
	Long: `Remove the MCP server entry named --name (default frappe) from an AI
client's user-level config: the reverse of 'ffc mcp install'.

  claude-code     runs: claude mcp remove --scope user <name>
                  (Claude Code owns ~/.claude.json; ffc only reads it)
  claude-desktop  <config dir>/Claude/claude_desktop_config.json
  cursor          ~/.cursor/mcp.json
  vscode          <config dir>/Code/User/mcp.json (default profile)
  codex           ~/.codex/config.toml ($CODEX_HOME/config.toml when set)

<config dir> is %APPDATA% on Windows, ~/Library/Application Support on macOS
and $XDG_CONFIG_HOME or ~/.config on Linux.

The change is shown as a diff (the command, for claude-code) and confirmed
before anything is written. The old file is kept as
<file>.ffc-<YYYYMMDD-HHMMSS>.bak. Only the entry goes: other servers,
comments and formatting are kept; a file that does not parse is refused and
left untouched. When there is no such entry, nothing changes (exit 0).

Examples:
  ffc mcp uninstall --client claude-desktop
  ffc mcp uninstall --client codex --print
  ffc mcp uninstall --client vscode --name frappe-prod --yes
`,
	Args: cobra.NoArgs,
	RunE: runMCPUninstall,
}

// uninstallResult is the machine-readable result.
type uninstallResult struct {
	Client  string   `json:"client"`
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Backup  string   `json:"backup"`
	Changed bool     `json:"changed"`
	Applied bool     `json:"applied"`
	Command []string `json:"command"`
}

func runMCPUninstall(_ *cobra.Command, _ []string) error {
	if muClient == "" {
		return usageErrorf("--client is required: one of %s", strings.Join(mcpinstall.Clients, ", "))
	}
	if !slices.Contains(mcpinstall.Clients, muClient) {
		return usageErrorf("--client %q: want one of %s", muClient, strings.Join(mcpinstall.Clients, ", "))
	}
	if err := mcpinstall.ValidName(muName); err != nil {
		return &usageError{fmt.Errorf("--name: %w", err)}
	}

	env, err := mcpInstallEnv()
	if err != nil {
		return err
	}
	ch, err := mcpinstall.PlanRemove(muClient, muName, env)
	if err != nil {
		if errors.Is(err, mcpinstall.ErrInvalid) {
			return &usageError{err}
		}
		return fmt.Errorf("mcp uninstall: %w", err)
	}

	human := !machineOutput()
	res := uninstallResult{
		Client:  ch.Client,
		Name:    ch.Name,
		Path:    ch.Path,
		Changed: ch.Changed(),
		Command: ch.CommandLines(),
	}
	if !ch.Changed() {
		if !human {
			return printResult(res)
		}
		switch {
		case ch.Client == mcpinstall.ClaudeCode:
			output.PrintSuccess(fmt.Sprintf("Claude Code's user config (%s) has no %q entry; nothing to remove", ch.Path, ch.Name))
		case ch.Old == nil:
			output.PrintSuccess(fmt.Sprintf("%s does not exist; nothing to remove", ch.Path))
		default:
			output.PrintSuccess(fmt.Sprintf("%s has no %q entry; nothing to remove", ch.Path, ch.Name))
		}
		return nil
	}

	if human || (!muPrint && !muYes) {
		// As for install: --print makes the diff or the command the output;
		// otherwise it is shown before the question (on stderr).
		var w io.Writer = os.Stderr
		if muPrint {
			w = os.Stdout
		}
		printUninstallPlan(w, ch)
	}
	if muPrint {
		if !human {
			return printResult(res)
		}
		return nil
	}
	if err := ch.Check(); err != nil {
		if errors.Is(err, mcpinstall.ErrClaudeNotFound) || errors.Is(err, mcpinstall.ErrClaudeBatch) {
			return &usageError{err}
		}
		return fmt.Errorf("mcp uninstall: %w", err)
	}
	if !muYes {
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
		return fmt.Errorf("mcp uninstall: %w", err)
	}
	res.Backup, res.Applied = backup, true
	if ch.Absent {
		// claude had no such entry after all: nothing changed.
		res.Changed, res.Applied = false, false
	}
	if !human {
		return printResult(res)
	}
	switch {
	case ch.Absent:
		output.PrintSuccess(fmt.Sprintf("Claude Code's user config has no %q entry; nothing was removed", ch.Name))
		return nil
	case ch.Client == mcpinstall.ClaudeCode:
		output.PrintSuccess(fmt.Sprintf("Removed %q from Claude Code's user config", ch.Name))
	default:
		output.PrintSuccess(fmt.Sprintf("Removed the %q entry from %s", ch.Name, ch.Path))
	}
	if backup != "" {
		fmt.Fprintf(os.Stderr, "Backup: %s\n", backup)
	}
	fmt.Fprintln(os.Stderr, ch.Hint)
	return nil
}

// printUninstallPlan shows what Apply would do: the explanation on stderr,
// the diff or the command on w.
func printUninstallPlan(w io.Writer, ch *mcpinstall.Change) {
	if ch.Client == mcpinstall.ClaudeCode {
		fmt.Fprintf(os.Stderr, "Claude Code keeps user-scope MCP servers in %s and rewrites that file itself,\n"+
			"so ffc runs the claude CLI instead (no diff, no backup):\n", ch.Path)
		if !ch.Replaces {
			fmt.Fprintf(os.Stderr, "(%s could not be read, so the %q entry may not exist; then nothing changes)\n", ch.Path, ch.Name)
		}
		for _, l := range ch.CommandLines() {
			fmt.Fprintln(w, text.Sanitize(l))
		}
		return
	}
	fmt.Fprintf(os.Stderr, "Removing the %q entry from %s:\n", ch.Name, ch.Path)
	fmt.Fprint(w, text.Sanitize(ch.Diff()))
}

func init() {
	mcpUninstallCmd.Flags().StringVar(&muClient, "client", "", "AI client: "+strings.Join(mcpinstall.Clients, ", ")+" (required)")
	mcpUninstallCmd.Flags().StringVar(&muName, "name", mcpinstall.DefaultName, "Name of the server entry")
	mcpUninstallCmd.Flags().BoolVar(&muPrint, "print", false, "Print the diff (or the claude command) and change nothing")
	mcpUninstallCmd.Flags().BoolVarP(&muYes, "yes", "y", false, "Write without asking")
	mcpUninstallCmd.MarkFlagsMutuallyExclusive("print", "yes")
	mcpCmd.AddCommand(mcpUninstallCmd)
}
