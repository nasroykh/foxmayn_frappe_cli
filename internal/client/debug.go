package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/go-resty/resty/v2"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// DebugLevel selects what --debug traces.
type DebugLevel int

const (
	// DebugOff traces nothing.
	DebugOff DebugLevel = iota
	// DebugBasic traces one line per HTTP exchange: method, URL, status,
	// Frappe exc_type, elapsed time and sizes.
	DebugBasic
	// DebugBody also traces headers and bodies (capped at debugBodyCap).
	DebugBody
)

// Debug is the trace level for every HTTP client built after it is set. It
// is a package variable, like Timeout, so the global --debug flag reaches
// site, login, OAuth and update-check requests alike.
var Debug = DebugOff

// debugOut is where the trace goes: stderr, never stdout, so it cannot
// corrupt data output or the MCP stdio channel. It is looked up per write so
// a redirected os.Stderr (tests, the detached MCP log) is honoured.
var debugOut = func() io.Writer { return os.Stderr }

// debugBodyCap is how much of each body --debug=body prints.
const debugBodyCap = 64 << 10

// debugMu keeps the blocks of concurrent requests (bulk runs, the MCP
// server) from interleaving.
var debugMu sync.Mutex

// debugSeq numbers exchanges so a request and its response can be matched.
var debugSeq atomic.Int64

// withDebug installs the trace on a resty client when Debug is on. The
// transport sits under resty, so it sees every attempt (retries included)
// with its final headers, and Clone (the raw client) shares it.
func withDebug(r *resty.Client) *resty.Client {
	if Debug == DebugOff {
		return r
	}
	hc := r.GetClient()
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	hc.Transport = &debugTransport{base: base, level: Debug}
	return r
}

// debugRetry is a resty retry hook that notes why a GET is repeated.
func debugRetry(resp *resty.Response, err error) {
	if Debug == DebugOff || resp == nil || resp.Request == nil {
		return
	}
	reason := "transport error"
	if err == nil {
		reason = fmt.Sprintf("HTTP %d", resp.StatusCode())
	}
	debugWrite(fmt.Sprintf("debug: retrying %s %s after %s (attempt %d)",
		resp.Request.Method, redactURL(resp.Request.URL), reason, resp.Request.Attempt+1))
}

func debugWrite(block string) {
	debugMu.Lock()
	defer debugMu.Unlock()
	_, _ = io.WriteString(debugOut(), text.Sanitize(block)+"\n")
}

type debugTransport struct {
	base  http.RoundTripper
	level DebugLevel
}

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	id := debugSeq.Add(1)
	start := time.Now()
	target := redactURL(req.URL.String())
	if t.level == DebugBody {
		var b strings.Builder
		fmt.Fprintf(&b, "debug #%d > %s %s", id, req.Method, target)
		writeHeaders(&b, id, ">", req.Header)
		if body := requestBody(req); len(body) > 0 {
			writeBody(&b, id, ">", body, len(body), req.Header.Get("Content-Type"))
		}
		debugWrite(b.String())
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		debugWrite(fmt.Sprintf("debug #%d %s %s: error after %s: %v", id, req.Method, target, since(start), err))
		return resp, err
	}
	capture := t.level == DebugBody || resp.StatusCode >= 400
	resp.Body = &debugBody{
		rc: resp.Body,
		finish: func(n int64, head []byte) {
			var b strings.Builder
			if t.level == DebugBody {
				fmt.Fprintf(&b, "debug #%d < %s", id, resp.Status)
			} else {
				fmt.Fprintf(&b, "debug #%d %s %s → %d", id, req.Method, target, resp.StatusCode)
			}
			if exc := excType(head); exc != "" {
				b.WriteString(" " + exc)
			}
			fmt.Fprintf(&b, " (%s, sent %s, received %s)", since(start), size(req.ContentLength), size(n))
			if t.level == DebugBody {
				writeHeaders(&b, id, "<", resp.Header)
				if n > 0 {
					writeBody(&b, id, "<", head, int(n), resp.Header.Get("Content-Type"))
				}
			}
			debugWrite(b.String())
		},
		capture: capture,
	}
	return resp, nil
}

// requestBody returns a copy of the request body without consuming it.
func requestBody(req *http.Request) []byte {
	if req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err == nil {
			defer rc.Close()
			b, _ := io.ReadAll(io.LimitReader(rc, debugBodyCap+1))
			return b
		}
	}
	// No way to rewind: read it and put it back.
	b, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	return b
}

// debugBody counts a response body as it is read, keeps its first
// debugBodyCap bytes when capture is set, and reports once at EOF or Close.
type debugBody struct {
	rc      io.ReadCloser
	n       int64
	head    []byte
	capture bool
	once    sync.Once
	finish  func(n int64, head []byte)
}

func (d *debugBody) Read(p []byte) (int, error) {
	n, err := d.rc.Read(p)
	d.n += int64(n)
	if d.capture && len(d.head) <= debugBodyCap {
		d.head = append(d.head, p[:min(n, debugBodyCap+1-len(d.head))]...)
	}
	if err == io.EOF {
		d.done()
	}
	return n, err
}

func (d *debugBody) Close() error {
	d.done()
	return d.rc.Close()
}

func (d *debugBody) done() { d.once.Do(func() { d.finish(d.n, d.head) }) }

func since(t time.Time) string { return time.Since(t).Round(time.Millisecond).String() }

func size(n int64) string {
	switch {
	case n < 0:
		return "? B"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

var excTypeRe = regexp.MustCompile(`"exc_type"\s*:\s*"([^"\\]{1,100})"`)

func excType(body []byte) string {
	if m := excTypeRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

func writeHeaders(b *strings.Builder, id int64, dir string, h http.Header) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(b, "\ndebug #%d %s %s: %s", id, dir, k, redactHeader(k, v))
		}
	}
}

func writeBody(b *strings.Builder, id int64, dir string, head []byte, total int, contentType string) {
	shown := head
	if len(shown) > debugBodyCap {
		shown = shown[:debugBodyCap]
	}
	var s string
	switch {
	case !utf8.Valid(shown) && !utf8.Valid(shown[:max(0, len(shown)-utf8.UTFMax)]):
		s = fmt.Sprintf("(binary, %s)", size(int64(total)))
	case strings.Contains(contentType, "x-www-form-urlencoded"):
		s = redactForm(string(shown))
	default:
		s = RedactJSON(string(shown))
	}
	if total > len(shown) && !strings.HasPrefix(s, "(binary") {
		s += fmt.Sprintf("\n… (%s more)", size(int64(total-len(shown))))
	}
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintf(b, "\ndebug #%d %s %s", id, dir, line)
	}
}

// ─── Redaction (shared with --dry-run) ───────────────────────────────────────

const redacted = "***"

// secretKey reports whether a header, query, form or JSON key carries a
// credential: passwords, secrets, tokens, session ids, OAuth codes and
// verifiers, and password-reset keys.
func secretKey(k string) bool {
	k = strings.ToLower(k)
	switch k {
	case "pwd", "sid", "code", "key", "authorization", "cookie", "set-cookie":
		return true
	}
	for _, s := range []string{"password", "secret", "token", "code_verifier"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// redactHeader keeps a credential header's scheme ("token", "Bearer") and
// cookie names, never their values.
func redactHeader(k, v string) string {
	switch strings.ToLower(k) {
	case "authorization":
		if scheme, _, ok := strings.Cut(v, " "); ok {
			return scheme + " " + redacted
		}
		return redacted
	case "cookie", "set-cookie":
		parts := strings.Split(v, ";")
		for i, p := range parts {
			name, _, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && (i == 0 || secretKey(name)) {
				parts[i] = " " + name + "=" + redacted
			}
		}
		return strings.TrimSpace(strings.Join(parts, ";"))
	}
	if secretKey(k) {
		return redacted
	}
	return v
}

// redactURL hides the values of secret query parameters.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	u.RawQuery = redactForm(u.RawQuery)
	return u.String()
}

// redactForm hides the values of secret keys in a URL-encoded form or
// query, keeping the order and encoding of everything else.
func redactForm(raw string) string {
	pairs := strings.Split(raw, "&")
	for i, p := range pairs {
		k, _, ok := strings.Cut(p, "=")
		if name, err := url.QueryUnescape(k); ok && err == nil && secretKey(name) {
			pairs[i] = k + "=" + redacted
		}
	}
	return strings.Join(pairs, "&")
}

// secretKeyRe is secretKey as a pattern for JSON keys.
const secretKeyRe = `(?:[^"\\]*(?:password|secret|token)[^"\\]*|pwd|sid|code|code_verifier|key)`

// secretPairRe and escapedPairRe match a JSON string member whose key looks
// secret: plain, or escaped inside a JSON string ({"doc": "{\"pwd\": \"x\"}"}).
// Regular expressions, not a parse, so a truncated body is redacted too.
var (
	secretPairRe  = regexp.MustCompile(`(?i)("` + secretKeyRe + `"\s*:\s*)"(?:[^"\\]|\\.)*"`)
	escapedPairRe = regexp.MustCompile(`(?i)(\\"` + secretKeyRe + `\\"\s*:\s*)\\"(?:[^"\\]|\\[^"])*?\\"`)
)

// RedactJSON hides the string values of secret keys in JSON text.
func RedactJSON(s string) string {
	s = secretPairRe.ReplaceAllString(s, `$1"`+redacted+`"`)
	return escapedPairRe.ReplaceAllString(s, `$1\"`+redacted+`\"`)
}
