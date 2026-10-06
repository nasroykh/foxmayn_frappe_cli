package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
)

// This file holds the OAuth client decision of site setup, free of
// prompts: the wizard (oauth_flow.go) and the non-interactive setup call it
// and handle the fallbacks at their edge.

// oauthScope is what ffc asks for: openid (who logged in) and all (the API).
// A registered client gets exactly these scopes: Frappe stores the
// registration's scope, or "all" without one (integrations/utils.py:246),
// and refuses an authorization request for a scope the client lacks
// (oauth.py:48).
const oauthScope = "openid all"

// oauthApp is the OAuth client a login uses.
type oauthApp struct {
	ID, Secret string
	Registered bool // created by dynamic client registration in this run
}

// noRegistrationError says why no client was registered: the site does not
// offer registration (unsupported), or the attempt failed (err).
type noRegistrationError struct {
	reason      string
	hint        string
	err         error
	unsupported bool
}

func (e *noRegistrationError) Error() string {
	s := e.reason
	switch {
	case e.err != nil && s != "":
		s += ": " + e.err.Error()
	case e.err != nil:
		s = e.err.Error()
	}
	if e.hint != "" {
		s += " (" + e.hint + ")"
	}
	return s
}

func (e *noRegistrationError) Unwrap() error { return e.err }

// rateLimitHint is shown when register_client answers 429. Frappe's
// develop branch rate-limits it with @rate_limit(limit=5, seconds=600),
// counted per IP address; v16.36.1 has no limit of its own, so a 429 there
// comes from the site-wide rate limit or a proxy.
const rateLimitHint = "the site limits client registrations; recent Frappe versions allow 5 per 10 minutes from one IP address, so wait a few minutes and try again"

// resolveOAuthApp returns the OAuth client for a login on siteURL whose
// callback is redirectURI. A manual client (--client-id) always wins and
// nothing is sent. Otherwise the site's authorization server metadata is
// read and, when it advertises a registration_endpoint, one public client
// is registered for exactly redirectURI. When that is not possible the
// error is a *noRegistrationError and the caller falls back (prompt, or a
// usage error without a terminal). A cancelled ctx returns ctx.Err().
func resolveOAuthApp(ctx context.Context, siteURL, redirectURI string, manual oauthApp) (oauthApp, error) {
	if manual.ID != "" {
		return manual, nil
	}
	md, err := client.DiscoverOAuthServer(ctx, siteURL)
	if ctx.Err() != nil {
		return oauthApp{}, ctx.Err()
	}
	switch {
	case err != nil && serverUnavailable(err):
		return oauthApp{}, &noRegistrationError{reason: "could not read the site's OAuth server metadata", err: err}
	case err != nil:
		return oauthApp{}, &noRegistrationError{unsupported: true,
			reason: `the site publishes no OAuth server metadata (Frappe v15, or "Show Auth Server Metadata" is off in OAuth Settings)`}
	case md.RegistrationEndpoint == "":
		return oauthApp{}, registrationOff()
	}

	reg, err := client.RegisterOAuthClient(ctx, siteURL, md.RegistrationEndpoint, oauthClientMetadata(redirectURI))
	if ctx.Err() != nil {
		return oauthApp{}, ctx.Err()
	}
	if err != nil {
		var ae *client.APIError
		switch {
		case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
			// register_client raises NotFound while registration is off.
			return oauthApp{}, registrationOff()
		case errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests:
			return oauthApp{}, &noRegistrationError{err: err, hint: rateLimitHint}
		}
		return oauthApp{}, &noRegistrationError{err: err}
	}
	return oauthApp{ID: reg.ClientID, Secret: reg.ClientSecret, Registered: true}, nil
}

func registrationOff() *noRegistrationError {
	return &noRegistrationError{unsupported: true,
		reason: `dynamic client registration is off on the site (OAuth Settings: "Enable Dynamic Client Registration")`}
}

// serverUnavailable reports whether err says nothing about the site's OAuth
// setup: no response, a rate limit or a server error.
func serverUnavailable(err error) bool {
	var te *client.TransportError
	var ae *client.APIError
	switch {
	case errors.As(err, &te):
		return true
	case errors.As(err, &ae):
		return ae.Status == http.StatusTooManyRequests || ae.Status >= 500
	}
	return false
}

// oauthClientMetadata is the registration request: a public client (PKCE,
// no secret) for the one callback URI of this run. Frappe stores the URIs
// joined by newlines but matches redirect_uri against them split on spaces
// (oauth.py:27-40, 170-179), so a client with more than one URI could never
// log in; the port is part of the URI, so the client is tied to it.
func oauthClientMetadata(redirectURI string) client.OAuthClientMetadata {
	return client.OAuthClientMetadata{
		ClientName:              oauthClientName(),
		RedirectURIs:            []string{redirectURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		Scope:                   oauthScope,
		ClientURI:               "https://github.com/nasroykh/foxmayn_frappe_cli",
		SoftwareID:              "foxmayn_frappe_cli",
		SoftwareVersion:         version.Version,
	}
}

// oauthClientName is the registered client's name, shown on the site's
// consent page and in the OAuth Client list: "ffc (<host name>)".
func oauthClientName() string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(text.Sanitize(host))
	if r := []rune(host); len(r) > 60 {
		host = string(r[:60])
	}
	if host == "" {
		return "ffc"
	}
	return "ffc (" + host + ")"
}

// noInputOAuthError is the setup error without a terminal, where no client
// ID can be asked for: a usage error when the site does not offer
// registration, the failure (its exit code kept) when registration failed.
func noInputOAuthError(err error) error {
	var nr *noRegistrationError
	if !errors.As(err, &nr) {
		return err
	}
	const fix = "pass --client-id with the ID of an OAuth Client created on the site, or use --api-key"
	if nr.unsupported {
		return usageErrorf("%v: %s", nr, fix)
	}
	return fmt.Errorf("%w; %s", nr, fix)
}
