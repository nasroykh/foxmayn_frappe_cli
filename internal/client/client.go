package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"

	"github.com/go-resty/resty/v2"
)

// FrappeClient wraps a resty client configured for a specific Frappe site.
type FrappeClient struct {
	r *resty.Client
}

// New creates a FrappeClient from the given site config.
func New(cfg *config.SiteConfig) *FrappeClient {
	r := resty.New().
		SetBaseURL(strings.TrimRight(cfg.URL, "/")).
		SetTimeout(30 * time.Second).
		SetRetryCount(2).
		SetRetryWaitTime(500 * time.Millisecond)

	// OAuth Bearer token takes priority; fall back to Frappe token auth.
	if cfg.AccessToken != "" {
		r.SetHeader("Authorization", "Bearer "+cfg.AccessToken)
	} else if cfg.APIKey != "" && cfg.APISecret != "" {
		r.SetHeader("Authorization", fmt.Sprintf("token %s:%s", cfg.APIKey, cfg.APISecret))
	}

	// Frappe returns JSON
	r.SetHeader("Accept", "application/json")

	return &FrappeClient{r: r}
}

// ListOptions contains query parameters for listing documents.
type ListOptions struct {
	Fields  []string // e.g. ["name", "creation"]
	Filters string   // raw JSON string, e.g. {"status":"Open"} or [["status","=","Open"]]
	Limit   int      // 0 means use Frappe default (20)
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

// parseFrappeError turns the raw Frappe error JSON body into a human-friendly error.
//
// Frappe errors contain a lot of noise: a full Python traceback nested inside
// JSON strings. This function extracts only what the user actually needs:
//   - The exception type   (exc_type)         e.g. "DataError"
//   - The human message    (_server_messages)  e.g. "Field not permitted in query: total_credita"
func parseFrappeError(statusCode int, body []byte) error {
	var fe frappeErrorResponse
	if err := json.Unmarshal(body, &fe); err != nil {
		// Body is not valid JSON — return it trimmed.
		return fmt.Errorf("server error %d: %s", statusCode, strings.TrimSpace(string(body)))
	}

	// 1. Try _server_messages first — it carries the already-translated user message.
	userMessage := ""
	if fe.ServerMessages != "" {
		// _server_messages value is itself a JSON-encoded array of JSON-encoded objects.
		var rawMsgs []string
		if err := json.Unmarshal([]byte(fe.ServerMessages), &rawMsgs); err == nil {
			for _, raw := range rawMsgs {
				var sm serverMessage
				if err := json.Unmarshal([]byte(raw), &sm); err == nil && sm.Message != "" {
					userMessage = sm.Message
					break
				}
			}
		}
	}

	// 2. Fall back to the exception field, stripping the Python module prefix.
	if userMessage == "" && fe.Exception != "" {
		// "frappe.exceptions.DataError: Field not permitted in query: total_credita"
		// Keep only the part after the first ": ".
		parts := strings.SplitN(fe.Exception, ": ", 2)
		if len(parts) == 2 {
			userMessage = parts[1]
		} else {
			userMessage = fe.Exception
		}
	}

	excType := fe.ExcType
	if excType == "" {
		excType = "ServerError"
	}

	if userMessage != "" {
		return fmt.Errorf("[%s] %s (HTTP %d)", excType, userMessage, statusCode)
	}
	return fmt.Errorf("server error %d (%s)", statusCode, excType)
}

// GetList calls GET /api/resource/<doctype> and returns the document rows.
func (c *FrappeClient) GetList(doctype string, opts ListOptions) ([]map[string]interface{}, error) {
	params := map[string]string{}

	// Fields
	if len(opts.Fields) > 0 {
		fieldsJSON, err := json.Marshal(opts.Fields)
		if err != nil {
			return nil, fmt.Errorf("encoding fields: %w", err)
		}
		params["fields"] = string(fieldsJSON)
	}

	// Filters — accept raw JSON as-is (user provides the string)
	if opts.Filters != "" {
		params["filters"] = opts.Filters
	}

	// Limit
	if opts.Limit > 0 {
		params["limit_page_length"] = fmt.Sprintf("%d", opts.Limit)
	}

	// Order by
	if opts.OrderBy != "" {
		params["order_by"] = opts.OrderBy
	}

	endpoint := fmt.Sprintf("/api/resource/%s", doctype)

	resp, err := c.r.R().
		SetQueryParams(params).
		Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	case http.StatusForbidden:
		return nil, fmt.Errorf("permission denied (403): your user may not have read access to %s", doctype)
	case http.StatusNotFound:
		return nil, fmt.Errorf("doctype %q not found on this site (404)", doctype)
	}
	if resp.StatusCode() >= 400 {
		return nil, parseFrappeError(resp.StatusCode(), resp.Body())
	}

	// Parse response — Frappe v14/v15 wraps list in "data", older in "message"
	var result listResponse
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if result.Data != nil {
		return result.Data, nil
	}
	return result.Message, nil
}

// CreateDoc posts a new document and returns the created document fields.
func (c *FrappeClient) CreateDoc(doctype string, data map[string]interface{}) (map[string]interface{}, error) {
	endpoint := fmt.Sprintf("/api/resource/%s", doctype)

	resp, err := c.r.R().
		SetBody(data).
		Post(endpoint)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	case http.StatusForbidden:
		return nil, fmt.Errorf("permission denied (403): your user may not have write access to %s", doctype)
	case http.StatusNotFound:
		return nil, fmt.Errorf("doctype %q not found on this site (404)", doctype)
	}
	if resp.StatusCode() >= 400 {
		return nil, parseFrappeError(resp.StatusCode(), resp.Body())
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
func (c *FrappeClient) UpdateDoc(doctype, name string, data map[string]interface{}) (map[string]interface{}, error) {
	endpoint := fmt.Sprintf("/api/resource/%s/%s", doctype, name)

	resp, err := c.r.R().
		SetBody(data).
		Put(endpoint)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	case http.StatusForbidden:
		return nil, fmt.Errorf("permission denied (403): your user may not have write access to %s", doctype)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s %q not found (404)", doctype, name)
	}
	if resp.StatusCode() >= 400 {
		return nil, parseFrappeError(resp.StatusCode(), resp.Body())
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
func (c *FrappeClient) DeleteDoc(doctype, name string) error {
	endpoint := fmt.Sprintf("/api/resource/%s/%s", doctype, name)

	resp, err := c.r.R().Delete(endpoint)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	case http.StatusForbidden:
		return fmt.Errorf("permission denied (403): your user may not have write access to %s", doctype)
	case http.StatusNotFound:
		return fmt.Errorf("%s %q not found (404)", doctype, name)
	}
	if resp.StatusCode() >= 400 {
		return parseFrappeError(resp.StatusCode(), resp.Body())
	}
	return nil
}

// CallMethod posts to /api/method/<method> and returns the "message" field of the response.
func (c *FrappeClient) CallMethod(method string, args map[string]interface{}) (interface{}, error) {
	endpoint := fmt.Sprintf("/api/method/%s", method)

	req := c.r.R()
	if len(args) > 0 {
		req = req.SetBody(args)
	}

	resp, err := req.Post(endpoint)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	case http.StatusForbidden:
		return nil, fmt.Errorf("permission denied (403): your user may not have access to method %s", method)
	case http.StatusNotFound:
		return nil, fmt.Errorf("method %q not found (404): check the method name and that it is whitelisted", method)
	}
	if resp.StatusCode() >= 400 {
		return nil, parseFrappeError(resp.StatusCode(), resp.Body())
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
func (c *FrappeClient) GetCount(doctype, filters string) (int, error) {
	body := map[string]interface{}{"doctype": doctype}
	if filters != "" {
		body["filters"] = filters
	}

	resp, err := c.r.R().SetBody(body).Post("/api/method/frappe.client.get_count")
	if err != nil {
		return 0, fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return 0, fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	case http.StatusForbidden:
		return 0, fmt.Errorf("permission denied (403): your user may not have read access to %s", doctype)
	}
	if resp.StatusCode() >= 400 {
		return 0, parseFrappeError(resp.StatusCode(), resp.Body())
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
// Returns the server response string.
func (c *FrappeClient) Ping() (string, error) {
	resp, err := c.r.R().Get("/api/method/frappe.ping")
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return "", fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	}
	if resp.StatusCode() >= 400 {
		return "", parseFrappeError(resp.StatusCode(), resp.Body())
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
func (c *FrappeClient) RunReport(reportName string, filters map[string]interface{}) (map[string]interface{}, error) {
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

	resp, err := c.r.R().
		SetBody(body).
		Post("/api/method/frappe.desk.query_report.run")
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	case http.StatusForbidden:
		return nil, fmt.Errorf("permission denied (403): your user may not have access to report %q", reportName)
	case http.StatusNotFound:
		return nil, fmt.Errorf("report %q not found (404)", reportName)
	}
	if resp.StatusCode() >= 400 {
		return nil, parseFrappeError(resp.StatusCode(), resp.Body())
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
func (c *FrappeClient) GetDoc(doctype, name string) (map[string]interface{}, error) {
	endpoint := fmt.Sprintf("/api/resource/%s/%s", doctype, name)

	resp, err := c.r.R().Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	switch resp.StatusCode() {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed (401): check your credentials or run 'ffc init' to reconfigure")
	case http.StatusForbidden:
		return nil, fmt.Errorf("permission denied (403): your user may not have read access to %s", doctype)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s %q not found (404)", doctype, name)
	}
	if resp.StatusCode() >= 400 {
		return nil, parseFrappeError(resp.StatusCode(), resp.Body())
	}

	var result struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return result.Data, nil
}
