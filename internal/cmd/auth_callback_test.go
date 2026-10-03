package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func callbackURL(cs *callbackServer, q url.Values) string {
	return cs.redirectURI() + "?" + q.Encode()
}

func getStatus(t *testing.T, rawURL string) int {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(rawURL)
	if err != nil {
		t.Errorf("GET %s: %v", rawURL, err)
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestCallbackServerBindsLoopbackIP(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.close()

	if ip := cs.addr.(*net.TCPAddr).IP.String(); ip != "127.0.0.1" {
		t.Errorf("bound to %s, want 127.0.0.1", ip)
	}
	if want := fmt.Sprintf("http://127.0.0.1:%d/callback", cs.port); cs.redirectURI() != want {
		t.Errorf("redirectURI = %q, want %q", cs.redirectURI(), want)
	}
}

func TestCallbackServerRejectsStateMismatch(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.close()

	for _, q := range []url.Values{
		{"code": {"evil"}},
		{"code": {"evil"}, "state": {"wrong"}},
		{"error": {"access_denied"}, "state": {"wrong"}},
	} {
		if got := getStatus(t, callbackURL(cs, q)); got != http.StatusBadRequest {
			t.Errorf("forged callback %v: status %d, want 400", q, got)
		}
	}
	// The forged requests must not have consumed the result slot.
	if got := getStatus(t, callbackURL(cs, url.Values{"code": {"good"}, "state": {cs.state}})); got != http.StatusOK {
		t.Errorf("valid callback: status %d, want 200", got)
	}
	code, err := cs.wait(context.Background())
	if err != nil || code != "good" {
		t.Fatalf("wait = (%q, %v), want (good, nil)", code, err)
	}
}

func TestCallbackServerDuplicateCallbacksDoNotHang(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.close()

	const n = 5
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = getStatus(t, callbackURL(cs, url.Values{"code": {fmt.Sprint("c", i)}, "state": {cs.state}}))
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("duplicate callbacks blocked their handlers")
	}

	ok := 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Errorf("unexpected status %d", s)
		}
	}
	if ok != 1 {
		t.Errorf("%d callbacks accepted, want exactly 1", ok)
	}

	start := time.Now()
	code, err := cs.wait(context.Background())
	if err != nil || !strings.HasPrefix(code, "c") {
		t.Fatalf("wait = (%q, %v)", code, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("wait+shutdown took %v", d)
	}
}

func TestCallbackServerErrorAndCancel(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"error": {"access_denied"}, "error_description": {"<b>no</b>"}, "state": {cs.state}}
	if got := getStatus(t, callbackURL(cs, q)); got != http.StatusBadRequest {
		t.Errorf("error callback: status %d, want 400", got)
	}
	if _, err := cs.wait(context.Background()); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("wait err = %v, want authorization denied", err)
	}

	cs2, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cs2.wait(ctx); !errors.Is(err, errAborted) {
		t.Errorf("cancelled wait err = %v, want errAborted", err)
	}
	cs2.close() // second close must be a no-op
}

func TestCallbackServerTimeout(t *testing.T) {
	old := oauthCallbackTimeout
	oauthCallbackTimeout = 50 * time.Millisecond
	defer func() { oauthCallbackTimeout = old }()

	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.wait(context.Background()); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("wait err = %v, want timeout", err)
	}
}
