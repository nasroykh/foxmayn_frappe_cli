package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	initOAuth    bool
	initAPIKey   bool
	initPassword bool
	initSetup    setupFlags
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create the ffc config file (interactive wizard or non-interactive flags)",
	Long: `Create the ffc configuration file interactively (default:
~/.config/ffc/config.yaml, or the path given with --config).

Without flags, a menu lets you choose the authentication method.
Use --oauth    to go directly to the OAuth 2.0 browser flow (Authorization Code + PKCE).
Use --apikey   to go directly to the API key / secret flow.
Use --password to go directly to the username/email + password flow.

API keys can be generated at: User → API Access → Generate Keys.
OAuth clients can be created at: Integrations → OAuth Client → New.

Non-interactive (no terminal needed): pass --name, --url and one credential set.
The secret is never a flag value: pipe it with --api-secret-stdin /
--password-stdin, or set FFC_API_SECRET / FFC_PASSWORD. The credentials are
checked against the site before the config is written. An existing config is
replaced only with --force. OAuth is always interactive.

Examples:
  ffc init
  ffc init --oauth
  echo "$SECRET" | ffc init --name prod --url https://erp.example.com --api-key KEY --api-secret-stdin
  FFC_API_SECRET="$SECRET" ffc init --name prod --url erp.example.com --api-key KEY --force
  echo "$PW" | ffc init --name dev --url http://localhost:8000 --username admin --password-stdin
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}

		setup := initSetup.active()
		var name string
		var site config.SiteConfig
		if setup {
			if name, site, err = initSetup.resolve(initOAuth, initAPIKey, initPassword); err != nil {
				return err
			}
		}

		switch _, err := os.Stat(cfgPath); {
		case err == nil && initSetup.force:
		case err == nil && (setup || inputDisabled()):
			return usageErrorf("config already exists at %s: pass --force to replace it (or use 'ffc site add')", cfgPath)
		case err == nil:
			overwrite, err := confirmPrompt(
				fmt.Sprintf("Config already exists at %s", cfgPath),
				"This will replace your entire config.\nTo add a site instead, use: ffc site add",
			)
			if err != nil {
				return err
			}
			if !overwrite {
				return fmt.Errorf("%w: existing config kept", errAborted)
			}
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("checking config: %w", err)
		}

		if setup {
			if err := verifySite(cmd.Context(), site); err != nil {
				return err
			}
		} else {
			method, err := chooseAuthMethod("How do you want to connect to your Frappe site?", initOAuth, initAPIKey, initPassword)
			if err != nil {
				return err
			}
			if name, site, err = collectSite(cmd.Context(), method, nil); err != nil {
				return err
			}
		}
		if err := writeInitConfig(cfgPath, name, site); err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "\n✓ Config written to %s\n", cfgPath)
		printSiteSaved(name, site)
		return nil
	},
}

func init() {
	initCmd.Flags().BoolVar(&initOAuth, "oauth", false, "Use OAuth 2.0 browser flow (Authorization Code + PKCE)")
	initCmd.Flags().BoolVar(&initAPIKey, "apikey", false, "Use API key / secret flow")
	initCmd.Flags().BoolVar(&initPassword, "password", false, "Use username/email + password (session cookie) login")
	initSetup.register(initCmd)
	initCmd.MarkFlagsMutuallyExclusive("oauth", "apikey", "password")
	rootCmd.AddCommand(initCmd)
}
