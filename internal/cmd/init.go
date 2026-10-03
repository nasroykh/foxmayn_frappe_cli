package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"
)

var (
	initOAuth    bool
	initAPIKey   bool
	initPassword bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive setup wizard that creates the ffc config file",
	Long: `Create the ffc configuration file interactively (default:
~/.config/ffc/config.yaml, or the path given with --config).

Without flags, a menu lets you choose the authentication method.
Use --oauth    to go directly to the OAuth 2.0 browser flow (Authorization Code + PKCE).
Use --apikey   to go directly to the API key / secret flow.
Use --password to go directly to the username/email + password flow.

API keys can be generated at: User → API Access → Generate Keys.
OAuth clients can be created at: Integrations → OAuth Client → New.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}

		switch _, err := os.Stat(cfgPath); {
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

		method, err := chooseAuthMethod("How do you want to connect to your Frappe site?", initOAuth, initAPIKey, initPassword)
		if err != nil {
			return err
		}
		name, site, err := collectSite(cmd.Context(), method, nil)
		if err != nil {
			return err
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
	initCmd.MarkFlagsMutuallyExclusive("oauth", "apikey", "password")
	rootCmd.AddCommand(initCmd)
}
