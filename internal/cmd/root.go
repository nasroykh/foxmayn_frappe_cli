package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"

	"github.com/spf13/cobra"
)

// Global flags shared across all subcommands.
var (
	siteName   string
	configPath string
	jsonOutput bool
	quiet      bool
	noInput    bool
)

var rootCmd = &cobra.Command{
	Use:   "ffc",
	Short: "Foxmayn Frappe CLI — manage your Frappe ERP site from the command line",
	Long: `ffc is a minimal CLI for interacting with Frappe ERP sites via the REST API.

Config file: ~/.config/ffc/config.yaml
Env vars:    FFC_SITE, FFC_CONFIG, FFC_TIMEOUT (like --site, --config, --timeout;
             a flag wins over the variable),
             FFC_API_KEY + FFC_API_SECRET (override the site's credentials),
             FFC_URL (with the env key pair, or alone when there is no config file),
             FFC_NO_UPDATE_CHECK (disable the daily update check)

Exit codes:  0 ok, 1 error, 2 usage, 3 auth, 4 not found, 5 permission,
             6 validation or conflict, 7 network or server, 8 partial bulk
             failure, 130 interrupted. With --json, errors are JSON on stderr.

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
	code, _ := execute(ctx, os.Args[1:], os.Stderr)
	stop()
	if code != exitOK {
		os.Exit(code)
	}
}

var trackOnce sync.Once

// execute runs the root command, reports a failure on stderr and returns the
// exit code (see exit.go) with the error.
func execute(ctx context.Context, args []string, stderr io.Writer) (int, error) {
	rootCmd.SetArgs(args)
	trackOnce.Do(func() { trackRunStart(rootCmd) })
	runStarted = false
	err := rootCmd.ExecuteContext(ctx)
	// Wait for any background update-check goroutine to finish writing the
	// state file before the process exits (capped at 2 s).
	waitForUpdateCheck()
	if err == nil {
		return exitOK, nil
	}
	if ctx.Err() != nil {
		err = fmt.Errorf("interrupted: %w", context.Canceled)
	} else if !runStarted {
		// Cobra's argument and flag checks run before RunE.
		if code, _ := classify(err); code == exitGeneric {
			err = &usageError{err}
		}
		// An unknown flag or command stops parsing before --json is read.
		if !jsonOutput && argsWantJSON(args) {
			return reportError(stderr, err, true), err
		}
	}
	return reportError(stderr, err, jsonOutput), err
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&siteName, "site", "s", "", "Site name from config (default: default_site)")
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to config file (default: ~/.config/ffc/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "Output raw JSON instead of a table")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress the progress spinner (also off when stderr is not a terminal, or NO_COLOR or CI is set)")
	rootCmd.PersistentFlags().BoolVar(&noInput, "no-input", false, "Never prompt; fail instead (also on when stdin is not a terminal). Pass --yes to confirm deletions")
	rootCmd.PersistentFlags().DurationVar(&client.Timeout, "timeout", client.Timeout, "HTTP timeout per request to the site (raise it for heavy reports), e.g. 2m")

	// Version template: "ffc version v0.1.0 (abc1234, 2026-03-09)"
	cobra.OnInitialize(applyEnv)

	rootCmd.SetVersionTemplate(fmt.Sprintf("ffc version %s (%s, %s)\n", version.Version, version.Commit, version.Date))
}

// envErr is an invalid environment setting found by applyEnv.
var envErr error

// applyEnv fills the global flags from FFC_SITE, FFC_CONFIG and FFC_TIMEOUT
// when the flag was not given (flags > env > config). It runs after flag
// parsing, before any command code.
func applyEnv() {
	envErr = nil
	flags := rootCmd.PersistentFlags()
	if v := os.Getenv("FFC_SITE"); v != "" && !flags.Changed("site") {
		siteName = v
	}
	if v := os.Getenv("FFC_CONFIG"); v != "" && !flags.Changed("config") {
		configPath = v
	}
	if v := os.Getenv("FFC_TIMEOUT"); v != "" && !flags.Changed("timeout") {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			envErr = usageErrorf("FFC_TIMEOUT=%q: want a positive duration such as 30s or 2m", v)
			return
		}
		client.Timeout = d
	}
}
