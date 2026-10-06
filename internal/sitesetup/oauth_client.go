package sitesetup

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
// prompts: the CLI wizard and the non-interactive setup call it and handle
// the fallbacks at their edge.

// OAuthScope is what ffc asks for: openid (who logged in) and all (the API).
// A registered client gets exactly these scopes: Frappe stores the
// registration's scope, or "all" without one (integrations/utils.py:246),
// and refuses an authorization request for a scope the client lacks
// (oauth.py:48).
const OAuthScope = "openid all"

// OAuthApp is the OAuth client a login uses.
type OAuthApp struct {
	ID, Secret string
	Registered bool // created by dynamic client registration in this run
}

// NoRegistrationError says why no client was registered: the site does not
// offer registration (Unsupported), or the attempt failed (the wrapped
// error).
type NoRegistrationError struct {
	reason string
	hint   string
	err    error
	// Unsupported: the site does not offer registration at all, so only a
	// client created by hand can log in. Otherwise the attempt failed and
	// may work later.
	Unsupported bool
}

func (e *NoRegistrationError) Error() string {
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

func (e *NoRegistrationError) Unwrap() error { return e.err }

// rateLimitHint is shown when register_client answers 429. Frappe's
// develop branch rate-limits it with @rate_limit(limit=5, seconds=600),
// counted per IP address; v16.36.1 has no limit of its own, so a 429 there
// comes from the site-wide rate limit or a proxy.
const rateLimitHint = "the site limits client registrations; recent Frappe versions allow 5 per 10 minutes from one IP address, so wait a few minutes and try again"

// ResolveOAuthApp returns the OAuth client for a login on siteURL whose
// callback is redirectURI. A manual client (--client-id) always wins and
// nothing is sent. Otherwise the site's authorization server metadata is
// read and, when it advertises a registration_endpoint, one public client
// is registered for exactly redirectURI. When that is not possible the
// error is a *NoRegistrationError and the caller falls back (prompt, or a
// usage error without a terminal). A cancelled ctx returns ctx.Err().
func ResolveOAuthApp(ctx context.Context, siteURL, redirectURI string, manual OAuthApp) (OAuthApp, error) {
	if manual.ID != "" {
		return manual, nil
	}
	md, err := client.DiscoverOAuthServer(ctx, siteURL)
	if ctx.Err() != nil {
		return OAuthApp{}, ctx.Err()
	}
	// Without metadata ffc does not know the registration endpoint, so it
	// never posts to register_client blind.
	var ae *client.APIError
	switch {
	case err != nil && serverUnavailable(err):
		return OAuthApp{}, &NoRegistrationError{reason: "could not read the site's OAuth server metadata", err: err}
	case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
		// register_client checks only enable_dynamic_client_registration
		// (oauth2.py:367), so registration may be on with discovery off.
		return OAuthApp{}, &NoRegistrationError{Unsupported: true,
			reason: `the site publishes no OAuth server metadata (Frappe v15, or "Show Auth Server Metadata" is off in OAuth Settings; ` +
				`dynamic client registration may still be on, but ffc finds it only through the metadata)`}
	case errors.As(err, &ae):
		// A proxy or firewall in front of the site (a WAF's 403, a login
		// page's 401) says nothing about the site's OAuth settings.
		return OAuthApp{}, &NoRegistrationError{Unsupported: true,
			reason: fmt.Sprintf("the site refused the OAuth server metadata request (HTTP %d)", ae.Status)}
	case err != nil:
		return OAuthApp{}, &NoRegistrationError{Unsupported: true,
			reason: "the site's answer to the OAuth server metadata request is not metadata (a login page or a proxy in front of the site?)"}
	case md.RegistrationEndpoint == "":
		return OAuthApp{}, registrationOff()
	}

	reg, err := client.RegisterOAuthClient(ctx, siteURL, md.RegistrationEndpoint, ClientMetadata(redirectURI))
	if ctx.Err() != nil {
		return OAuthApp{}, ctx.Err()
	}
	if err != nil {
		var ae *client.APIError
		switch {
		case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
			// register_client raises NotFound while registration is off.
			return OAuthApp{}, registrationOff()
		case errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests:
			return OAuthApp{}, &NoRegistrationError{err: err, hint: rateLimitHint}
		}
		return OAuthApp{}, &NoRegistrationError{err: err}
	}
	return OAuthApp{ID: reg.ClientID, Secret: reg.ClientSecret, Registered: true}, nil
}

func registrationOff() *NoRegistrationError {
	return &NoRegistrationError{Unsupported: true,
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

// ClientMetadata is the registration request: a public client (PKCE,
// no secret) for the one callback URI of this run. Frappe stores the URIs
// joined by newlines but matches redirect_uri against them split on spaces
// (oauth.py:27-40, 170-179), so a client with more than one URI could never
// log in; the port is part of the URI, so the client is tied to it.
func ClientMetadata(redirectURI string) client.OAuthClientMetadata {
	return client.OAuthClientMetadata{
		ClientName:              oauthClientName(),
		RedirectURIs:            []string{redirectURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		Scope:                   OAuthScope,
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
