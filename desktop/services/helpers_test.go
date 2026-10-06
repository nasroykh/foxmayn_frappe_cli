package services

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitecache"
)

// TestMain keeps the tests out of the developer's ffc cache: removing or
// renaming a site drops its cache directory.
func TestMain(m *testing.M) {
	cache, err := os.MkdirTemp("", "ffc-desktop-test-cache")
	if err != nil {
		panic(err)
	}
	sitecache.UserCacheDir = func() (string, error) { return cache, nil }
	code := m.Run()
	_ = os.RemoveAll(cache)
	os.Exit(code)
}

type event struct {
	name string
	data any
}

// fakeHost records events and what was opened. openURL, when set, runs for
// every OpenURL (a browser).
type fakeHost struct {
	mu      sync.Mutex
	events  []event
	opened  []string
	files   []string
	openURL func(string) error
}

func (h *fakeHost) Emit(name string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event{name, data})
}

func (h *fakeHost) OpenURL(u string) error {
	h.mu.Lock()
	h.opened = append(h.opened, u)
	f := h.openURL
	h.mu.Unlock()
	if f != nil {
		return f(u)
	}
	return nil
}

func (h *fakeHost) OpenFile(p string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files = append(h.files, p)
	return nil
}

func (h *fakeHost) named(name string) []any {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []any
	for _, e := range h.events {
		if e.name == name {
			out = append(out, e.data)
		}
	}
	return out
}

// approvingBrowser is a browser whose user approves the sign-in: it calls
// the redirect URI with the fake's code and the request's state, as
// Frappe's consent page would.
func approvingBrowser(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	q := u.Query()
	cb := q.Get("redirect_uri") + "?" + url.Values{"code": {frappetest.AuthCode}, "state": {q.Get("state")}}.Encode()
	go func() {
		if resp, err := http.Get(cb); err == nil {
			resp.Body.Close()
		}
	}()
	return nil
}

// newSites is a SitesService over a config path in a temp dir (the file
// does not exist yet).
func newSites(t *testing.T) (*SitesService, *fakeHost, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffc", "config.yaml")
	h := &fakeHost{}
	return NewSitesService(h, path), h, path
}
