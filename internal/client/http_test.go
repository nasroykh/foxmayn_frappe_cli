package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func newKeyClient(t *testing.T, url string) *FrappeClient {
	t.Helper()
	c, err := New(context.Background(), &config.SiteConfig{URL: url, APIKey: "k", APISecret: "s"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.r.SetRetryWaitTime(time.Millisecond).SetRetryMaxWaitTime(5 * time.Millisecond)
	return c
}

func TestRedirectPolicy(t *testing.T) {
	var targetHits atomic.Int32
	var targetMethods atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		targetMethods.Store(r.Method)
		_, _ = w.Write([]byte(`{"data":[{"name":"x"}]}`))
	}))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer redir.Close()
	c := newKeyClient(t, redir.URL)
	ctx := context.Background()

	writes := map[string]func() error{
		"DELETE": func() error { return c.DeleteDoc(ctx, "ToDo", "a") },
		"PUT":    func() error { _, err := c.UpdateDoc(ctx, "ToDo", "a", map[string]interface{}{"x": 1}); return err },
		"POST":   func() error { _, err := c.CreateDoc(ctx, "ToDo", map[string]interface{}{"x": 1}); return err },
	}
	for name, fn := range writes {
		t.Run(name, func(t *testing.T) {
			err := fn()
			if err == nil || !strings.Contains(err.Error(), "redirect") {
				t.Fatalf("want redirect error, got %v", err)
			}
			if n := targetHits.Load(); n != 0 {
				t.Fatalf("target received %d requests, want 0", n)
			}
		})
	}

	t.Run("GET followed", func(t *testing.T) {
		rows, err := c.GetList(ctx, "ToDo", ListOptions{})
		if err != nil {
			t.Fatalf("GetList: %v", err)
		}
		if len(rows) != 1 || targetHits.Load() != 1 {
			t.Fatalf("rows=%v hits=%d", rows, targetHits.Load())
		}
	})
}

func TestCallMethodGETArgEncoding(t *testing.T) {
	var got map[string]string
	var present bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		got = map[string]string{"filters": q.Get("filters"), "n": q.Get("n"), "s": q.Get("s"), "list": q.Get("list")}
		_, present = q["skip"]
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer srv.Close()
	c := newKeyClient(t, srv.URL)
	_, err := c.CallMethod(context.Background(), "x.y", map[string]interface{}{
		"filters": map[string]interface{}{"status": "Open"},
		"n":       1000000,
		"s":       "plain text",
		"list":    []interface{}{1, "a"},
		"skip":    nil,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"filters": `{"status":"Open"}`, "n": "1000000", "s": "plain text", "list": `[1,"a"]`}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if present {
		t.Error("nil arg was sent")
	}
}

func TestNonJSONErrorBodyTruncatedAndSanitized(t *testing.T) {
	body := strings.Repeat("A", 100<<10) + "\x1b[31mred"
	body = "\x1b[2J" + body
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := newKeyClient(t, srv.URL)
	err := c.DeleteDoc(context.Background(), "ToDo", "a")
	if err == nil {
		t.Fatal("want error")
	}
	if len(err.Error()) >= 1024 {
		t.Errorf("error length %d, want < 1024", len(err.Error()))
	}
	if strings.ContainsRune(err.Error(), 0x1b) {
		t.Error("error contains ESC")
	}
}

func TestRetryPolicy(t *testing.T) {
	t.Run("GET retried on 503", func(t *testing.T) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		c := newKeyClient(t, srv.URL)
		if _, err := c.GetList(context.Background(), "ToDo", ListOptions{}); err == nil {
			t.Fatal("want error")
		}
		if n.Load() != 3 {
			t.Errorf("requests = %d, want 3", n.Load())
		}
	})
	t.Run("POST not retried on 503", func(t *testing.T) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		c := newKeyClient(t, srv.URL)
		if _, err := c.CreateDoc(context.Background(), "ToDo", map[string]interface{}{"a": 1}); err == nil {
			t.Fatal("want error")
		}
		if n.Load() != 1 {
			t.Errorf("requests = %d, want 1", n.Load())
		}
	})
	t.Run("GET not retried on timeout", func(t *testing.T) {
		old := Timeout
		Timeout = 100 * time.Millisecond
		defer func() { Timeout = old }()
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			select {
			case <-time.After(600 * time.Millisecond):
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		c := newKeyClient(t, srv.URL)
		if _, err := c.GetList(context.Background(), "ToDo", ListOptions{}); err == nil {
			t.Fatal("want timeout error")
		}
		time.Sleep(50 * time.Millisecond)
		if n.Load() != 1 {
			t.Errorf("requests = %d, want 1", n.Load())
		}
	})
}

func TestMissingDataEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newKeyClient(t, srv.URL)
	ctx := context.Background()
	if _, err := c.GetDoc(ctx, "ToDo", "a"); err == nil {
		t.Error("GetDoc on {}: want error")
	}
	if _, err := c.CreateDoc(ctx, "ToDo", map[string]interface{}{"a": 1}); err == nil {
		t.Error("CreateDoc on {}: want error")
	}
	rows, err := c.GetList(ctx, "ToDo", ListOptions{})
	if err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("GetList on {} = %v, %v; want empty non-nil", rows, err)
	}
}

func TestNonJSON200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>Please log in</body></html>"))
	}))
	defer srv.Close()
	c := newKeyClient(t, srv.URL)
	_, err := c.GetDoc(context.Background(), "ToDo", "a")
	if err == nil || !strings.Contains(err.Error(), "non-JSON") {
		t.Fatalf("err = %v, want non-JSON mention", err)
	}
}

// fakeSessionServer implements login + a data endpoint that only accepts the
// sid in validSid.
type fakeSessionServer struct {
	logins   atomic.Int32
	cookies  chan []string
	valid    atomic.Value // string
	alwaysNo bool         // every request is refused, as for an expired session
	denyDocs bool         // documents are refused but the session stays valid
}

func (f *fakeSessionServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/method/login" {
			n := f.logins.Add(1)
			sid := "sid" + string(rune('0'+n))
			f.valid.Store(sid)
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: sid, Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":"Logged In"}`))
			return
		}
		if f.cookies != nil {
			f.cookies <- r.Header.Values("Cookie")
		}
		v, _ := f.valid.Load().(string)
		validSID := !f.alwaysNo && r.Header.Get("Cookie") == "sid="+v
		if r.URL.Path == "/api/method/frappe.auth.get_logged_user" && validSID {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message":"u"}`))
			return
		}
		if !validSID || f.denyDocs {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"exc_type":"PermissionError"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"name":"a"}}`))
	})
}

func newSessionClient(t *testing.T, f *fakeSessionServer) *FrappeClient {
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c, err := New(context.Background(), &config.SiteConfig{URL: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSessionCookieSentOnce(t *testing.T) {
	f := &fakeSessionServer{cookies: make(chan []string, 4)}
	c := newSessionClient(t, f)
	if _, err := c.GetDoc(context.Background(), "ToDo", "a"); err != nil {
		t.Fatal(err)
	}
	got := <-f.cookies
	if len(got) != 1 || got[0] != "sid=sid1" {
		t.Errorf("Cookie headers = %v, want exactly [sid=sid1]", got)
	}
}

func TestSessionRelogin(t *testing.T) {
	t.Run("stale session relogs once", func(t *testing.T) {
		f := &fakeSessionServer{}
		c := newSessionClient(t, f)
		// Server-side session expires: the next login hands out a new sid.
		c.mu.Lock()
		c.loggedInAt = time.Now().Add(-time.Hour)
		c.mu.Unlock()
		f.valid.Store("expired")
		if _, err := c.GetDoc(context.Background(), "ToDo", "a"); err != nil {
			t.Fatalf("GetDoc: %v", err)
		}
		if n := f.logins.Load(); n != 2 {
			t.Errorf("logins = %d, want 2 (initial + one relogin)", n)
		}
	})
	t.Run("permission error on a live session does not relogin", func(t *testing.T) {
		f := &fakeSessionServer{denyDocs: true}
		c := newSessionClient(t, f)
		c.mu.Lock()
		c.loggedInAt = time.Now().Add(-time.Hour)
		c.mu.Unlock()
		if _, err := c.GetDoc(context.Background(), "ToDo", "a"); err == nil {
			t.Fatal("want permission error")
		}
		if n := f.logins.Load(); n != 1 {
			t.Errorf("logins = %d, want 1 (session still valid)", n)
		}
	})
	t.Run("concurrent requests on an expired session relogin once", func(t *testing.T) {
		f := &fakeSessionServer{}
		c := newSessionClient(t, f)
		c.mu.Lock()
		c.loggedInAt = time.Now().Add(-time.Hour)
		c.mu.Unlock()
		f.valid.Store("expired")
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := c.GetDoc(context.Background(), "ToDo", "a")
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("GetDoc: %v", err)
			}
		}
		if n := f.logins.Load(); n != 2 {
			t.Errorf("logins = %d, want 2 (initial + one shared relogin)", n)
		}
	})
	t.Run("fresh refusal does not relogin", func(t *testing.T) {
		f := &fakeSessionServer{alwaysNo: true}
		c := newSessionClient(t, f)
		if _, err := c.GetDoc(context.Background(), "ToDo", "a"); err == nil {
			t.Fatal("want permission error")
		}
		if n := f.logins.Load(); n != 1 {
			t.Errorf("logins = %d, want 1", n)
		}
	})
}

func TestLoginPasswordErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"2fa", 200, `{"verification":{"x":1},"tmp_id":"t"}`, "two-factor"},
		{"bad creds", 401, `{"message":"Invalid login credentials"}`, "Invalid login"},
		{"html", 200, `<html></html>`, "non-JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := LoginPassword(context.Background(), srv.URL, "u", "p")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
