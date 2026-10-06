// Package sitesetup sets up ffc sites without a terminal: it validates a site
// name and URL, checks credentials against the site, runs the OAuth browser
// login (callback server, client registration, PKCE), writes sites to the
// config file and revokes an OAuth token on removal.
//
// It never prompts, prints or exits. The CLI (internal/cmd) wraps it with
// forms, spinners and messages; a desktop app can call it directly.
package sitesetup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// ValidateName rejects names that would be awkward to pass as --site or
// that could smuggle terminal escape sequences into later output.
func ValidateName(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("site name cannot be empty")
	}
	if !utf8.ValidString(s) {
		return errors.New("site name must be valid UTF-8")
	}
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			return errors.New("site name must not contain whitespace")
		case unicode.IsControl(r):
			return errors.New("site name must not contain control characters")
		}
	}
	return nil
}

// NormalizeURL turns user input into a site base URL: scheme://host[:port].
// A bare host gets https://. Paths, queries and fragments are dropped because
// every API call is built from the site root.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("URL cannot be empty")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q (use http or https)", u.Scheme)
	}
	if u.Hostname() == "" {
		return "", errors.New("URL must include a host")
	}
	if u.User != nil {
		return "", errors.New("URL must not contain credentials")
	}
	return scheme + "://" + u.Host, nil
}

// NameAndURL validates and normalizes a site name and URL as typed: the
// name trimmed, the URL from NormalizeURL.
func NameAndURL(name, rawURL string) (string, string, error) {
	name = strings.TrimSpace(name)
	if err := ValidateName(name); err != nil {
		return "", "", err
	}
	siteURL, err := NormalizeURL(rawURL)
	if err != nil {
		return "", "", err
	}
	return name, siteURL, nil
}

// ErrNoCredentials is Verify's answer for a site with no complete
// credential set.
var ErrNoCredentials = errors.New("site has no credentials to verify")

// Verify checks the credentials of site against its URL, in the order
// client.New picks them: an OAuth token or API key with an authenticated
// call (frappe.ping is guest-accessible, so it would not prove the
// credentials work), a username and password by logging in and out again.
// user is who the token or key belongs to ("" for a password, or when the
// site does not say).
func Verify(ctx context.Context, site config.SiteConfig) (user string, err error) {
	switch {
	case site.IsOAuth() || site.APIKey != "" && site.APISecret != "":
		return verifyAPIKey(ctx, site)
	case site.IsSessionAuth():
		return "", verifyPassword(ctx, site)
	}
	return "", ErrNoCredentials
}

func verifyAPIKey(ctx context.Context, site config.SiteConfig) (string, error) {
	c, err := client.New(ctx, &site)
	if err != nil {
		return "", err
	}
	msg, err := c.CallMethod(ctx, "frappe.auth.get_logged_user", nil, true)
	if err != nil {
		return "", fmt.Errorf("verifying API key: %w", err)
	}
	user, _ := msg.(string)
	return user, nil
}

// verifyPassword proves the username and password by logging in. The check
// only proves the password, so its session is ended again.
func verifyPassword(ctx context.Context, site config.SiteConfig) error {
	sid, err := client.LoginPassword(ctx, site.URL, site.Username, site.Password)
	if err != nil {
		return err
	}
	_ = client.Logout(ctx, site.URL, sid)
	return nil
}
