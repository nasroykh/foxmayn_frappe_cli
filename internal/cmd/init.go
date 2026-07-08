package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/spf13/cobra"
)

var (
	initOAuth    bool
	initAPIKey   bool
	initPassword bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive setup wizard for ~/.config/ffc/config.yaml",
	Long: `Create or update the ffc configuration file interactively.

Without flags, a menu lets you choose the authentication method.
Use --oauth    to go directly to the OAuth 2.0 browser flow (Authorization Code + PKCE).
Use --apikey   to go directly to the API key / secret flow.
Use --password to go directly to the username/email + password flow.

API keys can be generated at: User → API Access → Generate Keys.
OAuth clients can be created at: Integrations → OAuth Client → New.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgDir, err := defaultConfigDir()
		if err != nil {
			return fmt.Errorf("config dir: %w", err)
		}
		cfgPath := filepath.Join(cfgDir, "config.yaml")

		// Warn if a config already exists.
		var overwrite bool
		if _, err := os.Stat(cfgPath); err == nil {
			err := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Config already exists at %s", cfgPath)).
						Description("This will replace your entire config.\nTo add a site instead, use: ffc site add").
						Value(&overwrite),
				),
			).WithKeyMap(escQuitKeyMap()).Run()
			if errors.Is(err, huh.ErrUserAborted) || !overwrite {
				fmt.Fprintln(os.Stderr, "Aborted. Existing config kept.")
				return nil
			}
			if err != nil {
				return err
			}
		}

		// Resolve auth method: flag → interactive menu.
		method := ""
		switch {
		case initOAuth:
			method = "oauth"
		case initAPIKey:
			method = "apikey"
		case initPassword:
			method = "password"
		default:
			menuErr := huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("How do you want to connect to your Frappe site?").
						Options(
							huh.NewOption("OAuth 2.0          — browser login, no credentials stored", "oauth"),
							huh.NewOption("API Key            — paste your API key and secret", "apikey"),
							huh.NewOption("Username & Password — email/username + password login", "password"),
						).
						Value(&method),
				),
			).WithKeyMap(escQuitKeyMap()).Run()
			if errors.Is(menuErr, huh.ErrUserAborted) {
				fmt.Fprintln(os.Stderr, "Aborted. No config written.")
				return nil
			}
			if menuErr != nil {
				return menuErr
			}
		}

		if method == "oauth" {
			return runOAuthInitFlow(cmd.Context(), cfgPath)
		}
		if method == "password" {
			return runPasswordInitFlow(cmd.Context(), cfgPath)
		}

		// ── API key flow ─────────────────────────────────────────────────────
		var (
			siteName  string
			siteURL   string
			apiKey    string
			apiSecret string
		)

	initLoop:
		for {
			// Rebuild the form each iteration so huh's internal state is fresh
			// (a completed form cannot be re-run correctly).
			form := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Site name").
						Description("A short identifier, e.g. dev or production").
						Placeholder("dev").
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
						Description("Base URL of your Frappe site (https:// added if you omit the scheme)").
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
						Validate(func(s string) error {
							if strings.TrimSpace(s) == "" {
								return fmt.Errorf("API key cannot be empty")
							}
							return nil
						}).
						Value(&apiKey),

					huh.NewInput().
						Title("API Secret").
						EchoMode(huh.EchoModePassword).
						Validate(func(s string) error {
							if strings.TrimSpace(s) == "" {
								return fmt.Errorf("API secret cannot be empty")
							}
							return nil
						}).
						Value(&apiSecret),
				),
			)

			err := form.WithKeyMap(escQuitKeyMap()).Run()
			if errors.Is(err, huh.ErrUserAborted) {
				fmt.Fprintln(os.Stderr, "Aborted. No config written.")
				return nil
			}
			if err != nil {
				return err
			}

			siteName = strings.TrimSpace(siteName)
			siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
			if !strings.HasPrefix(siteURL, "http://") && !strings.HasPrefix(siteURL, "https://") {
				siteURL = "https://" + siteURL
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
				fmt.Fprintln(os.Stderr, "Aborted. No config written.")
				return nil
			}
			if reviewErr != nil {
				return reviewErr
			}
			if reviewChoice == "edit" {
				continue
			}
			break initLoop
		}

		// Write config file with a spinner.
		var writeErr error
		_ = runSpinner("Writing config...", func() {
			writeErr = writeConfig(cfgPath, siteName, siteURL, apiKey, apiSecret)
		})

		if writeErr != nil {
			return fmt.Errorf("write config: %w", writeErr)
		}

		fmt.Fprintf(os.Stderr, "\n✓ Config written to %s\n", cfgPath)
		fmt.Fprintf(os.Stderr, "  Run: ffc --site %s list-docs --doctype \"Sales Invoice\"\n", siteName)
		return nil
	},
}

func init() {
	initCmd.Flags().BoolVar(&initOAuth, "oauth", false, "Use OAuth 2.0 browser flow (Authorization Code + PKCE)")
	initCmd.Flags().BoolVar(&initAPIKey, "apikey", false, "Use API key / secret flow")
	initCmd.Flags().BoolVar(&initPassword, "password", false, "Use username/email + password (session cookie) login")
	initCmd.MarkFlagsMutuallyExclusive("oauth", "apikey", "password")
	rootCmd.AddCommand(initCmd)
}

// runPasswordInitFlow drives the interactive username/email + password setup
// flow for `ffc init --password` (and the menu equivalent).
func runPasswordInitFlow(ctx context.Context, cfgPath string) error {
	var (
		siteName string
		siteURL  string
		username string
		password string
	)

passwordInitLoop:
	for {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Site name").
					Description("A short identifier, e.g. dev or production").
					Placeholder("dev").
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
					Description("Base URL of your Frappe site (https:// added if you omit the scheme)").
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
					Title("Email or username").
					Placeholder("user@example.com").
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return fmt.Errorf("email/username cannot be empty")
						}
						return nil
					}).
					Value(&username),

				huh.NewInput().
					Title("Password").
					EchoMode(huh.EchoModePassword).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("password cannot be empty")
						}
						return nil
					}).
					Value(&password),
			),
		)

		err := form.WithKeyMap(escQuitKeyMap()).Run()
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "Aborted. No config written.")
			return nil
		}
		if err != nil {
			return err
		}

		siteName = strings.TrimSpace(siteName)
		siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
		if !strings.HasPrefix(siteURL, "http://") && !strings.HasPrefix(siteURL, "https://") {
			siteURL = "https://" + siteURL
		}
		username = strings.TrimSpace(username)

		// Probe the credentials against Frappe before persisting anything.
		var loginErr error
		// If the spinner is interrupted (Ctrl+C), abort rather than proceeding to
		// write the config with unverified credentials (L35).
		if runErr := runSpinner("Verifying credentials...", func() {
			_, loginErr = client.LoginPassword(ctx, siteURL, username, password)
		}); runErr != nil {
			fmt.Fprintln(os.Stderr, "Aborted. No config written.")
			return nil
		}
		if loginErr != nil {
			fmt.Fprintf(os.Stderr, "\n✗ %v\n\n", loginErr)
			continue
		}

		var reviewChoice string
		reviewErr := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Review configuration").
					Description(fmt.Sprintf(
						"Site name: %s\nSite URL:  %s\nUsername:  %s\nPassword:  (%d characters)",
						siteName, siteURL, username, len(password),
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
			fmt.Fprintln(os.Stderr, "Aborted. No config written.")
			return nil
		}
		if reviewErr != nil {
			return reviewErr
		}
		if reviewChoice == "edit" {
			continue
		}
		break passwordInitLoop
	}

	var writeErr error
	_ = runSpinner("Writing config...", func() {
		writeErr = writeConfigSession(cfgPath, siteName, siteURL, username, password)
	})
	if writeErr != nil {
		return fmt.Errorf("write config: %w", writeErr)
	}

	fmt.Fprintf(os.Stderr, "\n✓ Config written to %s\n", cfgPath)
	fmt.Fprintln(os.Stderr, "  Note: your account password is stored in cleartext in this file (mode 0600).")
	fmt.Fprintln(os.Stderr, "  Prefer OAuth (ffc init --oauth) or a scoped API key where possible.")
	fmt.Fprintf(os.Stderr, "  Run: ffc --site %s list-docs --doctype \"Sales Invoice\"\n", siteName)
	return nil
}

// writeConfigSession creates the config directory and writes a fresh config
// file for a username/password (session-cookie) authenticated site.
func writeConfigSession(path, siteName, siteURL, username, password string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// %q quotes and escapes every value so a name/URL/username/password
	// containing " : # or \ cannot corrupt the YAML (M10). Passwords in
	// particular routinely contain such characters.
	content := fmt.Sprintf(`# ffc configuration — generated by 'ffc init --password'
# Edit this file to add more sites.

default_site: %q

sites:
  %q:
    url: %q
    username: %q
    password: %q
`,
		siteName,
		siteName,
		siteURL,
		username,
		password,
	)

	return atomicWriteFile(path, []byte(content), 0o600) // 0600 owner-only; atomic (M9)
}

// writeConfig creates the config directory and writes a YAML config file.
func writeConfig(path, siteName, siteURL, apiKey, apiSecret string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// %q quotes and escapes every value so special characters cannot corrupt
	// the YAML (M10).
	content := fmt.Sprintf(`# ffc configuration — generated by 'ffc init'
# Edit this file to add more sites.

default_site: %q

sites:
  %q:
    url: %q
    api_key: %q
    api_secret: %q
`,
		siteName,
		siteName,
		siteURL,
		apiKey,
		apiSecret,
	)

	return atomicWriteFile(path, []byte(content), 0o600) // 0600 owner-only; atomic (M9)
}

// defaultConfigDir returns ~/.config/ffc — kept in sync with config package.
func defaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ffc"), nil
}
