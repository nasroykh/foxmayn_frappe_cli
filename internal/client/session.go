package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
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
	// A bounded timeout so a hung login endpoint cannot hang every command for
	// a session-auth site (M14). Login is intentionally not retried.
	r := resty.New().
		SetBaseURL(strings.TrimRight(siteURL, "/")).
		SetTimeout(30 * time.Second)

	resp, err := r.R().
		SetContext(ctx).
		SetBody(map[string]string{"usr": usr, "pwd": pwd}).
		Post("/api/method/login")
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}

	var body loginBody
	jsonErr := json.Unmarshal(resp.Body(), &body)

	if resp.StatusCode() >= 400 {
		if body.Message != "" {
			return "", fmt.Errorf("login failed: %s", body.Message)
		}
		return "", fmt.Errorf("login failed (HTTP %d)", resp.StatusCode())
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
		return "", fmt.Errorf("two-factor authentication is enabled for this account — use 'ffc init --oauth' or an API key instead")
	}

	if body.Message != "Logged In" && body.Message != "No App" && body.FullName == "" {
		return "", fmt.Errorf("login failed: unexpected response from server")
	}

	sid := sidFromCookies(resp.Cookies())
	if sid == "" {
		sid = body.SID
	}
	if sid == "" || sid == "Guest" {
		return "", fmt.Errorf("login succeeded but no session cookie was returned")
	}
	return sid, nil
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
