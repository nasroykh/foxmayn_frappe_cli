package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// newOAuthClient returns an OAuth client of s whose refresher spends the
// fake's refresh token, and the number of refresher calls.
func newOAuthClient(t *testing.T, s *frappetest.Site) (*FrappeClient, *atomic.Int32) {
	t.Helper()
	c, err := New(context.Background(), &config.SiteConfig{URL: s.URL, AccessToken: frappetest.Token})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	c.SetTokenRefresher(func(ctx context.Context, rejected string) (string, error) {
		if calls.Add(1) == 1 && rejected != frappetest.Token {
			t.Errorf("refresher got rejected token %q", rejected)
		}
		tok, err := RefreshOAuthToken(ctx, s.URL, frappetest.OAuthClientID, "", frappetest.RefreshToken)
		if err != nil {
			return "", err
		}
		return tok.AccessToken, nil
	})
	return c, &calls
}

func bearerOf(r frappetest.Request) string { return r.Header.Get("Authorization") }

func TestOAuthRefreshOn401(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("ToDo", "description")
	c, calls := newOAuthClient(t, s)
	s.ExpireToken(frappetest.Token)

	// A write is repeated once after the refresh: the 401 came from
	// authentication, so the first attempt created nothing.
	doc, err := c.CreateDoc(context.Background(), "ToDo", map[string]interface{}{"description": "x"})
	if err != nil {
		t.Fatalf("CreateDoc: %v", err)
	}
	if s.Count("ToDo") != 1 || doc["description"] != "x" {
		t.Fatalf("count %d, doc %v", s.Count("ToDo"), doc)
	}
	if calls.Load() != 1 || s.Refreshes() != 1 {
		t.Fatalf("refresher calls %d, refreshes %d; want 1, 1", calls.Load(), s.Refreshes())
	}
	posts := s.RequestsTo("POST", "/api/resource/ToDo")
	if len(posts) != 2 || bearerOf(posts[0]) != "Bearer "+frappetest.Token || bearerOf(posts[1]) != "Bearer "+frappetest.Token+"-1" {
		t.Fatalf("POSTs %d: %v", len(posts), posts)
	}
	if posts[0].Body != posts[1].Body {
		t.Errorf("the repeated body differs: %q vs %q", posts[0].Body, posts[1].Body)
	}
	// The new token stays: no further refresh.
	if _, err := c.GetDoc(context.Background(), "ToDo", doc["name"].(string)); err != nil || calls.Load() != 1 {
		t.Fatalf("GetDoc: %v, calls %d", err, calls.Load())
	}
}

func TestOAuthRefreshConcurrentOnce(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("ToDo", "description")
	c, calls := newOAuthClient(t, s)
	s.ExpireTokenAfter(frappetest.Token, 3)

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				_, err := c.CreateDoc(context.Background(), "ToDo", map[string]interface{}{"description": "x"})
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("CreateDoc: %v", err)
		}
	}
	if s.Count("ToDo") != 40 {
		t.Errorf("ToDo count %d, want 40", s.Count("ToDo"))
	}
	if calls.Load() != 1 || s.Refreshes() != 1 {
		t.Errorf("refresher calls %d, refreshes %d; want 1, 1", calls.Load(), s.Refreshes())
	}
}

func TestOAuthRefreshFailure(t *testing.T) {
	s := frappetest.New(t)
	c, calls := newOAuthClient(t, s)
	s.ExpireToken(frappetest.Token)
	s.FailRefresh(true)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetList(context.Background(), "ToDo", ListOptions{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		var e *APIError
		if !errors.As(err, &e) || e.Status != http.StatusUnauthorized || !strings.Contains(e.Message, "refreshing it failed") ||
			!strings.Contains(e.Message, "ffc site add --oauth") || e.ExcType != "AuthenticationError" {
			t.Errorf("err = %#v", err)
		}
	}
	// One attempt for the token, not one per rejected request.
	if calls.Load() != 1 {
		t.Errorf("refresher calls %d, want 1", calls.Load())
	}
}

func TestOAuthNoRefreshWhenTokenValid(t *testing.T) {
	// A method that raises AuthenticationError itself ran: the token is
	// fine, so nothing is refreshed and the call is not repeated.
	s := frappetest.New(t)
	var runs atomic.Int32
	s.HandleMethod("app.check_pin", func(*http.Request, map[string]interface{}) (interface{}, error) {
		runs.Add(1)
		return nil, &frappetest.Error{Status: http.StatusUnauthorized, ExcType: "AuthenticationError", Message: "Incorrect PIN"}
	})
	c, calls := newOAuthClient(t, s)
	_, err := c.CallMethod(context.Background(), "app.check_pin", map[string]interface{}{"pin": "1"}, false)
	var e *APIError
	if !errors.As(err, &e) || e.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v", err)
	}
	if runs.Load() != 1 || calls.Load() != 0 {
		t.Fatalf("method runs %d, refresher calls %d; want 1, 0", runs.Load(), calls.Load())
	}
}

func TestOAuthSecond401Returned(t *testing.T) {
	// The refreshed token is refused as well: the 401 is returned, no loop.
	s := frappetest.New(t)
	c, _ := New(context.Background(), &config.SiteConfig{URL: s.URL, AccessToken: frappetest.Token})
	var calls atomic.Int32
	c.SetTokenRefresher(func(context.Context, string) (string, error) {
		calls.Add(1)
		return "dead-token", nil
	})
	s.ExpireToken(frappetest.Token)
	_, err := c.GetList(context.Background(), "ToDo", ListOptions{})
	var e *APIError
	if !errors.As(err, &e) || e.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 || len(s.RequestsTo("GET", "/api/resource/ToDo")) != 2 {
		t.Fatalf("refresher calls %d, list requests %d; want 1, 2", calls.Load(), len(s.RequestsTo("GET", "/api/resource/ToDo")))
	}
}

func TestOAuthWithoutRefresher(t *testing.T) {
	s := frappetest.New(t)
	c, _ := New(context.Background(), &config.SiteConfig{URL: s.URL, AccessToken: frappetest.Token})
	s.ExpireToken(frappetest.Token)
	if _, err := c.GetList(context.Background(), "ToDo", ListOptions{}); err == nil {
		t.Fatal("want 401")
	}
	if n := len(s.Requests()); n != 1 {
		t.Errorf("%d requests, want 1 (no probe without a refresher)", n)
	}
}

func TestOAuthRefreshRaw(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("ToDo", "description")
	c, calls := newOAuthClient(t, s)
	s.ExpireToken(frappetest.Token)
	resp, err := c.Raw(context.Background(), RawRequest{
		Method: http.MethodPost, Path: "/api/resource/ToDo",
		Body:   []byte(`{"description":"raw"}`),
		Header: http.Header{"Content-Type": {"application/json"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.Status != http.StatusOK || !strings.Contains(string(body), `"raw"`) {
		t.Fatalf("status %d body %s", resp.Status, body)
	}
	posts := s.RequestsTo("POST", "/api/resource/ToDo")
	if calls.Load() != 1 || len(posts) != 2 || posts[1].Body != `{"description":"raw"}` || s.Count("ToDo") != 1 {
		t.Fatalf("calls %d, posts %v, count %d", calls.Load(), posts, s.Count("ToDo"))
	}

	// A failed refresh is reported as the 401 with the hint.
	s.ExpireToken(frappetest.Token + "-1")
	s.FailRefresh(true)
	_, err = c.Raw(context.Background(), RawRequest{Method: http.MethodGet, Path: "/api/resource/ToDo"})
	var e *APIError
	if !errors.As(err, &e) || e.Status != http.StatusUnauthorized || !strings.Contains(e.Message, "refreshing it failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestOAuthDryRunHasNoToken(t *testing.T) {
	s := frappetest.New(t)
	c, calls := newOAuthClient(t, s)
	err := c.DeleteDoc(WithDryRun(context.Background(), DryRunWrites), "ToDo", "TD-1")
	var dr *DryRunError
	if !errors.As(err, &dr) || len(dr.Requests) != 1 {
		t.Fatalf("err = %v", err)
	}
	for k, v := range dr.Requests[0].Headers {
		if strings.Contains(v, frappetest.Token) {
			t.Errorf("plan header %s carries the token", k)
		}
	}
	if len(s.Requests()) != 0 || calls.Load() != 0 {
		t.Errorf("dry run sent %d requests", len(s.Requests()))
	}
}
