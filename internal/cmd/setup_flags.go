package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitesetup"
	"github.com/spf13/cobra"
)

// setupFlags are the flags that let `init` and `site add` run without a
// terminal. Secrets are never flag values: they come from stdin or the
// environment.
type setupFlags struct {
	name, url, apiKey, username   string
	apiSecretStdin, passwordStdin bool
	force                         bool
	clientID                      string // --client-id (OAuth, wizard and flags alike)
}

func (s *setupFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&s.name, "name", "", "Site name (non-interactive setup)")
	f.StringVar(&s.url, "url", "", "Site URL (non-interactive setup)")
	f.StringVar(&s.apiKey, "api-key", "", "API key; the secret comes from --api-secret-stdin or $FFC_API_SECRET")
	f.BoolVar(&s.apiSecretStdin, "api-secret-stdin", false, "Read the API secret from stdin (one line)")
	f.StringVar(&s.username, "username", "", "Username/email; the password comes from --password-stdin or $FFC_PASSWORD")
	f.BoolVar(&s.passwordStdin, "password-stdin", false, "Read the password from stdin (one line)")
	f.BoolVar(&s.force, "force", false, "Replace an existing site/config without asking")
	f.StringVar(&s.clientID, "client-id", "", "OAuth: use this OAuth Client instead of registering one (secret, if any, from $FFC_OAUTH_CLIENT_SECRET)")
}

// active reports whether any non-interactive setup flag was given.
// --client-id alone is not one: it also applies to the OAuth wizard.
func (s *setupFlags) active() bool {
	return s.name != "" || s.url != "" || s.apiKey != "" || s.username != "" ||
		s.apiSecretStdin || s.passwordStdin
}

// oauthClient returns the OAuth client given with --client-id (its secret
// from $FFC_OAUTH_CLIENT_SECRET), or the zero sitesetup.OAuthApp.
// --client-id without --oauth is a usage error. With --oauth but no --client-id the secret is
// ignored (a registered or prompted client brings its own), which is said
// on stderr.
func (s *setupFlags) oauthClient(oauth bool) (sitesetup.OAuthApp, error) {
	id := strings.TrimSpace(s.clientID)
	if id == "" {
		if oauth && strings.TrimSpace(os.Getenv("FFC_OAUTH_CLIENT_SECRET")) != "" {
			fmt.Fprintln(os.Stderr, "warning: FFC_OAUTH_CLIENT_SECRET is ignored without --client-id")
		}
		return sitesetup.OAuthApp{}, nil
	}
	if !oauth {
		return sitesetup.OAuthApp{}, usageErrorf("--client-id needs --oauth")
	}
	return sitesetup.OAuthApp{ID: id, Secret: strings.TrimSpace(os.Getenv("FFC_OAUTH_CLIENT_SECRET"))}, nil
}

// resolve validates the flags and returns the normalised site. It reads the
// secret from stdin or the environment, but does not touch the network.
// oauth/apikey/passwordFlag are the wizard method flags. With oauth the site
// has only its URL: finish runs the browser login.
func (s *setupFlags) resolve(oauth, apikey, passwordFlag bool) (string, config.SiteConfig, error) {
	switch {
	case oauth && (s.apiKey != "" || s.username != "" || s.apiSecretStdin || s.passwordStdin):
		return "", config.SiteConfig{}, usageErrorf("--oauth cannot be combined with --api-key/--username: pick one credential set")
	case oauth && s.name == "":
		return "", config.SiteConfig{}, usageErrorf("missing --name")
	case oauth && s.url == "":
		return "", config.SiteConfig{}, usageErrorf("missing --url")
	case oauth:
		name, siteURL, err := sitesetup.NameAndURL(s.name, s.url)
		if err != nil {
			return "", config.SiteConfig{}, &usageError{err}
		}
		return name, config.SiteConfig{URL: siteURL}, nil
	case s.apiKey != "" && s.username != "":
		return "", config.SiteConfig{}, usageErrorf("--api-key and --username are mutually exclusive: pick one credential set")
	case s.apiSecretStdin && s.passwordStdin:
		return "", config.SiteConfig{}, usageErrorf("--api-secret-stdin and --password-stdin are mutually exclusive")
	case s.apiSecretStdin && s.apiKey == "":
		return "", config.SiteConfig{}, usageErrorf("--api-secret-stdin needs --api-key")
	case s.passwordStdin && s.username == "":
		return "", config.SiteConfig{}, usageErrorf("--password-stdin needs --username")
	case apikey && s.username != "", passwordFlag && s.apiKey != "":
		return "", config.SiteConfig{}, usageErrorf("--apikey/--password conflict with the credential flags given")
	case s.name == "":
		return "", config.SiteConfig{}, usageErrorf("missing --name")
	case s.url == "":
		return "", config.SiteConfig{}, usageErrorf("missing --url")
	case s.apiKey == "" && s.username == "":
		return "", config.SiteConfig{}, usageErrorf("missing credentials: pass --api-key (secret via --api-secret-stdin or $FFC_API_SECRET) or --username (password via --password-stdin or $FFC_PASSWORD)")
	}

	name, siteURL, err := sitesetup.NameAndURL(s.name, s.url)
	if err != nil {
		return "", config.SiteConfig{}, &usageError{err}
	}
	site := config.SiteConfig{URL: siteURL}

	if s.apiKey != "" {
		secret, err := readSecret(s.apiSecretStdin, "FFC_API_SECRET", "--api-secret-stdin")
		if err != nil {
			return "", config.SiteConfig{}, err
		}
		site.APIKey = strings.TrimSpace(s.apiKey)
		site.APISecret = strings.TrimSpace(secret)
		if site.APIKey == "" || site.APISecret == "" {
			return "", config.SiteConfig{}, usageErrorf("API key and secret cannot be empty")
		}
		return name, site, nil
	}

	password, err := readSecret(s.passwordStdin, "FFC_PASSWORD", "--password-stdin")
	if err != nil {
		return "", config.SiteConfig{}, err
	}
	site.Username = strings.TrimSpace(s.username)
	site.Password = password // stored verbatim, as in the wizard
	if site.Username == "" || site.Password == "" {
		return "", config.SiteConfig{}, usageErrorf("username and password cannot be empty")
	}
	return name, site, nil
}

// readSecret returns one line from stdin (fromStdin) or the environment
// variable env, and a usage error naming the flag when neither is available.
func readSecret(fromStdin bool, env, flagName string) (string, error) {
	if fromStdin {
		if isTerminal(os.Stdin) {
			// Typing it here would echo the secret on screen.
			return "", usageErrorf("%s reads a piped secret, but stdin is a terminal: pipe it in, or set $%s", flagName, env)
		}
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading %s from stdin: %w", strings.TrimSuffix(flagName, "-stdin"), err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			return "", usageErrorf("%s: stdin is empty", flagName)
		}
		return line, nil
	}
	if v := os.Getenv(env); v != "" {
		return v, nil
	}
	return "", usageErrorf("missing secret: pass %s or set $%s", flagName, env)
}

// finishSetup completes a site from resolve: the OAuth browser login
// (oauth), or the same credential check as the wizard.
func finishSetup(ctx context.Context, site config.SiteConfig, oauth bool, app sitesetup.OAuthApp) (config.SiteConfig, error) {
	if oauth {
		return collectOAuthSiteNoInput(ctx, site.URL, app)
	}
	return site, verifySite(ctx, site)
}

// verifySite checks the credentials of site against its URL under a
// spinner: the same check as the wizard (sitesetup.Verify).
func verifySite(ctx context.Context, site config.SiteConfig) error {
	var verifyErr error
	err := runSpinner("Verifying credentials...", func() {
		_, verifyErr = sitesetup.Verify(ctx, site)
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errAborted
	}
	if errors.Is(verifyErr, sitesetup.ErrNoCredentials) {
		return &usageError{verifyErr}
	}
	return verifyErr
}
