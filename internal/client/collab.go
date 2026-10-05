package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Collaboration writes: comments, assignments, tags and shares, all through
// the whitelisted methods the desk calls, so Frappe's permission checks,
// notifications and timeline entries apply. None of them may be done by
// writing the record directly: only a System Manager may write Comment,
// DocShare or Tag Link through /api/resource.

const (
	addCommentMethod    = "frappe.desk.form.utils.add_comment"
	assignAddMethod     = "frappe.desk.form.assign_to.add"
	assignRemoveMethod  = "frappe.desk.form.assign_to.remove"
	addTagMethod        = "frappe.desk.doctype.tag.tag.add_tag"
	removeTagMethod     = "frappe.desk.doctype.tag.tag.remove_tag"
	shareAddMethod      = "frappe.share.add"
	sharePermMethod     = "frappe.share.set_permission"
	shareGetUsersMethod = "frappe.share.get_users"
)

// docMethod POSTs a whitelisted method that acts on one document. A 404
// from these methods means the document is missing (Frappe raises
// DoesNotExistError), so the hint names the document, not the method.
func (c *FrappeClient) docMethod(ctx context.Context, method, doctype, name, access string, args map[string]interface{}) (interface{}, error) {
	var result map[string]interface{}
	err := c.do(ctx, http.MethodPost, "/api/method/"+url.PathEscape(method), args, nil, docHints(doctype, name, access), &result)
	if err != nil {
		return nil, err
	}
	return result["message"], nil
}

// AddComment adds a comment to a document's timeline. Frappe requires read
// permission on the document and sanitises content as HTML (nh3): scripts,
// forms and unknown attributes are removed, a bare "<" becomes "&lt;".
// email and by are recorded as the author; Frappe does not check them, so
// the caller passes the signed-in user.
func (c *FrappeClient) AddComment(ctx context.Context, doctype, name, content, email, by string) (map[string]interface{}, error) {
	res, err := c.docMethod(ctx, addCommentMethod, doctype, name, "read", map[string]interface{}{
		"reference_doctype": doctype, "reference_name": name, "content": content,
		"comment_email": email, "comment_by": by,
	})
	if err != nil {
		return nil, err
	}
	return docResult(res)
}

// docField reads one field of a document through a list query, which, unlike
// GET /api/resource/<doctype>/<name>, returns the underscore fields (_assign,
// _user_tags). A missing document is a 404 APIError, like GetDoc's.
func (c *FrappeClient) docField(ctx context.Context, doctype, name, field string) (interface{}, error) {
	filters, err := json.Marshal([][]interface{}{{"name", "=", name}})
	if err != nil {
		return nil, err
	}
	rows, err := c.GetList(ctx, doctype, ListOptions{Fields: []string{"name", field}, Filters: string(filters), Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &APIError{Status: http.StatusNotFound, ExcType: "DoesNotExistError", Message: fmt.Sprintf("%s %q not found (404)", doctype, name)}
	}
	return rows[0][field], nil
}

// Assignees returns the users a document is assigned to (its _assign field,
// the open ToDos). A missing document is a 404 APIError.
func (c *FrappeClient) Assignees(ctx context.Context, doctype, name string) ([]string, error) {
	v, err := c.docField(ctx, doctype, name, "_assign")
	if err != nil {
		return nil, err
	}
	out := []string{}
	if s, ok := v.(string); ok && s != "" {
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("unexpected _assign value %q: %w", s, err)
		}
	}
	return out, nil
}

// AssignOptions are the optional fields of the ToDo an assignment creates.
type AssignOptions struct {
	Description string // Frappe writes "Assignment for <doctype> <name>" when empty
	Date        string // due date, YYYY-MM-DD
	Priority    string // Low, Medium (Frappe's default) or High
}

// Assign assigns a document to users (frappe.desk.form.assign_to.add): one
// open ToDo per user. A user who already has an open ToDo for the document
// is skipped without an error. Frappe notifies each assignee (except
// oneself), makes them follow the document if their settings say so, and
// shares the document read-only with an assignee who cannot read it (or
// fails when document sharing is disabled). It returns the open
// assignments, at most 5, as Frappe lists them.
func (c *FrappeClient) Assign(ctx context.Context, doctype, name string, users []string, opts AssignOptions) ([]interface{}, error) {
	args := map[string]interface{}{"doctype": doctype, "name": name, "assign_to": users}
	// Unset keys are left out: Frappe uses args.get("date", nowdate()), so an
	// empty string would be stored as the date.
	for k, v := range map[string]string{"description": opts.Description, "date": opts.Date, "priority": opts.Priority} {
		if v != "" {
			args[k] = v
		}
	}
	res, err := c.docMethod(ctx, assignAddMethod, doctype, name, "read", args)
	if err != nil {
		return nil, err
	}
	rows, _ := res.([]interface{})
	return rows, nil
}

// Unassign cancels a user's open assignment of a document
// (frappe.desk.form.assign_to.remove). Removing a user who is not assigned
// is not an error. Frappe notifies the former assignee.
func (c *FrappeClient) Unassign(ctx context.Context, doctype, name, user string) error {
	_, err := c.docMethod(ctx, assignRemoveMethod, doctype, name, "read", map[string]interface{}{
		"doctype": doctype, "name": name, "assign_to": user,
	})
	return err
}

// Tags returns a document's tags (its _user_tags field).
func (c *FrappeClient) Tags(ctx context.Context, doctype, name string) ([]string, error) {
	v, err := c.docField(ctx, doctype, name, "_user_tags")
	if err != nil {
		return nil, err
	}
	out := []string{}
	s, _ := v.(string)
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out, nil
}

// AddTag tags a document, creating the Tag if it does not exist. It needs
// write permission on the document; a tag it already has is left alone.
func (c *FrappeClient) AddTag(ctx context.Context, doctype, name, tag string) error {
	_, err := c.docMethod(ctx, addTagMethod, doctype, name, "write", map[string]interface{}{"tag": tag, "dt": doctype, "dn": name})
	return err
}

// RemoveTag removes a tag from a document (any case); a tag it does not have
// is not an error. The Tag itself stays.
func (c *FrappeClient) RemoveTag(ctx context.Context, doctype, name, tag string) error {
	_, err := c.docMethod(ctx, removeTagMethod, doctype, name, "write", map[string]interface{}{"tag": tag, "dt": doctype, "dn": name})
	return err
}

// ShareOptions are the rights a share grants. Read is always granted.
type ShareOptions struct {
	User     string // ignored with Everyone
	Everyone bool
	Write    bool
	Submit   bool // only on a submittable DocType
	Share    bool // the user may share the document further
	Notify   bool // send the user a notification
}

func bit(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Share shares a document (frappe.share.add) and returns the DocShare. The
// caller needs share permission on the document and every right it grants.
// An existing share of the same user (or of everyone) is updated: its
// rights become exactly the ones given.
func (c *FrappeClient) Share(ctx context.Context, doctype, name string, o ShareOptions) (map[string]interface{}, error) {
	args := map[string]interface{}{
		"doctype": doctype, "name": name, "read": 1,
		"write": bit(o.Write), "submit": bit(o.Submit), "share": bit(o.Share),
		"everyone": bit(o.Everyone), "notify": bit(o.Notify),
	}
	if !o.Everyone {
		args["user"] = o.User
	}
	res, err := c.docMethod(ctx, shareAddMethod, doctype, name, "share", args)
	if err != nil {
		return nil, err
	}
	return docResult(res)
}

// Shares lists a document's DocShare rows (frappe.share.get_users). Frappe
// returns none to a user who cannot read the document.
func (c *FrappeClient) Shares(ctx context.Context, doctype, name string) ([]map[string]interface{}, error) {
	res, err := c.docMethod(ctx, shareGetUsersMethod, doctype, name, "read", map[string]interface{}{"doctype": doctype, "name": name})
	if err != nil {
		return nil, err
	}
	rows, _ := res.([]interface{})
	out := make([]map[string]interface{}, 0, len(rows))
	for _, r := range rows {
		if m, ok := r.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// Unshare removes a user's (or everyone's) share of a document. frappe.share
// .remove is not whitelisted, so this is what the desk does: clear the read
// right with frappe.share.set_permission, which clears the others too and
// deletes the DocShare. A share that does not exist is not an error.
func (c *FrappeClient) Unshare(ctx context.Context, doctype, name, user string, everyone bool) error {
	args := map[string]interface{}{
		"doctype": doctype, "name": name, "user": user,
		"permission_to": "read", "value": 0, "everyone": bit(everyone),
	}
	if everyone {
		args["user"] = "" // as the desk sends it; Frappe ignores the user then
	}
	_, err := c.docMethod(ctx, sharePermMethod, doctype, name, "share", args)
	return err
}
