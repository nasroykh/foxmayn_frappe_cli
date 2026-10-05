package frappetest

import (
	"fmt"
	"net/http"
)

// OAuth credentials of a new Site besides Token: RefreshToken refreshes it
// for the client OAuthClientID.
const (
	RefreshToken  = "test-refresh-token"
	OAuthClientID = "test-oauth-client"
)

// tokenEndpoint is Frappe's OAuth token endpoint (allow_guest).
const tokenEndpoint = "/api/method/frappe.integrations.oauth2.get_token"

// bearerToken is an issued access token. left counts the requests it still
// authenticates (<0: no limit); expired is set once it has run out.
type bearerToken struct {
	left    int
	expired bool
}

// oauthState is the Site's OAuth server: access tokens, active refresh
// tokens and the refresh counters.
type oauthState struct {
	tokens      map[string]*bearerToken
	refresh     map[string]bool
	refreshes   int
	failRefresh bool
}

func newOAuthState() oauthState {
	return oauthState{
		tokens:  map[string]*bearerToken{Token: {left: -1}},
		refresh: map[string]bool{RefreshToken: true},
	}
}

// ExpireToken makes an access token expire now: Frappe then refuses it in
// validate_auth with AuthenticationError (401).
func (s *Site) ExpireToken(token string) {
	s.ExpireTokenAfter(token, 0)
}

// ExpireTokenAfter lets an access token authenticate n more requests, then
// expire. Tokens issued by a refresh never expire unless told to.
func (s *Site) ExpireTokenAfter(token string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.oauth.tokens[token]
	if b == nil {
		b = &bearerToken{}
		s.oauth.tokens[token] = b
	}
	b.left, b.expired = n, n == 0
}

// FailRefresh makes the token endpoint refuse refresh tokens (invalid_grant)
// while on is true.
func (s *Site) FailRefresh(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauth.failRefresh = on
}

// Refreshes returns how many refresh_token grants succeeded.
func (s *Site) Refreshes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.oauth.refreshes
}

// useBearer reports whether token authenticates this request, spending one
// of its remaining requests.
func (s *Site) useBearer(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.oauth.tokens[token]
	switch {
	case b == nil || b.expired:
		return false
	case b.left == 0:
		b.expired = true
		return false
	case b.left > 0:
		b.left--
	}
	return true
}

// token answers the OAuth token endpoint for the refresh_token grant like
// Frappe's get_token: errors are oauthlib's {"error": …} with HTTP 400 (401
// for an unknown client). As in Frappe, a refresh issues a new refresh token
// and leaves the old one active.
func (s *Site) token(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method != http.MethodPost {
		writeError(w, Permission("Not permitted"))
		return
	}
	args := parseArgs(r, body)
	oauthErr := func(status int, code string) {
		writeJSON(w, status, map[string]string{"error": code})
	}
	if args["grant_type"] != "refresh_token" {
		oauthErr(http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if args["client_id"] != OAuthClientID {
		oauthErr(http.StatusUnauthorized, "invalid_client")
		return
	}
	rt, _ := args["refresh_token"].(string)
	s.mu.Lock()
	if s.oauth.failRefresh || !s.oauth.refresh[rt] {
		s.mu.Unlock()
		oauthErr(http.StatusBadRequest, "invalid_grant")
		return
	}
	s.oauth.refreshes++
	n := s.oauth.refreshes
	access, refresh := fmt.Sprintf("%s-%d", Token, n), fmt.Sprintf("%s-%d", RefreshToken, n)
	s.oauth.tokens[access] = &bearerToken{left: -1}
	s.oauth.refresh[refresh] = true
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"access_token": access, "token_type": "Bearer", "expires_in": 3600,
		"refresh_token": refresh, "scope": "all openid",
	})
}
