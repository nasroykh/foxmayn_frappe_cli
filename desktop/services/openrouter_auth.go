package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/loopback"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// EventOpenRouterAuth reports the steps of an OpenRouter browser sign-in.
// Its payload never carries the key.
const EventOpenRouterAuth = "auth:openrouter"

func init() {
	application.RegisterEvent[OpenRouterAuth](EventOpenRouterAuth)
}

// OpenRouter sign-in statuses, in order; the last one is "done",
// "cancelled" or "failed".
const (
	AuthBrowser    = "browser"    // waiting for the user in the browser
	AuthExchanging = "exchanging" // trading the code for a key
	AuthVerifying  = "verifying"  // checking the key, as SetKey does
	AuthDone       = "done"
	AuthCancelled  = "cancelled"
	AuthFailed     = "failed"
)

// OpenRouterAuth is the payload of EventOpenRouterAuth.
type OpenRouterAuth struct {
	ProviderID string `json:"providerID"`
	Status     string `json:"status"`
	// AuthURL is OpenRouter's sign-in page (status "browser"), so the UI can
	// offer to copy it when the browser did not open. It carries no secret:
	// the PKCE verifier stays in Go.
	AuthURL string `json:"authURL,omitempty"`
	// BrowserError is set when the browser could not be opened.
	BrowserError string `json:"browserError,omitempty"`
}

const (
	// openRouterAuthURL is the page that asks the user to create a key.
	openRouterAuthURL = "https://openrouter.ai/auth"
	// openRouterKeysURL trades the code (and the PKCE verifier) for the key.
	openRouterKeysURL = "https://openrouter.ai/api/v1/auth/keys"
	// openRouterKeyLabel names the key on the user's OpenRouter keys page.
	openRouterKeyLabel = "Foxmayn Frappe Desktop"
	// openRouterAuthTimeout bounds the wait for the browser.
	openRouterAuthTimeout = 5 * time.Minute
	// maxExchangeBytes bounds the exchange answer.
	maxExchangeBytes = 64 << 10
)

// orAuth is the OpenRouter sign-in in progress (at most one) and the
// addresses it uses; tests point those at fakes ("" and 0 mean the real
// ones).
type orAuth struct {
	mu  sync.Mutex
	cur *orAttempt

	authURL string
	keysURL string
	timeout time.Duration
}

type orAttempt struct {
	cancel context.CancelFunc
	// done is closed once the attempt has returned.
	done chan struct{}
}

func (o *orAuth) urls() (auth, keys string, timeout time.Duration) {
	auth, keys, timeout = openRouterAuthURL, openRouterKeysURL, openRouterAuthTimeout
	if o.authURL != "" {
		auth = o.authURL
	}
	if o.keysURL != "" {
		keys = o.keysURL
	}
	if o.timeout > 0 {
		timeout = o.timeout
	}
	return auth, keys, timeout
}

// SignInOpenRouter gets an OpenRouter key through the browser (OAuth PKCE):
// it opens OpenRouter's sign-in page, waits for the redirect to a local
// callback (at most 5 minutes), trades the code for a key, checks the key
// like SetKey and saves it in the OS keychain. The key goes nowhere else:
// the result and the "auth:openrouter" events carry the status only.
// CancelSignIn, or cancelling the call, stops it; a new call replaces a
// sign-in in progress. The provider must exist and be of kind openrouter.
func (a *AssistantService) SignInOpenRouter(ctx context.Context, providerID string) (ProviderInfo, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return ProviderInfo{}, err
	}
	defer done()
	if err := validProviderID(providerID); err != nil {
		return ProviderInfo{}, err
	}
	p, err := a.provider(st, providerID)
	if err != nil {
		return ProviderInfo{}, err
	}
	if p.Kind != KindOpenRouter {
		return ProviderInfo{}, invalid("provider", "Browser sign-in works with OpenRouter only.")
	}

	// The attempt ends with the call, with CancelSignIn, or when the
	// service shuts down.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.mu.Lock()
	life := a.ctx
	a.mu.Unlock()
	if life != nil {
		stop := context.AfterFunc(life, cancel)
		defer stop()
	}
	at := &orAttempt{cancel: cancel, done: make(chan struct{})}
	a.or.mu.Lock()
	old := a.or.cur
	if old != nil {
		old.cancel()
	}
	a.or.cur = at
	a.or.mu.Unlock()
	defer func() {
		a.or.mu.Lock()
		if a.or.cur == at {
			a.or.cur = nil
		}
		a.or.mu.Unlock()
		close(at.done)
	}()
	if old != nil {
		// Let the old attempt send its last event first.
		select {
		case <-old.done:
		case <-time.After(replaceWait):
		case <-ctx.Done():
		}
	}

	emit := func(ev OpenRouterAuth) {
		ev.ProviderID = providerID
		a.host.Emit(EventOpenRouterAuth, ev)
	}
	info, err := a.signInOpenRouter(ctx, providerID, emit)
	if err != nil {
		status := AuthFailed
		var se *Error
		if errors.As(err, &se) && se.Code == CodeCancelled {
			status = AuthCancelled
		}
		emit(OpenRouterAuth{Status: status})
		return ProviderInfo{}, err
	}
	emit(OpenRouterAuth{Status: AuthDone})
	return info, nil
}

func (a *AssistantService) signInOpenRouter(ctx context.Context, providerID string, emit func(OpenRouterAuth)) (ProviderInfo, error) {
	cancelled := func() error { return newError(CodeCancelled, "The sign-in was cancelled.", nil) }
	authBase, keysURL, timeout := a.or.urls()

	verifier, err := loopback.NewVerifier()
	if err != nil {
		return ProviderInfo{}, newError(CodeFailed, "The sign-in could not start on this computer.", err)
	}
	// OpenRouter documents localhost callbacks on any port, not 127.0.0.1:
	// the server holds the port on both loopback addresses.
	srv, err := loopback.Start(loopback.Config{Host: loopback.HostLocalhost, App: openRouterKeyLabel})
	if err != nil {
		return ProviderInfo{}, newError(CodeFailed, "The sign-in could not start on this computer.", err)
	}
	defer srv.Close()

	q := url.Values{
		"callback_url":          {srv.RedirectURI()},
		"code_challenge":        {loopback.Challenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {srv.State()},
		"key_label":             {openRouterKeyLabel},
	}
	authURL := authBase + "?" + q.Encode()
	ev := OpenRouterAuth{Status: AuthBrowser, AuthURL: authURL}
	if err := a.host.OpenURL(authURL); err != nil {
		ev.BrowserError = text.Sanitize(err.Error())
	}
	emit(ev)

	code, err := srv.Wait(ctx, timeout)
	switch {
	case ctx.Err() != nil:
		return ProviderInfo{}, cancelled()
	case err != nil && strings.Contains(err.Error(), "timed out"):
		return ProviderInfo{}, newError(CodeFailed, fmt.Sprintf("The sign-in took too long. Try again, and finish it in your browser within %d minutes.", int(timeout.Minutes())), err)
	case err != nil && strings.Contains(err.Error(), "authorization denied"):
		return ProviderInfo{}, newError(CodeAuth, "OpenRouter did not give the app a key.", err)
	case err != nil:
		return ProviderInfo{}, newError(CodeFailed, "The sign-in did not finish.", err)
	}

	emit(OpenRouterAuth{Status: AuthExchanging})
	key, err := exchangeOpenRouterCode(ctx, keysURL, code, verifier)
	if err != nil {
		if ctx.Err() != nil {
			return ProviderInfo{}, cancelled()
		}
		return ProviderInfo{}, err
	}

	emit(OpenRouterAuth{Status: AuthVerifying})
	defer a.lockProvider(providerID)()
	_, st, err := a.ready()
	if err != nil {
		return ProviderInfo{}, err
	}
	// Read again: the provider may have been changed or removed while the
	// browser was open.
	p, err := a.provider(st, providerID)
	if err != nil {
		return ProviderInfo{}, err
	}
	if p.Kind != KindOpenRouter {
		return ProviderInfo{}, invalid("provider", "Browser sign-in works with OpenRouter only.")
	}
	vctx, vcancel := context.WithTimeout(ctx, providerCallTimeout)
	defer vcancel()
	if err := a.verifyAndSaveKey(vctx, p, key); err != nil {
		if ctx.Err() != nil {
			return ProviderInfo{}, cancelled()
		}
		return ProviderInfo{}, err
	}
	return a.info(p), nil
}

// exchangeOpenRouterCode trades the authorization code for a key. Neither
// the answer's body nor the key reach an error: only the HTTP status does.
func exchangeOpenRouterCode(ctx context.Context, keysURL, code, verifier string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, providerCallTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]string{
		"code":                  code,
		"code_verifier":         verifier,
		"code_challenge_method": "S256",
	})
	if err != nil {
		return "", newError(CodeFailed, "The sign-in did not finish.", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, keysURL, bytes.NewReader(body))
	if err != nil {
		return "", newError(CodeFailed, "The sign-in did not finish.", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	hc := &http.Client{
		// A redirected POST would be replayed as a GET elsewhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", newError(CodeNetwork, "OpenRouter did not answer in time.", nil)
		}
		return "", newError(CodeNetwork, "OpenRouter could not be reached. Check your internet connection.", err)
	}
	defer resp.Body.Close()
	status := fmt.Errorf("OpenRouter answered HTTP %d", resp.StatusCode)
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return "", newError(CodeAuth, "OpenRouter did not accept the sign-in. Try again.", status)
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated:
		return "", newError(CodeNetwork, "OpenRouter could not finish the sign-in. Try again.", status)
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxExchangeBytes)).Decode(&out); err != nil || strings.TrimSpace(out.Key) == "" {
		return "", newError(CodeFailed, "OpenRouter's answer had no key.", nil)
	}
	return strings.TrimSpace(out.Key), nil
}

// CancelSignIn stops the OpenRouter sign-in in progress, if any.
func (a *AssistantService) CancelSignIn() {
	a.or.mu.Lock()
	defer a.or.mu.Unlock()
	if a.or.cur != nil {
		a.or.cur.cancel()
	}
}
