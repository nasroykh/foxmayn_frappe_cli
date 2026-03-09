package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Global flags shared across all subcommands
var (
	siteName   string
	configPath string
	jsonOutput bool
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
	// No Run — shows help when called with no subcommand
}

// Execute is the single entry point called from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&siteName, "site", "s", "", "Site name from config (default: default_site)")
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to config file (default: ~/.config/ffc/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "Output raw JSON instead of a table")
}
