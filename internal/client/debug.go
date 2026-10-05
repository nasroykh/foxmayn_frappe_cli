package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
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

// DebugNote writes one line to the --debug trace (nothing when tracing is
// off), e.g. that a command answered from the local cache instead of a
// request. msg must hold no secret.
func DebugNote(msg string) {
	if Debug != DebugOff {
		debugWrite("debug: " + msg)
	}
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
		body, err := requestBody(req)
		if err != nil {
			return nil, err
		}
		if len(body) > 0 {
			total := len(body)
			if req.ContentLength > 0 {
				total = int(req.ContentLength)
			}
			writeBody(&b, id, ">", body, total, req.Header.Get("Content-Type"))
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

// requestBody returns the first debugBodyCap+1 bytes of the request body
// without consuming it.
func requestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	if req.GetBody != nil {
		if rc, err := req.GetBody(); err == nil {
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, debugBodyCap+1))
		}
	}
	// No way to rewind: read the head and send it followed by the rest.
	head, err := io.ReadAll(io.LimitReader(req.Body, debugBodyCap+1))
	if err != nil {
		_ = req.Body.Close()
		return nil, fmt.Errorf("reading request body: %w", err)
	}
	req.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), req.Body), req.Body}
	return head, nil
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
		s = redactJSON(string(shown))
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

// secretParts mark a header, query, form or JSON key as a credential when
// they appear anywhere in it; "pwd", "sid", "code" (OAuth) and "key"
// (password reset) only as the whole key.
var secretParts = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie",
	"api-key", "api_key", "apikey", "private_key", "signature", "credential", "code_verifier",
}

// SecretKey reports whether a header, query, form or JSON key carries a
// credential.
func SecretKey(k string) bool { return secretKey(k) }

// RedactArgs returns a copy of v (decoded JSON, such as MCP tool arguments)
// with every secret hidden as in the debug trace. v itself is not changed.
func RedactArgs(v interface{}) interface{} {
	b, err := json.Marshal(v)
	if err != nil {
		return redacted
	}
	c, ok := parseJSONValue(b)
	if !ok {
		return redacted
	}
	r, _ := redactValue(c)
	return r
}

func secretKey(k string) bool {
	k = strings.ToLower(k)
	switch k {
	case "pwd", "sid", "code", "key":
		return true
	}
	for _, s := range secretParts {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// redactHeader keeps a credential header's scheme ("token", "Bearer") and
// cookie names, never their values.
func redactHeader(k, v string) string {
	switch lk := strings.ToLower(k); {
	case strings.HasSuffix(lk, "authorization"):
		if scheme, _, ok := strings.Cut(v, " "); ok {
			return scheme + " " + redacted
		}
		return redacted
	case lk == "cookie" || lk == "set-cookie":
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

// redactURL hides the values of secret query parameters and the password
// of any user info in the URL.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		if base, query, ok := strings.Cut(raw, "?"); ok {
			return base + "?" + redactForm(query)
		}
		return raw
	}
	u.RawQuery = redactForm(u.RawQuery)
	return u.Redacted()
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

// redactJSON hides the values of secret members in JSON text. The text
// pass keeps the layout and key order and also covers a body cut at the
// trace limit; when the result parses, redactValue then catches what only
// the structure shows ({"fieldname": "new_password", "value": …}).
func redactJSON(s string) string {
	out := redactJSONText(s)
	if v, ok := parseJSONValue([]byte(out)); ok {
		if r, changed := redactValue(v); changed {
			return encodeJSON(r, strings.Contains(s, "\n"))
		}
	}
	return out
}

// redactValue hides the values of secret keys in decoded JSON, in JSON
// documents passed as strings (Frappe method arguments), and the value of a
// frappe.client.set_value-style {"fieldname": "new_password", "value": …}.
// It reports whether anything was hidden.
func redactValue(v interface{}) (interface{}, bool) {
	changed := false
	switch val := v.(type) {
	case map[string]interface{}:
		if f, ok := val["fieldname"].(string); ok && secretKey(f) {
			if x, ok := val["value"]; ok && x != redacted {
				val["value"], changed = redacted, true
			}
		}
		for k, x := range val {
			if secretKey(k) {
				if x != redacted {
					val[k], changed = redacted, true
				}
			} else if r, ch := redactValue(x); ch {
				val[k], changed = r, true
			}
		}
	case []interface{}:
		for i, x := range val {
			if r, ch := redactValue(x); ch {
				val[i], changed = r, true
			}
		}
	case string:
		t := strings.TrimSpace(val)
		if t == "" || (t[0] != '{' && t[0] != '[') {
			return v, false
		}
		if inner, ok := parseJSONValue([]byte(t)); ok {
			if r, ch := redactValue(inner); ch {
				return encodeJSON(r, false), true
			}
			return v, false
		}
		if r := redactJSONText(val); r != val {
			return r, true
		}
	}
	return v, changed
}

// parseJSONValue decodes b as exactly one JSON value, keeping number literals.
func parseJSONValue(b []byte) (interface{}, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}

func encodeJSON(v interface{}, indent bool) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return redacted
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// jsonKeyRe matches a JSON member key and its colon, at any escaping depth:
// a key inside a JSON document passed as a string has its quotes escaped
// (\"pwd\"), one level deeper (\\\"pwd\\\") and so on. \uXXXX escapes in
// the key are allowed.
var (
	jsonKeyRe     = regexp.MustCompile(`(\\*)"((?:[^"\\]|\\+u[0-9a-fA-F]{4}){1,100}?)(\\*)"\s*:\s*`)
	keyUnicodeRe  = regexp.MustCompile(`\\+u([0-9a-fA-F]{4})`)
	scalarEndRune = ",}]\"\\ \t\r\n"
)

// redactJSONText hides the values of secret members in text that does not
// parse as JSON, typically a body cut at the trace limit. Whatever follows
// a secret key is hidden: a string, number, array or object, up to its end
// or the end of the text.
func redactJSONText(s string) string {
	var out strings.Builder
	pos := 0
	for pos < len(s) {
		m := jsonKeyRe.FindStringSubmatchIndex(s[pos:])
		if m == nil {
			break
		}
		open, keyStart, keyEnd, closeLen, end := m[3]-m[2], pos+m[4], pos+m[5], m[7]-m[6], pos+m[1]
		key := keyUnicodeRe.ReplaceAllStringFunc(s[keyStart:keyEnd], func(e string) string {
			r, err := strconv.ParseUint(e[len(e)-4:], 16, 32)
			if err != nil {
				return e
			}
			return string(rune(r))
		})
		if open != closeLen || !secretKey(key) {
			out.WriteString(s[pos:end])
			pos = end
			continue
		}
		esc := strings.Repeat(`\`, open)
		out.WriteString(s[pos:end] + esc + `"` + redacted + esc + `"`)
		pos = skipJSONValue(s, end, open)
	}
	out.WriteString(s[pos:])
	return out.String()
}

// skipJSONValue returns the end of the JSON value that starts at s[i], at an
// escaping depth whose quotes are preceded by n backslashes (0, 1, 3, 7…),
// or len(s) when the text ends first.
func skipJSONValue(s string, i, n int) int {
	// A quote of this depth follows c backslashes with c%(2n+2) == n; a
	// quote inside one of its strings follows 2n+1 (mod 2n+2).
	isQuote := func(j int) bool {
		c := 0
		for k := j - 1; k >= i && s[k] == '\\'; k-- {
			c++
		}
		return c%(2*n+2) == n
	}
	switch {
	case strings.HasPrefix(s[i:], strings.Repeat(`\`, n)+`"`):
		for j := i + n + 1; j < len(s); j++ {
			if s[j] == '"' && isQuote(j) {
				return j + 1
			}
		}
		return len(s)
	case i < len(s) && (s[i] == '{' || s[i] == '['):
		depth, inString := 0, false
		for j := i; j < len(s); j++ {
			switch s[j] {
			case '"':
				if isQuote(j) {
					inString = !inString
				}
			case '{', '[':
				if !inString {
					depth++
				}
			case '}', ']':
				if !inString {
					if depth--; depth == 0 {
						return j + 1
					}
				}
			}
		}
		return len(s)
	}
	j := i
	for j < len(s) && !strings.ContainsRune(scalarEndRune, rune(s[j])) {
		j++
	}
	return j
}
