package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Document context: the desk's form sidebar and timeline, read through the
// methods the desk itself calls. All are sent as GET (whitelisted for every
// HTTP method), so they are retried like any read and run under --dry-run.

const (
	docinfoMethod  = "frappe.desk.form.load.get_docinfo"
	timelineMethod = "frappe.desk.form.activity.get_activity_timeline"
	linksMethod    = "frappe.desk.notifications.get_open_count"
	getdocMethod   = "frappe.desk.form.load.getdoc"
)

// ErrNoTimeline is returned when the site has no activity timeline method:
// it appeared in Frappe v16 and was backported to v15 in August 2026, so
// older releases of both lack it.
var ErrNoTimeline = errors.New("this Frappe release has no activity timeline (frappe.desk.form.activity, added in 2026)")

// formCall GETs a desk method with doctype and name, reporting 403 and 404
// in terms of the document.
func (c *FrappeClient) formCall(ctx context.Context, method string, args map[string]string, doctype, name string) (map[string]interface{}, error) {
	var out map[string]interface{}
	err := c.do(ctx, http.MethodGet, "/api/method/"+url.PathEscape(method), nil, args, docHints(doctype, name, "read"), &out)
	return out, err
}

// DocInfo returns a document's docinfo (frappe.desk.form.load.get_docinfo):
// comments split by kind, the last 10 versions (only when the DocType tracks
// changes), attachments, assignments, shares, tags, communications, the
// user's permissions on it and more. The method has no side effects; it
// sets frappe.response["docinfo"] rather than "message".
func (c *FrappeClient) DocInfo(ctx context.Context, doctype, name string) (map[string]interface{}, error) {
	out, err := c.formCall(ctx, docinfoMethod, map[string]string{"doctype": doctype, "name": name}, doctype, name)
	if err != nil {
		return nil, err
	}
	info, ok := out["docinfo"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response: %s returned no docinfo", docinfoMethod)
	}
	return info, nil
}

// ActivityTimeline returns the desk's activity timeline of a document
// (frappe.desk.form.activity.get_activity_timeline): {activities,
// has_more_emails, ...}, each activity {type, key, timestamp, author, data}.
// Version changes are filtered by the fields the user may read. On a site
// without the method the error wraps ErrNoTimeline.
func (c *FrappeClient) ActivityTimeline(ctx context.Context, doctype, name string) (map[string]interface{}, error) {
	out, err := c.formCall(ctx, timelineMethod, map[string]string{"doctype": doctype, "name": name}, doctype, name)
	if err != nil {
		if missingMethod(err, timelineMethod) {
			return nil, fmt.Errorf("%w: %w", ErrNoTimeline, err)
		}
		return nil, err
	}
	msg, ok := out["message"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response: %s returned no timeline", timelineMethod)
	}
	return msg, nil
}

// LinkCounts returns the counts the form's Connections panel shows
// (frappe.desk.notifications.get_open_count): {count: {external_links_found:
// [{doctype, count, open_count}], internal_links_found: [{doctype, count,
// names}]}}. The DocTypes come from the DocType's dashboard (its
// _dashboard.py and Links table). Each count stops at 100 and is "?" when
// its query ran over one second; the counts ignore the user's permissions
// (the desk shows them the same way).
func (c *FrappeClient) LinkCounts(ctx context.Context, doctype, name string) (map[string]interface{}, error) {
	out, err := c.formCall(ctx, linksMethod, map[string]string{"doctype": doctype, "name": name}, doctype, name)
	if err != nil {
		return nil, err
	}
	msg, ok := out["message"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response: %s returned no counts", linksMethod)
	}
	return msg, nil
}

// FormLoad loads a document the way the desk form does
// (frappe.desk.form.load.getdoc): the document with its __onload values and
// its docinfo, in one request. Unlike DocInfo it writes: on DocTypes that
// track views or seen it adds a View Log entry and marks the document seen
// by the user, and it runs the controller's onload.
func (c *FrappeClient) FormLoad(ctx context.Context, doctype, name string) (doc, docinfo map[string]interface{}, err error) {
	out, err := c.formCall(ctx, getdocMethod, map[string]string{"doctype": doctype, "name": name}, doctype, name)
	if err != nil {
		return nil, nil, err
	}
	// A missing document is not an error there: getdoc answers
	// {"message": []} with no docs.
	docs, _ := out["docs"].([]interface{})
	if len(docs) == 0 {
		return nil, nil, &APIError{Status: http.StatusNotFound, ExcType: "DoesNotExistError",
			Message: fmt.Sprintf("%s %q not found (404)", doctype, name)}
	}
	doc, ok := docs[0].(map[string]interface{})
	info, ok2 := out["docinfo"].(map[string]interface{})
	if !ok || !ok2 {
		return nil, nil, fmt.Errorf("unexpected response: %s returned no document or docinfo", getdocMethod)
	}
	return doc, info, nil
}

// missingMethod reports whether err is Frappe's "Failed to get method for
// command <method>" (417). The message is translated; the method name in it
// is not.
func missingMethod(err error, method string) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusExpectationFailed && strings.Contains(api.Message, method)
}
