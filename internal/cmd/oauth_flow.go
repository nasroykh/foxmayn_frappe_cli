package cmd

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
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
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

// ─── Browser ─────────────────────────────────────────────────────────────────

// openBrowserFn opens the authorization URL; tests replace it with a
// function that delivers the callback themselves.
var openBrowserFn = openBrowser

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		// rundll32 takes the URL as a single argument; `cmd /c start "" <url>`
		// would let cmd.exe treat the '&' in the query string as a command
		// separator and truncate the auth URL, breaking login (L20).
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

// ─── Local callback server ───────────────────────────────────────────────────

// callbackHost is a loopback IP literal, not "localhost": a browser that
// resolves localhost to ::1 first could hand the code to another process
// listening on [::1] (RFC 8252 §8.3).
const callbackHost = "127.0.0.1"

// oauthCallbackTimeout bounds how long wait blocks for the browser redirect.
var oauthCallbackTimeout = 5 * time.Minute

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
<p>You can close this tab and return to the terminal.</p>
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
// (Ctrl+C / SIGTERM), or oauthCallbackTimeout elapses. The server is closed
// on return.
func (cs *callbackServer) wait(ctx context.Context) (string, error) {
	defer cs.close()
	timer := time.NewTimer(oauthCallbackTimeout)
	defer timer.Stop()
	select {
	case r := <-cs.result:
		return r.code, r.err
	case <-ctx.Done():
		// The root installs a SIGINT/SIGTERM-cancelled context, which disables
		// Go's default terminate-on-signal — so this wait must honour it or the
		// user can't abort the browser flow (regression guard).
		return "", errAborted
	case <-timer.C:
		return "", fmt.Errorf("timed out waiting for browser authorization (%s)", oauthCallbackTimeout)
	}
}

// ─── OAuth wizard ────────────────────────────────────────────────────────────

// collectOAuthSite runs the OAuth Authorization Code + PKCE flow and returns
// the new site with its tokens. See collectSite for checkName. manual is the
// client given with --client-id; without one, ffc registers a client when
// the site offers dynamic registration and asks for one otherwise.
func collectOAuthSite(ctx context.Context, checkName func(string) error, manual oauthApp) (string, config.SiteConfig, error) {
	var name, rawURL string
	if err := runForm(huh.NewGroup(siteNameInput(&name), siteURLInput(&rawURL))); err != nil {
		return "", config.SiteConfig{}, err
	}
	siteName, siteURL, err := siteNameAndURL(name, rawURL)
	if err != nil {
		return "", config.SiteConfig{}, err
	}
	if checkName != nil {
		if err := checkName(siteName); err != nil {
			return "", config.SiteConfig{}, err
		}
	}

	// Start the callback server before registering or showing the
	// instructions: the redirect URI names its port.
	cs, err := startCallbackServer()
	if err != nil {
		return "", config.SiteConfig{}, err
	}
	defer cs.close()

	app, err := setUpOAuthApp(ctx, siteURL, cs, manual)
	var nr *noRegistrationError
	switch {
	case errors.As(err, &nr):
		if app, err = promptOAuthApp(siteURL, cs.redirectURI(), nr); err != nil {
			return "", config.SiteConfig{}, err
		}
	case err != nil:
		return "", config.SiteConfig{}, err
	}

	tokens, user, err := oauthLogin(ctx, siteURL, cs, app)
	if err != nil {
		return "", config.SiteConfig{}, err
	}

	clientLine := text.Sanitize(app.ID)
	if app.Registered {
		clientLine += " (registered by ffc)"
	}
	if _, err := reviewSite(fmt.Sprintf(
		"Site name:  %s\nSite URL:   %s\nClient ID:  %s\nLogged in:  %s",
		siteName, siteURL, clientLine, orDefault(user, "(unknown)"),
	), false); err != nil {
		return "", config.SiteConfig{}, err
	}
	return siteName, oauthSiteConfig(siteURL, app, tokens), nil
}

// setUpOAuthApp runs resolveOAuthApp under a spinner and reports a
// registration. A *noRegistrationError leaves the fallback to the caller.
func setUpOAuthApp(ctx context.Context, siteURL string, cs *callbackServer, manual oauthApp) (oauthApp, error) {
	var app oauthApp
	var resolveErr error
	if err := runSpinner("Setting up the OAuth client...", func() {
		app, resolveErr = resolveOAuthApp(ctx, siteURL, cs.redirectURI(), manual)
	}); err != nil || ctx.Err() != nil {
		return oauthApp{}, errAborted
	}
	if resolveErr == nil && app.Registered {
		fmt.Fprintf(os.Stderr, "✓ Registered OAuth client %s on %s.\n", text.Sanitize(app.ID), siteURL)
	}
	return app, resolveErr
}

// promptOAuthApp explains why no client was registered and asks for the
// one the user creates by hand.
func promptOAuthApp(siteURL, redirectURI string, why *noRegistrationError) (oauthApp, error) {
	fmt.Fprintf(os.Stderr, `
No OAuth client was registered automatically: %v.
Create one by hand (or run again with --apikey to use an API key instead).

OAuth Client setup (one-time, on your Frappe site)
──────────────────────────────────────────────────
1. Go to: %s/app/oauth-client/new-oauth-client-1
2. Fill in:
     App Name:      ffc (or any name)
     Grant Type:    Authorization Code
     Scopes:        %s
     Redirect URIs: %s
3. Save → copy the Client ID (and Client Secret if using Confidential type).

`, why, siteURL, oauthScope, redirectURI)

	var clientID, clientSecret string
	if err := runForm(huh.NewGroup(
		huh.NewInput().
			Title("OAuth Client ID").
			Description("From the OAuth Client you just created on Frappe").
			Validate(nonEmpty("client ID")).
			Value(&clientID),
		huh.NewInput().
			Title("OAuth Client Secret").
			Description("Leave empty if using a Public client (no secret)").
			EchoMode(huh.EchoModePassword).
			Value(&clientSecret),
	)); err != nil {
		return oauthApp{}, err
	}
	return oauthApp{ID: strings.TrimSpace(clientID), Secret: strings.TrimSpace(clientSecret)}, nil
}

// collectOAuthSiteNoInput is the OAuth flow of the non-interactive setup
// (--oauth with --name and --url): no prompt and no review, but the browser
// login still needs a person. Without --client-id the site must offer
// dynamic client registration.
func collectOAuthSiteNoInput(ctx context.Context, siteURL string, manual oauthApp) (config.SiteConfig, error) {
	cs, err := startCallbackServer()
	if err != nil {
		return config.SiteConfig{}, err
	}
	defer cs.close()

	app, err := setUpOAuthApp(ctx, siteURL, cs, manual)
	if err != nil {
		return config.SiteConfig{}, noInputOAuthError(err)
	}
	tokens, user, err := oauthLogin(ctx, siteURL, cs, app)
	if err != nil {
		return config.SiteConfig{}, err
	}
	if user != "" {
		fmt.Fprintf(os.Stderr, "✓ Logged in as %s.\n", user)
	}
	return oauthSiteConfig(siteURL, app, tokens), nil
}

// oauthLogin sends the user to the site's authorization page, waits for the
// callback on cs and exchanges the code (PKCE). user is "" when the site
// does not say who logged in.
func oauthLogin(ctx context.Context, siteURL string, cs *callbackServer, app oauthApp) (*client.OAuthTokens, string, error) {
	verifier, err := generateCodeVerifier()
	if err != nil {
		return nil, "", fmt.Errorf("generating PKCE verifier: %w", err)
	}
	redirectURI := cs.redirectURI()
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {app.ID},
		"redirect_uri":          {redirectURI},
		"scope":                 {oauthScope},
		"code_challenge":        {generateCodeChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {cs.state}, // CSRF protection (M6)
	}
	authURL := siteURL + "/api/method/frappe.integrations.oauth2.authorize?" + params.Encode()

	fmt.Fprintf(os.Stderr, "\nOpening browser for authorization...\n")
	fmt.Fprintf(os.Stderr, "If the browser doesn't open automatically, visit:\n  %s\n\n", authURL)
	if err := openBrowserFn(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "(Could not open browser: %v)\n\n", err)
	}
	fmt.Fprintf(os.Stderr, "Waiting for authorization (timeout: %s)...\n", oauthCallbackTimeout)

	code, err := cs.wait(ctx)
	if err != nil {
		if errors.Is(err, errAborted) {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("authorization: %w", err)
	}

	var tokens *client.OAuthTokens
	var exchangeErr error
	if err := runSpinner("Exchanging authorization code for tokens...", func() {
		tokens, exchangeErr = client.ExchangeOAuthCode(ctx, siteURL, app.ID, app.Secret, code, redirectURI, verifier)
	}); err != nil || ctx.Err() != nil {
		return nil, "", errAborted
	}
	if exchangeErr != nil {
		return nil, "", fmt.Errorf("token exchange: %w", exchangeErr)
	}

	var user string
	if err := runSpinner("Fetching user info...", func() {
		user, _ = client.GetOAuthUser(ctx, siteURL, tokens.AccessToken)
	}); err != nil || ctx.Err() != nil {
		return nil, "", errAborted
	}
	// The site chose the name: no terminal escapes from it.
	return tokens, text.Sanitize(user), nil
}

// oauthSiteConfig is the site entry for a finished login.
func oauthSiteConfig(siteURL string, app oauthApp, tokens *client.OAuthTokens) config.SiteConfig {
	return config.SiteConfig{
		URL:               siteURL,
		OAuthClientID:     app.ID,
		OAuthClientSecret: app.Secret,
		AccessToken:       tokens.AccessToken,
		RefreshToken:      tokens.RefreshToken,
		TokenExpiry:       tokens.ExpiresAt,
	}
}
