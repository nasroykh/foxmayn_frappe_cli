package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/loopback"
)

const (
	orCode   = "or-code-123"
	orBadKey = "sk-or-v1-refusedrefusedREFUSED"
)

// fakeOpenRouter is OpenRouter's /auth page and key exchange. The /auth
// page redirects to callback_url with the code and the state, as the real
// one does after the user creates the key.
type fakeOpenRouter struct {
	srv *httptest.Server

	mu        sync.Mutex
	challenge string
	authQuery url.Values
	exchanges []map[string]string
	key       string // the key the exchange hands out
	status    int    // the exchange's status (200 when 0)
}

func newFakeOpenRouter(t *testing.T) *fakeOpenRouter {
	t.Helper()
	f := &fakeOpenRouter{key: goodKey}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		f.authQuery, f.challenge = q, q.Get("code_challenge")
		f.mu.Unlock()
		cb, err := url.Parse(q.Get("callback_url"))
		if err != nil {
			http.Error(w, "bad callback", http.StatusBadRequest)
			return
		}
		cq := url.Values{"code": {orCode}, "state": {q.Get("state")}}
		cb.RawQuery = cq.Encode()
		http.Redirect(w, r, cb.String(), http.StatusFound)
	})
	mux.HandleFunc("POST /api/v1/auth/keys", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.exchanges = append(f.exchanges, body)
		ch, key, status := f.challenge, f.key, f.status
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid code or code_verifier","key":"` + key + `"}}`))
			return
		}
		if body["code"] != orCode || body["code_challenge_method"] != "S256" || loopback.Challenge(body["code_verifier"]) != ch {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"key": key, "user_id": "u1"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// orRig is the assistant rig with an "openrouter" provider and the fake
// wired in. browse runs as the user's browser.
func orRig(t *testing.T, browse func(authURL string) error) (*assistantRig, *fakeOpenRouter) {
	t.Helper()
	g := newAssistantRig(t)
	f := newFakeOpenRouter(t)
	g.a.or.authURL = f.srv.URL + "/auth"
	g.a.or.keysURL = f.srv.URL + "/api/v1/auth/keys"
	if _, err := g.a.SaveProvider(ProviderInfo{Kind: KindOpenRouter}); err != nil {
		t.Fatal(err)
	}
	g.h.openURL = browse
	return g, f
}

// browser follows the auth page's redirect to the callback, as a browser
// does (in the background: OpenURL returns at once).
func browser(t *testing.T) func(string) error {
	return func(u string) error {
		go func() {
			resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(u)
			if err != nil {
				t.Errorf("browser: %v", err)
				return
			}
			resp.Body.Close()
		}()
		return nil
	}
}

func orStatuses(g *assistantRig) []string {
	var out []string
	for _, e := range g.h.named(EventOpenRouterAuth) {
		out = append(out, e.(OpenRouterAuth).Status)
	}
	return out
}

func storedKey(g *assistantRig) string {
	k, _ := g.keys.Get("openrouter")
	return k
}

// assertNoKey checks that no event carries a secret.
func assertNoKey(t *testing.T, g *assistantRig, secrets ...string) {
	t.Helper()
	g.h.mu.Lock()
	defer g.h.mu.Unlock()
	for _, e := range g.h.events {
		b := mustJSON(t, e.data)
		for _, s := range secrets {
			if strings.Contains(string(b), s) {
				t.Fatalf("secret in event %s: %s", e.name, b)
			}
		}
	}
}

func TestOpenRouterSignIn(t *testing.T) {
	g, f := orRig(t, browser(t))
	info, err := g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if !info.KeySet || info.KeyLast4 != goodKey[len(goodKey)-4:] || info.Kind != KindOpenRouter {
		t.Errorf("info = %+v", info)
	}
	if storedKey(g) != goodKey {
		t.Errorf("keychain has %q", storedKey(g))
	}
	// The provider stays pinned to OpenRouter's host.
	list, _ := g.a.ListProviders()
	if len(list) != 1 || list[0].BaseURL != officialBase(KindOpenRouter) {
		t.Errorf("providers = %+v", list)
	}

	q := f.authQuery
	cb, _ := url.Parse(q.Get("callback_url"))
	if cb.Scheme != "http" || cb.Hostname() != "localhost" || cb.Path != "/callback" || cb.Port() == "" {
		t.Errorf("callback_url = %q", q.Get("callback_url"))
	}
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || len(q.Get("state")) != 43 ||
		q.Get("key_label") != "Foxmayn Frappe Desktop" {
		t.Errorf("auth query = %v", q)
	}
	if len(f.exchanges) != 1 {
		t.Fatalf("%d exchanges", len(f.exchanges))
	}

	want := []string{AuthBrowser, AuthExchanging, AuthVerifying, AuthDone}
	if got := orStatuses(g); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("statuses %v, want %v", got, want)
	}
	first := g.h.named(EventOpenRouterAuth)[0].(OpenRouterAuth)
	if first.ProviderID != "openrouter" || !strings.HasPrefix(first.AuthURL, f.srv.URL+"/auth?") || first.BrowserError != "" {
		t.Errorf("browser event = %+v", first)
	}
	assertNoKey(t, g, goodKey, f.exchanges[0]["code_verifier"])
	ks, err := g.a.KeyStatus("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{mustJSONString(t, info), mustJSONString(t, list), mustJSONString(t, ks)} {
		if strings.Contains(b, goodKey) {
			t.Errorf("key in %s", b)
		}
	}
	for _, suffix := range []string{"", "-wal"} {
		if raw, err := os.ReadFile(g.dbPath + suffix); err == nil && strings.Contains(string(raw), goodKey) {
			t.Errorf("key in the store file %s", g.dbPath+suffix)
		}
	}

	// The callback server is closed: the redirect URI no longer answers.
	if _, err := (&http.Client{Timeout: time.Second}).Get(q.Get("callback_url")); err == nil {
		t.Error("callback server still running")
	}
}

// A redirect with the wrong state is refused and does not end the sign-in:
// the real one that follows still works.
func TestOpenRouterSignInIgnoresBadState(t *testing.T) {
	g, f := orRig(t, nil)
	g.h.openURL = func(u string) error {
		go func() {
			c := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := c.Get(u)
			if err != nil {
				t.Errorf("auth page: %v", err)
				return
			}
			resp.Body.Close()
			good := resp.Header.Get("Location")
			gu, _ := url.Parse(good)
			bad := *gu
			bq := gu.Query()
			bq.Set("state", "forged")
			bq.Set("code", "evil")
			bad.RawQuery = bq.Encode()
			for _, u := range []string{bad.String(), gu.Scheme + "://" + gu.Host + gu.Path + "?code=evil"} {
				r, err := c.Get(u)
				if err != nil {
					t.Errorf("forged: %v", err)
					return
				}
				r.Body.Close()
				if r.StatusCode != http.StatusBadRequest {
					t.Errorf("forged callback: %d", r.StatusCode)
				}
			}
			r, err := c.Get(good)
			if err != nil {
				t.Errorf("good: %v", err)
				return
			}
			r.Body.Close()
		}()
		return nil
	}
	if _, err := g.a.SignInOpenRouter(t.Context(), "openrouter", "a1"); err != nil {
		t.Fatal(err)
	}
	if storedKey(g) != goodKey || len(f.exchanges) != 1 || f.exchanges[0]["code"] != orCode {
		t.Errorf("key %q, exchanges %v", storedKey(g), f.exchanges)
	}
}

func TestOpenRouterSignInTimeout(t *testing.T) {
	g, f := orRig(t, func(string) error { return nil }) // the user never finishes
	g.a.or.timeout = 50 * time.Millisecond
	_, err := g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	if errorCode(t, err) != CodeFailed || !strings.Contains(err.Error(), "took too long") {
		t.Errorf("err = %v", err)
	}
	if storedKey(g) != "" || len(f.exchanges) != 0 {
		t.Error("a key was fetched or saved")
	}
	if got := orStatuses(g); strings.Join(got, ",") != AuthBrowser+","+AuthFailed {
		t.Errorf("statuses %v", got)
	}
}

func TestOpenRouterSignInCancel(t *testing.T) {
	g, f := orRig(t, nil)
	g.h.openURL = func(string) error {
		go g.a.CancelSignIn()
		return nil
	}
	_, err := g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	if errorCode(t, err) != CodeCancelled {
		t.Errorf("err = %v", err)
	}
	if storedKey(g) != "" || len(f.exchanges) != 0 {
		t.Error("a key was fetched or saved")
	}
	if got := orStatuses(g); strings.Join(got, ",") != AuthBrowser+","+AuthCancelled {
		t.Errorf("statuses %v", got)
	}
	// Cancelling the call's context stops it too.
	g.h.openURL = func(string) error { return nil }
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.a.SignInOpenRouter(ctx, "openrouter", "a1"); errorCode(t, err) != CodeCancelled {
		t.Errorf("ctx err = %v", err)
	}
	g.a.CancelSignIn() // nothing in progress: a no-op
}

// A key the provider refuses is not saved, and neither the error nor an
// event shows it; nor does an exchange error body that echoes a key.
func TestOpenRouterSignInRefusedKey(t *testing.T) {
	g, f := orRig(t, browser(t))
	f.key = orBadKey
	_, err := g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	if errorCode(t, err) != CodeAuth || strings.Contains(err.Error(), orBadKey) {
		t.Errorf("err = %v", err)
	}
	if storedKey(g) != "" {
		t.Error("refused key saved")
	}
	assertNoKey(t, g, orBadKey)

	f.status = http.StatusForbidden
	_, err = g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	if errorCode(t, err) != CodeAuth || strings.Contains(mustJSONString(t, err), orBadKey) {
		t.Errorf("exchange 403 err = %v", err)
	}
	f.status = http.StatusInternalServerError
	_, err = g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	if errorCode(t, err) != CodeNetwork || strings.Contains(mustJSONString(t, err), orBadKey) {
		t.Errorf("exchange 500 err = %v", err)
	}
	if storedKey(g) != "" {
		t.Error("key saved")
	}
	assertNoKey(t, g, orBadKey)
}

func mustJSONString(t *testing.T, v any) string { return string(mustJSON(t, v)) }

// The user declines on OpenRouter's page (the redirect carries an error and
// the state).
func TestOpenRouterSignInDenied(t *testing.T) {
	g, _ := orRig(t, nil)
	g.h.openURL = func(u string) error {
		au, _ := url.Parse(u)
		cb := au.Query().Get("callback_url") + "?" + url.Values{"error": {"access_denied"}, "state": {au.Query().Get("state")}}.Encode()
		go func() {
			if r, err := http.Get(cb); err == nil {
				r.Body.Close()
			}
		}()
		return nil
	}
	_, err := g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	if errorCode(t, err) != CodeAuth {
		t.Errorf("err = %v", err)
	}
}

func TestOpenRouterSignInRefusesOtherProviders(t *testing.T) {
	g, _ := orRig(t, browser(t))
	g.provider(t) // "p1", a custom provider
	if _, err := g.a.SignInOpenRouter(t.Context(), "p1", "a1"); errorCode(t, err) != CodeInvalid {
		t.Errorf("custom provider: %v", err)
	}
	if _, err := g.a.SignInOpenRouter(t.Context(), "nope", "a1"); errorCode(t, err) != CodeNotFound {
		t.Errorf("missing provider: %v", err)
	}
	if len(g.h.opened) != 0 {
		t.Errorf("browser opened: %v", g.h.opened)
	}
}

// When the browser cannot be opened, the event says so and carries the
// page to open by hand.
func TestOpenRouterSignInBrowserError(t *testing.T) {
	g, _ := orRig(t, nil)
	g.a.or.timeout = 50 * time.Millisecond
	g.h.openURL = func(string) error { return errBrowser }
	_, _ = g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	ev := g.h.named(EventOpenRouterAuth)[0].(OpenRouterAuth)
	if ev.Status != AuthBrowser || ev.BrowserError == "" || ev.AuthURL == "" {
		t.Errorf("event = %+v", ev)
	}
}

var errBrowser = &Error{Code: CodeFailed, Message: "no browser"}
