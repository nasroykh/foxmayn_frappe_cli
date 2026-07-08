package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"

	"github.com/spf13/cobra"
)

// Global flags shared across all subcommands.
var (
	siteName   string
	configPath string
	jsonOutput bool
	quiet      bool
)

var rootCmd = &cobra.Command{
	Use:   "ffc",
	Short: "Foxmayn Frappe CLI — manage your Frappe ERP site from the command line",
	Long: `ffc is a minimal CLI for interacting with Frappe ERP sites via the REST API.

Config file: ~/.config/ffc/config.yaml
Env vars:    FFC_URL, FFC_API_KEY, FFC_API_SECRET

Example config:

  default_site: dev
  sites:
    dev:
      url: "http://mysite.localhost:8000"
      api_key: "your_api_key"
      api_secret: "your_api_secret"
`,
	Version: version.Version,
	// No Run — shows help when called with no subcommand.
	// Silence cobra's own error+usage printing: Execute below prints the error
	// once to stderr. Without this, cobra prints "Error: <msg>" plus the full
	// usage block on every runtime error, then Execute prints it again (L32).
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute is the single entry point called from main.
func Execute() {
	// A context cancelled on Ctrl+C / SIGTERM, wired into every command via
	// cmd.Context(), so long-running or bulk operations can be interrupted
	// cleanly (M18, L14, L35).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := rootCmd.ExecuteContext(ctx)
	// Wait for any background update-check goroutine to finish writing the
	// state file before the process exits (capped at 2 s).
	waitForUpdateCheck()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&siteName, "site", "s", "", "Site name from config (default: default_site)")
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to config file (default: ~/.config/ffc/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "Output raw JSON instead of a table")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress the progress spinner (also on when NO_COLOR or CI is set)")

	// Version template: "ffc version v0.1.0 (abc1234, 2026-03-09)"
	rootCmd.SetVersionTemplate(fmt.Sprintf("ffc version %s (%s, %s)\n", version.Version, version.Commit, version.Date))
}
