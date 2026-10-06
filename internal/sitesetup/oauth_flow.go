package sitesetup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// ─── PKCE ────────────────────────────────────────────────────────────────────

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ─── Local callback server ───────────────────────────────────────────────────

// callbackHost is a loopback IP literal, not "localhost": a browser that
// resolves localhost to ::1 first could hand the code to another process
// listening on [::1] (RFC 8252 §8.3).
const callbackHost = "127.0.0.1"

// defaultCallbackTimeout is OAuthFlow.Timeout unless the caller changes it.
const defaultCallbackTimeout = 5 * time.Minute

type callbackResult struct {
	code string
	err  error
}

// callbackServer holds a running local HTTP server waiting for the OAuth callback.
type callbackServer struct {
	port   int
	state  string // expected OAuth state parameter (CSRF protection, M6)
	result chan callbackResult
	srv    *http.Server
	addr   net.Addr

	once      sync.Once
	closeOnce sync.Once
}

// deliver records the first callback outcome. Later callbacks (page reloads,
// duplicate redirects) are dropped instead of blocking their handler forever,
// which used to make Shutdown hang.
func (cs *callbackServer) deliver(r callbackResult) bool {
	first := false
	cs.once.Do(func() {
		first = true
		cs.result <- r
	})
	return first
}

// redirectURI is the URI to register on the Frappe OAuth client.
func (cs *callbackServer) redirectURI() string {
	return fmt.Sprintf("http://%s:%d/callback", callbackHost, cs.port)
}

// startCallbackServer binds to a free port and starts the HTTP server immediately.
// Call this before showing the redirect URI to the user so the port is guaranteed
// to be held when Frappe redirects back. The caller must call close.
func startCallbackServer() (*callbackServer, error) {
	// Prefer a small set of fixed ports so the redirect URI stays stable across
	// re-auths — Frappe validates redirect_uri against a registered allow-list,
	// so a fresh random port each time forces re-registration (L17). Fall back
	// to an ephemeral port if all preferred ports are taken.
	var ln net.Listener
	var err error
	for _, p := range []int{53682, 53683, 53684, 0} {
		ln, err = net.Listen("tcp", net.JoinHostPort(callbackHost, fmt.Sprint(p)))
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("binding callback port: %w", err)
	}

	// Random state tying the auth request to this callback. A forged or
	// cross-origin request carrying the wrong (or no) state is rejected without
	// consuming the result, so it can't hijack or DoS the login (M6).
	state, err := generateCodeVerifier()
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("generating state: %w", err)
	}

	mux := http.NewServeMux()
	cs := &callbackServer{
		port:   ln.Addr().(*net.TCPAddr).Port,
		state:  state,
		result: make(chan callbackResult, 1),
		srv:    &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		addr:   ln.Addr(),
	}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// Verify state first — a mismatch keeps the server listening for the
		// legitimate redirect.
		if q.Get("state") != state {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<html><body style='font-family:sans-serif;padding:2rem'><h2>Invalid request</h2><p>State mismatch; ignoring.</p></body></html>")
			return
		}
		var res callbackResult
		var msg string
		switch {
		case q.Get("error") != "":
			msg = q.Get("error")
			if desc := q.Get("error_description"); desc != "" {
				msg += ": " + desc
			}
			res.err = fmt.Errorf("authorization denied: %s", msg)
		case q.Get("code") == "":
			msg = "No code received."
			res.err = errors.New("no authorization code in callback URL")
		default:
			res.code = q.Get("code")
		}
		if !cs.deliver(res) {
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, "<html><body style='font-family:sans-serif;padding:2rem'><h2>Already handled</h2><p>This login was already processed. You can close this tab.</p></body></html>")
			return
		}
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			// html.EscapeString: never reflect raw query params into HTML (L1).
			fmt.Fprintf(w, "<html><body style='font-family:sans-serif;padding:2rem'><h2>Authorization failed</h2><p>%s</p><p>You can close this tab.</p></body></html>", html.EscapeString(msg))
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<html><body style="font-family:sans-serif;padding:2rem;text-align:center">
<h2>&#10003; Authorization successful</h2>
<p>You can close this tab and return to ffc.</p>
</body></html>`)
	})

	go func() {
		if err := cs.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cs.deliver(callbackResult{err: fmt.Errorf("callback server: %w", err)})
		}
	}()
	return cs, nil
}

// close stops the server, giving in-flight responses a moment to finish.
// Safe to call more than once.
func (cs *callbackServer) close() {
	cs.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := cs.srv.Shutdown(ctx); err != nil {
			cs.srv.Close()
		}
	})
}

// wait blocks until the OAuth callback delivers a code, the context is cancelled
// (Ctrl+C / SIGTERM), or timeout elapses. The server is closed on return.
func (cs *callbackServer) wait(ctx context.Context, timeout time.Duration) (string, error) {
	defer cs.close()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-cs.result:
		return r.code, r.err
	case <-ctx.Done():
		// The CLI installs a SIGINT/SIGTERM-cancelled context, which disables
		// Go's default terminate-on-signal — so this wait must honour it or the
		// user can't abort the browser flow (regression guard).
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("timed out waiting for browser authorization (%s)", timeout)
	}
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
