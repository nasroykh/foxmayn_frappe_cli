package client

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
	return resty.New().
		SetBaseURL(strings.TrimRight(baseURL, "/")).
		SetTimeout(Timeout).
		SetResponseBodyLimit(MaxResponseBytes).
		SetCookieJar(nil).
		SetLogger(nopLogger{}).
		SetRedirectPolicy(resty.RedirectPolicyFunc(checkRedirect))
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
func checkRedirect(req *http.Request, via []*http.Request) error {
	orig := via[0]
	if orig.Method != http.MethodGet && orig.Method != http.MethodHead {
		return fmt.Errorf("site redirected %s %s to %s: update the site URL in your config (e.g. use https://)",
			orig.Method, orig.URL.Redacted(), req.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
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
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
			(errors.As(err, &ne) && ne.Timeout()) {
			return false
		}
		return true
	}
	switch resp.StatusCode() {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// requestError wraps a transport error with a hint for the common cases.
func requestError(err error) error {
	if errors.Is(err, resty.ErrResponseBodyTooLarge) {
		return fmt.Errorf("response larger than %d MiB: narrow the request with a limit, fields or filters", MaxResponseBytes>>20)
	}
	return fmt.Errorf("HTTP request failed: %w", err)
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
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "":
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
