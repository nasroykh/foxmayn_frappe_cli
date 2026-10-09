// Package loopback is the local half of an OAuth Authorization Code + PKCE
// login in a native app (RFC 8252): the PKCE verifier and challenge, and a
// one-shot HTTP server on a loopback address that waits for the browser's
// redirect. It never prompts and never writes to stdout, so the CLI and the
// desktop app share it.
package loopback

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"sync"
	"time"
)

// ─── PKCE ────────────────────────────────────────────────────────────────────

// NewVerifier returns a PKCE code verifier: 32 random bytes, base64url
// without padding (43 characters). It also serves as an OAuth state.
func NewVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Challenge is the S256 code challenge of verifier.
func Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ─── Server ──────────────────────────────────────────────────────────────────

// Hosts a Server can listen on.
const (
	// HostIP binds 127.0.0.1 only; the redirect URI names the IP literal.
	// Preferred: a browser that resolves localhost to ::1 first could hand
	// the code to another process listening on [::1] (RFC 8252 §8.3).
	HostIP = "127.0.0.1"
	// HostLocalhost is for a provider that only accepts "localhost" in the
	// redirect URI: the server binds the same port on 127.0.0.1 and on ::1
	// (when this computer has IPv6 loopback), so no other process can take
	// either address of that port.
	HostLocalhost = "localhost"
)

// DefaultPath is the callback path when Config.Path is empty.
const DefaultPath = "/callback"

// Config says where a Server listens and what its pages say.
type Config struct {
	// Host is HostIP (the default) or HostLocalhost.
	Host string
	// Ports are tried in order; when all are taken, or none is given, the
	// system picks a free port.
	Ports []int
	// Path is the callback path (DefaultPath when empty).
	Path string
	// App names the app on the success page: "You can close this tab and
	// return to <App>."
	App string
}

type result struct {
	code string
	err  error
}

// Server is a running callback server waiting for one OAuth redirect. The
// first redirect carrying the right state ends it; one with a wrong or
// missing state gets 400 and the server keeps waiting, and any redirect
// after the first gets 409. Close it when done; Wait closes it too.
type Server struct {
	port   int
	state  string
	host   string
	path   string
	result chan result
	srv    *http.Server
	lns    []net.Listener

	once      sync.Once
	closeOnce sync.Once
}

// Start binds the port and starts serving at once, so the redirect URI can be
// shown or sent before the browser opens.
func Start(cfg Config) (*Server, error) {
	host := cfg.Host
	if host == "" {
		host = HostIP
	}
	if host != HostIP && host != HostLocalhost {
		return nil, fmt.Errorf("loopback: unsupported host %q", host)
	}
	path := cfg.Path
	if path == "" {
		path = DefaultPath
	}
	lns, err := listen(host, cfg.Ports)
	if err != nil {
		return nil, fmt.Errorf("binding callback port: %w", err)
	}

	// Random state tying the auth request to this callback. A forged or
	// cross-origin request carrying the wrong (or no) state is rejected without
	// consuming the result, so it can't hijack or DoS the login.
	state, err := NewVerifier()
	if err != nil {
		closeAll(lns)
		return nil, fmt.Errorf("generating state: %w", err)
	}

	mux := http.NewServeMux()
	s := &Server{
		port:   lns[0].Addr().(*net.TCPAddr).Port,
		state:  state,
		host:   host,
		path:   path,
		result: make(chan result, 1),
		srv:    &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		lns:    lns,
	}
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, cfg.App) })

	for _, ln := range lns {
		go func(ln net.Listener) {
			if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.deliver(result{err: fmt.Errorf("callback server: %w", err)})
			}
		}(ln)
	}
	return s, nil
}

// listen binds the first free port of ports (then any port) on host.
func listen(host string, ports []int) ([]net.Listener, error) {
	try := append(append([]int{}, ports...), 0)
	if host == HostIP {
		var err error
		for _, p := range try {
			var ln net.Listener
			if ln, err = net.Listen("tcp", net.JoinHostPort(HostIP, fmt.Sprint(p))); err == nil {
				return []net.Listener{ln}, nil
			}
		}
		return nil, err
	}

	// localhost: the same port on both loopback addresses. Without IPv6
	// loopback the name resolves to 127.0.0.1 only, so one listener is enough.
	v6 := hasIPv6Loopback()
	var err error
	for _, p := range try {
		// An ephemeral port is picked on 127.0.0.1 and may be taken on ::1:
		// a few draws find one free on both.
		draws := 1
		if p == 0 {
			draws = 8
		}
		for i := 0; i < draws; i++ {
			var ln4 net.Listener
			if ln4, err = net.Listen("tcp", net.JoinHostPort(HostIP, fmt.Sprint(p))); err != nil {
				break
			}
			if !v6 {
				return []net.Listener{ln4}, nil
			}
			port := ln4.Addr().(*net.TCPAddr).Port
			var ln6 net.Listener
			if ln6, err = net.Listen("tcp", net.JoinHostPort("::1", fmt.Sprint(port))); err == nil {
				return []net.Listener{ln4, ln6}, nil
			}
			ln4.Close()
		}
	}
	return nil, err
}

// hasIPv6Loopback reports whether ::1 can be bound at all.
func hasIPv6Loopback() bool {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func closeAll(lns []net.Listener) {
	for _, ln := range lns {
		ln.Close()
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request, app string) {
	q := r.URL.Query()
	// Verify state first — a mismatch keeps the server listening for the
	// legitimate redirect.
	if q.Get("state") != s.state {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "<html><body style='font-family:sans-serif;padding:2rem'><h2>Invalid request</h2><p>State mismatch; ignoring.</p></body></html>")
		return
	}
	var res result
	var msg string
	switch {
	case q.Get("error") != "":
		msg = q.Get("error")
		if desc := q.Get("error_description"); desc != "" {
			msg += ": " + desc
		}
		res.err = fmt.Errorf("authorization denied: %s", msg)
	case q.Get("code") == "":
		msg = "No code received."
		res.err = errors.New("no authorization code in callback URL")
	default:
		res.code = q.Get("code")
	}
	if !s.deliver(res) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, "<html><body style='font-family:sans-serif;padding:2rem'><h2>Already handled</h2><p>This login was already processed. You can close this tab.</p></body></html>")
		return
	}
	if res.err != nil {
		w.WriteHeader(http.StatusBadRequest)
		// html.EscapeString: never reflect raw query params into HTML.
		fmt.Fprintf(w, "<html><body style='font-family:sans-serif;padding:2rem'><h2>Authorization failed</h2><p>%s</p><p>You can close this tab.</p></body></html>", html.EscapeString(msg))
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<html><body style="font-family:sans-serif;padding:2rem;text-align:center">
<h2>&#10003; Authorization successful</h2>
<p>You can close this tab and return to %s.</p>
</body></html>`, html.EscapeString(app))
}

// deliver records the first callback outcome. Later callbacks (page reloads,
// duplicate redirects) are dropped instead of blocking their handler forever,
// which would make Shutdown hang.
func (s *Server) deliver(r result) bool {
	first := false
	s.once.Do(func() {
		first = true
		s.result <- r
	})
	return first
}

// Port is the bound port.
func (s *Server) Port() int { return s.port }

// State is the OAuth state the callback must carry.
func (s *Server) State() string { return s.state }

// Addr is the address of the first listener (127.0.0.1:<port>).
func (s *Server) Addr() net.Addr { return s.lns[0].Addr() }

// Addrs are the addresses of every listener.
func (s *Server) Addrs() []net.Addr {
	out := make([]net.Addr, len(s.lns))
	for i, ln := range s.lns {
		out[i] = ln.Addr()
	}
	return out
}

// RedirectURI is http://<host>:<port><path>, host being the Config's.
func (s *Server) RedirectURI() string {
	return fmt.Sprintf("http://%s:%d%s", s.host, s.port, s.path)
}

// Close stops the server, giving in-flight responses a moment to finish.
// Safe to call more than once.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			s.srv.Close()
		}
		// Shutdown closes the listeners Serve got; these may not have
		// reached Serve yet.
		closeAll(s.lns)
	})
}

// Wait blocks until the callback delivers a code, ctx is cancelled, or
// timeout elapses. The server is closed on return. A cancelled ctx returns
// ctx.Err(); a denied authorization returns an error naming the provider's
// error code.
func (s *Server) Wait(ctx context.Context, timeout time.Duration) (string, error) {
	defer s.Close()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-s.result:
		return r.code, r.err
	case <-ctx.Done():
		// The CLI installs a SIGINT/SIGTERM-cancelled context, which disables
		// Go's default terminate-on-signal — so this wait must honour it or the
		// user can't abort the browser flow.
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("timed out waiting for browser authorization (%s)", timeout)
	}
}
