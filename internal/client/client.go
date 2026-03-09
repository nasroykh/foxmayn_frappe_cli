package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"foxmayn_frappe_cli/internal/config"

	"github.com/go-resty/resty/v2"
)

// FrappeClient wraps a resty client configured for a specific Frappe site.
type FrappeClient struct {
	r    *resty.Client
	base string
}

// New creates a FrappeClient from the given site config.
func New(cfg *config.SiteConfig) *FrappeClient {
	r := resty.New().
		SetBaseURL(strings.TrimRight(cfg.URL, "/")).
		SetTimeout(30 * time.Second).
		SetRetryCount(2).
		SetRetryWaitTime(500 * time.Millisecond)

	// Frappe token auth: Authorization: token <api_key>:<api_secret>
	if cfg.APIKey != "" || cfg.APISecret != "" {
		r.SetHeader("Authorization", fmt.Sprintf("token %s:%s", cfg.APIKey, cfg.APISecret))
	}

	// Frappe returns JSON
	r.SetHeader("Accept", "application/json")

	return &FrappeClient{r: r, base: cfg.URL}
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

	if resp.StatusCode() == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication failed (401): check api_key and api_secret in your config")
	}
	if resp.StatusCode() == http.StatusForbidden {
		return nil, fmt.Errorf("permission denied (403): your user may not have read access to %s", doctype)
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, fmt.Errorf("doctype %q not found on this site (404)", doctype)
	}
	if resp.StatusCode() >= 400 {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode(), resp.String())
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
