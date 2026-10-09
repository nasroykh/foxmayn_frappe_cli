package loopback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func get(t *testing.T, rawURL string) (int, string) {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(rawURL)
	if err != nil {
		t.Errorf("GET %s: %v", rawURL, err)
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func start(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func callback(s *Server, q url.Values) string { return s.RedirectURI() + "?" + q.Encode() }

// RFC 7636 appendix B.
func TestChallengeVector(t *testing.T) {
	if got := Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("Challenge = %q", got)
	}
	a, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewVerifier()
	if len(a) != 43 || a == b || strings.ContainsAny(a, "+/=") {
		t.Errorf("verifiers %q, %q", a, b)
	}
}

func TestStartIP(t *testing.T) {
	s := start(t, Config{App: "ffc"})
	if ip := s.Addr().(*net.TCPAddr).IP.String(); ip != "127.0.0.1" || len(s.Addrs()) != 1 {
		t.Errorf("bound to %v, want 127.0.0.1 only", s.Addrs())
	}
	if want := fmt.Sprintf("http://127.0.0.1:%d/callback", s.Port()); s.RedirectURI() != want {
		t.Errorf("RedirectURI = %q, want %q", s.RedirectURI(), want)
	}
	if len(s.State()) != 43 {
		t.Errorf("state %q", s.State())
	}
}

// A preferred port is used when it is free, the next one when it is taken.
func TestStartPreferredPorts(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freePort := free.Addr().(*net.TCPAddr).Port
	free.Close()
	s := start(t, Config{Ports: []int{busy.Addr().(*net.TCPAddr).Port, freePort}})
	if s.Port() != freePort {
		t.Errorf("port %d, want %d", s.Port(), freePort)
	}
}

func TestStartLocalhost(t *testing.T) {
	s := start(t, Config{Host: HostLocalhost, Path: "/cb", App: "Foxmayn Frappe Desktop"})
	if want := fmt.Sprintf("http://localhost:%d/cb", s.Port()); s.RedirectURI() != want {
		t.Errorf("RedirectURI = %q, want %q", s.RedirectURI(), want)
	}
	addrs := s.Addrs()
	if addrs[0].(*net.TCPAddr).IP.String() != "127.0.0.1" {
		t.Errorf("first listener %v", addrs[0])
	}
	if hasIPv6Loopback() {
		if len(addrs) != 2 || addrs[1].(*net.TCPAddr).IP.String() != "::1" || addrs[1].(*net.TCPAddr).Port != s.Port() {
			t.Fatalf("listeners %v, want 127.0.0.1 and ::1 on one port", addrs)
		}
		// Both addresses reach the same flow: a bad state on ::1 is refused,
		// the good one on 127.0.0.1 accepted.
		v6 := fmt.Sprintf("http://[::1]:%d/cb?state=wrong&code=x", s.Port())
		if st, _ := get(t, v6); st != http.StatusBadRequest {
			t.Errorf("::1 bad state: %d", st)
		}
	} else if len(addrs) != 1 {
		t.Errorf("listeners %v without IPv6 loopback", addrs)
	}
	v4 := fmt.Sprintf("http://127.0.0.1:%d/cb?%s", s.Port(), url.Values{"state": {s.State()}, "code": {"good"}}.Encode())
	st, body := get(t, v4)
	if st != http.StatusOK || !strings.Contains(body, "return to Foxmayn Frappe Desktop.") {
		t.Errorf("status %d, body %s", st, body)
	}
	if code, err := s.Wait(context.Background(), time.Second); err != nil || code != "good" {
		t.Errorf("Wait = %q, %v", code, err)
	}
}

// With a localhost port held on ::1 by someone else, Start picks another
// port rather than share it.
func TestStartLocalhostSkipsPortTakenOnIPv6(t *testing.T) {
	if !hasIPv6Loopback() {
		t.Skip("no IPv6 loopback")
	}
	busy, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	p := busy.Addr().(*net.TCPAddr).Port
	s := start(t, Config{Host: HostLocalhost, Ports: []int{p}})
	if s.Port() == p {
		t.Errorf("shared port %d with another listener on ::1", p)
	}
}

func TestStartUnknownHost(t *testing.T) {
	if _, err := Start(Config{Host: "0.0.0.0"}); err == nil {
		t.Error("Start accepted a non-loopback host")
	}
}

func TestStateMismatchDoesNotConsume(t *testing.T) {
	s := start(t, Config{})
	for _, q := range []url.Values{
		{"code": {"evil"}},
		{"code": {"evil"}, "state": {"wrong"}},
		{"error": {"access_denied"}},
		{"error": {"access_denied"}, "state": {"wrong"}},
	} {
		if st, _ := get(t, callback(s, q)); st != http.StatusBadRequest {
			t.Errorf("forged callback %v: status %d, want 400", q, st)
		}
	}
	if st, _ := get(t, callback(s, url.Values{"code": {"good"}, "state": {s.State()}})); st != http.StatusOK {
		t.Errorf("valid callback: status %d", st)
	}
	if code, err := s.Wait(context.Background(), time.Second); err != nil || code != "good" {
		t.Fatalf("Wait = (%q, %v)", code, err)
	}
}

func TestDuplicatesAre409(t *testing.T) {
	s := start(t, Config{})
	const n = 5
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], _ = get(t, callback(s, url.Values{"code": {fmt.Sprint("c", i)}, "state": {s.State()}}))
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, st := range statuses {
		switch st {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Errorf("status %d", st)
		}
	}
	if ok != 1 {
		t.Errorf("%d accepted, want 1", ok)
	}
	if code, err := s.Wait(context.Background(), time.Second); err != nil || !strings.HasPrefix(code, "c") {
		t.Errorf("Wait = (%q, %v)", code, err)
	}
}

func TestDeniedIsEscaped(t *testing.T) {
	s := start(t, Config{})
	st, body := get(t, callback(s, url.Values{"error": {"access_denied"}, "error_description": {"<b>no</b>"}, "state": {s.State()}}))
	if st != http.StatusBadRequest || strings.Contains(body, "<b>") || !strings.Contains(body, "&lt;b&gt;") {
		t.Errorf("status %d, body %s", st, body)
	}
	if _, err := s.Wait(context.Background(), time.Second); err == nil || !strings.Contains(err.Error(), "authorization denied: access_denied") {
		t.Errorf("Wait err = %v", err)
	}
}

func TestNoCode(t *testing.T) {
	s := start(t, Config{})
	if st, _ := get(t, callback(s, url.Values{"state": {s.State()}})); st != http.StatusBadRequest {
		t.Errorf("status %d", st)
	}
	if _, err := s.Wait(context.Background(), time.Second); err == nil || !strings.Contains(err.Error(), "no authorization code") {
		t.Errorf("Wait err = %v", err)
	}
}

func TestCancelTimeoutAndClose(t *testing.T) {
	s := start(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Wait(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Wait err = %v", err)
	}
	// Wait closed the server: the port no longer answers.
	if _, err := (&http.Client{Timeout: time.Second}).Get(callback(s, url.Values{"state": {s.State()}, "code": {"x"}})); err == nil {
		t.Error("server still answers after Wait")
	}
	s.Close() // a second close is a no-op

	s2 := start(t, Config{})
	if _, err := s2.Wait(context.Background(), 30*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("Wait err = %v, want timeout", err)
	}
}

func TestOtherPathsAre404(t *testing.T) {
	s := start(t, Config{})
	u := fmt.Sprintf("http://127.0.0.1:%d/other?state=%s&code=x", s.Port(), s.State())
	if st, _ := get(t, u); st != http.StatusNotFound {
		t.Errorf("status %d", st)
	}
}

func do(t *testing.T, method, rawURL, host string, hdr map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestErrorsWrapSentinels(t *testing.T) {
	s := start(t, Config{})
	get(t, callback(s, url.Values{"error": {"access_denied"}, "state": {s.State()}}))
	if _, err := s.Wait(context.Background(), time.Second); !errors.Is(err, ErrDenied) || err.Error() != "authorization denied: access_denied" {
		t.Errorf("denied err = %v", err)
	}
	s2 := start(t, Config{})
	if _, err := s2.Wait(context.Background(), 20*time.Millisecond); !errors.Is(err, ErrTimeout) || !strings.HasPrefix(err.Error(), "timed out waiting for browser authorization (") {
		t.Errorf("timeout err = %v", err)
	}
}

// A refusal without any state ends the flow only with AcceptStatelessError,
// only as a browser navigation, and never with a wrong state or a code.
func TestStatelessError(t *testing.T) {
	nav := map[string]string{"Sec-Fetch-Mode": "navigate"}
	off := start(t, Config{})
	if st, _ := do(t, "GET", callback(off, url.Values{"error": {"access_denied"}}), "", nav); st != http.StatusBadRequest {
		t.Errorf("option off: %d", st)
	}

	s := start(t, Config{AcceptStatelessError: true})
	for _, c := range []struct {
		q   url.Values
		hdr map[string]string
	}{
		{url.Values{"error": {"access_denied"}}, nil},                                         // not a navigation
		{url.Values{"error": {"access_denied"}}, map[string]string{"Sec-Fetch-Mode": "cors"}}, // a script's fetch
		{url.Values{"error": {"access_denied"}, "state": {"wrong"}}, nav},                     // wrong state
		{url.Values{"error": {"access_denied"}, "state": {""}}, nav},                          // empty state is not absent
		{url.Values{"error": {"access_denied"}, "code": {"x"}}, nav},                          // a code
		{url.Values{"code": {"x"}}, nav},                                                      // no error
	} {
		st, body := do(t, "GET", callback(s, c.q), "", c.hdr)
		if st != http.StatusBadRequest || !strings.Contains(body, "press Cancel") {
			t.Errorf("%v %v: status %d, body %s", c.q, c.hdr, st, body)
		}
	}
	if st, _ := do(t, "GET", callback(s, url.Values{"error": {"access_denied"}}), "", nav); st != http.StatusBadRequest {
		t.Errorf("stateless refusal: %d", st)
	}
	if _, err := s.Wait(context.Background(), time.Second); !errors.Is(err, ErrDenied) {
		t.Errorf("Wait err = %v", err)
	}
}

func TestOnlyLoopbackHostAndGET(t *testing.T) {
	s := start(t, Config{})
	good := callback(s, url.Values{"code": {"good"}, "state": {s.State()}})
	port := fmt.Sprint(s.Port())
	for _, h := range []string{"evil.example:" + port, "127.0.0.1:1", "localhost", "127.0.0.2:" + port} {
		if st, _ := do(t, "GET", good, h, nil); st != http.StatusBadRequest {
			t.Errorf("Host %q: %d", h, st)
		}
	}
	for _, m := range []string{"POST", "PUT", "HEAD"} {
		if st, _ := do(t, m, good, "", nil); st != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, st)
		}
	}
	for _, h := range []string{"localhost:" + port, "[::1]:" + port} {
		s2 := start(t, Config{})
		u := callback(s2, url.Values{"code": {"good"}, "state": {s2.State()}})
		if st, _ := do(t, "GET", u, strings.Replace(h, port, fmt.Sprint(s2.Port()), 1), nil); st != http.StatusOK {
			t.Errorf("Host %q: %d", h, st)
		}
	}
	// None of the refused requests used the flow.
	if st, _ := do(t, "GET", good, "", nil); st != http.StatusOK {
		t.Errorf("good callback: %d", st)
	}
	if code, err := s.Wait(context.Background(), time.Second); err != nil || code != "good" {
		t.Errorf("Wait = %q, %v", code, err)
	}
}
