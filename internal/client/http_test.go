package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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

func TestRedirectGuards(t *testing.T) {
	get := func(raw string, h http.Header) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if h == nil {
			h = http.Header{}
		}
		return &http.Request{Method: http.MethodGet, URL: u, Header: h}
	}
	token := http.Header{"Authorization": {"token k:s"}}
	cookie := http.Header{"Cookie": {"sid=x"}}
	for _, c := range []struct {
		name     string
		from, to string
		h        http.Header
		refused  string
	}{
		{"https to http", "https://erp.example.com/a", "http://erp.example.com/a", nil, "leaves https"},
		{"https to http with token", "https://erp.example.com/a", "http://erp.example.com/a", token, "leaves https"},
		{"subdomain with token", "https://example.com/a", "https://files.example.com/a", token, "credentials"},
		{"other host with cookie", "https://erp.example.com/a", "https://cdn.example.net/a", cookie, "credentials"},
		{"same host https", "https://erp.example.com/a", "https://ERP.example.com./b", token, ""},
		{"http upgraded to https", "http://erp.example.com/a", "https://erp.example.com/a", token, ""},
		{"other host without credentials", "https://github.com/a", "https://objects.githubusercontent.com/a", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := checkRedirect(get(c.to, nil), []*http.Request{get(c.from, c.h)})
			if c.refused == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var re *RedirectError
			if !errors.As(err, &re) || !strings.Contains(err.Error(), c.refused) {
				t.Fatalf("err = %v, want a RedirectError with %q", err, c.refused)
			}
		})
	}
	// A downgrade later in the chain is caught at its hop.
	chain := []*http.Request{get("http://erp.example.com/a", nil), get("https://erp.example.com/a", nil)}
	if err := checkRedirect(get("http://erp.example.com/b", nil), chain); err == nil {
		t.Fatal("https to http on the second hop was followed")
	}
}

func TestRedirectOverTheWire(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plainHits.Add(1)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer plain.Close()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/down":
			http.Redirect(w, r, plain.URL+"/x", http.StatusFound)
		case "/moved":
			http.Redirect(w, r, "/here", http.StatusFound)
		default:
			_, _ = w.Write([]byte(`{"data":[{"name":"x"}]}`))
		}
	}))
	defer tlsSrv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(tlsSrv.Certificate())
	c := newKeyClient(t, tlsSrv.URL)
	c.r.SetTLSClientConfig(&tls.Config{RootCAs: pool})
	ctx := context.Background()

	t.Run("same host https followed", func(t *testing.T) {
		resp, err := c.r.R().SetContext(ctx).Get("/moved")
		if err != nil || resp.StatusCode() != 200 || resp.RawResponse.Request.URL.Path != "/here" {
			t.Fatalf("err=%v resp=%v", err, resp)
		}
	})
	t.Run("https to http refused", func(t *testing.T) {
		_, err := c.r.R().SetContext(ctx).Get("/down")
		var re *RedirectError
		if !errors.As(err, &re) {
			t.Fatalf("err = %v, want RedirectError", err)
		}
		if n := plainHits.Load(); n != 0 {
			t.Fatalf("http target received %d requests", n)
		}
	})
	t.Run("credentials not sent to another host", func(t *testing.T) {
		var hits atomic.Int32
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer other.Close()
		// 127.0.0.1 and localhost are different host names.
		otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
		redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, otherURL+r.URL.Path, http.StatusFound)
		}))
		defer redir.Close()
		_, err := newKeyClient(t, redir.URL).GetList(ctx, "ToDo", ListOptions{})
		if err == nil || !strings.Contains(err.Error(), "credentials") || hits.Load() != 0 {
			t.Fatalf("err=%v hits=%d", err, hits.Load())
		}
		// Without credentials (update checks, downloads from GitHub) the
		// redirect is followed.
		if resp, err := NewHTTPClient(5 * time.Second).R().Get(redir.URL + "/x"); err != nil || resp.StatusCode() != 200 || hits.Load() != 1 {
			t.Fatalf("anonymous redirect: err=%v hits=%d", err, hits.Load())
		}
	})
}

// A certificate that does not verify and a refused redirect fail the same
// way on every attempt: neither the resty path nor Download retries them.
func TestNoRetryOnTLSOrRedirectErrors(t *testing.T) {
	ctx := context.Background()
	var conns atomic.Int32
	tlsSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	tlsSrv.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsSrv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	tlsSrv.StartTLS()
	defer tlsSrv.Close()

	var hits atomic.Int32
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, strings.Replace(tlsSrv.URL, "127.0.0.1", "localhost", 1)+r.URL.Path, http.StatusFound)
	}))
	defer redir.Close()

	for _, c := range []struct {
		name  string
		url   string
		count *atomic.Int32
	}{
		{"untrusted certificate", tlsSrv.URL, &conns},
		{"refused redirect", redir.URL, &hits},
	} {
		cl := newKeyClient(t, c.url)
		c.count.Store(0)
		if _, err := cl.GetList(ctx, "ToDo", ListOptions{}); err == nil {
			t.Fatalf("%s: GetList succeeded", c.name)
		}
		if n := c.count.Load(); n != 1 {
			t.Errorf("%s: GetList made %d attempts, want 1", c.name, n)
		}
		c.count.Store(0)
		if resp, err := cl.Download(ctx, "/files/x", nil); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("%s: Download succeeded", c.name)
		}
		if n := c.count.Load(); n != 1 {
			t.Errorf("%s: Download made %d attempts, want 1", c.name, n)
		}
	}
	if retryableTransport(fmt.Errorf("wrapped: %w", &url.Error{Op: "Get", URL: "x", Err: x509.UnknownAuthorityError{}})) {
		t.Error("x509 error is retryable")
	}
	if !retryableTransport(&url.Error{Op: "Get", URL: "x", Err: syscall.ECONNRESET}) {
		t.Error("connection reset is not retryable")
	}

	// A server that requires a client certificate refuses the handshake with
	// an alert, which arrives as a net.OpError "remote error", not as
	// tls.AlertError.
	mtls := httptest.NewUnstartedServer(http.NotFoundHandler())
	mtls.Config.ErrorLog = log.New(io.Discard, "", 0)
	mtls.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert, MaxVersion: tls.VersionTLS12}
	mtls.StartTLS()
	defer mtls.Close()
	conn, err := tls.Dial("tcp", mtls.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12})
	if err == nil {
		err = conn.Handshake()
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("handshake without a client certificate succeeded")
	}
	if retryableTransport(&url.Error{Op: "Get", URL: "x", Err: err}) {
		t.Errorf("TLS alert %v (%T) is retryable", err, err)
	}
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
	t.Run("GET waits for a short Retry-After", func(t *testing.T) {
		var n atomic.Int32
		var first time.Time
		var gap time.Duration
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if n.Add(1) == 1 {
				first = time.Now()
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			gap = time.Since(first)
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer srv.Close()
		c := newKeyClient(t, srv.URL)
		c.r.SetRetryMaxWaitTime(maxRetryAfter) // newKeyClient shortens waits
		if _, err := c.GetList(context.Background(), "ToDo", ListOptions{}); err != nil {
			t.Fatal(err)
		}
		if n.Load() != 2 || gap < 900*time.Millisecond {
			t.Errorf("requests = %d, gap = %v; want 2 and about 1s", n.Load(), gap)
		}
	})
	// 10000000000 s wraps to a negative time.Duration if multiplied unchecked.
	for _, after := range []string{"120", "10000000000"} {
		t.Run("GET not retried on Retry-After "+after, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n.Add(1)
				w.Header().Set("Retry-After", after)
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer srv.Close()
			c := newKeyClient(t, srv.URL)
			if _, err := c.GetList(context.Background(), "ToDo", ListOptions{}); err == nil {
				t.Fatal("want error")
			}
			if n.Load() != 1 {
				t.Errorf("requests = %d, want 1", n.Load())
			}
		})
	}
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

func TestWarnIfInsecureSkipsLoopback(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	for _, u := range []string{"http://localhost:8000", "http://127.0.0.1", "http://mysite.localhost:8000"} {
		warnIfInsecure(u)
	}
	warnIfInsecure("http://erp.example.com")
	os.Stderr = stderr
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if got := strings.Count(string(out), "warning:"); got != 1 || !strings.Contains(string(out), "erp.example.com") {
		t.Errorf("warnings = %q, want one for erp.example.com", out)
	}
}
