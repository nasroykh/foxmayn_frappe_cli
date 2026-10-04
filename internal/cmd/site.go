package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/charmbracelet/huh"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/spf13/cobra"
)

// ─── flags ───────────────────────────────────────────────────────────────────

var (
	saOAuth    bool
	saAPIKey   bool
	saPassword bool
)

// ─── ffc site ────────────────────────────────────────────────────────────────

var siteCmd = &cobra.Command{
	Use:   "site",
	Short: "Manage Frappe sites in your config",
	Long: `Add, list, remove, or switch between Frappe sites in your config.

Examples:
  ffc site list
  ffc site add
  ffc site add --oauth
  ffc site remove staging
  ffc site use production
`,
}

// ─── ffc site list ───────────────────────────────────────────────────────────

var siteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured sites",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}
		cfg, err := readConfig(cfgPath)
		if err != nil {
			return err
		}
		if len(cfg.Sites) == 0 {
			fmt.Fprintln(os.Stderr, "No sites configured. Run 'ffc init' or 'ffc site add'.")
			return nil
		}

		names := sortedSiteNames(cfg)
		rows := make([]map[string]interface{}, 0, len(names))
		for _, name := range names {
			site := cfg.Sites[name]
			rows = append(rows, map[string]interface{}{
				"name":    name,
				"url":     site.URL,
				"auth":    authLabel(site),
				"default": name == cfg.DefaultSite,
			})
		}

		if jsonOutput {
			return output.PrintJSON(rows)
		}
		for _, row := range rows {
			if row["default"] == true {
				row["default"] = "✓"
			} else {
				row["default"] = ""
			}
		}
		output.PrintTable(rows, []string{"name", "url", "auth", "default"})
		return nil
	},
}

// authLabel names the credentials client.New will actually use, in the same
// priority order.
func authLabel(site config.SiteConfig) string {
	switch {
	case site.IsOAuth():
		return "OAuth 2.0"
	case site.APIKey != "" && site.APISecret != "":
		return "API Key"
	case site.IsSessionAuth():
		return "Username/Password"
	}
	return "none"
}

// ─── ffc site add ────────────────────────────────────────────────────────────

var siteAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new site to your config",
	Long: `Add a new Frappe site to your config file (default: ~/.config/ffc/config.yaml).

Without flags, a menu lets you choose the authentication method.
Use --oauth    to use the OAuth 2.0 browser flow (Authorization Code + PKCE).
Use --apikey   to use the API key / secret flow.
Use --password to use username/email + password (session cookie) login.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}
		// Read up front so a malformed config fails before any browser flow.
		cfg, err := readConfig(cfgPath)
		if err != nil {
			return err
		}

		method, err := chooseAuthMethod("How do you want to connect to the new site?", saOAuth, saAPIKey, saPassword)
		if err != nil {
			return err
		}
		confirmOverwrite := func(name string) error {
			if _, exists := cfg.Sites[name]; !exists {
				return nil
			}
			ok, err := confirmPrompt(fmt.Sprintf("Site %q already exists.", name), "Replace it with the new credentials?")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: site %q kept", errAborted, name)
			}
			return nil
		}
		name, site, err := collectSite(cmd.Context(), method, confirmOverwrite)
		if err != nil {
			return err
		}
		if err := addSiteToConfig(cfgPath, name, site); err != nil {
			return fmt.Errorf("saving site: %w", err)
		}

		fmt.Fprintf(os.Stderr, "\n✓ Site %q added to %s\n", name, cfgPath)
		printSiteSaved(name, site)
		return nil
	},
}

// ─── ffc site remove ─────────────────────────────────────────────────────────

var siteRemoveCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove a site from your config",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}
		cfg, err := readConfig(cfgPath)
		if err != nil {
			return err
		}
		name, err := siteArg(cfg, args, "Which site do you want to remove?")
		if err != nil {
			return err
		}

		ok, err := confirmPrompt(fmt.Sprintf("Remove site %q?", name), fmt.Sprintf("URL: %s", cfg.Sites[name].URL))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: site %q kept", errAborted, name)
		}

		var wasDefault bool
		var newDefault string
		if err := config.Edit(cfgPath, func(f *config.File) error {
			wasDefault = f.Get("default_site") == name
			if err := f.RemoveSite(name); err != nil {
				return err
			}
			newDefault = f.Get("default_site")
			return nil
		}); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "✓ Site %q removed.\n", name)
		if wasDefault {
			if newDefault != "" {
				fmt.Fprintf(os.Stderr, "  It was your default site — default is now %q.\n", newDefault)
			} else {
				fmt.Fprintln(os.Stderr, "  It was your default site; no sites remain.")
			}
		}
		return nil
	},
}

// ─── ffc site use ────────────────────────────────────────────────────────────

var siteUseCmd = &cobra.Command{
	Use:   "use [name]",
	Short: "Set the default site",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}
		cfg, err := readConfig(cfgPath)
		if err != nil {
			return err
		}
		name, err := siteArg(cfg, args, "Which site do you want to use as default?")
		if err != nil {
			return err
		}
		if err := setConfigValues(cfgPath, []configValue{{"default_site", name}}); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "✓ Default site set to %q.\n", name)
		return nil
	},
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// siteArg returns the site named in args (which must exist) or, without an
// argument, lets the user pick one.
func siteArg(cfg *config.Config, args []string, title string) (string, error) {
	if len(cfg.Sites) == 0 {
		return "", fmt.Errorf("no sites configured — run 'ffc init' or 'ffc site add'")
	}
	if len(args) == 1 {
		if _, ok := cfg.Sites[args[0]]; !ok {
			return "", fmt.Errorf("site %q not found in config", args[0])
		}
		return args[0], nil
	}
	return pickSite(cfg, title)
}

// pickSite shows a selection menu of all configured sites and returns the
// chosen name. An abort returns errAborted.
func pickSite(cfg *config.Config, title string) (string, error) {
	names := sortedSiteNames(cfg)
	opts := make([]huh.Option[string], 0, len(names))
	for _, n := range names {
		label := n + "  (" + cfg.Sites[n].URL + ")"
		if n == cfg.DefaultSite {
			label += "  ✓ default"
		}
		opts = append(opts, huh.NewOption(label, n))
	}

	var chosen string
	if err := runForm(huh.NewGroup(
		huh.NewSelect[string]().Title(title).Options(opts...).Value(&chosen),
	)); err != nil {
		return "", err
	}
	return chosen, nil
}

func sortedSiteNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Sites))
	for n := range cfg.Sites {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ─── init ────────────────────────────────────────────────────────────────────

func init() {
	siteAddCmd.Flags().BoolVar(&saOAuth, "oauth", false, "Use OAuth 2.0 browser flow")
	siteAddCmd.Flags().BoolVar(&saAPIKey, "apikey", false, "Use API key / secret flow")
	siteAddCmd.Flags().BoolVar(&saPassword, "password", false, "Use username/email + password (session cookie) login")
	siteAddCmd.MarkFlagsMutuallyExclusive("oauth", "apikey", "password")

	siteCmd.AddCommand(siteListCmd, siteAddCmd, siteRemoveCmd, siteUseCmd)
	rootCmd.AddCommand(siteCmd)
}
