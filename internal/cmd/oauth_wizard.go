package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitesetup"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// The OAuth part of the setup wizard and of the non-interactive setup: the
// prompts, spinners and messages around sitesetup's OAuth flow.

// ─── Browser ─────────────────────────────────────────────────────────────────

// openBrowserFn opens the authorization URL; tests replace it with a
// function that delivers the callback themselves.
var openBrowserFn = openBrowser

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		// rundll32 takes the URL as a single argument; `cmd /c start "" <url>`
		// would let cmd.exe treat the '&' in the query string as a command
		// separator and truncate the auth URL, breaking login (L20).
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

// ─── OAuth wizard ────────────────────────────────────────────────────────────

// collectOAuthSite runs the OAuth Authorization Code + PKCE flow and returns
// the new site with its tokens. See collectSite for checkName. manual is the
// client given with --client-id; without one, ffc registers a client when
// the site offers dynamic registration and asks for one otherwise.
func collectOAuthSite(ctx context.Context, checkName func(string) error, manual sitesetup.OAuthApp) (string, config.SiteConfig, error) {
	var name, rawURL string
	if err := runForm(huh.NewGroup(siteNameInput(&name), siteURLInput(&rawURL))); err != nil {
		return "", config.SiteConfig{}, err
	}
	siteName, siteURL, err := sitesetup.NameAndURL(name, rawURL)
	if err != nil {
		return "", config.SiteConfig{}, err
	}
	if checkName != nil {
		if err := checkName(siteName); err != nil {
			return "", config.SiteConfig{}, err
		}
	}

	// Start the callback server before registering or showing the
	// instructions: the redirect URI names its port.
	flow, err := sitesetup.StartOAuthFlow()
	if err != nil {
		return "", config.SiteConfig{}, err
	}
	defer flow.Close()

	app, err := setUpOAuthApp(ctx, siteURL, flow.RedirectURI(), manual)
	var nr *sitesetup.NoRegistrationError
	switch {
	case errors.As(err, &nr):
		if app, err = promptOAuthApp(siteURL, flow.RedirectURI(), nr); err != nil {
			return "", config.SiteConfig{}, err
		}
	case err != nil:
		return "", config.SiteConfig{}, err
	}

	site, user, err := oauthLogin(ctx, siteURL, flow, app)
	if err != nil {
		return "", config.SiteConfig{}, err
	}

	clientLine := text.Sanitize(app.ID)
	if app.Registered {
		clientLine += " (registered by ffc)"
	}
	if _, err := reviewSite(fmt.Sprintf(
		"Site name:  %s\nSite URL:   %s\nClient ID:  %s\nLogged in:  %s",
		siteName, siteURL, clientLine, orDefault(user, "(unknown)"),
	), false); err != nil {
		return "", config.SiteConfig{}, err
	}
	return siteName, site, nil
}

// setUpOAuthApp runs sitesetup.ResolveOAuthApp under a spinner and reports a
// registration. A *sitesetup.NoRegistrationError leaves the fallback to the
// caller.
func setUpOAuthApp(ctx context.Context, siteURL, redirectURI string, manual sitesetup.OAuthApp) (sitesetup.OAuthApp, error) {
	var app sitesetup.OAuthApp
	var resolveErr error
	if err := runSpinner("Setting up the OAuth client...", func() {
		app, resolveErr = sitesetup.ResolveOAuthApp(ctx, siteURL, redirectURI, manual)
	}); err != nil || ctx.Err() != nil {
		return sitesetup.OAuthApp{}, errAborted
	}
	if resolveErr == nil && app.Registered {
		fmt.Fprintf(os.Stderr, "✓ Registered OAuth client %s on %s.\n", text.Sanitize(app.ID), siteURL)
	}
	return app, resolveErr
}

// promptOAuthApp explains why no client was registered and asks for the
// one the user creates by hand.
func promptOAuthApp(siteURL, redirectURI string, why *sitesetup.NoRegistrationError) (sitesetup.OAuthApp, error) {
	fmt.Fprintf(os.Stderr, `
No OAuth client was registered automatically: %v.
Create one by hand (or run again with --apikey to use an API key instead).

OAuth Client setup (one-time, on your Frappe site)
──────────────────────────────────────────────────
1. Go to: %s/app/oauth-client/new-oauth-client-1
2. Fill in:
     App Name:      ffc (or any name)
     Grant Type:    Authorization Code
     Scopes:        %s
     Redirect URIs: %s
3. Save → copy the Client ID (and Client Secret if using Confidential type).

`, why, siteURL, sitesetup.OAuthScope, redirectURI)

	var clientID, clientSecret string
	if err := runForm(huh.NewGroup(
		huh.NewInput().
			Title("OAuth Client ID").
			Description("From the OAuth Client you just created on Frappe").
			Validate(nonEmpty("client ID")).
			Value(&clientID),
		huh.NewInput().
			Title("OAuth Client Secret").
			Description("Leave empty if using a Public client (no secret)").
			EchoMode(huh.EchoModePassword).
			Value(&clientSecret),
	)); err != nil {
		return sitesetup.OAuthApp{}, err
	}
	return sitesetup.OAuthApp{ID: strings.TrimSpace(clientID), Secret: strings.TrimSpace(clientSecret)}, nil
}

// collectOAuthSiteNoInput is the OAuth flow of the non-interactive setup
// (--oauth with --name and --url): no prompt and no review, but the browser
// login still needs a person. Without --client-id the site must offer
// dynamic client registration.
func collectOAuthSiteNoInput(ctx context.Context, siteURL string, manual sitesetup.OAuthApp) (config.SiteConfig, error) {
	flow, err := sitesetup.StartOAuthFlow()
	if err != nil {
		return config.SiteConfig{}, err
	}
	defer flow.Close()

	app, err := setUpOAuthApp(ctx, siteURL, flow.RedirectURI(), manual)
	if err != nil {
		return config.SiteConfig{}, noInputOAuthError(err)
	}
	site, user, err := oauthLogin(ctx, siteURL, flow, app)
	if err != nil {
		return config.SiteConfig{}, err
	}
	if user != "" {
		fmt.Fprintf(os.Stderr, "✓ Logged in as %s.\n", user)
	}
	return site, nil
}

// noInputOAuthError is the setup error without a terminal, where no client
// ID can be asked for: a usage error when the site does not offer
// registration, the failure (its exit code kept) when registration failed.
func noInputOAuthError(err error) error {
	var nr *sitesetup.NoRegistrationError
	if !errors.As(err, &nr) {
		return err
	}
	const fix = "pass --client-id with the ID of an OAuth Client created on the site, or use --api-key"
	if nr.Unsupported {
		return usageErrorf("%v: %s", nr, fix)
	}
	return fmt.Errorf("%w; %s", nr, fix)
}

// oauthLogin runs flow.Login with the browser, the messages and the
// spinners of the terminal. user is "" when the site does not say who
// logged in. Ctrl+C (a cancelled ctx) or an aborted spinner is errAborted.
func oauthLogin(ctx context.Context, siteURL string, flow *sitesetup.OAuthFlow, app sitesetup.OAuthApp) (config.SiteConfig, string, error) {
	site, user, err := flow.Login(ctx, siteURL, app, sitesetup.LoginHooks{
		OpenBrowser: func(authURL string) error {
			fmt.Fprintf(os.Stderr, "\nOpening browser for authorization...\n")
			fmt.Fprintf(os.Stderr, "If the browser doesn't open automatically, visit:\n  %s\n\n", authURL)
			if err := openBrowserFn(authURL); err != nil {
				fmt.Fprintf(os.Stderr, "(Could not open browser: %v)\n\n", err)
			}
			fmt.Fprintf(os.Stderr, "Waiting for authorization (timeout: %s)...\n", flow.Timeout)
			return nil
		},
		Step: func(title string, run func()) error {
			if err := runSpinner(title, run); err != nil {
				return errAborted
			}
			return nil
		},
	})
	if err != nil && ctx.Err() != nil {
		return config.SiteConfig{}, "", errAborted
	}
	return site, user, err
}
