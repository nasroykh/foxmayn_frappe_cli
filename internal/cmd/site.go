package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// ─── flags ───────────────────────────────────────────────────────────────────

var (
	saOAuth  bool
	saAPIKey bool
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
		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("no config found at %s — run 'ffc init' to create one", cfgPath)
		}
		var cfg config.Config
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parsing config: %w", err)
		}
		if len(cfg.Sites) == 0 {
			fmt.Fprintln(os.Stderr, "No sites configured. Run 'ffc init' or 'ffc site add'.")
			return nil
		}

		names := make([]string, 0, len(cfg.Sites))
		for n := range cfg.Sites {
			names = append(names, n)
		}
		sort.Strings(names)

		rows := make([]map[string]interface{}, 0, len(names))
		for _, name := range names {
			site := cfg.Sites[name]
			authMode := "—"
			if site.AccessToken != "" {
				authMode = "OAuth 2.0"
			} else if site.APIKey != "" {
				authMode = "API Key"
			}
			dflt := ""
			if name == cfg.DefaultSite {
				dflt = "✓"
			}
			rows = append(rows, map[string]interface{}{
				"name":    name,
				"url":     site.URL,
				"auth":    authMode,
				"default": dflt,
			})
		}

		if jsonOutput {
			output.PrintJSON(rows)
		} else {
			output.PrintTable(rows, []string{"name", "url", "auth", "default"})
		}
		return nil
	},
}

// ─── ffc site add ────────────────────────────────────────────────────────────

var siteAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new site to your config",
	Long: `Add a new Frappe site to ~/.config/ffc/config.yaml.

Without flags, a menu lets you choose the authentication method.
Use --oauth   to use the OAuth 2.0 browser flow (Authorization Code + PKCE).
Use --apikey  to use the API key / secret flow.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}
		if _, err := os.Stat(cfgPath); err != nil {
			return fmt.Errorf("no config found at %s — run 'ffc init' first", cfgPath)
		}

		useOAuth := saOAuth
		if !saOAuth && !saAPIKey {
			var method string
			menuErr := huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("How do you want to connect to the new site?").
						Options(
							huh.NewOption("OAuth 2.0  — browser login, no credentials stored", "oauth"),
							huh.NewOption("API Key    — paste your API key and secret", "apikey"),
						).
						Value(&method),
				),
			).WithKeyMap(escQuitKeyMap()).Run()
			if errors.Is(menuErr, huh.ErrUserAborted) {
				fmt.Fprintln(os.Stderr, "Aborted.")
				return nil
			}
			if menuErr != nil {
				return menuErr
			}
			useOAuth = method == "oauth"
		}

		if useOAuth {
			return siteAddOAuthFlow(cfgPath)
		}
		return siteAddAPIKeyFlow(cfgPath)
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

		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("reading config: %w", err)
		}
		var cfg config.Config
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parsing config: %w", err)
		}
		if len(cfg.Sites) == 0 {
			return fmt.Errorf("no sites configured — run 'ffc init' or 'ffc site add'")
		}

		var name string
		if len(args) == 1 {
			name = args[0]
			if _, ok := cfg.Sites[name]; !ok {
				return fmt.Errorf("site %q not found in config", name)
			}
		} else {
			name, err = pickSite(cfg, "Which site do you want to remove?")
			if err != nil {
				return err
			}
			if name == "" {
				return nil
			}
		}

		var confirmed bool
		confirmErr := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Remove site %q?", name)).
					Description(fmt.Sprintf("URL: %s", cfg.Sites[name].URL)).
					Value(&confirmed),
			),
		).WithKeyMap(escQuitKeyMap()).Run()
		if errors.Is(confirmErr, huh.ErrUserAborted) || !confirmed {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		if confirmErr != nil {
			return confirmErr
		}

		if err := removeSiteFromConfig(cfgPath, name); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "✓ Site %q removed.\n", name)
		if cfg.DefaultSite == name {
			fmt.Fprintln(os.Stderr, "  It was your default site — run 'ffc site use <name>' to set a new one.")
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

		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("reading config: %w", err)
		}
		var cfg config.Config
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parsing config: %w", err)
		}
		if len(cfg.Sites) == 0 {
			return fmt.Errorf("no sites configured — run 'ffc init' or 'ffc site add'")
		}

		var name string
		if len(args) == 1 {
			name = args[0]
			if _, ok := cfg.Sites[name]; !ok {
				return fmt.Errorf("site %q not found in config", name)
			}
		} else {
			name, err = pickSite(cfg, "Which site do you want to use as default?")
			if err != nil {
				return err
			}
			if name == "" {
				return nil
			}
		}

		if err := setDefaultSite(cfgPath, name); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "✓ Default site set to %q.\n", name)
		return nil
	},
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// pickSite shows a huh selection menu of all configured sites and returns the
// chosen name. Returns ("", nil) if the user aborts.
func pickSite(cfg config.Config, title string) (string, error) {
	names := make([]string, 0, len(cfg.Sites))
	for n := range cfg.Sites {
		names = append(names, n)
	}
	sort.Strings(names)

	opts := make([]huh.Option[string], 0, len(names))
	for _, n := range names {
		site := cfg.Sites[n]
		label := n + "  (" + site.URL + ")"
		if n == cfg.DefaultSite {
			label += "  ✓ default"
		}
		opts = append(opts, huh.NewOption(label, n))
	}

	var chosen string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(title).
				Options(opts...).
				Value(&chosen),
		),
	).WithKeyMap(escQuitKeyMap()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Fprintln(os.Stderr, "Aborted.")
		return "", nil
	}
	return chosen, err
}

// ─── init ────────────────────────────────────────────────────────────────────

func init() {
	siteAddCmd.Flags().BoolVar(&saOAuth, "oauth", false, "Use OAuth 2.0 browser flow")
	siteAddCmd.Flags().BoolVar(&saAPIKey, "apikey", false, "Use API key / secret flow")
	siteAddCmd.MarkFlagsMutuallyExclusive("oauth", "apikey")

	siteCmd.AddCommand(siteListCmd, siteAddCmd, siteRemoveCmd, siteUseCmd)
	rootCmd.AddCommand(siteCmd)
}

// ─── site add: API key flow ───────────────────────────────────────────────────

func siteAddAPIKeyFlow(cfgPath string) error {
	// Load existing config for site-name conflict detection.
	raw, _ := os.ReadFile(cfgPath)
	var existingCfg config.Config
	_ = yaml.Unmarshal(raw, &existingCfg)

	var siteName, siteURL, apiKey, apiSecret string

addLoop:
	for {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Site name").
					Description("A short identifier, e.g. staging or production").
					Placeholder("staging").
					Validate(func(s string) error {
						s = strings.TrimSpace(s)
						if s == "" {
							return fmt.Errorf("site name cannot be empty")
						}
						if strings.ContainsAny(s, " \t") {
							return fmt.Errorf("site name must not contain spaces")
						}
						return nil
					}).
					Value(&siteName),
				huh.NewInput().
					Title("Site URL").
					Description("Base URL of your Frappe site (https:// added if omitted)").
					Placeholder("mysite.example.com").
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("URL cannot be empty")
						}
						return nil
					}).
					Value(&siteURL),
			),
			huh.NewGroup(
				huh.NewInput().
					Title("API Key").
					Description("From User → API Access → Generate Keys").
					Placeholder("21393a7e100ae26").
					Value(&apiKey),
				huh.NewInput().
					Title("API Secret").
					EchoMode(huh.EchoModePassword).
					Value(&apiSecret),
			),
		)
		if err := form.WithKeyMap(escQuitKeyMap()).Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				fmt.Fprintln(os.Stderr, "Aborted.")
				return nil
			}
			return err
		}

		siteName = strings.TrimSpace(siteName)
		siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
		if !strings.HasPrefix(siteURL, "http://") && !strings.HasPrefix(siteURL, "https://") {
			siteURL = "https://" + siteURL
		}

		// Site already exists → confirm overwrite.
		if _, exists := existingCfg.Sites[siteName]; exists {
			var overwrite bool
			overErr := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Site %q already exists.", siteName)).
						Description("Update it with the new credentials?").
						Value(&overwrite),
				),
			).WithKeyMap(escQuitKeyMap()).Run()
			if errors.Is(overErr, huh.ErrUserAborted) || !overwrite {
				fmt.Fprintln(os.Stderr, "Aborted.")
				return nil
			}
			if overErr != nil {
				return overErr
			}
		}

		secretSummary := "(empty)"
		if strings.TrimSpace(apiSecret) != "" {
			secretSummary = fmt.Sprintf("(%d characters)", len(apiSecret))
		}
		var reviewChoice string
		reviewErr := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Review configuration").
					Description(fmt.Sprintf(
						"Site name:  %s\nSite URL:   %s\nAPI key:    %s\nAPI secret: %s",
						siteName, siteURL, apiKey, secretSummary,
					)).
					Options(
						huh.NewOption("Confirm", "confirm"),
						huh.NewOption("Edit", "edit"),
						huh.NewOption("Cancel", "cancel"),
					).
					Value(&reviewChoice),
			),
		).WithKeyMap(escQuitKeyMap()).Run()
		if errors.Is(reviewErr, huh.ErrUserAborted) || reviewChoice == "cancel" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		if reviewErr != nil {
			return reviewErr
		}
		if reviewChoice == "edit" {
			continue
		}
		break addLoop
	}

	var writeErr error
	_ = spinner.New().
		Title("Saving site...").
		Action(func() {
			writeErr = upsertSiteInConfig(cfgPath, siteName, buildAPIKeySiteYAML(siteURL, apiKey, apiSecret))
		}).
		Run()
	if writeErr != nil {
		return fmt.Errorf("saving site: %w", writeErr)
	}

	fmt.Fprintf(os.Stderr, "\n✓ Site %q added to config.\n", siteName)
	fmt.Fprintf(os.Stderr, "  Run: ffc --site %s list-docs --doctype \"Sales Invoice\"\n", siteName)
	return nil
}

// ─── site add: OAuth flow ─────────────────────────────────────────────────────

func siteAddOAuthFlow(cfgPath string) error {
	// Load existing config for site-name conflict detection.
	raw, _ := os.ReadFile(cfgPath)
	var existingCfg config.Config
	_ = yaml.Unmarshal(raw, &existingCfg)

	// ── Step 1: site name + URL ───────────────────────────────────────────────
	var siteName, siteURL string
	siteForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Site name").
				Description("A short identifier, e.g. staging or production").
				Placeholder("staging").
				Validate(func(s string) error {
					s = strings.TrimSpace(s)
					if s == "" {
						return fmt.Errorf("site name cannot be empty")
					}
					if strings.ContainsAny(s, " \t") {
						return fmt.Errorf("site name must not contain spaces")
					}
					return nil
				}).
				Value(&siteName),
			huh.NewInput().
				Title("Site URL").
				Description("Base URL of your Frappe site (https:// added if omitted)").
				Placeholder("mysite.example.com").
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("URL cannot be empty")
					}
					return nil
				}).
				Value(&siteURL),
		),
	)
	if err := siteForm.WithKeyMap(escQuitKeyMap()).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		return err
	}

	siteName = strings.TrimSpace(siteName)
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if !strings.HasPrefix(siteURL, "http://") && !strings.HasPrefix(siteURL, "https://") {
		siteURL = "https://" + siteURL
	}

	// Site already exists → confirm before running the browser flow.
	if _, exists := existingCfg.Sites[siteName]; exists {
		var overwrite bool
		overErr := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Site %q already exists.", siteName)).
					Description("Update it with a new OAuth token?").
					Value(&overwrite),
			),
		).WithKeyMap(escQuitKeyMap()).Run()
		if errors.Is(overErr, huh.ErrUserAborted) || !overwrite {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		if overErr != nil {
			return overErr
		}
	}

	// ── Step 2: start callback server ─────────────────────────────────────────
	cs, err := startCallbackServer()
	if err != nil {
		return err
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", cs.port)

	fmt.Fprintf(os.Stderr, `
OAuth Client setup (one-time, on your Frappe site)
──────────────────────────────────────────────────
1. Go to: %s/app/oauth-client/new-oauth-client-1
2. Fill in:
     App Name:      ffc (or any name)
     Grant Type:    Authorization Code
     Scopes:        openid all
     Redirect URIs: %s
3. Save → copy the Client ID (and Client Secret if using Confidential type).

`, siteURL, redirectURI)

	// ── Step 3: client ID + secret ────────────────────────────────────────────
	var clientID, clientSecret string
	credForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("OAuth Client ID").
				Description("From the OAuth Client you just created on Frappe").
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("Client ID cannot be empty")
					}
					return nil
				}).
				Value(&clientID),
			huh.NewInput().
				Title("OAuth Client Secret").
				Description("Leave empty if using a Public client (no secret)").
				EchoMode(huh.EchoModePassword).
				Value(&clientSecret),
		),
	)
	if err := credForm.WithKeyMap(escQuitKeyMap()).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
		return err
	}
	clientID = strings.TrimSpace(clientID)

	// ── Step 4: PKCE + browser ────────────────────────────────────────────────
	verifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("generating PKCE verifier: %w", err)
	}
	challenge := generateCodeChallenge(verifier)

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"openid all"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authURL := strings.TrimRight(siteURL, "/") + "/api/method/frappe.integrations.oauth2.authorize?" + params.Encode()

	fmt.Fprintf(os.Stderr, "\nOpening browser for authorization...\n")
	fmt.Fprintf(os.Stderr, "If the browser doesn't open automatically, visit:\n  %s\n\n", authURL)

	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "(Could not open browser: %v)\n\n", err)
	}

	fmt.Fprintf(os.Stderr, "Waiting for authorization (timeout: 5 minutes)...\n")

	// ── Step 5: wait for callback ─────────────────────────────────────────────
	code, err := cs.wait()
	if err != nil {
		return fmt.Errorf("authorization: %w", err)
	}

	// ── Step 6: exchange code for tokens ──────────────────────────────────────
	var tokens *client.OAuthTokens
	var exchangeErr error
	_ = spinner.New().
		Title("Exchanging authorization code for tokens...").
		Action(func() {
			tokens, exchangeErr = client.ExchangeOAuthCode(siteURL, clientID, clientSecret, code, redirectURI, verifier)
		}).
		Run()
	if exchangeErr != nil {
		return fmt.Errorf("token exchange: %w", exchangeErr)
	}

	// ── Step 7: fetch logged-in user ──────────────────────────────────────────
	var loggedUser string
	_ = spinner.New().
		Title("Fetching user info...").
		Action(func() {
			loggedUser, _ = client.GetOAuthUser(siteURL, tokens.AccessToken)
		}).
		Run()

	userDisplay := loggedUser
	if userDisplay == "" {
		userDisplay = "(unknown)"
	}

	// ── Step 8: review + confirm ──────────────────────────────────────────────
	var reviewChoice string
	reviewErr := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Review OAuth configuration").
				Description(fmt.Sprintf(
					"Site name:  %s\nSite URL:   %s\nClient ID:  %s\nLogged in:  %s",
					siteName, siteURL, clientID, userDisplay,
				)).
				Options(
					huh.NewOption("Confirm", "confirm"),
					huh.NewOption("Cancel", "cancel"),
				).
				Value(&reviewChoice),
		),
	).WithKeyMap(escQuitKeyMap()).Run()
	if errors.Is(reviewErr, huh.ErrUserAborted) || reviewChoice == "cancel" {
		fmt.Fprintln(os.Stderr, "Aborted.")
		return nil
	}
	if reviewErr != nil {
		return reviewErr
	}

	// ── Step 9: write ──────────────────────────────────────────────────────────
	var writeErr error
	_ = spinner.New().
		Title("Saving site...").
		Action(func() {
			writeErr = upsertSiteInConfig(cfgPath, siteName, buildOAuthSiteYAML(siteURL, clientID, clientSecret, tokens))
		}).
		Run()
	if writeErr != nil {
		return fmt.Errorf("saving site: %w", writeErr)
	}

	fmt.Fprintf(os.Stderr, "\n✓ Site %q added to config.\n", siteName)
	fmt.Fprintf(os.Stderr, "  Logged in as: %s\n", userDisplay)
	fmt.Fprintf(os.Stderr, "  Run: ffc --site %s list-docs --doctype \"Sales Invoice\"\n", siteName)
	return nil
}
