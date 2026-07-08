package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"

	"github.com/go-resty/resty/v2"
)

// FrappeClient wraps a resty client configured for a specific Frappe site.
type FrappeClient struct {
	r *resty.Client
}

var insecureWarnOnce sync.Once

// warnIfInsecure prints a one-time stderr warning when credentials will be sent
// over a non-localhost plain-HTTP connection (M5). Localhost HTTP is left alone.
func warnIfInsecure(rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" {
		return
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "":
		return
	}
	insecureWarnOnce.Do(func() {
		fmt.Fprintf(os.Stderr,
			"warning: %s uses plain HTTP — credentials are transmitted unencrypted. Use https:// if the site supports it.\n",
			rawURL)
	})
}

// resourcePath builds an /api/resource path, URL-escaping every segment so that
// doctypes with spaces ("Sales Invoice") and names with "/", "#" or "?"
// ("INV/2025/001") cannot break out of their path segment (H2).
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

	r := resty.New().
		SetBaseURL(strings.TrimRight(cfg.URL, "/")).
		SetTimeout(30 * time.Second).
		SetRetryCount(2).
		SetRetryWaitTime(500 * time.Millisecond).
		// Only retry idempotent GETs on a transport error. Retrying a POST/PUT/
		// DELETE that timed out after the server already committed would create
		// duplicate documents or mis-report a completed write (H3).
		AddRetryCondition(func(resp *resty.Response, err error) bool {
			return err != nil && resp != nil && resp.Request != nil &&
				resp.Request.Method == http.MethodGet
		})

	// OAuth Bearer token takes priority; fall back to Frappe token auth, then
	// to a fresh username/password session login.
	switch {
	case cfg.AccessToken != "":
		r.SetHeader("Authorization", "Bearer "+cfg.AccessToken)
	case cfg.APIKey != "" && cfg.APISecret != "":
		r.SetHeader("Authorization", fmt.Sprintf("token %s:%s", cfg.APIKey, cfg.APISecret))
	case cfg.IsSessionAuth():
		sid, err := LoginPassword(ctx, cfg.URL, cfg.Username, cfg.Password)
		if err != nil {
			return nil, fmt.Errorf("session login: %w", err)
		}
		r.SetHeader("Cookie", "sid="+sid)
	default:
		// No usable credentials — fail loudly rather than issue anonymous
		// requests that mysteriously 403/return Guest data (L22).
		return nil, fmt.Errorf("site has no usable credentials (need an API key/secret, OAuth token, or username/password)")
	}

	// Frappe returns JSON
	r.SetHeader("Accept", "application/json")

	return &FrappeClient{r: r}, nil
}

// ListOptions contains query parameters for listing documents.
type ListOptions struct {
	Fields  []string // e.g. ["name", "creation"]
	Filters string   // raw JSON string, e.g. {"status":"Open"} or [["status","=","Open"]]
	Limit   int      // >0: page length; 0: Frappe default (20); <0: unlimited
	Start   int      // offset into the result set (limit_start); 0 = from the beginning
	OrderBy string   // e.g. "name asc"
}

// listResponse is the envelope Frappe wraps list results in.
type listResponse struct {
	Data    []map[string]interface{} `json:"data"`
	Message []map[string]interface{} `json:"message"` // v1 variant
}

// frappeErrorResponse represents a Frappe server-side error JSON body.
type frappeErrorResponse struct {
	Exception      string `json:"exception"`        // e.g. "frappe.exceptions.DataError: ..."
	ExcType        string `json:"exc_type"`         // e.g. "DataError"
	ServerMessages string `json:"_server_messages"` // JSON-encoded list of message objects
}

// serverMessage is a single entry inside _server_messages.
type serverMessage struct {
	Message string `json:"message"`
	Title   string `json:"title"`
}

var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

// stripHTML removes HTML tags Frappe embeds in translated messages and trims.
func stripHTML(s string) string {
	return strings.TrimSpace(htmlTagRE.ReplaceAllString(s, ""))
}

// frappeUserMessage extracts the human-facing message(s) from a Frappe error
// body: every entry of _server_messages (joined), HTML-stripped, falling back
// to the exception text. Returns "" if nothing usable is found (L13).
func frappeUserMessage(body []byte) string {
	var fe frappeErrorResponse
	if err := json.Unmarshal(body, &fe); err != nil {
		return ""
	}

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
		if parts := strings.SplitN(fe.Exception, ": ", 2); len(parts) == 2 {
			return stripHTML(parts[1])
		}
		return stripHTML(fe.Exception)
	}
	return ""
}

// parseFrappeError turns a raw Frappe error body into a human-friendly error.
func parseFrappeError(statusCode int, body []byte) error {
	var fe frappeErrorResponse
	if err := json.Unmarshal(body, &fe); err != nil {
		return fmt.Errorf("server error %d: %s", statusCode, strings.TrimSpace(string(body)))
	}
	msg := frappeUserMessage(body)
	excType := fe.ExcType
	if excType == "" {
		excType = "ServerError"
	}
	if msg != "" {
		return fmt.Errorf("[%s] %s (HTTP %d)", excType, msg, statusCode)
	}
	return fmt.Errorf("server error %d (%s)", statusCode, excType)
}

// apiError converts a >=400 response into a user-facing error. For statuses
// with an actionable hint it combines the hint with the specific Frappe message
// (so 401/403/404 no longer discard the server's explanation, L18); otherwise
// it defers to parseFrappeError.
func apiError(resp *resty.Response, hints map[int]string) error {
	code := resp.StatusCode()
	msg := frappeUserMessage(resp.Body())
	if hint, ok := hints[code]; ok {
		if msg != "" {
			return fmt.Errorf("%s — %s (HTTP %d)", hint, msg, code)
		}
		return fmt.Errorf("%s (HTTP %d)", hint, code)
	}
	return parseFrappeError(code, resp.Body())
}

func authHint() string {
	return "authentication failed (401): check your credentials or run 'ffc init' to reconfigure"
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
	// >0 explicit page length, 0 leaves the Frappe default (M12).
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

	resp, err := c.r.R().SetContext(ctx).SetQueryParams(params).Get(resourcePath(doctype))
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, apiError(resp, map[int]string{
			http.StatusUnauthorized: authHint(),
			http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have read access to %s", doctype),
			http.StatusNotFound:     fmt.Sprintf("doctype %q not found on this site (404)", doctype),
		})
	}

	// Frappe v14/v15 wraps the list in "data", older in "message".
	var result listResponse
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if result.Data != nil {
		return result.Data, nil
	}
	if result.Message != nil {
		return result.Message, nil
	}
	// Never return nil: `list-docs --json` must emit [] (not null) so `| jq '.[]'`
	// works even for an empty or unrecognized 2xx envelope (L13).
	return []map[string]interface{}{}, nil
}

// CreateDoc posts a new document and returns the created document fields.
func (c *FrappeClient) CreateDoc(ctx context.Context, doctype string, data map[string]interface{}) (map[string]interface{}, error) {
	resp, err := c.r.R().SetContext(ctx).SetBody(data).Post(resourcePath(doctype))
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, apiError(resp, map[int]string{
			http.StatusUnauthorized: authHint(),
			http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have write access to %s", doctype),
			http.StatusNotFound:     fmt.Sprintf("doctype %q not found on this site (404)", doctype),
		})
	}

	var result struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}

// UpdateDoc sends a PUT request to update an existing document and returns the updated fields.
func (c *FrappeClient) UpdateDoc(ctx context.Context, doctype, name string, data map[string]interface{}) (map[string]interface{}, error) {
	resp, err := c.r.R().SetContext(ctx).SetBody(data).Put(resourcePath(doctype, name))
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, apiError(resp, map[int]string{
			http.StatusUnauthorized: authHint(),
			http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have write access to %s", doctype),
			http.StatusNotFound:     fmt.Sprintf("%s %q not found (404)", doctype, name),
		})
	}

	var result struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}

// DeleteDoc sends a DELETE request to remove a document. Returns nil on success.
func (c *FrappeClient) DeleteDoc(ctx context.Context, doctype, name string) error {
	resp, err := c.r.R().SetContext(ctx).Delete(resourcePath(doctype, name))
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return apiError(resp, map[int]string{
			http.StatusUnauthorized: authHint(),
			http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have write access to %s", doctype),
			http.StatusNotFound:     fmt.Sprintf("%s %q not found (404)", doctype, name),
		})
	}
	return nil
}

// CallMethod invokes /api/method/<method>. When httpGET is true the args are
// sent as query parameters via GET (for methods whitelisted GET-only, L16);
// otherwise they are POSTed as a JSON body. Returns the "message" field.
func (c *FrappeClient) CallMethod(ctx context.Context, method string, args map[string]interface{}, httpGET bool) (interface{}, error) {
	req := c.r.R().SetContext(ctx)
	endpoint := "/api/method/" + url.PathEscape(method)

	var resp *resty.Response
	var err error
	if httpGET {
		if len(args) > 0 {
			qp := make(map[string]string, len(args))
			for k, v := range args {
				qp[k] = fmt.Sprintf("%v", v)
			}
			req = req.SetQueryParams(qp)
		}
		resp, err = req.Get(endpoint)
	} else {
		if len(args) > 0 {
			req = req.SetBody(args)
		}
		resp, err = req.Post(endpoint)
	}
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, apiError(resp, map[int]string{
			http.StatusUnauthorized: authHint(),
			http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have access to method %s", method),
			http.StatusNotFound:     fmt.Sprintf("method %q not found (404): check the method name and that it is whitelisted", method),
		})
	}

	var result struct {
		Message interface{} `json:"message"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Message, nil
}

// GetCount returns the number of documents matching the given doctype and filters.
func (c *FrappeClient) GetCount(ctx context.Context, doctype, filters string) (int, error) {
	body := map[string]interface{}{"doctype": doctype}
	if filters != "" {
		body["filters"] = filters
	}

	resp, err := c.r.R().SetContext(ctx).SetBody(body).Post("/api/method/frappe.client.get_count")
	if err != nil {
		return 0, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return 0, apiError(resp, map[int]string{
			http.StatusUnauthorized: authHint(),
			http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have read access to %s", doctype),
		})
	}

	var result struct {
		Message int `json:"message"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return 0, fmt.Errorf("parsing response: %w", err)
	}
	return result.Message, nil
}

// Ping checks server connectivity by calling GET /api/method/frappe.ping.
func (c *FrappeClient) Ping(ctx context.Context) (string, error) {
	resp, err := c.r.R().SetContext(ctx).Get("/api/method/frappe.ping")
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return "", apiError(resp, map[int]string{http.StatusUnauthorized: authHint()})
	}

	var result struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
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

	resp, err := c.r.R().SetContext(ctx).SetBody(body).Post("/api/method/frappe.desk.query_report.run")
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, apiError(resp, map[int]string{
			http.StatusUnauthorized: authHint(),
			http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have access to report %q", reportName),
			http.StatusNotFound:     fmt.Sprintf("report %q not found (404)", reportName),
		})
	}

	var result struct {
		Message map[string]interface{} `json:"message"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Message, nil
}

// GetDoc calls GET /api/resource/<doctype>/<name> and returns the document fields.
func (c *FrappeClient) GetDoc(ctx context.Context, doctype, name string) (map[string]interface{}, error) {
	resp, err := c.r.R().SetContext(ctx).Get(resourcePath(doctype, name))
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, apiError(resp, map[int]string{
			http.StatusUnauthorized: authHint(),
			http.StatusForbidden:    fmt.Sprintf("permission denied (403): your user may not have read access to %s", doctype),
			http.StatusNotFound:     fmt.Sprintf("%s %q not found (404)", doctype, name),
		})
	}

	var result struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return result.Data, nil
}
