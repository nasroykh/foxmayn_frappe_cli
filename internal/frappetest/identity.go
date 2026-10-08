package frappetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
)

// App is an installed app as frappe.utils.change_log.get_versions reports it.
type App struct {
	Title   string
	Version string
	Branch  string
}

// stdRights are the permission types frappe.client.get_doc_permissions
// returns, in the order Frappe lists them.
var stdRights = []string{"select", "read", "write", "create", "delete", "submit", "cancel", "amend",
	"print", "email", "report", "import", "export", "share", "impersonate"}

func (s *Site) registerIdentity() {
	s.apps = map[string]App{
		"frappe": {Title: "Frappe Framework", Version: "16.36.1", Branch: "version-16"},
	}
	s.denied = map[string]map[string]bool{}
	s.docPerms = map[string][]map[string]interface{}{}
	s.metaFlag = map[string]map[string]interface{}{}
	s.methods["frappe.utils.change_log.get_versions"] = s.getVersions
	s.methods["frappe.client.has_permission"] = s.hasPermission
	s.methods["frappe.client.get_doc_permissions"] = s.getDocPermissions
	s.methods["frappe.client.get_list"] = s.getList
}

// SetApps replaces the installed apps get_versions reports (a new Site has
// frappe 16.36.1).
func (s *Site) SetApps(apps map[string]App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apps = apps
}

// SetUser makes the site report user as the logged-in user and roles as the
// Has Role rows of that user, whatever credentials a request carries.
func (s *Site) SetUser(user string, roles ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userName, s.roles = user, roles
}

// Deny makes has_permission and get_doc_permissions refuse the permission
// types on a DocType, whoever asks.
func (s *Site) Deny(doctype string, ptypes ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.denied[doctype] == nil {
		s.denied[doctype] = map[string]bool{}
	}
	for _, p := range ptypes {
		s.denied[doctype][p] = true
	}
}

// DocPerm adds a permission row to the DocType's meta, as getdoctype lists
// them: {"role": "Sales User", "read": 1, "write": 1, "permlevel": 0}.
func (s *Site) DocPerm(doctype string, row map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docPerms[doctype] = append(s.docPerms[doctype], row)
}

// DocTypeFlags sets meta flags getdoctype reports for a DocType: istable,
// is_submittable, allow_import (a new DocType has none of them).
func (s *Site) DocTypeFlags(doctype string, flags map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metaFlag[doctype] == nil {
		s.metaFlag[doctype] = map[string]interface{}{}
	}
	for k, v := range flags {
		s.metaFlag[doctype][k] = v
	}
}

// DisableV2 makes /api/v2 answer 404, like a site without the v2 API.
func (s *Site) DisableV2() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noV2 = true
}

// loggedUser is the user a request runs as.
func (s *Site) loggedUser(authenticated string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userName != "" {
		return s.userName
	}
	return authenticated
}

func (s *Site) getVersions(_ *http.Request, _ map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]interface{}{}
	for name, a := range s.apps {
		out[name] = map[string]interface{}{
			"title": a.Title, "description": a.Title, "branch": a.Branch,
			"color": nil, "logo": nil, "version": a.Version,
		}
	}
	return out, nil
}

// checkDoc fails like Frappe for a document that cannot be checked: an
// unknown DocType is a PermissionError, a missing document a 404.
func (s *Site) checkDoc(doctype, name string) error {
	docs, ok := s.doctypes[doctype]
	if !ok {
		return Permission(fmt.Sprintf("No permission for %s", doctype))
	}
	if _, ok := docs[name]; !ok {
		return NotFound(fmt.Sprintf("%s %s not found", doctype, name))
	}
	return nil
}

func (s *Site) hasPermission(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := args["docname"]; !ok {
		return nil, &Error{http.StatusInternalServerError, "TypeError", "has_permission() missing 1 required positional argument: 'docname'"}
	}
	// Like Frappe, Administrator is allowed everything before the DocType or
	// the document is even looked up.
	if s.isAdmin() {
		return map[string]interface{}{"has_permission": true}, nil
	}
	doctype, name := argString(args, "doctype"), argString(args, "docname")
	if err := s.checkDoc(doctype, name); err != nil {
		return nil, err
	}
	ptype := argString(args, "perm_type")
	if ptype == "" {
		ptype = "read"
	}
	return map[string]interface{}{"has_permission": s.allowed(doctype, ptype)}, nil
}

// isAdmin reports whether the fake's user is Administrator (the default).
func (s *Site) isAdmin() bool { return s.userName == "" || s.userName == "Administrator" }

// keepNoWriteAccess undoes, in an update, the changes Frappe drops without
// an error: a field at a permission level the user's roles do not write
// (validate_higher_perm_levels) and, in v16, a masked field the user's roles
// do not unmask (_restore_masked_fields_from_db). Child rows use the
// parent's permission rows. The caller holds s.mu.
func (s *Site) keepNoWriteAccess(doctype string, doc, patch map[string]interface{}) {
	rows := s.docPerms[doctype]
	if s.isAdmin() || len(rows) == 0 {
		return
	}
	have := map[string]bool{"All": true, "Guest": true}
	for _, r := range s.roles {
		have[r] = true
	}
	write, mask := map[int]bool{0: true}, map[int]bool{}
	for _, row := range rows {
		if role, _ := row["role"].(string); !have[role] {
			continue
		}
		level, _ := strconv.Atoi(fmt.Sprint(row["permlevel"]))
		if fmt.Sprint(row["write"]) == "1" {
			write[level] = true
		}
		if fmt.Sprint(row["mask"]) == "1" {
			mask[level] = true
		}
	}
	dropped := func(dt string) map[string]bool {
		out := map[string]bool{}
		for _, f := range s.meta[dt] {
			if !write[f.permlevel] || (fmt.Sprint(f.props["mask"]) == "1" && !mask[f.permlevel]) {
				out[f.name] = true
			}
		}
		return out
	}
	for f := range dropped(doctype) {
		delete(patch, f)
	}
	for field, child := range s.tables[doctype] {
		sent, ok := patch[field].([]interface{})
		if !ok {
			continue
		}
		stored := map[string]map[string]interface{}{}
		for _, r := range rowList(doc[field]) {
			if row, ok := r.(map[string]interface{}); ok {
				stored[fmt.Sprint(row["name"])] = row
			}
		}
		drop := dropped(child)
		for _, r := range sent {
			row, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			old := stored[fmt.Sprint(row["name"])]
			for f := range drop {
				if old != nil {
					row[f] = old[f]
				} else {
					delete(row, f)
				}
			}
		}
	}
}

// allowed reports whether ptype is a right the fake knows and nobody denied.
func (s *Site) allowed(doctype, ptype string) bool {
	if s.denied[doctype][ptype] {
		return false
	}
	for _, r := range stdRights {
		if r == ptype {
			return true
		}
	}
	return false
}

func (s *Site) getDocPermissions(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doctype, name := argString(args, "doctype"), argString(args, "docname")
	if err := s.checkDoc(doctype, name); err != nil {
		return nil, err
	}
	perms := map[string]interface{}{"if_owner": map[string]interface{}{}, "has_if_owner_enabled": false}
	for _, r := range stdRights {
		v := 0
		if s.isAdmin() || s.allowed(doctype, r) {
			v = 1
		}
		perms[r] = v
	}
	return map[string]interface{}{"permissions": perms}, nil
}

// getList answers frappe.client.get_list: "Has Role" lists the roles set
// with SetUser, a child DocType with `parent` its table rows (childList),
// any other DocType its documents, narrowed by filters, with the requested
// fields.
func (s *Site) getList(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	doctype := argString(args, "doctype")
	if doctype == "Has Role" {
		s.mu.Lock()
		defer s.mu.Unlock()
		roles := append([]string(nil), s.roles...)
		sort.Strings(roles)
		rows := []interface{}{}
		for _, r := range roles {
			rows = append(rows, map[string]interface{}{"role": r})
		}
		return rows, nil
	}
	if parent := argString(args, "parent"); parent != "" {
		return s.childList(doctype, parent, args)
	}
	rows, err := s.query(doctype, args["filters"])
	if err != nil {
		return nil, err
	}
	fields := toStrings(args["fields"])
	if len(fields) == 0 {
		fields = []string{"name"}
	}
	out := []interface{}{}
	for _, d := range rows {
		row := map[string]interface{}{}
		for _, f := range fields {
			row[f] = d[f]
		}
		out = append(out, row)
	}
	return out, nil
}

func toStrings(v interface{}) []string {
	var out []string
	if str, ok := v.(string); ok {
		var list []interface{}
		if json.Unmarshal([]byte(str), &list) == nil {
			v = list
		}
	}
	for _, x := range toList(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
