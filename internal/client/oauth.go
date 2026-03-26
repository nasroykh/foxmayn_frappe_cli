package client

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-resty/resty/v2"
)

// OAuthTokens holds the tokens returned by the Frappe OAuth token endpoint.
type OAuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// ExchangeOAuthCode exchanges an authorization code for access/refresh tokens
// using PKCE (codeVerifier). clientSecret is optional for public clients.
func ExchangeOAuthCode(siteURL, clientID, clientSecret, code, redirectURI, codeVerifier string) (*OAuthTokens, error) {
	r := resty.New().SetBaseURL(strings.TrimRight(siteURL, "/"))

	body := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"client_id":     clientID,
		"code_verifier": codeVerifier,
	}
	if clientSecret != "" {
		body["client_secret"] = clientSecret
	}

	resp, err := r.R().
		SetFormData(body).
		Post("/api/method/frappe.integrations.oauth2.get_token")
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode(), strings.TrimSpace(string(resp.Body())))
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(resp.Body(), &tokens); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in server response")
	}
	return &tokens, nil
}

// RefreshOAuthToken uses a refresh token to obtain a new access token.
// clientSecret is optional for public clients.
func RefreshOAuthToken(siteURL, clientID, clientSecret, refreshToken string) (*OAuthTokens, error) {
	r := resty.New().SetBaseURL(strings.TrimRight(siteURL, "/"))

	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
	}
	if clientSecret != "" {
		body["client_secret"] = clientSecret
	}

	resp, err := r.R().
		SetFormData(body).
		Post("/api/method/frappe.integrations.oauth2.get_token")
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode(), strings.TrimSpace(string(resp.Body())))
	}

	var tokens OAuthTokens
	if err := json.Unmarshal(resp.Body(), &tokens); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in server response")
	}
	return &tokens, nil
}

// GetOAuthUser fetches the username of the authenticated user via the Frappe
// session endpoint. Returns the email/username string.
func GetOAuthUser(siteURL, accessToken string) (string, error) {
	r := resty.New().
		SetBaseURL(strings.TrimRight(siteURL, "/")).
		SetHeader("Authorization", "Bearer "+accessToken)

	resp, err := r.R().Get("/api/method/frappe.auth.get_logged_user")
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return "", fmt.Errorf("get user failed (%d)", resp.StatusCode())
	}

	var result struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", fmt.Errorf("parsing user response: %w", err)
	}
	return result.Message, nil
}
