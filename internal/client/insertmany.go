package client

import (
	"context"
	"fmt"
	"net/http"
)

// MaxInsertMany is the most documents Frappe's insert_many takes in one
// request; it refuses more with "Only 200 inserts allowed in one request".
const MaxInsertMany = 200

// InsertManyDocs checks docs for InsertMany and returns copies to send,
// each with "doctype" set to doctype. Nothing is rewritten silently: a
// document naming another DocType is an error, because insert_many inserts
// each one as its own DocType. So is a document with "parent" and
// "parenttype": Frappe then appends it to that existing parent and saves
// the parent, which is not a create. Callers use it to refuse bad input
// before any request.
func InsertManyDocs(doctype string, docs []map[string]interface{}) ([]map[string]interface{}, error) {
	if len(docs) > MaxInsertMany {
		return nil, fmt.Errorf("%d documents exceed the %d insert_many takes in one request", len(docs), MaxInsertMany)
	}
	out := make([]map[string]interface{}, len(docs))
	for i, d := range docs {
		if v, ok := d["doctype"]; ok && v != doctype {
			return nil, fmt.Errorf("item %d has doctype %v, not %q: insert_many inserts each item as its own DocType", i+1, v, doctype)
		}
		if d["parent"] != nil && d["parenttype"] != nil {
			return nil, fmt.Errorf("item %d has parent and parenttype: Frappe would add it to that existing parent document, not create a document", i+1)
		}
		cp := make(map[string]interface{}, len(d)+1)
		for k, v := range d {
			cp[k] = v
		}
		cp["doctype"] = doctype
		out[i] = cp
	}
	return out, nil
}

// InsertMany inserts docs of a DocType in one request, through
// frappe.client.insert_many, and returns their names in order. The request
// is one database transaction: if any document fails, none is created. A
// controller hook that commits, or a DDL statement (MariaDB commits
// implicitly, as inserting a Custom Field does), ends the transaction early
// and breaks that. See InsertManyDocs for what is refused.
func (c *FrappeClient) InsertMany(ctx context.Context, doctype string, docs []map[string]interface{}) ([]string, error) {
	items, err := InsertManyDocs(doctype, docs)
	if err != nil {
		return nil, err
	}
	hints := readHints(doctype)
	hints[http.StatusForbidden] = fmt.Sprintf("permission denied (403): your user may not have create access to %s", doctype)
	hints[http.StatusNotFound] = fmt.Sprintf("method %q not found (404): check that the site is a Frappe site", "frappe.client.insert_many")
	var env struct {
		Message []interface{} `json:"message"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/method/frappe.client.insert_many", map[string]interface{}{"docs": items}, nil, hints, &env); err != nil {
		return nil, c.missingDocType(ctx, doctype, err)
	}
	if len(env.Message) != len(items) {
		return nil, fmt.Errorf("unexpected response: insert_many returned %d names for %d documents", len(env.Message), len(items))
	}
	names := make([]string, len(env.Message))
	for i, v := range env.Message {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected response: name %d is %T, not a string", i+1, v)
		}
		names[i] = s
	}
	return names, nil
}
