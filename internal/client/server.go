package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// What ffc knows about the site it talks to: who the credentials belong to,
// what they may do, and which Frappe apps run there. Every call is a GET of
// a whitelisted method any logged-in user may call.

const (
	versionsMethod = "frappe.utils.change_log.get_versions"
	userMethod     = "frappe.auth.get_logged_user"
)

// AppVersion is one installed app as get_versions reports it.
type AppVersion struct {
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
	Branch  string `json:"branch,omitempty"`
}

// ServerInfo is the set of installed apps of a site, as cached by ffc.
type ServerInfo struct {
	URL       string                `json:"url"` // the site it was read from; a changed URL is another server
	FetchedAt time.Time             `json:"fetched_at"`
	Apps      map[string]AppVersion `json:"apps"`
}

// Version returns the version of an installed app, or "" when it is not
// installed.
func (s *ServerInfo) Version(app string) string {
	if s == nil {
		return ""
	}
	return s.Apps[app].Version
}

// FrappeVersion returns the version of the Frappe framework ("16.36.1").
func (s *ServerInfo) FrappeVersion() string { return s.Version("frappe") }

// FrappeMajor returns the major version of the Frappe framework (15, 16),
// or 0 when it is unknown. Code that must behave differently on v15 and v16
// asks this instead of probing for a feature.
func (s *ServerInfo) FrappeMajor() int { return ParseMajor(s.FrappeVersion()) }

// AppNames returns the installed apps, sorted.
func (s *ServerInfo) AppNames() []string {
	var names []string
	if s != nil {
		for n := range s.Apps {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// ParseMajor returns the leading number of a version string: "16.36.1" and
// "v15.2.0-dev" give 16 and 15; anything else gives 0.
func ParseMajor(v string) int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	n := 0
	digits := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		if digits++; digits > 6 {
			return 0
		}
	}
	if digits == 0 {
		return 0
	}
	return n
}

// convert re-reads a decoded JSON value (a method's "message") as out.
func convert(v interface{}, out interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// ServerVersions asks the site which apps are installed and at which version
// (frappe.utils.change_log.get_versions). Any logged-in user may call it.
func (c *FrappeClient) ServerVersions(ctx context.Context) (*ServerInfo, error) {
	res, err := c.CallMethod(ctx, versionsMethod, nil, true)
	if err != nil {
		return nil, err
	}
	apps := map[string]AppVersion{}
	if err := convert(res, &apps); err != nil || len(apps) == 0 {
		return nil, fmt.Errorf("unexpected response from %s: expected the installed apps", versionsMethod)
	}
	return &ServerInfo{URL: c.baseURL, FetchedAt: time.Now().UTC(), Apps: apps}, nil
}

// LoggedUser returns the user the credentials belong to
// (frappe.auth.get_logged_user). The Guest user means the request was not
// authenticated.
func (c *FrappeClient) LoggedUser(ctx context.Context) (string, error) {
	res, err := c.CallMethod(ctx, userMethod, nil, true)
	if err != nil {
		return "", err
	}
	user, ok := res.(string)
	if !ok || user == "" {
		return "", fmt.Errorf("unexpected response from %s: expected a user name", userMethod)
	}
	return user, nil
}

// UserRoles returns the roles assigned to a user. A user who may not manage
// users cannot read the "roles" field of their own User document (it has
// permission level 1), but can list its Has Role rows through
// frappe.client.get_list with the parent DocType: that is what this does.
// Frappe adds the automatic roles (All, Guest, Desk User) on its own; they are
// not rows, so they are not listed.
func (c *FrappeClient) UserRoles(ctx context.Context, user string) ([]string, error) {
	res, err := c.CallMethod(ctx, "frappe.client.get_list", map[string]interface{}{
		"doctype":           "Has Role",
		"parent":            "User",
		"fields":            []string{"role"},
		"filters":           map[string]interface{}{"parent": user},
		"limit_page_length": 0,
	}, true)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Role string `json:"role"`
	}
	if err := convert(res, &rows); err != nil {
		return nil, fmt.Errorf("unexpected response listing the roles of %s: %w", user, err)
	}
	roles := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Role != "" {
			roles = append(roles, r.Role)
		}
	}
	sort.Strings(roles)
	return roles, nil
}

// PermTypes are the permission types Frappe checks for a document. Custom
// rights a site adds are not listed.
var PermTypes = []string{"select", "read", "write", "create", "delete", "submit", "cancel", "amend",
	"print", "email", "report", "import", "export", "share"}

// HasPermission asks whether the user may do ptype on a document
// (frappe.client.has_permission). It is the check the server itself makes
// before it acts, user permissions, sharing and controller rules included.
// The method needs a document: a missing one is a 404, and a DocType the
// user cannot see is a 403.
func (c *FrappeClient) HasPermission(ctx context.Context, doctype, name, ptype string) (bool, error) {
	res, err := c.CallMethod(ctx, "frappe.client.has_permission", map[string]interface{}{
		"doctype": doctype, "docname": name, "perm_type": ptype,
	}, true)
	if err != nil {
		return false, err
	}
	var out struct {
		Has *bool `json:"has_permission"`
	}
	if err := convert(res, &out); err != nil || out.Has == nil {
		return false, fmt.Errorf("unexpected response from frappe.client.has_permission: expected has_permission")
	}
	return *out.Has, nil
}

// DocPermissions returns every permission type the user holds on a
// document (frappe.client.get_doc_permissions), as 0 or 1. It evaluates the
// role rules; HasPermission can still differ in the cases the rules do not
// cover (a User may edit their own User document, for one). A document the
// controller refuses outright comes back as {"null": 0}, which is returned
// as an empty set.
func (c *FrappeClient) DocPermissions(ctx context.Context, doctype, name string) (map[string]int, error) {
	res, err := c.CallMethod(ctx, "frappe.client.get_doc_permissions", map[string]interface{}{
		"doctype": doctype, "docname": name,
	}, true)
	if err != nil {
		return nil, err
	}
	var out struct {
		Permissions map[string]json.RawMessage `json:"permissions"`
	}
	if err := convert(res, &out); err != nil || out.Permissions == nil {
		return nil, fmt.Errorf("unexpected response from frappe.client.get_doc_permissions: expected permissions")
	}
	perms := map[string]int{}
	for k, raw := range out.Permissions {
		var n int
		// if_owner is an object and has_if_owner_enabled a flag, not rights.
		if k == "null" || k == "if_owner" || k == "has_if_owner_enabled" || json.Unmarshal(raw, &n) != nil {
			continue
		}
		perms[k] = n
	}
	return perms, nil
}

// DocTypePermission is a role-level answer to "may I do this to any
// document of this DocType".
type DocTypePermission struct {
	Allowed bool
	// OwnerOnly is set when the rights come only from rows limited to the
	// documents the user created.
	OwnerOnly bool
	Roles     []string // the roles evaluated
}

func truthy(v interface{}) bool {
	switch n := v.(type) {
	case bool:
		return n
	case json.Number:
		return n.String() != "0" && n.String() != ""
	case float64:
		return n != 0
	case int:
		return n != 0
	case int64:
		return n != 0
	case string:
		return n != "" && n != "0"
	}
	return false
}

// DocTypePermission evaluates the DocType's permission rows for the user's
// roles. This is what Frappe's role permission system grants before user
// permissions, sharing and controller rules narrow it, and the only
// question a site can answer without a document: has_permission needs one.
// The user Administrator may do everything. The automatic roles All and Guest
// count, and so does Desk User: ffc cannot read user_type (permission level 1)
// and a desk or API user is normally a System User.
func (c *FrappeClient) DocTypePermission(ctx context.Context, doctype, ptype string) (*DocTypePermission, error) {
	user, err := c.LoggedUser(ctx)
	if err != nil {
		return nil, err
	}
	if user == "Administrator" {
		return &DocTypePermission{Allowed: true}, nil
	}
	roles, err := c.UserRoles(ctx, user)
	if err != nil {
		return nil, err
	}
	env, err := c.CallMethodFull(ctx, "frappe.desk.form.load.getdoctype", map[string]interface{}{"doctype": doctype}, true)
	if err != nil {
		return nil, err
	}
	var docs []struct {
		Name        string                   `json:"name"`
		Permissions []map[string]interface{} `json:"permissions"`
	}
	if err := convert(env["docs"], &docs); err != nil || len(docs) == 0 {
		return nil, fmt.Errorf("unexpected response from getdoctype: no meta for %s", doctype)
	}
	return EvalDocTypePermission(docs[0].Permissions, roles, ptype), nil
}

// EvalDocTypePermission applies DocPerm rows (permission level 0) to a set of
// roles. See DocTypePermission.
func EvalDocTypePermission(rows []map[string]interface{}, roles []string, ptype string) *DocTypePermission {
	have := map[string]bool{"All": true, "Guest": true, "Desk User": true}
	for _, r := range roles {
		have[r] = true
	}
	out := &DocTypePermission{Roles: roles}
	owner := false
	for _, row := range rows {
		role, _ := row["role"].(string)
		if !have[role] || !truthy(row[ptype]) {
			continue
		}
		if lvl := row["permlevel"]; lvl != nil && truthy(lvl) {
			continue // field-level rows do not grant document rights
		}
		if truthy(row["if_owner"]) {
			owner = true
			continue
		}
		out.Allowed = true
	}
	out.OwnerOnly = !out.Allowed && owner
	return out
}
