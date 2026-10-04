package client

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func TestSitePath(t *testing.T) {
	ok := map[string]string{
		"/api/resource/ToDo":           "/api/resource/ToDo",
		"api/resource/ToDo":            "/api/resource/ToDo",
		"/api/resource/Sales Invoice":  "/api/resource/Sales%20Invoice",
		"/api/resource/A%2FB?x=1&y=2":  "/api/resource/A%2FB",
		"/private/files/a b.pdf?v=1#f": "/private/files/a%20b.pdf",
		"api/resource/A%2FB":           "/api/resource/A%2FB",
	}
	for in, want := range ok {
		got, _, err := SitePath(in)
		if err != nil || got != want {
			t.Errorf("SitePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, q, _ := SitePath("/x?a=1&a=2&b=3"); len(q["a"]) != 2 || q.Get("b") != "3" {
		t.Errorf("query = %v", q)
	}
	for _, bad := range []string{"https://evil.example/x", "//evil.example/x", "http:/x", "javascript:x", "/x?%zz"} {
		if _, _, err := SitePath(bad); err == nil {
			t.Errorf("SitePath(%q): want error", bad)
		}
	}
}

func TestRawRefusesAuthHeaders(t *testing.T) {
	s := frappetest.New(t)
	c, err := New(context.Background(), &config.SiteConfig{URL: s.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"Authorization", "cookie", "Host", "x-frappe-site-name", "X-Forwarded-Host"} {
		hdr := http.Header{}
		hdr.Set(h, "x")
		if _, err := c.Raw(context.Background(), RawRequest{Method: "GET", Path: "/api/method/frappe.ping", Header: hdr}); err == nil {
			t.Errorf("%s accepted", h)
		}
	}
	if _, err := c.Raw(context.Background(), RawRequest{Method: "GET", Path: "//evil.example/x"}); err == nil {
		t.Error("protocol-relative path accepted")
	}
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("%d requests sent", n)
	}

	resp, err := c.Raw(context.Background(), RawRequest{Method: "GET", Path: "/api/method/frappe.ping"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.Status != 200 || string(b) == "" {
		t.Fatalf("status %d body %q", resp.Status, b)
	}
}

func TestRawReloginsExpiredSession(t *testing.T) {
	f := &fakeSessionServer{}
	c := newSessionClient(t, f)
	c.mu.Lock()
	c.loggedInAt = time.Now().Add(-time.Hour)
	c.mu.Unlock()
	f.valid.Store("expired")
	resp, err := c.Raw(context.Background(), RawRequest{Method: "GET", Path: "/api/resource/ToDo/a"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Status != 200 || f.logins.Load() != 2 {
		t.Fatalf("status %d, logins %d (want 200, 2)", resp.Status, f.logins.Load())
	}
}
