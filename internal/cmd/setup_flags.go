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
	"github.com/spf13/cobra"
)

// setupFlags are the flags that let `init` and `site add` run without a
// terminal. Secrets are never flag values: they come from stdin or the
// environment.
type setupFlags struct {
	name, url, apiKey, username   string
	apiSecretStdin, passwordStdin bool
	force                         bool
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
}

// active reports whether any non-interactive setup flag was given.
func (s *setupFlags) active() bool {
	return s.name != "" || s.url != "" || s.apiKey != "" || s.username != "" ||
		s.apiSecretStdin || s.passwordStdin
}

// resolve validates the flags and returns the normalised site. It reads the
// secret from stdin or the environment, but does not touch the network.
// oauth/apikey/passwordFlag are the wizard method flags.
func (s *setupFlags) resolve(oauth, apikey, passwordFlag bool) (string, config.SiteConfig, error) {
	switch {
	case oauth:
		return "", config.SiteConfig{}, usageErrorf("--oauth is interactive and cannot be combined with --name/--url/--api-key/--username")
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

	name, siteURL, err := siteNameAndURL(s.name, s.url)
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

// verifySite checks the credentials of site against its URL: the same calls
// the wizard makes (an authenticated call for key/token, a login plus logout
// for a password).
func verifySite(ctx context.Context, site config.SiteConfig) error {
	var verifyErr error
	err := runSpinner("Verifying credentials...", func() {
		if site.IsOAuth() || site.APIKey != "" && site.APISecret != "" {
			_, verifyErr = verifyAPIKey(ctx, site)
		} else if site.IsSessionAuth() {
			verifyErr = verifyPassword(ctx, site)
		} else {
			verifyErr = usageErrorf("site has no credentials to verify")
		}
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errAborted
	}
	return verifyErr
}
