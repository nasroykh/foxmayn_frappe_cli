package services

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

// gatedProvider holds the key check (Models) until release is closed, so a
// test can act after the exchange and before the save.
type gatedProvider struct {
	llm.Provider
	entered chan struct{}
	once    *sync.Once
	release chan struct{}
}

func (p gatedProvider) Models(ctx context.Context) ([]llm.Model, error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return p.Provider.Models(ctx)
}

// gate makes the rig's key check wait; it returns "the check started" and
// the release.
func gate(g *assistantRig) (entered, release chan struct{}) {
	entered, release = make(chan struct{}), make(chan struct{})
	once := &sync.Once{}
	g.a.mk = func(_ store.Provider, key string) (llm.Provider, error) {
		return gatedProvider{keyedProvider{g.prov, key, goodKey}, entered, once, release}, nil
	}
	return entered, release
}

type signInResult struct {
	info ProviderInfo
	err  error
}

func signInAsync(g *assistantRig, ctx context.Context, attempt string) chan signInResult {
	ch := make(chan signInResult, 1)
	go func() {
		info, err := g.a.SignInOpenRouter(ctx, "openrouter", attempt)
		ch <- signInResult{info, err}
	}()
	return ch
}

func recv(t *testing.T, ch chan signInResult) signInResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("sign-in did not return")
		return signInResult{}
	}
}

// statusesOf lists the statuses of one attempt.
func statusesOf(g *assistantRig, attempt string) []string {
	var out []string
	for _, e := range g.h.named(EventOpenRouterAuth) {
		if ev := e.(OpenRouterAuth); ev.Attempt == attempt {
			out = append(out, ev.Status)
		}
	}
	return out
}

func joined(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

func opened(g *assistantRig) int {
	g.h.mu.Lock()
	defer g.h.mu.Unlock()
	return len(g.h.opened)
}

// Cancelled before it starts: nothing is bound, opened or saved.
func TestOpenRouterSignInCancelledBeforeBrowser(t *testing.T) {
	g, f := orRig(t, browser(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := g.a.SignInOpenRouter(ctx, "openrouter", "a1")
	if errorCode(t, err) != CodeCancelled {
		t.Errorf("err = %v", err)
	}
	if opened(g) != 0 || len(f.exchanges) != 0 || storedKey(g) != "" {
		t.Errorf("opened %d, exchanges %d, key %q", opened(g), len(f.exchanges), storedKey(g))
	}
	if got := joined(statusesOf(g, "a1")); got != AuthCancelled {
		t.Errorf("statuses %s", got)
	}
}

// After the exchange the key exists on the account: a cancel no longer
// stops the save, and the sign-in ends done.
func TestOpenRouterSignInCancelAfterExchangeStillSaves(t *testing.T) {
	g, _ := orRig(t, browser(t))
	entered, release := gate(g)
	ctx, cancel := context.WithCancel(t.Context())
	ch := signInAsync(g, ctx, "a1")
	<-entered
	g.a.CancelSignIn()
	cancel()
	close(release)
	r := recv(t, ch)
	if r.err != nil || !r.info.KeySet {
		t.Fatalf("result %+v, %v", r.info, r.err)
	}
	if storedKey(g) != goodKey {
		t.Errorf("key %q", storedKey(g))
	}
	want := joined([]string{AuthBrowser, AuthExchanging, AuthVerifying, AuthDone})
	if got := joined(statusesOf(g, "a1")); got != want {
		t.Errorf("statuses %s, want %s", got, want)
	}
	assertNoKey(t, g, goodKey)
}

// A second sign-in replaces one waiting for the browser: the first ends
// cancelled (its last event before the second's browser step), the second
// signs in.
func TestOpenRouterSignInReplace(t *testing.T) {
	g, f := orRig(t, nil)
	var mu sync.Mutex
	calls := 0
	follow := browser(t)
	g.h.openURL = func(u string) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return nil // the user never finishes the first one
		}
		return follow(u)
	}
	first := signInAsync(g, t.Context(), "a1")
	waitFor(t, func() bool { return len(statusesOf(g, "a1")) == 1 })
	info, err := g.a.SignInOpenRouter(t.Context(), "openrouter", "a2")
	if err != nil || !info.KeySet {
		t.Fatalf("second = %+v, %v", info, err)
	}
	if r := recv(t, first); errorCode(t, r.err) != CodeCancelled {
		t.Errorf("first err = %v", r.err)
	}
	if got := joined(statusesOf(g, "a1")); got != AuthBrowser+","+AuthCancelled {
		t.Errorf("first statuses %s", got)
	}
	var order []string
	for _, e := range g.h.named(EventOpenRouterAuth) {
		ev := e.(OpenRouterAuth)
		order = append(order, ev.Attempt+":"+ev.Status)
	}
	if len(order) < 3 || order[1] != "a1:cancelled" || order[2] != "a2:browser" {
		t.Errorf("event order %v", order)
	}
	if storedKey(g) != goodKey || len(f.exchanges) != 1 {
		t.Errorf("key %q, exchanges %d", storedKey(g), len(f.exchanges))
	}
}

// CancelSignIn while a new sign-in waits for the old one to let go: the new
// one ends without binding or opening anything; the old one, past its
// exchange, still saves.
func TestOpenRouterCancelDuringReplaceWait(t *testing.T) {
	g, f := orRig(t, browser(t))
	entered, release := gate(g)
	first := signInAsync(g, t.Context(), "a1")
	<-entered
	g.a.or.mu.Lock()
	a1 := g.a.or.cur
	g.a.or.mu.Unlock()

	second := signInAsync(g, t.Context(), "a2")
	waitFor(t, func() bool {
		g.a.or.mu.Lock()
		defer g.a.or.mu.Unlock()
		return g.a.or.cur != nil && g.a.or.cur != a1
	})
	g.a.CancelSignIn()
	if r := recv(t, second); errorCode(t, r.err) != CodeCancelled {
		t.Errorf("second err = %v", r.err)
	}
	if got := joined(statusesOf(g, "a2")); got != AuthCancelled {
		t.Errorf("second statuses %s", got)
	}
	if opened(g) != 1 || len(f.exchanges) != 1 {
		t.Errorf("opened %d, exchanges %d", opened(g), len(f.exchanges))
	}

	close(release)
	if r := recv(t, first); r.err != nil {
		t.Errorf("first err = %v", r.err)
	}
	if storedKey(g) != goodKey {
		t.Errorf("key %q", storedKey(g))
	}
}

// Shutdown while the browser is open stops the sign-in; nothing is saved.
func TestOpenRouterSignInShutdown(t *testing.T) {
	g, f := orRig(t, func(string) error { return nil })
	ch := signInAsync(g, t.Context(), "a1")
	waitFor(t, func() bool { return len(statusesOf(g, "a1")) == 1 })
	if err := g.a.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if r := recv(t, ch); errorCode(t, r.err) != CodeCancelled {
		t.Errorf("err = %v", r.err)
	}
	if storedKey(g) != "" || len(f.exchanges) != 0 {
		t.Error("a key was fetched or saved")
	}
}

// Shutdown after the exchange waits for the save.
func TestOpenRouterSignInShutdownAfterExchange(t *testing.T) {
	g, _ := orRig(t, browser(t))
	entered, release := gate(g)
	ch := signInAsync(g, t.Context(), "a1")
	<-entered
	down := make(chan error, 1)
	go func() { down <- g.a.ServiceShutdown() }()
	select {
	case <-down:
		t.Fatal("shutdown did not wait for the save")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if r := recv(t, ch); r.err != nil {
		t.Errorf("err = %v", r.err)
	}
	if err := <-down; err != nil {
		t.Error(err)
	}
	if storedKey(g) != goodKey {
		t.Errorf("key %q", storedKey(g))
	}
}

// OpenRouter drops the state when the user refuses: the browser's
// navigation with an error and no state ends the sign-in as refused.
func TestOpenRouterSignInStatelessDenial(t *testing.T) {
	g, _ := orRig(t, nil)
	g.h.openURL = func(u string) error {
		au, _ := url.Parse(u)
		cb := au.Query().Get("callback_url") + "?error=access_denied"
		go func() {
			req, _ := http.NewRequest("GET", cb, nil)
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			if r, err := http.DefaultClient.Do(req); err == nil {
				r.Body.Close()
			}
		}()
		return nil
	}
	start := time.Now()
	_, err := g.a.SignInOpenRouter(t.Context(), "openrouter", "a1")
	if errorCode(t, err) != CodeAuth || time.Since(start) > 5*time.Second {
		t.Errorf("err = %v after %v", err, time.Since(start))
	}
	if storedKey(g) != "" {
		t.Error("key saved")
	}
}
