package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// OAuthTokens holds the tokens returned by the Frappe OAuth token endpoint.
type OAuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`

	// ExpiresAt is the absolute Unix expiry, computed when the token is
	// received (not when the config is written, which can be much later if a
	// review prompt is left open).
	ExpiresAt int64 `json:"-"`
}

// defaultTokenLifetime is Frappe's access-token lifetime, used when the server
// omits expires_in so the token can still be refreshed proactively.
const defaultTokenLifetime = 3600

const tokenEndpoint = "/api/method/frappe.integrations.oauth2.get_token"

// ExchangeOAuthCode exchanges an authorization code for access/refresh tokens
// using PKCE (codeVerifier). clientSecret is optional for public clients.
func ExchangeOAuthCode(ctx context.Context, siteURL, clientID, clientSecret, code, redirectURI, codeVerifier string) (*OAuthTokens, error) {
	return postToken(ctx, siteURL, "token exchange", map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"client_id":     clientID,
		"code_verifier": codeVerifier,
	}, clientSecret)
}

// RefreshOAuthToken uses a refresh token to obtain a new access token.
// clientSecret is optional for public clients.
func RefreshOAuthToken(ctx context.Context, siteURL, clientID, clientSecret, refreshToken string) (*OAuthTokens, error) {
	return postToken(ctx, siteURL, "token refresh", map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
	}, clientSecret)
}

func postToken(ctx context.Context, siteURL, what string, form map[string]string, clientSecret string) (*OAuthTokens, error) {
	warnIfInsecure(siteURL)
	if clientSecret != "" {
		form["client_secret"] = clientSecret
	}
	resp, err := newResty(siteURL).R().SetContext(ctx).SetFormData(form).Post(tokenEndpoint)
	if err != nil {
		return nil, requestError(err)
	}
	if resp.StatusCode() >= 400 {
		// Frappe's get_token answers with oauthlib's error shape
		// ({"error": "invalid_grant", "error_description": …}), not a
		// Frappe exception.
		if e := oauthErrorBody(resp.StatusCode(), resp.Body()); e != nil {
			return nil, fmt.Errorf("%s failed: %w", what, e)
		}
		return nil, fmt.Errorf("%s failed: %w", what, apiError(resp, nil))
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(resp.Body(), &tokens); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in server response")
	}
	lifetime := tokens.ExpiresIn
	if lifetime <= 0 {
		lifetime = defaultTokenLifetime
	}
	tokens.ExpiresAt = time.Now().Unix() + int64(lifetime)
	return &tokens, nil
}

// GetOAuthUser fetches the username of the authenticated user via the Frappe
// session endpoint. Returns the email/username string.
func GetOAuthUser(ctx context.Context, siteURL, accessToken string) (string, error) {
	resp, err := newResty(siteURL).R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+accessToken).
		Get("/api/method/frappe.auth.get_logged_user")
	if err != nil {
		return "", requestError(err)
	}
	if resp.StatusCode() >= 400 {
		return "", fmt.Errorf("get user failed: %w", apiError(resp, map[int]string{http.StatusUnauthorized: "access token rejected"}))
	}
	var result struct {
		Message string `json:"message"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		return "", err
	}
	return result.Message, nil
}

// Frappe's OAuth paths besides the token endpoint (frappe/integrations/oauth2.py).
const (
	// oauthMetadataPath is RFC 8414 authorization server metadata. Frappe
	// v16 serves it (handle_wellknown, behind the OAuth Settings check "Show
	// Auth Server Metadata", on by default); v15 has no such route.
	oauthMetadataPath = "/.well-known/oauth-authorization-server"
	// revokeEndpoint is RFC 7009 revocation (v15 and v16, allow_guest, POST).
	revokeEndpoint = "/api/method/frappe.integrations.oauth2.revoke_token"
)

// OAuthServerMetadata is the part of the authorization server metadata ffc
// reads. RegistrationEndpoint is empty unless the site has dynamic client
// registration enabled.
type OAuthServerMetadata struct {
	Issuer               string `json:"issuer"`
	RegistrationEndpoint string `json:"registration_endpoint"`
}

// DiscoverOAuthServer reads the site's authorization server metadata. Any
// answer but a 200 with a JSON object is an error: an *APIError for a
// status (404 on Frappe v15 or with the metadata turned off), a
// *TransportError when the site cannot be reached, a parse error for an
// HTML page.
func DiscoverOAuthServer(ctx context.Context, siteURL string) (*OAuthServerMetadata, error) {
	resp, err := newResty(siteURL).R().SetContext(ctx).SetHeader("Accept", "application/json").Get(oauthMetadataPath)
	if err != nil {
		return nil, requestError(err)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("authorization server metadata: %w", apiError(resp, nil))
	}
	var md OAuthServerMetadata
	if err := decodeJSON(resp, &md); err != nil {
		return nil, fmt.Errorf("authorization server metadata: %w", err)
	}
	return &md, nil
}

// OAuthClientMetadata is an RFC 7591 client registration request.
type OAuthClientMetadata struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	SoftwareID              string   `json:"software_id,omitempty"`
	SoftwareVersion         string   `json:"software_version,omitempty"`
}

// RegisteredOAuthClient is the server's answer to a registration. Frappe
// leaves out client_secret for a public client (token_endpoint_auth_method
// "none").
type RegisteredOAuthClient struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// RegisterOAuthClient registers a client at endpoint, the metadata's
// registration_endpoint. The request goes to the configured site URL with
// the endpoint's path: Frappe builds the endpoint from the URL it was
// reached at, which behind a TLS proxy can say http://, and a redirected
// POST is refused. An endpoint on another host is an error. It is never
// retried: each call creates an OAuth Client on the server.
func RegisterOAuthClient(ctx context.Context, siteURL, endpoint string, md OAuthClientMetadata) (*RegisteredOAuthClient, error) {
	path, err := sitePathOf(siteURL, endpoint)
	if err != nil {
		return nil, err
	}
	resp, err := newResty(siteURL).R().SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "application/json").
		SetBody(md).Post(path)
	if err != nil {
		return nil, requestError(err)
	}
	if resp.StatusCode() != http.StatusCreated && resp.StatusCode() != http.StatusOK {
		// register_client answers a refused request with RFC 7591's
		// {"error": "invalid_client_metadata", "error_description": …}; a
		// disabled registration is a NotFound, a rate limit a 429
		// RateLimitExceededError.
		if e := oauthErrorBody(resp.StatusCode(), resp.Body()); e != nil {
			return nil, fmt.Errorf("registering an OAuth client: %w", e)
		}
		return nil, fmt.Errorf("registering an OAuth client: %w", apiError(resp, nil))
	}
	var reg RegisteredOAuthClient
	if err := decodeJSON(resp, &reg); err != nil {
		return nil, fmt.Errorf("registering an OAuth client: %w", err)
	}
	if reg.ClientID == "" {
		return nil, fmt.Errorf("registering an OAuth client: no client_id in the server response")
	}
	return &reg, nil
}

// sitePathOf returns the path (and query) of endpoint, which must be on the
// site's host. A relative endpoint is taken as a site path.
func sitePathOf(siteURL, endpoint string) (string, error) {
	ep, err := url.Parse(endpoint)
	if err != nil || !strings.HasPrefix(ep.Path, "/") {
		return "", fmt.Errorf("invalid registration endpoint %q", text.Sanitize(endpoint))
	}
	if ep.IsAbs() || ep.Host != "" {
		site, err := url.Parse(siteURL)
		if err != nil || !sameHostname(site, ep) {
			return "", fmt.Errorf("registration endpoint %s is not on the site's host", text.Sanitize(ep.Redacted()))
		}
	}
	p := ep.EscapedPath()
	if ep.RawQuery != "" {
		p += "?" + ep.RawQuery
	}
	return p, nil
}

// oauthErrorBody turns an OAuth error body ({"error": code,
// "error_description": text}) into an *APIError, or returns nil when body
// is not one.
func oauthErrorBody(status int, body []byte) *APIError {
	var oe struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &oe) != nil || oe.Error == "" {
		return nil
	}
	msg := oe.Error
	if oe.Description != "" {
		msg += ": " + oe.Description
	}
	return &APIError{Status: status, Message: fmt.Sprintf("%s (HTTP %d)", snippet([]byte(msg)), status)}
}

// RevokeOAuthToken revokes token (RFC 7009) on the site. hint is
// "refresh_token" or "access_token": Frappe looks a token up by the hint
// only, and revoking a refresh token revokes the OAuth Bearer Token record
// that holds it, its access token included. Frappe's validator needs the
// client_id in the body to find the client (it checks no secret); it
// answers 200 for a token it does not know too. The token goes in the form
// body only, never in the URL or an error. Never retried.
func RevokeOAuthToken(ctx context.Context, siteURL, clientID, clientSecret, token, hint string) error {
	warnIfInsecure(siteURL)
	form := map[string]string{"token": token, "token_type_hint": hint}
	if clientID != "" {
		form["client_id"] = clientID
	}
	if clientSecret != "" {
		form["client_secret"] = clientSecret
	}
	resp, err := newResty(siteURL).R().SetContext(ctx).SetFormData(form).Post(revokeEndpoint)
	if err != nil {
		return requestError(err)
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("token revocation failed: %w", apiError(resp, nil))
	}
	return nil
}
