package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// childChunk caps the parent names of one child-row request: the `in`
// list becomes one SQL IN clause.
const childChunk = 500

// ChildRows lists the rows of one table field of the given parent
// documents, through frappe.client.get_list with `parent` set: Frappe then
// checks read access on the parent DocType, as the desk's Data Export does
// (exporter.py get_data_as_docs). Each row has name, idx, parent and
// parentfield plus fields, ordered by idx within each parent; rows of
// different parents arrive in no useful order, so callers group them by
// parent. Long name lists go in chunks of childChunk.
func (c *FrappeClient) ChildRows(ctx context.Context, parentDoctype, table, childDoctype string, parents, fields []string) ([]map[string]interface{}, error) {
	cols := []string{"name", "idx", "parent", "parentfield"}
	for _, f := range fields {
		if f != "name" && f != "idx" && f != "parent" && f != "parentfield" {
			cols = append(cols, f)
		}
	}
	var out []map[string]interface{}
	for len(parents) > 0 {
		chunk := parents[:min(len(parents), childChunk)]
		parents = parents[len(chunk):]
		res, err := c.CallMethod(ctx, "frappe.client.get_list", map[string]interface{}{
			"doctype": childDoctype,
			"parent":  parentDoctype,
			"fields":  cols,
			"filters": []interface{}{
				[]interface{}{childDoctype, "parent", "in", chunk},
				[]interface{}{childDoctype, "parentfield", "=", table},
				[]interface{}{childDoctype, "parenttype", "=", parentDoctype},
			},
			"order_by":          "idx asc",
			"limit_page_length": 0, // every row
		}, false)
		if err != nil {
			return nil, fmt.Errorf("reading the %s rows: %w", table, err)
		}
		list, ok := res.([]interface{})
		if !ok && res != nil {
			return nil, fmt.Errorf("reading the %s rows: unexpected response %T", table, res)
		}
		for _, r := range list {
			if m, ok := r.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// TemplateOptions are the arguments of Frappe's Data Import template
// download (frappe.core.doctype.data_import.data_import.download_template).
type TemplateOptions struct {
	// Fields maps the DocType and each table fieldname to the fieldnames
	// of its columns ("name" included). Frappe fails without it.
	Fields map[string][]string
	// Records is "blank_template" (header only), "5_records", or anything
	// else for every document matching Filters.
	Records string
	// Filters is a JSON filter object or list; empty sends none.
	Filters string
	// FileType is "CSV" or "Excel".
	FileType string
}

// DownloadTemplate POSTs download_template and returns the response
// unread: a CSV attachment or an .xlsx workbook (binary). Frappe builds the
// whole file in memory. It needs read access to the DocType, and export
// permission when Records includes data. The caller must check the status.
func (c *FrappeClient) DownloadTemplate(ctx context.Context, doctype string, o TemplateOptions) (*RawResponse, error) {
	args := map[string]interface{}{
		"doctype":        doctype,
		"export_fields":  o.Fields,
		"export_records": o.Records,
		"file_type":      o.FileType,
	}
	if o.Filters != "" {
		args["export_filters"] = json.RawMessage(o.Filters)
	}
	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encoding the template request: %w", err)
	}
	return c.Raw(ctx, RawRequest{
		Method: http.MethodPost,
		Path:   "/api/method/frappe.core.doctype.data_import.data_import.download_template",
		Body:   body,
		Header: http.Header{"Content-Type": {"application/json"}, "Accept": {"*/*"}},
	})
}
