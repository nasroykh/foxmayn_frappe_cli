package frappetest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// OAuth credentials of a new Site besides Token: RefreshToken refreshes it
// for the client OAuthClientID. AuthCode is the authorization code the
// token endpoint accepts for any known client (the fake has no consent
// page: a test hands it to ffc's callback server itself).
const (
	RefreshToken  = "test-refresh-token"
	OAuthClientID = "test-oauth-client"
	AuthCode      = "test-auth-code"
)

// Frappe's OAuth endpoints (frappe/integrations/oauth2.py, v16).
const (
	tokenEndpoint    = "/api/method/frappe.integrations.oauth2.get_token"
	revokeEndpoint   = "/api/method/frappe.integrations.oauth2.revoke_token"
	registerEndpoint = "/api/method/frappe.integrations.oauth2.register_client"
	metadataPath     = "/.well-known/oauth-authorization-server"
)

// bearerToken is an issued access token. left counts the requests it still
// authenticates (<0: no limit); expired is set once it has run out.
type bearerToken struct {
	left    int
	expired bool
}

// Registration is one dynamic client registration the Site accepted: the
// client_id it issued and the request body as sent.
type Registration struct {
	ClientID string
	Metadata map[string]interface{}
}

// oauthState is the Site's OAuth server: access tokens, active refresh
// tokens (with the access token issued alongside, the same OAuth Bearer
// Token record in Frappe), clients, registrations and revocations.
type oauthState struct {
	tokens      map[string]*bearerToken
	refresh     map[string]string // refresh token → its access token
	refreshes   int
	issued      int
	failRefresh bool

	clients        map[string][]string // client_id → redirect URIs (nil: any)
	noMetadata     bool
	noRegistration bool
	failRegister   int
	registrations  []Registration
	revoked        []string
}

func newOAuthState() oauthState {
	return oauthState{
		tokens:  map[string]*bearerToken{Token: {left: -1}},
		refresh: map[string]string{RefreshToken: Token},
		clients: map[string][]string{OAuthClientID: nil},
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

// SetAuthServerMetadata turns /.well-known/oauth-authorization-server on
// (the default, as OAuth Settings "Show Auth Server Metadata") or off (a
// 404, as on Frappe v15).
func (s *Site) SetAuthServerMetadata(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauth.noMetadata = !on
}

// SetDynamicRegistration turns dynamic client registration on (the default,
// as OAuth Settings "Enable Dynamic Client Registration") or off: the
// metadata then has no registration_endpoint and register_client is a 404.
func (s *Site) SetDynamicRegistration(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauth.noRegistration = !on
}

// FailRegistration makes register_client fail with status (429: Frappe's
// rate limit, RateLimitExceededError; any other: invalid_client_metadata);
// 0 lets it succeed again.
func (s *Site) FailRegistration(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauth.failRegister = status
}

// Registrations returns the accepted client registrations in order.
func (s *Site) Registrations() []Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Registration(nil), s.oauth.registrations...)
}

// Revoked returns the tokens revoke_token revoked, in order.
func (s *Site) Revoked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.oauth.revoked...)
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

// issue stores a new access/refresh token pair. The caller holds s.mu.
func (s *Site) issue(access, refresh string) map[string]interface{} {
	s.oauth.tokens[access] = &bearerToken{left: -1}
	s.oauth.refresh[refresh] = access
	return map[string]interface{}{
		"access_token": access, "token_type": "Bearer", "expires_in": 3600,
		"refresh_token": refresh, "scope": "all openid",
	}
}

// token answers the OAuth token endpoint like Frappe's get_token: errors are
// oauthlib's {"error": …} with HTTP 400 (401 for an unknown client). As in
// Frappe, a refresh issues a new refresh token and leaves the old one
// active; a revoked refresh token is a 403 PermissionError (pinned by
// TestContractOAuthRevoke). The authorization_code grant takes AuthCode for any known client,
// with a redirect_uri the client registered and a code_verifier (PKCE).
func (s *Site) token(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method != http.MethodPost {
		writeError(w, Permission("Not permitted"))
		return
	}
	args := parseArgs(r, body)
	oauthErr := func(status int, code string) {
		writeJSON(w, status, map[string]string{"error": code})
	}
	grant := args["grant_type"]
	if grant != "refresh_token" && grant != "authorization_code" {
		oauthErr(http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	clientID, _ := args["client_id"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	uris, known := s.oauth.clients[clientID]
	if !known {
		oauthErr(http.StatusUnauthorized, "invalid_client")
		return
	}
	if grant == "authorization_code" {
		redirect, _ := args["redirect_uri"].(string)
		switch {
		case args["code"] != AuthCode, args["code_verifier"] == nil || args["code_verifier"] == "":
			oauthErr(http.StatusBadRequest, "invalid_grant")
		case uris != nil && !contains(uris, redirect):
			oauthErr(http.StatusBadRequest, "invalid_grant")
		default:
			s.oauth.issued++
			n := s.oauth.issued
			writeJSON(w, http.StatusOK, s.issue(fmt.Sprintf("%s-code-%d", Token, n), fmt.Sprintf("%s-code-%d", RefreshToken, n)))
		}
		return
	}
	rt, _ := args["refresh_token"].(string)
	if contains(s.oauth.revoked, rt) {
		// validate_refresh_token's get_doc finds no Active record and raises
		// DoesNotExistError, which is no OAuth2Error, so it leaves get_token
		// and the app turns it into a PermissionError for Guest
		// (permissions.py:926, handle_does_not_exist_error).
		writeError(w, Permission("User Guest does not have doctype access via role permission for document OAuth Bearer Token"))
		return
	}
	if _, ok := s.oauth.refresh[rt]; s.oauth.failRefresh || !ok {
		oauthErr(http.StatusBadRequest, "invalid_grant")
		return
	}
	s.oauth.refreshes++
	n := s.oauth.refreshes
	writeJSON(w, http.StatusOK, s.issue(fmt.Sprintf("%s-%d", Token, n), fmt.Sprintf("%s-%d", RefreshToken, n)))
}

// authServerMetadata answers /.well-known/oauth-authorization-server like
// _get_authorization_server_metadata (oauth2.py:324): endpoints under the
// URL the request reached, registration_endpoint only while registration
// is enabled; a 404 page when the metadata is off.
func (s *Site) authServerMetadata(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	off, noReg := s.oauth.noMetadata, s.oauth.noRegistration
	s.mu.Unlock()
	if off || r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "<html><body><h1>Not Found</h1></body></html>")
		return
	}
	issuer := "http://" + r.Host
	md := map[string]interface{}{
		"issuer":                                     issuer,
		"authorization_endpoint":                     issuer + "/api/method/frappe.integrations.oauth2.authorize",
		"token_endpoint":                             issuer + tokenEndpoint,
		"response_types_supported":                   []string{"code"},
		"response_modes_supported":                   []string{"query"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":      []string{"none", "client_secret_basic"},
		"revocation_endpoint":                        issuer + revokeEndpoint,
		"revocation_endpoint_auth_methods_supported": []string{"client_secret_basic"},
		"code_challenge_methods_supported":           []string{"S256"},
	}
	if !noReg {
		md["registration_endpoint"] = issuer + registerEndpoint
	}
	writeJSON(w, http.StatusOK, md)
}

// registerClient answers register_client like Frappe (oauth2.py:360,
// integrations/utils.py:206-274): NotFound while disabled; a JSON body
// validated as OAuth2DynamicClientMetadata (redirect_uris and client_name
// required) and by validate_dynamic_client_metadata (grant types
// authorization_code/refresh_token only, response type code only, https or
// loopback-IP http redirect URIs); 201 with the client_id and, for a
// public client, no client_secret.
func (s *Site) registerClient(w http.ResponseWriter, r *http.Request, body []byte) {
	s.mu.Lock()
	noReg, fail := s.oauth.noRegistration, s.oauth.failRegister
	s.mu.Unlock()
	invalid := func(desc string) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata", "error_description": desc})
	}
	switch {
	case r.Method != http.MethodPost:
		writeError(w, Permission("Not permitted"))
		return
	case noReg:
		writeError(w, NotFound("Not Found"))
		return
	case fail == http.StatusTooManyRequests:
		writeError(w, &Error{http.StatusTooManyRequests, "RateLimitExceededError",
			"You hit the rate limit because of too many requests. Please try after sometime."})
		return
	case fail != 0:
		writeJSON(w, fail, map[string]string{"error": "invalid_client_metadata", "error_description": "refused by the test"})
		return
	}
	var md struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              *string  `json:"client_name"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		Scope                   string   `json:"scope"`
	}
	var raw map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if len(body) == 0 || dec.Decode(&raw) != nil {
		invalid("Request body is empty")
		return
	}
	if json.Unmarshal(body, &md) != nil || md.RedirectURIs == nil || md.ClientName == nil {
		invalid("validation error: redirect_uris and client_name are required")
		return
	}
	var reasons []string
	if len(md.RedirectURIs) == 0 {
		reasons = append(reasons, "redirect_uris is required")
	}
	for _, g := range md.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			reasons = append(reasons, "only 'authorization_code' and 'refresh_token' grant types are supported")
			break
		}
	}
	for _, rt := range md.ResponseTypes {
		if rt != "code" {
			reasons = append(reasons, "only 'code' response_type is supported")
			break
		}
	}
	for _, u := range md.RedirectURIs {
		if !secureRedirect(u) {
			reasons = append(reasons, "redirect_uris must be https")
			break
		}
	}
	if len(reasons) > 0 {
		invalid(strings.Join(reasons, ",\n"))
		return
	}
	scope := md.Scope
	if scope == "" {
		scope = "all"
	}
	s.mu.Lock()
	id := fmt.Sprintf("registered-client-%d", len(s.oauth.registrations)+1)
	s.oauth.clients[id] = append([]string(nil), md.RedirectURIs...)
	s.oauth.registrations = append(s.oauth.registrations, Registration{ClientID: id, Metadata: raw})
	s.mu.Unlock()
	resp := map[string]interface{}{
		"client_id": id, "client_id_issued_at": 1767258000, "client_secret_expires_at": 0,
		"client_name": *md.ClientName, "grant_types": []string{"authorization_code"},
		"response_types": []string{"code"}, "scope": scope, "redirect_uris": md.RedirectURIs,
	}
	if md.TokenEndpointAuthMethod != "none" {
		resp["client_secret"] = "registered-secret"
	}
	writeJSON(w, http.StatusCreated, resp)
}

// secureRedirect mirrors _is_secure_redirect_uri (integrations/utils.py:228)
// outside developer mode: https, or http to a loopback IP literal.
func secureRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		ip := net.ParseIP(strings.Trim(u.Hostname(), "[]"))
		return ip != nil && ip.IsLoopback()
	}
	return false
}

// revokeToken answers revoke_token like Frappe (oauth2.py:188 with oauthlib's
// RevocationEndpoint and OAuthWebRequestValidator, oauth.py:93-127,263):
// POST only, a missing token is 400, the client comes from client_id (an
// unknown one is a DoesNotExistError, 404) or, without one, from the access
// token named by token; a refresh token revokes its record (the access
// token too), anything else is taken as an access token, and an unknown
// token is still a 200. The body is always {}.
func (s *Site) revokeToken(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method != http.MethodPost {
		writeError(w, Permission("Not permitted"))
		return
	}
	args := parseArgs(r, body)
	token, _ := args["token"].(string)
	clientID, _ := args["client_id"].(string)
	hint, _ := args["token_type_hint"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{})
		return
	}
	if clientID != "" {
		if _, ok := s.oauth.clients[clientID]; !ok {
			writeError(w, NotFound("OAuth Client "+clientID+" not found"))
			return
		}
	} else if _, ok := s.oauth.tokens[token]; !ok {
		writeError(w, NotFound("OAuth Client None not found"))
		return
	}
	if hint == "refresh_token" {
		if access, ok := s.oauth.refresh[token]; ok {
			delete(s.oauth.refresh, token)
			if b := s.oauth.tokens[access]; b != nil {
				b.expired = true
			}
			s.oauth.revoked = append(s.oauth.revoked, token)
		}
	} else if b := s.oauth.tokens[token]; b != nil {
		b.expired = true
		for rt, access := range s.oauth.refresh {
			if access == token {
				delete(s.oauth.refresh, rt)
			}
		}
		s.oauth.revoked = append(s.oauth.revoked, token)
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
