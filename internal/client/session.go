package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// loginBody is the JSON response shape of POST /api/method/login.
type loginBody struct {
	Message      string         `json:"message"`
	FullName     string         `json:"full_name"`
	Verification map[string]any `json:"verification"`
	TmpID        string         `json:"tmp_id"`
	SID          string         `json:"sid"`
}

// LoginPassword authenticates with a Frappe site using a username/email and
// password via POST /api/method/login, and returns the resulting session
// cookie ("sid") value.
func LoginPassword(ctx context.Context, siteURL, usr, pwd string) (string, error) {
	warnIfInsecure(siteURL)
	// Shares the client policy (timeout, no redirects of the POST, no
	// logging). Login is intentionally not retried.
	resp, err := newResty(siteURL).R().
		SetContext(ctx).
		SetBody(map[string]string{"usr": usr, "pwd": pwd}).
		Post("/api/method/login")
	if err != nil {
		return "", requestError(err)
	}

	var body loginBody
	jsonErr := json.Unmarshal(resp.Body(), &body)

	switch code := resp.StatusCode(); {
	case code == http.StatusUnauthorized, code == http.StatusForbidden, code == http.StatusExpectationFailed:
		// Rejected credentials (or a disabled user).
		if body.Message != "" {
			return "", &AuthError{resp.StatusCode(), "login failed: " + stripHTML(body.Message)}
		}
		return "", &AuthError{resp.StatusCode(), fmt.Sprintf("login failed (HTTP %d)", resp.StatusCode())}
	case code >= 400:
		// Rate limit, server error, wrong path: not a credentials problem.
		return "", apiError(resp, nil)
	}

	// A 2xx with a non-JSON body means we hit something other than Frappe's
	// login endpoint (a proxy/WAF/redirect/rate-limit page) — say so clearly (I2).
	if jsonErr != nil {
		return "", fmt.Errorf("login endpoint returned a non-JSON response (HTTP %d) — check the site URL", resp.StatusCode())
	}

	// Frappe returns HTTP 200 with a verification/tmp_id payload instead of
	// "Logged In" when two-factor authentication is required. This simple
	// usr/pwd flow doesn't support 2FA.
	if body.Verification != nil || body.TmpID != "" {
		return "", &AuthError{Message: "two-factor authentication is enabled for this account — use 'ffc init --oauth' or an API key instead"}
	}

	if body.Message != "Logged In" && body.Message != "No App" && body.FullName == "" {
		return "", &AuthError{Message: "login failed: unexpected response from server"}
	}

	sid := sidFromCookies(resp.Cookies())
	if sid == "" {
		sid = body.SID
	}
	if sid == "" || sid == "Guest" {
		return "", &AuthError{Message: "login succeeded but no session cookie was returned"}
	}
	return sid, nil
}

// Logout ends the server-side session sid (POST /api/method/logout).
func Logout(ctx context.Context, siteURL, sid string) error {
	resp, err := newResty(siteURL).R().
		SetContext(ctx).
		SetHeader("Cookie", "sid="+sid).
		Post("/api/method/logout")
	if err != nil {
		return requestError(err)
	}
	if resp.StatusCode() >= 400 {
		return fmt.Errorf("logout failed (HTTP %d)", resp.StatusCode())
	}
	return nil
}

// sidFromCookies extracts the "sid" cookie value from a set of response cookies.
func sidFromCookies(cookies []*http.Cookie) string {
	for _, c := range cookies {
		if c.Name == "sid" {
			return c.Value
		}
	}
	return ""
}
