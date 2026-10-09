package sitesetup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/loopback"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// ─── PKCE ────────────────────────────────────────────────────────────────────

func generateCodeVerifier() (string, error) { return loopback.NewVerifier() }

func generateCodeChallenge(verifier string) string { return loopback.Challenge(verifier) }

// ─── Local callback server ───────────────────────────────────────────────────

// callbackHost is a loopback IP literal, not "localhost": a browser that
// resolves localhost to ::1 first could hand the code to another process
// listening on [::1] (RFC 8252 §8.3).
const callbackHost = loopback.HostIP

// defaultCallbackTimeout is OAuthFlow.Timeout unless the caller changes it.
const defaultCallbackTimeout = 5 * time.Minute

// callbackPorts are tried first so the redirect URI stays stable across
// re-auths — Frappe validates redirect_uri against a registered allow-list,
// so a fresh random port each time forces re-registration (L17). An
// ephemeral port is the fallback when all are taken.
var callbackPorts = []int{53682, 53683, 53684}

// callbackServer is the loopback.Server waiting for the OAuth callback: it
// accepts one result, answers duplicates with 409 and ignores a wrong state.
type callbackServer struct {
	port  int
	state string // expected OAuth state parameter (CSRF protection, M6)
	addr  net.Addr
	srv   *loopback.Server

	closeOnce sync.Once
}

// redirectURI is the URI to register on the Frappe OAuth client.
func (cs *callbackServer) redirectURI() string { return cs.srv.RedirectURI() }

// startCallbackServer binds to a free port and starts the HTTP server immediately.
// Call this before showing the redirect URI to the user so the port is guaranteed
// to be held when Frappe redirects back. The caller must call close.
func startCallbackServer() (*callbackServer, error) {
	srv, err := loopback.Start(loopback.Config{Host: callbackHost, Ports: callbackPorts, App: "ffc"})
	if err != nil {
		return nil, err
	}
	return &callbackServer{port: srv.Port(), state: srv.State(), addr: srv.Addr(), srv: srv}, nil
}

// close stops the server. Safe to call more than once.
func (cs *callbackServer) close() {
	cs.closeOnce.Do(cs.srv.Close)
}

// wait blocks until the OAuth callback delivers a code, the context is cancelled
// (Ctrl+C / SIGTERM), or timeout elapses. The server is closed on return.
func (cs *callbackServer) wait(ctx context.Context, timeout time.Duration) (string, error) {
	defer cs.close()
	return cs.srv.Wait(ctx, timeout)
}

// ─── Login ───────────────────────────────────────────────────────────────────

// OAuthFlow is one OAuth Authorization Code + PKCE login: a local callback
// server on 127.0.0.1, bound by StartOAuthFlow so its redirect URI is
// known before an OAuth client is chosen or registered (ResolveOAuthApp).
// Close it when done; Login closes it too.
//
// A flow is good for one Login: the server accepts one callback and is
// closed when Login returns. A second Login, or a Login after Close,
// returns ErrFlowUsed at once; to retry (say, after the browser could not
// be opened), start a new flow.
type OAuthFlow struct {
	// Timeout bounds how long Login waits for the browser redirect
	// (5 minutes unless changed before Login).
	Timeout time.Duration

	cs *callbackServer

	mu   sync.Mutex
	used bool // Login started or Close called
}

// ErrFlowUsed is Login's answer on an OAuthFlow that already ran a login or
// was closed.
var ErrFlowUsed = errors.New("OAuth flow already used; start a new one")

// StartOAuthFlow binds the callback server and starts serving.
func StartOAuthFlow() (*OAuthFlow, error) {
	cs, err := startCallbackServer()
	if err != nil {
		return nil, err
	}
	return &OAuthFlow{Timeout: defaultCallbackTimeout, cs: cs}, nil
}

// RedirectURI is the URI to register on the Frappe OAuth client:
// http://127.0.0.1:<port>/callback.
func (f *OAuthFlow) RedirectURI() string { return f.cs.redirectURI() }

// Close stops the callback server. Safe to call more than once; the flow
// cannot be used for a login afterwards.
func (f *OAuthFlow) Close() {
	f.take()
	f.cs.close()
}

// take marks the flow used and reports whether it was still unused.
func (f *OAuthFlow) take() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	fresh := !f.used
	f.used = true
	return fresh
}

// LoginHooks are the points where Login hands over to its caller.
type LoginHooks struct {
	// OpenBrowser (required) gets the authorization URL, to open in a
	// browser (and show, in case that fails). Login waits for the redirect
	// once it returns nil; an error ends the login with that error.
	OpenBrowser func(authURL string) error
	// Step, when set, runs each network call after the redirect (the code
	// exchange, then the user lookup); title says what it does. An error it
	// returns ends the login with that error. Without it they run directly.
	Step func(title string, run func()) error
}

// Login sends the user to the site's authorization page through
// h.OpenBrowser, waits for the callback (at most f.Timeout), exchanges the
// code (PKCE) and asks who logged in. It returns the site entry for siteURL
// with app and the tokens, and the user ("" when the site does not say;
// stripped of terminal controls, the site chose it). A cancelled ctx
// returns ctx.Err(). The callback server is closed on return, so a flow
// logs in once (see OAuthFlow). A nil h.OpenBrowser is an error and leaves
// the flow unused.
func (f *OAuthFlow) Login(ctx context.Context, siteURL string, app OAuthApp, h LoginHooks) (config.SiteConfig, string, error) {
	if h.OpenBrowser == nil {
		return config.SiteConfig{}, "", errors.New("sitesetup: LoginHooks.OpenBrowser is nil")
	}
	if !f.take() {
		return config.SiteConfig{}, "", ErrFlowUsed
	}
	defer f.Close()
	step := h.Step
	if step == nil {
		step = func(_ string, run func()) error { run(); return nil }
	}
	verifier, err := generateCodeVerifier()
	if err != nil {
		return config.SiteConfig{}, "", fmt.Errorf("generating PKCE verifier: %w", err)
	}
	cs := f.cs
	redirectURI := cs.redirectURI()
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {app.ID},
		"redirect_uri":          {redirectURI},
		"scope":                 {OAuthScope},
		"code_challenge":        {generateCodeChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {cs.state}, // CSRF protection (M6)
	}
	authURL := siteURL + "/api/method/frappe.integrations.oauth2.authorize?" + params.Encode()

	if err := h.OpenBrowser(authURL); err != nil {
		return config.SiteConfig{}, "", err
	}

	code, err := cs.wait(ctx, f.Timeout)
	if err != nil {
		if ctx.Err() != nil {
			return config.SiteConfig{}, "", ctx.Err()
		}
		return config.SiteConfig{}, "", fmt.Errorf("authorization: %w", err)
	}

	var tokens *client.OAuthTokens
	var exchangeErr error
	if err := step("Exchanging authorization code for tokens...", func() {
		tokens, exchangeErr = client.ExchangeOAuthCode(ctx, siteURL, app.ID, app.Secret, code, redirectURI, verifier)
	}); err != nil {
		return config.SiteConfig{}, "", err
	}
	if ctx.Err() != nil {
		return config.SiteConfig{}, "", ctx.Err()
	}
	if exchangeErr != nil {
		return config.SiteConfig{}, "", fmt.Errorf("token exchange: %w", exchangeErr)
	}

	var user string
	if err := step("Fetching user info...", func() {
		user, _ = client.GetOAuthUser(ctx, siteURL, tokens.AccessToken)
	}); err != nil {
		return config.SiteConfig{}, "", err
	}
	if ctx.Err() != nil {
		return config.SiteConfig{}, "", ctx.Err()
	}
	return oauthSiteConfig(siteURL, app, tokens), text.Sanitize(user), nil
}

// oauthSiteConfig is the site entry for a finished login.
func oauthSiteConfig(siteURL string, app OAuthApp, tokens *client.OAuthTokens) config.SiteConfig {
	return config.SiteConfig{
		URL:               siteURL,
		OAuthClientID:     app.ID,
		OAuthClientSecret: app.Secret,
		AccessToken:       tokens.AccessToken,
		RefreshToken:      tokens.RefreshToken,
		TokenExpiry:       tokens.ExpiresAt,
	}
}
