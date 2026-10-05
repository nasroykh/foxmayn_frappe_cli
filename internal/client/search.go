package client

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

// SearchLink looks a document up the way a Link field does
// (frappe.desk.search.search_link): it honours the DocType's search_fields,
// title field, standard or custom search query and the user's permissions.
// Each row has value (the name) and description, and a label when the
// DocType shows a title. Frappe marks the answer cacheable (max-age=60);
// ffc keeps no cache, but a proxy in front of the site may serve it for a
// minute.
func (c *FrappeClient) SearchLink(ctx context.Context, doctype, txt string, limit int) ([]map[string]interface{}, error) {
	query := map[string]string{
		"doctype":     doctype,
		"txt":         txt,
		"page_length": strconv.Itoa(limit),
	}
	return c.searchRows(ctx, "/api/method/frappe.desk.search.search_link", query, readHints(doctype))
}

// GlobalSearch searches the __global_search index
// (frappe.utils.global_search.search). Only DocTypes listed in Global Search
// Settings and fields flagged in_global_search are indexed, and only
// documents the user may read come back. Each row has doctype, name, content
// and rank, plus title and image when the DocType defines them. A text with
// "&" is split into separate phrases, whose hits are combined.
func (c *FrappeClient) GlobalSearch(ctx context.Context, text string, limit int) ([]map[string]interface{}, error) {
	query := map[string]string{
		"text":  text,
		"limit": strconv.Itoa(limit),
	}
	hints := map[int]string{
		http.StatusUnauthorized: authHint,
		http.StatusForbidden:    "permission denied (403): your user may not use global search",
	}
	return c.searchRows(ctx, "/api/method/frappe.utils.global_search.search", query, hints)
}

func (c *FrappeClient) searchRows(ctx context.Context, path string, query map[string]string, hints map[int]string) ([]map[string]interface{}, error) {
	var result struct {
		Message *[]map[string]interface{} `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, query, hints, &result); err != nil {
		return nil, err
	}
	if result.Message == nil {
		return nil, fmt.Errorf("unexpected response: search returned no result list")
	}
	return *result.Message, nil
}
