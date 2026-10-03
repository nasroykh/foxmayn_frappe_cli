package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
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
