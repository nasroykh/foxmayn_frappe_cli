package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// Timeout bounds every HTTP request made to a Frappe site. It is a package
// variable so the global --timeout flag can raise it for heavy reports.
var Timeout = 30 * time.Second

// MaxResponseBytes caps how much of a response body is read into memory. An
// unlimited list of a large DocType fails with a clear error instead of
// exhausting memory (and taking a long-running MCP server down with it).
const MaxResponseBytes = 128 << 20

// newResty returns a resty client with the policy every Frappe call shares:
// bounded timeout and body size, no cookie jar (auth is explicit), no resty
// log lines on stderr (they duplicated errors and leaked query strings), and a
// redirect policy that refuses to silently turn a write into a GET.
func newResty(baseURL string) *resty.Client {
	return withDebug(resty.New().
		SetBaseURL(strings.TrimRight(baseURL, "/")).
		SetTimeout(Timeout).
		SetResponseBodyLimit(MaxResponseBytes).
		SetCookieJar(nil).
		SetLogger(nopLogger{}).
		SetRedirectPolicy(resty.RedirectPolicyFunc(checkRedirect)))
}

// NewHTTPClient returns a resty client with the same transport policy as site
// clients (silent logger, body cap, GET-only redirects) for non-site requests
// such as the GitHub release API. resty's default logger would print
// "WARN RESTY ..." lines on stderr, e.g. when the background update check
// fails offline in the middle of an unrelated command.
func NewHTTPClient(timeout time.Duration) *resty.Client {
	return newResty("").SetTimeout(timeout)
}

// checkRedirect follows redirects for GET/HEAD only. Go's client rewrites a
// redirected POST/PUT/DELETE into a body-less GET, so a site configured as
// http:// behind an https redirect used to report deletes and updates as
// successful while the server only ever saw GETs.
//
// A followed redirect never leaves https for http, and a request that
// carries credentials (Authorization or Cookie) is never redirected to
// another host: Go copies those headers to a subdomain of the original host
// (example.com to files.example.com), so a token would reach a server the
// config never named.
func checkRedirect(req *http.Request, via []*http.Request) error {
	orig := via[0]
	if orig.Method != http.MethodGet && orig.Method != http.MethodHead {
		return &RedirectError{fmt.Sprintf("site redirected %s %s to %s: update the site URL in your config (e.g. use https://)",
			orig.Method, orig.URL.Redacted(), req.URL.Redacted())}
	}
	if len(via) >= 10 {
		return &RedirectError{"stopped after 10 redirects"}
	}
	if prev := via[len(via)-1]; strings.EqualFold(prev.URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
		return &RedirectError{fmt.Sprintf("refused redirect from %s to %s: it leaves https",
			prev.URL.Redacted(), req.URL.Redacted())}
	}
	if (orig.Header.Get("Authorization") != "" || orig.Header.Get("Cookie") != "") && !sameHostname(orig.URL, req.URL) {
		return &RedirectError{fmt.Sprintf("refused redirect from %s to %s: credentials are only sent to the site's host; update the site URL in your config",
			orig.URL.Redacted(), req.URL.Redacted())}
	}
	return nil
}

// RedirectError is a redirect checkRedirect refused. It is never retried.
type RedirectError struct{ msg string }

func (e *RedirectError) Error() string { return e.msg }

// sameHostname compares host names (case and a trailing dot ignored), not
// ports: an http:// site redirected to https:// on the same host is fine.
func sameHostname(a, b *url.URL) bool {
	h := func(u *url.URL) string { return strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")) }
	return h(a) != "" && h(a) == h(b)
}

// retryableGET reports whether a failed request is worth repeating: only
// idempotent GETs, only on connection-level errors or gateway/rate-limit
// statuses. A timeout is not retried (the server may still be running the
// same expensive query), and writes never are (a POST that timed out after
// the server committed would create duplicates).
func retryableGET(resp *resty.Response, err error) bool {
	if resp == nil || resp.Request == nil || resp.Request.Method != http.MethodGet {
		return false
	}
	if err != nil {
		return retryableTransport(err)
	}
	return retryableStatus(resp.StatusCode(), resp.Header())
}

// retryableTransport reports whether a GET that got no response is worth
// repeating: not after a timeout or a cancel, and not when the next attempt
// is bound to fail the same way (a refused redirect, a certificate that does
// not verify, a TLS handshake with a server that does not speak TLS).
func retryableTransport(err error) bool {
	var (
		ne   net.Error
		re   *RedirectError
		cv   *tls.CertificateVerificationError
		ua   x509.UnknownAuthorityError
		he   x509.HostnameError
		ci   x509.CertificateInvalidError
		rh   tls.RecordHeaderError
		alrt tls.AlertError
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled),
		errors.As(err, &ne) && ne.Timeout(),
		errors.As(err, &re), errors.As(err, &cv), errors.As(err, &ua), errors.As(err, &he),
		errors.As(err, &ci), errors.As(err, &rh), errors.As(err, &alrt):
		return false
	}
	return true
}

// retryableStatus reports whether a GET answered with code is worth
// repeating: rate limits and gateway errors, including the 503 Frappe v16
// sends when a concurrency-limited method (download_pdf) found no free slot
// within 10 s (Retry-After: 10).
func retryableStatus(code int, h http.Header) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		// A server that asks for a longer pause than we are willing to wait
		// gets its error reported instead of a retry it has refused.
		return retryAfterDelay(h) <= maxRetryAfter
	}
	return false
}

// requestError wraps a transport error with a hint for the common cases.
// maxRetryAfter is the longest Retry-After a GET retry waits for.
const maxRetryAfter = 10 * time.Second

// retryAfterDelay parses a Retry-After header (seconds or an HTTP date). It
// returns 0 when the header is absent or invalid, which leaves the default
// backoff in place.
func retryAfterDelay(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		// Clamp before multiplying: a huge value would overflow into a
		// negative Duration and look like "retry now".
		if secs > int(maxRetryAfter/time.Second) {
			return maxRetryAfter + time.Second
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// retryAfter is the resty RetryAfter callback: it honours the server's
// Retry-After (retryableGET already refused waits over maxRetryAfter).
func retryAfter(_ *resty.Client, resp *resty.Response) (time.Duration, error) {
	return retryAfterDelay(resp.Header()), nil
}

func requestError(err error) error {
	if errors.Is(err, resty.ErrResponseBodyTooLarge) {
		return fmt.Errorf("response larger than %d MiB: narrow the request with a limit, fields or filters", MaxResponseBytes>>20)
	}
	return &TransportError{Err: err}
}

type nopLogger struct{}

func (nopLogger) Errorf(string, ...interface{}) {}
func (nopLogger) Warnf(string, ...interface{})  {}
func (nopLogger) Debugf(string, ...interface{}) {}

var insecureWarned sync.Map

// warnIfInsecure prints a one-time (per URL) stderr warning when credentials
// will be sent over a non-localhost plain-HTTP connection.
func warnIfInsecure(rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" {
		return
	}
	switch host := u.Hostname(); {
	case host == "localhost", host == "127.0.0.1", host == "::1", host == "",
		strings.HasSuffix(host, ".localhost"): // RFC 6761: always loopback
		return
	}
	if _, seen := insecureWarned.LoadOrStore(rawURL, true); seen {
		return
	}
	fmt.Fprintf(os.Stderr,
		"warning: %s uses plain HTTP — credentials are transmitted unencrypted. Use https:// if the site supports it.\n",
		rawURL)
}

var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

// stripHTML removes the HTML tags and entities Frappe embeds in translated
// messages, plus terminal control characters.
func stripHTML(s string) string {
	return text.Sanitize(strings.TrimSpace(html.UnescapeString(htmlTagRE.ReplaceAllString(s, ""))))
}

// snippet returns a short, sanitized excerpt of a non-JSON body for errors.
func snippet(body []byte) string {
	const max = 300
	r := []rune(text.Sanitize(strings.TrimSpace(string(body))))
	if len(r) > max {
		return string(r[:max]) + "… (truncated)"
	}
	return string(r)
}
