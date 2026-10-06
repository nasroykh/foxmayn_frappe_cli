package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"

	"github.com/go-resty/resty/v2"
)

// FrappeClient wraps a resty client configured for a specific Frappe site. It
// is safe for concurrent use: the only mutable state (a session id or an
// OAuth access token) is guarded by mu and attached per request, never by
// mutating the shared resty client.
type FrappeClient struct {
	baseURL string // the site URL, without a trailing slash
	r       *resty.Client
	raw     *resty.Client // r without retries, for Raw: a streamed body cannot be retried

	// loginSem (one slot) serialises re-logins (session sites) and token
	// refreshes (OAuth sites), so that concurrent requests rejected for the
	// same reason log in or refresh once. It is a channel, not a mutex, so a
	// waiter whose context ends stops waiting (lockLogin).
	loginSem chan struct{}
	mu       sync.Mutex

	// Username/password sites only.
	session    *sessionCreds
	sid        string
	loggedInAt time.Time

	// OAuth sites only. token is the current access token; refresher (nil:
	// no refresh) replaces it after a 401. A definitive refresh failure
	// (permanentRefreshError) is remembered with the token it was for, so
	// later requests rejected with that token do not each spend a refresh.
	bearer      bool
	token       string
	refresher   TokenRefresher
	failedToken string
	failedErr   error
}

// TokenRefresher returns a new access token for an OAuth client whose
// access token rejected was refused with a 401. It may return a token
// another process obtained in the meantime instead of refreshing again. ctx
// is the context of the rejected request.
type TokenRefresher func(ctx context.Context, rejected string) (string, error)

// SetTokenRefresher makes an OAuth client refresh its access token with f
// when the site rejects it, and repeat the request once. It must be called
// before the client is used; it does nothing for other clients.
func (c *FrappeClient) SetTokenRefresher(f TokenRefresher) {
	if c.bearer {
		c.refresher = f
	}
}

type sessionCreds struct{ url, user, pwd string }

// reloginAfter is how old a session must be before a 401/403 can trigger a
// transparent re-login. A fresh session that is refused is a genuine
// permission error, so it is not retried.
const reloginAfter = time.Minute

// resourcePath builds an /api/resource path, URL-escaping every segment so that
// doctypes with spaces ("Sales Invoice") and names with "/", "#" or "?"
// ("INV/2025/001") cannot break out of their path segment.
func resourcePath(doctype string, name ...string) string {
	p := "/api/resource/" + url.PathEscape(doctype)
	for _, n := range name {
		p += "/" + url.PathEscape(n)
	}
	return p
}

// New creates a FrappeClient from the given site config. For username/password
// (session-cookie) sites, this performs a live login call against the Frappe
// site, so it can fail. ctx bounds that login call.
func New(ctx context.Context, cfg *config.SiteConfig) (*FrappeClient, error) {
	warnIfInsecure(cfg.URL)

	r := newResty(cfg.URL).
		SetRetryCount(2).
		SetRetryWaitTime(500*time.Millisecond).
		SetRetryMaxWaitTime(maxRetryAfter).
		SetRetryAfter(retryAfter).
		AddRetryCondition(retryableGET).
		AddRetryHook(debugRetry).
		SetPreRequestHook(setStreamedBody).
		SetHeader("Accept", "application/json")
	c := &FrappeClient{r: r, baseURL: strings.TrimRight(cfg.URL, "/"), loginSem: make(chan struct{}, 1)}

	// OAuth Bearer token takes priority; fall back to Frappe token auth, then
	// to a fresh username/password session login.
	switch {
	case cfg.AccessToken != "":
		// Attached per request (send): a refresh replaces it mid-run.
		c.bearer, c.token = true, cfg.AccessToken
	case cfg.APIKey != "" && cfg.APISecret != "":
		r.SetHeader("Authorization", fmt.Sprintf("token %s:%s", cfg.APIKey, cfg.APISecret))
	case cfg.IsSessionAuth():
		c.session = &sessionCreds{url: cfg.URL, user: cfg.Username, pwd: cfg.Password}
		if err := c.login(ctx); err != nil {
			return nil, err
		}
	default:
		// No usable credentials — fail loudly rather than issue anonymous
		// requests that mysteriously 403/return Guest data.
		return nil, fmt.Errorf("site has no usable credentials (need an API key/secret, OAuth token, or username/password)")
	}
	c.raw = r.Clone().SetRetryCount(0)
	return c, nil
}

func (c *FrappeClient) login(ctx context.Context) error {
	sid, err := LoginPassword(ctx, c.session.url, c.session.user, c.session.pwd)
	if err != nil {
		return fmt.Errorf("session login: %w", err)
	}
	c.mu.Lock()
	c.sid, c.loggedInAt = sid, time.Now()
	c.mu.Unlock()
	return nil
}

// relogin handles a 401/403 received with session id used. It reports true
// when the request should be repeated with the current session id: another
// request already logged in again, or this call did. It reports false (the
// rejection stands) when the session is too young for expiry to explain it,
// when the session is still valid (a genuine permission error), or when the
// login fails.
func (c *FrappeClient) relogin(ctx context.Context, used string) bool {
	if !c.lockLogin(ctx) {
		return false
	}
	defer c.unlockLogin()

	c.mu.Lock()
	current, age := c.sid, time.Since(c.loggedInAt)
	c.mu.Unlock()
	if current != used {
		return true
	}
	if age < reloginAfter || c.sessionValid(ctx, used) {
		return false
	}
	return c.login(ctx) == nil
}

// sessionValid reports whether sid still belongs to a logged-in user, so a
// permission error is not mistaken for an expired session (which would open
// a new server-side session on every denied call).
func (c *FrappeClient) sessionValid(ctx context.Context, sid string) bool {
	var res struct {
		Message string `json:"message"`
	}
	resp, err := c.r.R().SetContext(ctx).SetHeader("Cookie", "sid="+sid).
		Get("/api/method/frappe.auth.get_logged_user")
	if err != nil || resp.StatusCode() != http.StatusOK || json.Unmarshal(resp.Body(), &res) != nil {
		return false
	}
	return res.Message != "" && res.Message != "Guest"
}

// Close ends the server-side session of a username/password client so the
// process does not leave it behind on the server. It is a no-op for
// token-authenticated clients. A failure is ignored: the session then
// expires on its own.
func (c *FrappeClient) Close(ctx context.Context) {
	if c.session == nil {
		return
	}
	c.mu.Lock()
	sid := c.sid
	c.mu.Unlock()
	_ = Logout(ctx, c.session.url, sid)
}

// CloseQuietly is Close bounded by its own short timeout, for deferred cleanup
// that must still run after the command's context was cancelled (Ctrl+C).
func (c *FrappeClient) CloseQuietly() {
	if c.session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Close(ctx)
}

// lockLogin takes loginSem, or gives up when ctx ends first: a refresh can
// hold it for up to config.MaxLockHold (the config lock plus the token
// request), longer than a waiter's --timeout.
func (c *FrappeClient) lockLogin(ctx context.Context) bool {
	select {
	case c.loginSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *FrappeClient) unlockLogin() { <-c.loginSem }

// ErrNoRefreshToken is a TokenRefresher failure for a site that stores no
// refresh token. Like a token endpoint refusal, it is definitive.
var ErrNoRefreshToken = errors.New("no refresh token is stored")

var (
	errNoNewToken     = errors.New("no new access token")
	errRefreshRefused = errors.New("the site refused the refreshed access token too")
)

// permanentRefreshError reports whether a refresh failure will not go away
// by trying again: the token endpoint refused the grant or the client
// (oauthlib answers 400 invalid_grant or 401 invalid_client; Frappe answers
// a revoked refresh token with 403 PermissionError, because
// validate_refresh_token's get_doc raises DoesNotExistError and
// handle_does_not_exist_error, permissions.py:926, turns it into a
// PermissionError for Guest), there is no refresh token, or the new token
// is refused as well. A 5xx, a network error, a timeout or a busy config
// lock is transient and not remembered, so a long-lived client (the MCP
// server) recovers once the cause is gone.
func permanentRefreshError(err error) bool {
	var e *APIError
	if errors.As(err, &e) {
		return e.Status == http.StatusBadRequest || e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
	}
	return errors.Is(err, ErrNoRefreshToken) || errors.Is(err, errNoNewToken) || errors.Is(err, errRefreshRefused)
}

// readOnlyMethod reports whether an HTTP method cannot change anything.
func readOnlyMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// refreshToken handles a 401 received by a method request with access token
// used. It reports true when the request should be repeated with the
// current token: another request already refreshed, or this call did. It
// reports false with a nil error (the rejection stands) when the client
// cannot refresh, or when the token used is still valid: then the method
// itself raised the 401 after it ran, and repeating it could run a write
// twice. A write is therefore repeated after another request's refresh only
// once its own token is shown refused. A failed refresh is the error; a
// definitive one is returned to every request rejected with the same token.
func (c *FrappeClient) refreshToken(ctx context.Context, method, used string) (bool, error) {
	if !c.lockLogin(ctx) {
		return false, ctx.Err()
	}
	defer c.unlockLogin()

	c.mu.Lock()
	current, failed, failedErr := c.token, c.failedToken, c.failedErr
	c.mu.Unlock()
	switch {
	case current != used:
		return readOnlyMethod(method) || c.tokenRefused(ctx, used), nil
	case c.refresher == nil:
		return false, nil
	case failed == used:
		return false, failedErr
	case !c.tokenRefused(ctx, used):
		return false, nil
	}
	tok, err := c.refresher(ctx, used)
	if err == nil && (tok == "" || tok == used) {
		err = errNoNewToken
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil && permanentRefreshError(err) {
			c.failedToken, c.failedErr = used, err
		}
		return false, err
	}
	c.token = tok
	return true, nil
}

// refreshedRefused handles a 401 received again with token, the token a
// refresh just produced. When the site refuses that token too (a disabled
// user or narrowed scopes: Frappe still refreshes, since validate_refresh_token
// checks only that the token is Active), it is remembered as failed, so
// later requests do not each refresh again, and the error is returned. nil
// when the token is fine (the method raised the 401) or was replaced.
func (c *FrappeClient) refreshedRefused(ctx context.Context, token string) error {
	if !c.lockLogin(ctx) {
		return nil
	}
	defer c.unlockLogin()
	c.mu.Lock()
	current, failed, failedErr := c.token, c.failedToken, c.failedErr
	c.mu.Unlock()
	switch {
	case failed == token:
		return failedErr
	case current != token || !c.tokenRefused(ctx, token):
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failedToken, c.failedErr = token, errRefreshRefused
	return errRefreshRefused
}

// tokenRefused reports whether the site refuses token outright: a request
// with it is turned away by authentication (validate_auth) before any method
// runs. Only a 401 counts. A network error or a 5xx says nothing about the
// token, and refreshing then would let a write that may have run repeat.
func (c *FrappeClient) tokenRefused(ctx context.Context, token string) bool {
	resp, err := c.r.R().SetContext(ctx).SetHeader("Authorization", "Bearer "+token).
		Get("/api/method/frappe.auth.get_logged_user")
	return err == nil && resp.StatusCode() == http.StatusUnauthorized
}

// refreshFailed is the error for a request rejected with an access token
// that could not be refreshed: the 401, with the refresh failure as hint.
func refreshFailed(resp *resty.Response, cause error) error {
	body := resp.Body()
	if b := resp.RawBody(); b != nil {
		if len(body) == 0 {
			// A streamed (Raw) response has not been read yet.
			body, _ = io.ReadAll(io.LimitReader(b, 64<<10))
		}
		_ = b.Close()
	}
	hint := fmt.Sprintf("authentication failed (401): the OAuth access token was rejected and refreshing it failed (%v); run 'ffc site add --oauth' to sign in again", cause)
	return responseError(http.StatusUnauthorized, body, map[int]string{http.StatusUnauthorized: hint})
}

// send executes a request built by build on r, attaching the session cookie
// or the OAuth access token, and repeats it once after a fresh login when
// the session was rejected, or after a token refresh when the token was.
func (c *FrappeClient) send(ctx context.Context, r *resty.Client, method, path string, build func(*resty.Request)) (*resty.Response, error) {
	var sid, tok string // the session id or access token the last attempt used
	attempt := func() (*resty.Response, error) {
		req := r.R().SetContext(ctx)
		build(req)
		if holdBack(ctx, method) {
			return nil, plan(r, req, method, path)
		}
		c.mu.Lock()
		sid, tok = c.sid, c.token
		c.mu.Unlock()
		if c.session != nil {
			req.SetHeader("Cookie", "sid="+sid)
		}
		if c.bearer {
			req.SetHeader("Authorization", "Bearer "+tok)
		}
		resp, err := req.Execute(method, path)
		if err != nil {
			return nil, requestError(err)
		}
		return resp, nil
	}
	discard := func(resp *resty.Response) {
		if b := resp.RawBody(); b != nil {
			_ = b.Close()
		}
	}

	resp, err := attempt()
	if err != nil {
		return nil, err
	}
	code := resp.StatusCode()
	switch {
	case c.session != nil && (code == http.StatusUnauthorized || code == http.StatusForbidden) && c.relogin(ctx, sid):
		// The request was rejected before it ran, so repeating it (even a
		// write) cannot duplicate anything.
		discard(resp)
		return attempt()
	case c.bearer && code == http.StatusUnauthorized:
		// Frappe checks the bearer token in validate_auth (frappe/auth.py)
		// before the method runs, and answers an expired or revoked token
		// with AuthenticationError (401). refreshToken refreshes only when
		// the token itself is refused, so the rejected request did not run
		// and repeating it once, writes included, cannot duplicate anything.
		// The body is a byte slice or a value resty encodes again, so it is
		// sent intact. A second 401 is returned, never retried: never a loop.
		retry, err := c.refreshToken(ctx, method, tok)
		if err != nil {
			if ctx.Err() != nil {
				discard(resp)
				return nil, &TransportError{ctx.Err()}
			}
			return nil, refreshFailed(resp, err)
		}
		if retry {
			discard(resp)
			resp, err = attempt()
			if err != nil || resp.StatusCode() != http.StatusUnauthorized {
				return resp, err
			}
			if err := c.refreshedRefused(ctx, tok); err != nil {
				return nil, refreshFailed(resp, err)
			}
		}
	}
	return resp, nil
}

// do executes a request and decodes the JSON response into out (if non-nil).
func (c *FrappeClient) do(ctx context.Context, method, path string, body interface{}, query map[string]string, hints map[int]string, out interface{}) error {
	resp, err := c.send(ctx, c.r, method, path, func(req *resty.Request) {
		if body != nil {
			req.SetBody(body)
		}
		if len(query) > 0 {
			req.SetQueryParams(query)
		}
	})
	if err != nil {
		return err
	}
	if resp.StatusCode() >= 400 {
		return apiError(resp, hints)
	}
	if out == nil {
		// The result is not needed, but a 2xx HTML page (a login proxy, a
		// wrong URL) must not pass for success: require JSON unless empty.
		if len(resp.Body()) == 0 {
			return nil
		}
		var ignored json.RawMessage
		return decodeJSON(resp, &ignored)
	}
	return decodeJSON(resp, out)
}

// decodeJSON unmarshals a 2xx body, reporting an HTML or other non-JSON page
// (an SSO/login proxy, a wrong URL) clearly instead of "invalid character '<'".
// Numbers decode as json.Number: integers above 2^53 keep their precision,
// and the literal tells an Int field (2025) from a Float or Currency (1500.0).
func decodeJSON(resp *resty.Response, out interface{}) error {
	body := resp.Body()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	err := dec.Decode(out)
	if err == nil && dec.More() {
		err = fmt.Errorf("unexpected data after the JSON value")
	}
	if err != nil {
		ct := resp.Header().Get("Content-Type")
		if ct != "" && !strings.Contains(ct, "json") {
			return fmt.Errorf("unexpected non-JSON response (%s, HTTP %d): check the site URL: %s", ct, resp.StatusCode(), snippet(body))
		}
		return fmt.Errorf("parsing response: %w", err)
	}
	return nil
}

// ListOptions contains query parameters for listing documents.
type ListOptions struct {
	Fields  []string // e.g. ["name", "creation"]
	Filters string   // raw JSON string, e.g. {"status":"Open"} or [["status","=","Open"]]
	Limit   int      // >0: page length; 0: Frappe default (20); <0: unlimited
	Start   int      // offset into the result set (limit_start); 0 = from the beginning
	OrderBy string   // e.g. "name asc"
}

// frappeErrorResponse represents a Frappe server-side error JSON body.
type frappeErrorResponse struct {
	Exception      string `json:"exception"`        // e.g. "frappe.exceptions.DataError: ..."
	ExcType        string `json:"exc_type"`         // e.g. "DataError"
	ServerMessages string `json:"_server_messages"` // JSON-encoded list of message objects
	Message        any    `json:"message"`          // some endpoints (login, whitelisted methods) put the error here
}

// serverMessage is a single entry inside _server_messages.
type serverMessage struct {
	Message string `json:"message"`
}

// userMessage extracts the human-facing message(s) from a parsed Frappe error
// body: every entry of _server_messages (joined), falling back to the
// exception text, then to a plain string "message". "" if nothing usable.
func (fe *frappeErrorResponse) userMessage() string {
	if fe.ServerMessages != "" {
		// _server_messages is a JSON-encoded array of JSON-encoded objects.
		var rawMsgs []string
		if err := json.Unmarshal([]byte(fe.ServerMessages), &rawMsgs); err == nil {
			var msgs []string
			for _, raw := range rawMsgs {
				var sm serverMessage
				if err := json.Unmarshal([]byte(raw), &sm); err == nil && sm.Message != "" {
					if m := stripHTML(sm.Message); m != "" {
						msgs = append(msgs, m)
					}
				}
			}
			if len(msgs) > 0 {
				return strings.Join(msgs, "; ")
			}
		}
	}
	if fe.Exception != "" {
		// "frappe.exceptions.DataError: Field not permitted ..." → keep the tail.
		if _, tail, ok := strings.Cut(fe.Exception, ": "); ok {
			return stripHTML(tail)
		}
		return stripHTML(fe.Exception)
	}
	if s, ok := fe.Message.(string); ok {
		return stripHTML(s)
	}
	return ""
}

// apiError converts a >=400 response into a user-facing error. For statuses
// with an actionable hint it combines the hint with the specific Frappe
// message; otherwise it reports the exception type and message. Non-JSON
// bodies (proxy error pages) are truncated and sanitized.
func apiError(resp *resty.Response, hints map[int]string) error {
	return responseError(resp.StatusCode(), resp.Body(), hints)
}

// ResponseError converts an error response read by a Raw caller into the
// same *APIError the other client methods return.
func ResponseError(status int, body []byte) error {
	return responseError(status, body, map[int]string{http.StatusUnauthorized: authHint})
}

func responseError(code int, body []byte, hints map[int]string) error {
	var fe frappeErrorResponse
	isJSON := json.Unmarshal(body, &fe) == nil
	msg := ""
	if isJSON {
		msg = fe.userMessage()
	}

	e := &APIError{Status: code, ExcType: fe.ExcType}
	if hint, ok := hints[code]; ok {
		if msg != "" {
			e.Message = fmt.Sprintf("%s — %s (HTTP %d)", hint, msg, code)
		} else {
			e.Message = fmt.Sprintf("%s (HTTP %d)", hint, code)
		}
		return e
	}
	if !isJSON {
		e.ExcType = ""
		if s := snippet(body); s != "" {
			e.Message = fmt.Sprintf("server error %d: %s", code, s)
		} else {
			e.Message = fmt.Sprintf("server error %d", code)
		}
		return e
	}
	excType := fe.ExcType
	if excType == "" {
		excType = "ServerError"
	}
	if msg != "" {
		e.Message = fmt.Sprintf("[%s] %s (HTTP %d)", excType, msg, code)
	} else {
		e.Message = fmt.Sprintf("server error %d (%s)", code, excType)
	}
	return e
}

const authHint = "authentication failed (401): check your credentials or run 'ffc init' to reconfigure"

func readHints(doctype string) map[int]string {
	return map[int]string{
		http.StatusUnauthorized: authHint,
		http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have read access to %s", doctype),
		http.StatusNotFound:     fmt.Sprintf("doctype %q not found on this site (404)", doctype),
	}
}

func docHints(doctype, name, access string) map[int]string {
	return map[int]string{
		http.StatusUnauthorized: authHint,
		http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have %s access to %s", access, doctype),
		http.StatusNotFound:     fmt.Sprintf("%s %q not found (404)", doctype, name),
	}
}

// dataEnvelope is the {"data": {...}} wrapper of /api/resource document calls.
type dataEnvelope struct {
	Data map[string]interface{} `json:"data"`
}

func (e dataEnvelope) doc() (map[string]interface{}, error) {
	if e.Data == nil {
		return nil, fmt.Errorf("unexpected response: no document in the server reply")
	}
	return e.Data, nil
}

// GetList calls GET /api/resource/<doctype> and returns the document rows.
// It always returns a non-nil slice on success (empty when there are no rows).
func (c *FrappeClient) GetList(ctx context.Context, doctype string, opts ListOptions) ([]map[string]interface{}, error) {
	params := map[string]string{}
	if len(opts.Fields) > 0 {
		fieldsJSON, err := json.Marshal(opts.Fields)
		if err != nil {
			return nil, fmt.Errorf("encoding fields: %w", err)
		}
		params["fields"] = string(fieldsJSON)
	}
	if opts.Filters != "" {
		params["filters"] = opts.Filters
	}
	// Limit: <0 unlimited (Frappe treats limit_page_length=0 as "all"),
	// >0 explicit page length, 0 leaves the Frappe default.
	if opts.Limit < 0 {
		params["limit_page_length"] = "0"
	} else if opts.Limit > 0 {
		params["limit_page_length"] = strconv.Itoa(opts.Limit)
	}
	if opts.Start > 0 {
		params["limit_start"] = strconv.Itoa(opts.Start)
	}
	if opts.OrderBy != "" {
		params["order_by"] = opts.OrderBy
	}

	// Frappe v14/v15 wraps the list in "data", older versions in "message".
	var result struct {
		Data    []map[string]interface{} `json:"data"`
		Message []map[string]interface{} `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, resourcePath(doctype), nil, params, readHints(doctype), &result); err != nil {
		return nil, err
	}
	switch {
	case result.Data != nil:
		return result.Data, nil
	case result.Message != nil:
		return result.Message, nil
	}
	// Never return nil: `list-docs --json` must emit [] (not null).
	return []map[string]interface{}{}, nil
}

// GetDoc calls GET /api/resource/<doctype>/<name> and returns the document fields.
func (c *FrappeClient) GetDoc(ctx context.Context, doctype, name string) (map[string]interface{}, error) {
	var env dataEnvelope
	if err := c.do(ctx, http.MethodGet, resourcePath(doctype, name), nil, nil, docHints(doctype, name, "read"), &env); err != nil {
		return nil, c.missingDocType(ctx, doctype, err)
	}
	return env.doc()
}

// CreateDoc posts a new document and returns the created document fields.
func (c *FrappeClient) CreateDoc(ctx context.Context, doctype string, data map[string]interface{}) (map[string]interface{}, error) {
	hints := readHints(doctype)
	hints[http.StatusForbidden] = fmt.Sprintf("permission denied (403): your user may not have create access to %s", doctype)
	var env dataEnvelope
	if err := c.do(ctx, http.MethodPost, resourcePath(doctype), data, nil, hints, &env); err != nil {
		return nil, c.missingDocType(ctx, doctype, err)
	}
	return env.doc()
}

// UpdateDoc sends a PUT request to update an existing document and returns the updated fields.
func (c *FrappeClient) UpdateDoc(ctx context.Context, doctype, name string, data map[string]interface{}) (map[string]interface{}, error) {
	var env dataEnvelope
	if err := c.do(ctx, http.MethodPut, resourcePath(doctype, name), data, nil, docHints(doctype, name, "write"), &env); err != nil {
		return nil, c.missingDocType(ctx, doctype, err)
	}
	return env.doc()
}

// missingDocType turns the 500 ImportError Frappe returns for a document of
// a DocType that does not exist (it looks for the controller in Core) into
// a not-found error. The message alone cannot tell it from a broken
// controller, and sites that hide tracebacks send none, so it asks the
// DocType's list, which is a 404 only when the DocType has no record.
func (c *FrappeClient) missingDocType(ctx context.Context, doctype string, err error) error {
	var e *APIError
	if !errors.As(err, &e) || e.Status != http.StatusInternalServerError || e.ExcType != "ImportError" {
		return err
	}
	probe := c.do(ctx, http.MethodGet, resourcePath(doctype), nil,
		map[string]string{"fields": `["name"]`, "limit_page_length": "1"}, nil, nil)
	var pe *APIError
	if errors.As(probe, &pe) && pe.Status == http.StatusNotFound {
		e.MissingDocType = true
		e.Message = fmt.Sprintf("doctype %q not found on this site (%s, HTTP %d)", doctype, e.ExcType, e.Status)
	}
	return err
}

// DeleteDoc sends a DELETE request to remove a document. Returns nil on success.
func (c *FrappeClient) DeleteDoc(ctx context.Context, doctype, name string) error {
	return c.do(ctx, http.MethodDelete, resourcePath(doctype, name), nil, nil, docHints(doctype, name, "delete"), nil)
}

// CallMethod invokes /api/method/<method> and returns its "message" field.
// See CallMethodFull.
func (c *FrappeClient) CallMethod(ctx context.Context, method string, args map[string]interface{}, httpGET bool) (interface{}, error) {
	env, err := c.CallMethodFull(ctx, method, args, httpGET)
	if err != nil {
		return nil, err
	}
	return env["message"], nil
}

// CallMethodFull invokes /api/method/<method> and returns the whole response
// object: "message" plus whatever else the method sets at the top level
// (desk methods return "docs", "docinfo", "_server_messages"). When httpGET
// is true the args are sent as query parameters via GET (for methods
// whitelisted GET-only): strings are sent as-is and every other value
// JSON-encoded, which is what Frappe's argument parsing expects (Go's %v
// would send "map[a:1]"). Otherwise they are POSTed as a JSON body.
func (c *FrappeClient) CallMethodFull(ctx context.Context, method string, args map[string]interface{}, httpGET bool) (map[string]interface{}, error) {
	endpoint := "/api/method/" + url.PathEscape(method)
	hints := map[int]string{
		http.StatusUnauthorized: authHint,
		http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have access to method %s", method),
		http.StatusNotFound:     fmt.Sprintf("method %q not found (404): check the method name and that it is whitelisted", method),
	}

	var result map[string]interface{}
	var err error
	if httpGET {
		var qp map[string]string
		if qp, err = QueryArgs(args); err != nil {
			return nil, err
		}
		err = c.do(ctx, http.MethodGet, endpoint, nil, qp, hints, &result)
	} else {
		var body interface{}
		if len(args) > 0 {
			body = args
		}
		err = c.do(ctx, http.MethodPost, endpoint, body, nil, hints, &result)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// QueryArgs encodes method arguments as query parameters: strings as-is,
// other values as JSON, nil values left out.
func QueryArgs(args map[string]interface{}) (map[string]string, error) {
	qp := make(map[string]string, len(args))
	for k, v := range args {
		switch val := v.(type) {
		case nil:
			continue
		case string:
			qp[k] = val
		default:
			b, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("encoding argument %q: %w", k, err)
			}
			qp[k] = string(bytes.TrimSpace(b))
		}
	}
	return qp, nil
}

// GetCount returns the number of documents matching the given doctype and filters.
func (c *FrappeClient) GetCount(ctx context.Context, doctype, filters string) (int, error) {
	body := map[string]interface{}{"doctype": doctype}
	if filters != "" {
		body["filters"] = filters
	}
	hints := readHints(doctype)
	delete(hints, http.StatusNotFound) // get_count reports a missing doctype differently
	var result struct {
		Message int `json:"message"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/method/frappe.client.get_count", body, nil, hints, &result); err != nil {
		return 0, err
	}
	return result.Message, nil
}

// Ping checks server connectivity by calling GET /api/method/frappe.ping.
func (c *FrappeClient) Ping(ctx context.Context) (string, error) {
	var result struct {
		Message string `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/method/frappe.ping", nil, nil, map[int]string{http.StatusUnauthorized: authHint}, &result); err != nil {
		return "", err
	}
	return result.Message, nil
}

// RunReport executes a Frappe query report and returns its columns and data rows.
func (c *FrappeClient) RunReport(ctx context.Context, reportName string, filters map[string]interface{}) (map[string]interface{}, error) {
	filtersJSON := "{}"
	if len(filters) > 0 {
		b, err := json.Marshal(filters)
		if err != nil {
			return nil, fmt.Errorf("encoding filters: %w", err)
		}
		filtersJSON = string(b)
	}
	body := map[string]interface{}{
		"report_name":            reportName,
		"filters":                filtersJSON,
		"ignore_prepared_report": 1,
	}
	hints := map[int]string{
		http.StatusUnauthorized: authHint,
		http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have access to report %q", reportName),
		http.StatusNotFound:     fmt.Sprintf("report %q not found (404)", reportName),
	}
	var result struct {
		Message map[string]interface{} `json:"message"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/method/frappe.desk.query_report.run", body, nil, hints, &result); err != nil {
		return nil, err
	}
	if result.Message == nil {
		return nil, fmt.Errorf("unexpected response: report %q returned no result", reportName)
	}
	return result.Message, nil
}
