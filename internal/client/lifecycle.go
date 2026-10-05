package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Document lifecycle: submit, cancel, amend, copy, rename, restore and
// workflow actions, all through Frappe's own whitelisted methods so its
// validations, permissions and hooks run.

// docResult converts a method's "message" into a document.
func docResult(v interface{}) (map[string]interface{}, error) {
	doc, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response: expected a document, got %T", v)
	}
	return doc, nil
}

// SubmitDoc submits a draft (docstatus 0 → 1). It sends the document as
// read, so Frappe's timestamp check rejects the submit if someone changed
// it in between.
func (c *FrappeClient) SubmitDoc(ctx context.Context, doctype, name string) (map[string]interface{}, error) {
	doc, err := c.GetDoc(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	res, err := c.CallMethod(ctx, "frappe.client.submit", map[string]interface{}{"doc": doc}, false)
	if err != nil {
		return nil, err
	}
	return docResult(res)
}

// CancelDoc cancels a submitted document (docstatus 1 → 2).
func (c *FrappeClient) CancelDoc(ctx context.Context, doctype, name string) (map[string]interface{}, error) {
	res, err := c.CallMethod(ctx, "frappe.client.cancel", map[string]interface{}{"doctype": doctype, "name": name}, false)
	if err != nil {
		return nil, err
	}
	return docResult(res)
}

// LinkedDoc is a submitted document that links to another one.
type LinkedDoc struct {
	DocType string `json:"doctype"`
	Name    string `json:"name"`
}

// SubmittedLinkedDocs lists the submitted documents that link to a document,
// directly or through other submitted documents: they block its cancel.
func (c *FrappeClient) SubmittedLinkedDocs(ctx context.Context, doctype, name string) ([]LinkedDoc, error) {
	res, err := c.CallMethod(ctx, "frappe.desk.form.linked_with.get_submitted_linked_docs", map[string]interface{}{"doctype": doctype, "name": name}, false)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	var out struct {
		Docs []LinkedDoc `json:"docs"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unexpected linked documents response: %w", err)
	}
	return out.Docs, nil
}

// ErrNeedsV16 is returned for features the site's Frappe version lacks.
var ErrNeedsV16 = errors.New("this needs Frappe v16 or later")

const discardMethod = "frappe.desk.form.save.discard"

// DiscardDoc discards a draft (Frappe v16+): it becomes cancelled without
// having been submitted. On an older site the method does not exist; the
// error then wraps both ErrNeedsV16 and the site's APIError.
func (c *FrappeClient) DiscardDoc(ctx context.Context, doctype, name string) error {
	_, err := c.CallMethod(ctx, discardMethod, map[string]interface{}{"doctype": doctype, "name": name}, false)
	var api *APIError
	// "Failed to get method for command <cmd> ..." is translated; the
	// method name in it is not.
	if errors.As(err, &api) && api.Status == http.StatusExpectationFailed && strings.Contains(api.Message, discardMethod) {
		return fmt.Errorf("discard: %w: %w", ErrNeedsV16, err)
	}
	return err
}

// copyFields are cleared on a copy, on the document and on child rows. The
// desk also resets lft and rgt: a tree node (an Account, a Cost Center)
// that keeps them is not inserted as a new node and corrupts the tree.
var copyFields = []string{"name", "owner", "creation", "modified", "modified_by", "docstatus", "parent",
	"amended_from", "amendment_date", "cancel_reason", "lft", "rgt"}

// clean turns a document read from the site into an unsaved copy, in place:
// identity and private ("__") fields are removed, on the document and its
// child rows, and so are the fields drop lists for each one's DocType.
func clean(doc map[string]interface{}, drop map[string]map[string]bool) {
	skip := drop[fmt.Sprint(doc["doctype"])]
	for k, v := range doc {
		if strings.HasPrefix(k, "__") || skip[k] {
			delete(doc, k)
			continue
		}
		if rows, ok := v.([]interface{}); ok {
			for _, r := range rows {
				if row, ok := r.(map[string]interface{}); ok {
					clean(row, drop)
				}
			}
		}
	}
	for _, k := range copyFields {
		delete(doc, k)
	}
}

// docMeta is what a copy needs from a DocType's meta.
type docMeta struct {
	fields map[string]bool // every field
	drop   map[string]bool // fields a copy leaves out
}

// copyMetas reads the meta of a DocType and of its child tables, by
// DocType, from the desk's frappe.desk.form.load.getdoctype (custom fields
// and property setters included). Password fields are always left out (the
// site sends them masked); "no copy" fields too unless keepNoCopy.
func (c *FrappeClient) copyMetas(ctx context.Context, doctype string, keepNoCopy bool) (map[string]docMeta, error) {
	env, err := c.CallMethodFull(ctx, "frappe.desk.form.load.getdoctype", map[string]interface{}{"doctype": doctype}, true)
	if err != nil {
		return nil, fmt.Errorf("reading the %s meta: %w", doctype, err)
	}
	b, err := json.Marshal(env["docs"])
	if err != nil {
		return nil, err
	}
	var metas []struct {
		Name   string `json:"name"`
		Fields []struct {
			Fieldname string      `json:"fieldname"`
			Fieldtype string      `json:"fieldtype"`
			NoCopy    json.Number `json:"no_copy"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(b, &metas); err != nil {
		return nil, fmt.Errorf("unexpected %s meta response: %w", doctype, err)
	}
	if len(metas) == 0 {
		return nil, fmt.Errorf("unexpected %s meta response: no docs", doctype)
	}
	out := map[string]docMeta{}
	for _, m := range metas {
		dm := docMeta{fields: map[string]bool{}, drop: map[string]bool{}}
		for _, f := range m.Fields {
			dm.fields[f.Fieldname] = true
			if f.Fieldtype == "Password" || (!keepNoCopy && f.NoCopy == "1") {
				dm.drop[f.Fieldname] = true
			}
		}
		out[m.Name] = dm
	}
	return out, nil
}

// copyOf reads a document and returns it as an unsaved copy with overrides
// applied, and its DocType's meta.
func (c *FrappeClient) copyOf(ctx context.Context, doc map[string]interface{}, doctype string, keepNoCopy bool, overrides map[string]interface{}) (docMeta, error) {
	metas, err := c.copyMetas(ctx, doctype, keepNoCopy)
	if err != nil {
		return docMeta{}, err
	}
	drop := make(map[string]map[string]bool, len(metas))
	for dt, m := range metas {
		drop[dt] = m.drop
	}
	clean(doc, drop)
	for k, v := range overrides {
		doc[k] = v
	}
	return metas[doctype], nil
}

// AmendDoc creates the amendment of a cancelled document, like the desk's
// Amend: a copy that keeps the "no copy" fields, with amended_from set (and
// amendment_date, when the DocType has it, to today), which Frappe names
// <name>-1 (<prefix>-<n+1> for an amendment of an amendment). overrides are
// applied on top.
func (c *FrappeClient) AmendDoc(ctx context.Context, doctype, name string, overrides map[string]interface{}) (map[string]interface{}, error) {
	doc, err := c.GetDoc(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	if ds := fmt.Sprint(doc["docstatus"]); ds != "2" {
		return nil, &StateError{fmt.Sprintf("%s %s is not cancelled (docstatus %s): only a cancelled document can be amended", doctype, name, ds)}
	}
	meta, err := c.copyOf(ctx, doc, doctype, true, overrides)
	if err != nil {
		return nil, err
	}
	doc["amended_from"] = name
	if _, set := overrides["amendment_date"]; meta.fields["amendment_date"] && !set {
		doc["amendment_date"] = time.Now().Format("2006-01-02")
	}
	return c.CreateDoc(ctx, doctype, doc)
}

// DuplicateDoc creates a copy of a document, like the desk's Duplicate: the
// "no copy" fields are left out, child rows are copied. overrides are
// applied on top.
func (c *FrappeClient) DuplicateDoc(ctx context.Context, doctype, name string, overrides map[string]interface{}) (map[string]interface{}, error) {
	doc, err := c.GetDoc(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	if _, err := c.copyOf(ctx, doc, doctype, false, overrides); err != nil {
		return nil, err
	}
	return c.CreateDoc(ctx, doctype, doc)
}

// RenameDoc renames a document and returns its new name. With merge, the
// document is merged into an existing one named newName.
func (c *FrappeClient) RenameDoc(ctx context.Context, doctype, oldName, newName string, merge bool) (string, error) {
	res, err := c.CallMethod(ctx, "frappe.client.rename_doc", map[string]interface{}{
		"doctype": doctype, "old_name": oldName, "new_name": newName, "merge": merge,
	}, false)
	if err != nil {
		return "", err
	}
	if s, ok := res.(string); ok {
		return s, nil
	}
	return fmt.Sprint(res), nil
}

// RestoreDeleted restores a "Deleted Document" record (System Manager only)
// and returns the restored document's name: a DocType named by hash or
// series gives it a new one.
func (c *FrappeClient) RestoreDeleted(ctx context.Context, deletedName string) (string, error) {
	if _, err := c.CallMethod(ctx, "frappe.core.doctype.deleted_document.deleted_document.restore",
		map[string]interface{}{"name": deletedName, "alert": false}, false); err != nil {
		return "", err
	}
	rec, err := c.GetDoc(ctx, "Deleted Document", deletedName)
	if err != nil {
		return "", fmt.Errorf("restored, but reading the Deleted Document: %w", err)
	}
	name, _ := rec["new_name"].(string)
	return name, nil
}

// ActiveWorkflow returns the active Workflow of a DocType, or "" when it has
// none. known is false when the user may not read Workflows.
func (c *FrappeClient) ActiveWorkflow(ctx context.Context, doctype string) (name string, known bool, err error) {
	filters, err := json.Marshal(map[string]interface{}{"document_type": doctype, "is_active": 1})
	if err != nil {
		return "", false, err
	}
	rows, err := c.GetList(ctx, "Workflow", ListOptions{Fields: []string{"name"}, Filters: string(filters), Limit: 1})
	var api *APIError
	if errors.As(err, &api) && api.Status == http.StatusForbidden {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if len(rows) == 0 {
		return "", true, nil
	}
	return fmt.Sprint(rows[0]["name"]), true, nil
}

// WorkflowTransitions lists the workflow actions the current user can apply
// to a document in its current state.
func (c *FrappeClient) WorkflowTransitions(ctx context.Context, doctype, name string) ([]interface{}, error) {
	res, err := c.CallMethod(ctx, "frappe.model.workflow.get_transitions",
		map[string]interface{}{"doc": map[string]interface{}{"doctype": doctype, "name": name}}, false)
	if err != nil {
		return nil, err
	}
	list, ok := res.([]interface{})
	if !ok && res != nil {
		return nil, fmt.Errorf("unexpected transitions response: %T", res)
	}
	return list, nil
}

// ApplyWorkflow applies a workflow action to a document and returns it. The
// action may submit or cancel it.
func (c *FrappeClient) ApplyWorkflow(ctx context.Context, doctype, name, action string) (map[string]interface{}, error) {
	res, err := c.CallMethod(ctx, "frappe.model.workflow.apply_workflow", map[string]interface{}{
		"doc": map[string]interface{}{"doctype": doctype, "name": name}, "action": action,
	}, false)
	if err != nil {
		return nil, err
	}
	return docResult(res)
}
