package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/huh"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitesetup"
)

// errAborted is returned when the user cancels or declines a prompt. It is an
// error (not a nil return) so scripts see a non-zero exit status.
var errAborted = errors.New("aborted")

const (
	authOAuth    = "oauth"
	authAPIKey   = "apikey"
	authPassword = "password"
)

// errNoInput is returned instead of prompting when input is disabled.
var errNoInput = &usageError{errors.New("this needs interactive input, but --no-input is set or stdin is not a terminal")}

// inputDisabled reports whether prompts must not be shown: --no-input, or a
// stdin that is not a terminal (a pipe, a file, CI).
func inputDisabled() bool {
	if noInput {
		return true
	}
	return !isTerminal(os.Stdin)
}

// runForm runs a huh form with Esc bound to quit. Esc/Ctrl+C becomes
// errAborted; with input disabled it returns errNoInput without prompting;
// any other error is returned unchanged.
func runForm(groups ...*huh.Group) error {
	if inputDisabled() {
		return errNoInput
	}
	err := huh.NewForm(groups...).WithKeyMap(escQuitKeyMap()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return errAborted
	}
	return err
}

// confirmPrompt asks a yes/no question and reports the answer.
func confirmPrompt(title, description string) (bool, error) {
	var ok bool
	err := runForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Description(description).Value(&ok),
	))
	return ok, err
}

// ─── Validation ──────────────────────────────────────────────────────────────

func nonEmpty(what string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s cannot be empty", what)
		}
		return nil
	}
}

// ─── Shared inputs ───────────────────────────────────────────────────────────

func siteNameInput(v *string) *huh.Input {
	return huh.NewInput().
		Title("Site name").
		Description("A short identifier, e.g. dev or production").
		Placeholder("dev").
		Validate(sitesetup.ValidateName).
		Value(v)
}

func siteURLInput(v *string) *huh.Input {
	return huh.NewInput().
		Title("Site URL").
		Description("Base URL of your Frappe site (https:// added if you omit the scheme)").
		Placeholder("mysite.example.com").
		Validate(func(s string) error {
			_, err := sitesetup.NormalizeURL(s)
			return err
		}).
		Value(v)
}

// checkNameOnce wraps a site-name check (e.g. "overwrite existing site?") so
// it is asked again only when the name changes between Edit iterations.
func checkNameOnce(check func(string) error) func(string) error {
	if check == nil {
		return func(string) error { return nil }
	}
	var accepted string
	return func(name string) error {
		if name == accepted {
			return nil
		}
		if err := check(name); err != nil {
			return err
		}
		accepted = name
		return nil
	}
}

// reviewSite shows the summary and returns edit=true when the user wants to
// go back and change the input. Cancel returns errAborted.
func reviewSite(summary string, allowEdit bool) (edit bool, err error) {
	opts := []huh.Option[string]{huh.NewOption("Confirm", "confirm")}
	if allowEdit {
		opts = append(opts, huh.NewOption("Edit", "edit"))
	}
	opts = append(opts, huh.NewOption("Cancel", "cancel"))

	var choice string
	if err := runForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Review configuration").
			Description(summary).
			Options(opts...).
			Value(&choice),
	)); err != nil {
		return false, err
	}
	switch choice {
	case "cancel":
		return false, errAborted
	case "edit":
		return true, nil
	}
	return false, nil
}

// ─── Auth method selection ───────────────────────────────────────────────────

// chooseAuthMethod returns the method selected by flag, or asks for one.
func chooseAuthMethod(title string, oauth, apiKey, password bool) (string, error) {
	switch {
	case oauth:
		return authOAuth, nil
	case apiKey:
		return authAPIKey, nil
	case password:
		return authPassword, nil
	}
	var method string
	err := runForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(title).
			Options(
				huh.NewOption("OAuth 2.0          — browser login, no password stored", authOAuth),
				huh.NewOption("API Key            — paste your API key and secret", authAPIKey),
				huh.NewOption("Username & Password — email/username + password login", authPassword),
			).
			Value(&method),
	))
	return method, err
}

// collectSite runs the wizard for method and returns the new site. checkName
// (optional) runs once the name is known, before any credentials are verified
// or the browser flow starts; returning an error stops the wizard. oauth is
// the OAuth client given with --client-id (zero: register one or ask).
func collectSite(ctx context.Context, method string, checkName func(string) error, oauth oauthApp) (string, config.SiteConfig, error) {
	switch method {
	case authOAuth:
		return collectOAuthSite(ctx, checkName, oauth)
	case authPassword:
		return collectPasswordSite(ctx, checkName)
	case authAPIKey:
		return collectAPIKeySite(ctx, checkName)
	}
	return "", config.SiteConfig{}, fmt.Errorf("unknown auth method %q", method)
}

// ─── API key ─────────────────────────────────────────────────────────────────

func collectAPIKeySite(ctx context.Context, checkName func(string) error) (string, config.SiteConfig, error) {
	check := checkNameOnce(checkName)
	var name, rawURL, apiKey, apiSecret string
	for {
		err := runForm(
			huh.NewGroup(siteNameInput(&name), siteURLInput(&rawURL)),
			huh.NewGroup(
				huh.NewInput().
					Title("API Key").
					Description("From User → API Access → Generate Keys").
					Placeholder("21393a7e100ae26").
					Validate(nonEmpty("API key")).
					Value(&apiKey),
				huh.NewInput().
					Title("API Secret").
					EchoMode(huh.EchoModePassword).
					Validate(nonEmpty("API secret")).
					Value(&apiSecret),
			),
		)
		if err != nil {
			return "", config.SiteConfig{}, err
		}
		siteName, siteURL, err := sitesetup.NameAndURL(name, rawURL)
		if err != nil {
			return "", config.SiteConfig{}, err
		}
		if err := check(siteName); err != nil {
			return "", config.SiteConfig{}, err
		}
		site := config.SiteConfig{
			URL:       siteURL,
			APIKey:    strings.TrimSpace(apiKey),
			APISecret: strings.TrimSpace(apiSecret),
		}

		var user string
		var verifyErr error
		if err := runSpinner("Verifying credentials...", func() {
			user, verifyErr = sitesetup.Verify(ctx, site)
		}); err != nil || ctx.Err() != nil {
			return "", config.SiteConfig{}, errAborted
		}
		if verifyErr != nil {
			fmt.Fprintf(os.Stderr, "\n✗ %v\n\n", verifyErr)
			continue
		}

		edit, err := reviewSite(fmt.Sprintf(
			"Site name:  %s\nSite URL:   %s\nAPI key:    %s\nAPI secret: (%d characters)\nLogged in:  %s",
			siteName, site.URL, site.APIKey, len(site.APISecret), orDefault(user, "(unknown)"),
		), true)
		if err != nil {
			return "", config.SiteConfig{}, err
		}
		if !edit {
			return siteName, site, nil
		}
	}
}

// ─── Username / password ─────────────────────────────────────────────────────

func collectPasswordSite(ctx context.Context, checkName func(string) error) (string, config.SiteConfig, error) {
	check := checkNameOnce(checkName)
	var name, rawURL, username, password string
	for {
		err := runForm(
			huh.NewGroup(siteNameInput(&name), siteURLInput(&rawURL)),
			huh.NewGroup(
				huh.NewInput().
					Title("Email or username").
					Placeholder("user@example.com").
					Validate(nonEmpty("email/username")).
					Value(&username),
				huh.NewInput().
					Title("Password").
					EchoMode(huh.EchoModePassword).
					Validate(func(s string) error {
						if s == "" {
							return errors.New("password cannot be empty")
						}
						return nil
					}).
					Value(&password),
			),
		)
		if err != nil {
			return "", config.SiteConfig{}, err
		}
		siteName, siteURL, err := sitesetup.NameAndURL(name, rawURL)
		if err != nil {
			return "", config.SiteConfig{}, err
		}
		if err := check(siteName); err != nil {
			return "", config.SiteConfig{}, err
		}
		// Passwords are stored verbatim: leading/trailing spaces may be real.
		site := config.SiteConfig{URL: siteURL, Username: strings.TrimSpace(username), Password: password}

		var loginErr error
		if err := runSpinner("Verifying credentials...", func() {
			_, loginErr = sitesetup.Verify(ctx, site)
		}); err != nil || ctx.Err() != nil {
			return "", config.SiteConfig{}, errAborted
		}
		if loginErr != nil {
			fmt.Fprintf(os.Stderr, "\n✗ %v\n\n", loginErr)
			continue
		}

		edit, err := reviewSite(fmt.Sprintf(
			"Site name: %s\nSite URL:  %s\nUsername:  %s\nPassword:  (%d characters)",
			siteName, site.URL, site.Username, len(site.Password),
		), true)
		if err != nil {
			return "", config.SiteConfig{}, err
		}
		if !edit {
			return siteName, site, nil
		}
	}
}

// ─── Persisting ──────────────────────────────────────────────────────────────

// writeInitConfig replaces the config at path with one holding only this site.
func writeInitConfig(path, name string, site config.SiteConfig) error {
	err := config.Overwrite(path, func(f *config.File) error {
		f.Set("default_site", name)
		return f.PutSite(name, site)
	})
	if err == nil {
		dropSiteCache(name) // a cache under this name belongs to an earlier site
	}
	return err
}

// addSiteToConfig adds or replaces one site, keeping the rest of the file.
// A replaced site's cache (another login, maybe another server) is dropped.
func addSiteToConfig(path, name string, site config.SiteConfig) error {
	err := config.Edit(path, func(f *config.File) error {
		return f.PutSite(name, site)
	})
	if err == nil {
		dropSiteCache(name)
	}
	return err
}

// printSiteSaved prints the post-write hints shared by init and site add.
func printSiteSaved(name string, site config.SiteConfig) {
	if site.IsSessionAuth() {
		fmt.Fprintln(os.Stderr, "  Note: your account password is stored in cleartext in this file (mode 0600).")
		fmt.Fprintln(os.Stderr, "  Prefer OAuth (--oauth) or a scoped API key where possible.")
	}
	fmt.Fprintf(os.Stderr, "  Run: ffc --site %s list-docs --doctype \"Sales Invoice\"\n", shellQuote(name))
}

// shellQuote quotes s for a POSIX shell when it holds anything beyond a safe
// set of characters (a name like "#dev" would otherwise start a comment).
func shellQuote(s string) string {
	safe := s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r)))
	}) < 0
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
