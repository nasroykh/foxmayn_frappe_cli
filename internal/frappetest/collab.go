package frappetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Collaboration methods with the argument names, results and errors of
// Frappe v15/v16 (checked by the contract tests): comments, assignments
// (ToDo, the document's _assign), tags (the document's _user_tags, Tag and
// Tag Link) and shares (DocShare). A missing document is a 404
// DoesNotExistError, a right taken with Deny a 403 PermissionError, a user
// that is not a User record (other than Administrator and Guest) a 417
// LinkValidationError. HandleMethod replaces any of them.

func (s *Site) registerCollab() {
	s.methods["frappe.desk.form.utils.add_comment"] = s.addComment
	s.methods["frappe.desk.form.assign_to.add"] = s.assignAdd
	s.methods["frappe.desk.form.assign_to.remove"] = s.assignRemove
	s.methods["frappe.desk.doctype.tag.tag.add_tag"] = s.addTag
	s.methods["frappe.desk.doctype.tag.tag.remove_tag"] = s.removeTag
	s.methods["frappe.share.add"] = s.shareAdd
	s.methods["frappe.share.set_permission"] = s.shareSetPermission
	s.methods["frappe.share.get_users"] = s.shareGetUsers
}

// collabDoc returns a stored document after the permission check Frappe
// makes; s.mu must be held.
func (s *Site) collabDoc(doctype, name, ptype string) (map[string]interface{}, error) {
	doc, ok := s.doctypes[doctype][name]
	if !ok {
		return nil, NotFound(fmt.Sprintf("%s %s not found", doctype, name))
	}
	if s.denied[doctype][ptype] {
		return nil, Permission(fmt.Sprintf("No permission for %s", doctype))
	}
	return doc, nil
}

// DenyUser makes users other than the session user unable to read a
// DocType unless a DocShare gives them the document, as has_permission(doc,
// user=...) answers for them. assign_to.add then shares the document with
// such an assignee.
func (s *Site) DenyUser(doctype string, users ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cannotRead == nil {
		s.cannotRead = map[string]map[string]bool{}
	}
	if s.cannotRead[doctype] == nil {
		s.cannotRead[doctype] = map[string]bool{}
	}
	for _, u := range users {
		s.cannotRead[doctype][strings.ToLower(u)] = true
	}
}

// userCanRead is has_permission(doc, user=user) for another user: a user
// DenyUser names reads only through a share; s.mu must be held.
func (s *Site) userCanRead(doctype, name, user string) bool {
	return !s.cannotRead[doctype][strings.ToLower(user)] ||
		s.findShare(doctype, name, user, false) != nil || s.findShare(doctype, name, "", true) != nil
}

// sessionUser is the user requests run as (frappe.session.user); s.mu must
// be held.
func (s *Site) sessionUser() string {
	if s.userName != "" {
		return s.userName
	}
	return Username
}

// msgprints wraps a result with msgprint alerts in _server_messages.
func msgprints(result interface{}, msgs ...string) interface{} {
	if len(msgs) == 0 {
		return result
	}
	list := make([]string, 0, len(msgs))
	for _, m := range msgs {
		b, _ := json.Marshal(map[string]interface{}{"message": m, "alert": true})
		list = append(list, string(b))
	}
	sm, _ := json.Marshal(list)
	return Response{"message": result, "_server_messages": string(sm)}
}

// userExists reports whether user is a User; s.mu must be held.
func (s *Site) userExists(user string) bool {
	if user == "Administrator" || user == "Guest" {
		return true
	}
	_, ok := s.doctypes["User"][user]
	return ok
}

func linkError(label, value string) *Error {
	return &Error{http.StatusExpectationFailed, "LinkValidationError", fmt.Sprintf("Could not find %s: %s", label, value)}
}

// insert stores a new document of a collab DocType; s.mu must be held.
func (s *Site) insert(doctype string, doc map[string]interface{}) map[string]interface{} {
	if s.doctypes[doctype] == nil {
		s.doctypes[doctype] = map[string]map[string]interface{}{}
	}
	s.seq++
	doc["name"] = fmt.Sprintf("%s-%04d", strings.ToLower(strings.ReplaceAll(doctype, " ", "")), s.seq)
	doc["doctype"] = doctype
	s.stamp(doctype, doc, true)
	s.doctypes[doctype][doc["name"].(string)] = doc
	return copyDoc(doc)
}

func (s *Site) addComment(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	var missing []string
	for _, k := range []string{"reference_doctype", "reference_name", "content", "comment_email", "comment_by"} {
		if _, ok := args[k]; !ok {
			missing = append(missing, "'"+k+"'")
		}
	}
	if len(missing) > 0 {
		return nil, &Error{http.StatusInternalServerError, "TypeError", fmt.Sprintf("add_comment() missing %d required positional arguments: %s", len(missing), strings.Join(missing, " and "))}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doctype, name := argString(args, "reference_doctype"), argString(args, "reference_name")
	if _, err := s.collabDoc(doctype, name, "read"); err != nil {
		return nil, err
	}
	return s.insert("Comment", map[string]interface{}{
		"comment_type": "Comment", "reference_doctype": doctype, "reference_name": name,
		"content": argString(args, "content"), "comment_email": argString(args, "comment_email"),
		"comment_by": argString(args, "comment_by"),
	}), nil
}

// openToDos lists the open assignments of a document like assign_to.get:
// [{"owner": user, "name": todo}]; s.mu must be held.
func (s *Site) openToDos(doctype, name string) []interface{} {
	out := []interface{}{}
	for _, t := range s.doctypes["ToDo"] {
		if t["reference_type"] == doctype && t["reference_name"] == name && t["status"] == "Open" {
			out = append(out, map[string]interface{}{"owner": t["allocated_to"], "name": t["name"]})
		}
	}
	return out
}

// syncAssign rewrites the document's _assign from its open ToDos, as
// ToDo.update_in_reference does; s.mu must be held.
func (s *Site) syncAssign(doc map[string]interface{}, doctype, name string) {
	users := []string{}
	for _, t := range s.openToDos(doctype, name) {
		users = append(users, fmt.Sprint(t.(map[string]interface{})["owner"]))
	}
	b, _ := json.Marshal(users)
	doc["_assign"] = string(b)
}

func (s *Site) assignAdd(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	var users []string
	switch v := args["assign_to"].(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &users); err != nil {
			return nil, Validation("assign_to must be a JSON list")
		}
	case []interface{}:
		for _, u := range v {
			users = append(users, fmt.Sprint(u))
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doctype, name := argString(args, "doctype"), argString(args, "name")
	var duplicates, shared []string
	for _, u := range users {
		doc, err := s.collabDoc(doctype, name, "read")
		if err != nil {
			return nil, err
		}
		if !s.userExists(u) {
			return nil, linkError("Allocated To", u)
		}
		dup := false
		for _, t := range s.openToDos(doctype, name) {
			if strings.EqualFold(fmt.Sprint(t.(map[string]interface{})["owner"]), u) {
				dup = true
			}
		}
		if dup {
			duplicates = append(duplicates, u)
			continue
		}
		desc := argString(args, "description")
		if strings.TrimSpace(desc) == "" {
			desc = fmt.Sprintf("Assignment for %s %s", doctype, name)
		}
		priority := argString(args, "priority")
		if priority == "" {
			priority = "Medium"
		}
		date := s.clock.Format("2006-01-02")
		if _, ok := args["date"]; ok {
			date = argString(args, "date")
		}
		// An assignee who cannot read the document gets it shared read-only
		// through frappe.share.add, which checks that the session user may
		// share (assign_to.py, add). The fake checks before inserting the
		// ToDo; Frappe rolls the insert back.
		if !s.userCanRead(doctype, name, u) {
			if s.denied[doctype]["share"] {
				return nil, Permission(fmt.Sprintf("No permission to share %s %s", doctype, name))
			}
			s.insert("DocShare", map[string]interface{}{
				"share_doctype": doctype, "share_name": name, "user": u, "everyone": json.Number("0"),
				"read": json.Number("1"), "write": json.Number("0"), "submit": json.Number("0"), "share": json.Number("0"),
			})
			shared = append(shared, u)
		}
		s.insert("ToDo", map[string]interface{}{
			"allocated_to": u, "reference_type": doctype, "reference_name": name, "description": desc,
			"priority": priority, "status": "Open", "date": date, "assigned_by": "Administrator",
		})
		s.syncAssign(doc, doctype, name)
	}
	var msgs []string
	if len(shared) > 0 {
		msgs = append(msgs, "Shared with the following Users with Read access:<br><br>"+strings.Join(shared, "<br>"))
	}
	if len(duplicates) > 0 {
		msgs = append(msgs, "Already in the following Users ToDo list:<br><br>"+strings.Join(duplicates, "<br>"))
	}
	return msgprints(s.openToDos(doctype, name), msgs...), nil
}

func (s *Site) assignRemove(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doctype, name, user := argString(args, "doctype"), argString(args, "name"), argString(args, "assign_to")
	doc, err := s.collabDoc(doctype, name, "read")
	if err != nil {
		return nil, err
	}
	for _, t := range s.doctypes["ToDo"] {
		if t["reference_type"] == doctype && t["reference_name"] == name && t["allocated_to"] == user && t["status"] != "Cancelled" {
			t["status"] = "Cancelled"
			break
		}
	}
	s.syncAssign(doc, doctype, name)
	return s.openToDos(doctype, name), nil
}

func splitTags(doc map[string]interface{}) []string {
	var out []string
	for _, t := range strings.Split(argString(doc, "_user_tags"), ",") {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (s *Site) addTag(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tag, doctype, name := argString(args, "tag"), argString(args, "dt"), argString(args, "dn")
	doc, err := s.collabDoc(doctype, name, "write")
	if err != nil {
		return nil, err
	}
	tags := splitTags(doc)
	for _, t := range tags {
		if t == tag {
			return tag, nil
		}
	}
	doc["_user_tags"] = strings.Join(append(tags, tag), ",")
	if _, ok := s.doctypes["Tag"][tag]; !ok {
		if s.doctypes["Tag"] == nil {
			s.doctypes["Tag"] = map[string]map[string]interface{}{}
		}
		s.doctypes["Tag"][tag] = map[string]interface{}{"name": tag, "doctype": "Tag"}
	}
	s.insert("Tag Link", map[string]interface{}{"document_type": doctype, "document_name": name, "tag": tag})
	return tag, nil
}

func (s *Site) removeTag(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tag, doctype, name := argString(args, "tag"), argString(args, "dt"), argString(args, "dn")
	doc, err := s.collabDoc(doctype, name, "write")
	if err != nil {
		return nil, err
	}
	var keep []string
	for _, t := range splitTags(doc) {
		if !strings.EqualFold(t, tag) {
			keep = append(keep, t)
		}
	}
	doc["_user_tags"] = strings.Join(keep, ",")
	for k, l := range s.doctypes["Tag Link"] {
		if l["document_type"] == doctype && l["document_name"] == name && strings.EqualFold(fmt.Sprint(l["tag"]), tag) {
			delete(s.doctypes["Tag Link"], k)
		}
	}
	return nil, nil
}

// isSet reports whether a meta flag is on (1, "1" or true).
func isSet(v interface{}) bool {
	switch fmt.Sprint(v) {
	case "1", "true":
		return true
	}
	return false
}

func flag(args map[string]interface{}, k string) int {
	switch v := args[k].(type) {
	case bool:
		if v {
			return 1
		}
	case json.Number:
		if v.String() != "0" {
			return 1
		}
	case float64:
		if v != 0 {
			return 1
		}
	case string:
		if v != "" && v != "0" && !strings.EqualFold(v, "false") {
			return 1
		}
	}
	return 0
}

// findShare returns the DocShare of a user (or of everyone); s.mu must be
// held.
func (s *Site) findShare(doctype, name, user string, everyone bool) map[string]interface{} {
	for _, d := range s.doctypes["DocShare"] {
		if d["share_doctype"] != doctype || d["share_name"] != name {
			continue
		}
		if everyone && d["everyone"] == json.Number("1") || !everyone && d["everyone"] != json.Number("1") && strings.EqualFold(fmt.Sprint(d["user"]), user) {
			return d
		}
	}
	return nil
}

func (s *Site) shareAdd(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doctype, name := argString(args, "doctype"), argString(args, "name")
	if _, err := s.collabDoc(doctype, name, "share"); err != nil {
		return nil, err
	}
	for _, p := range []string{"write", "submit"} {
		if flag(args, p) == 1 && s.denied[doctype][p] {
			return nil, Permission(fmt.Sprintf("You cannot share `%s` on %s `%s` as you do not have `%s` permission on `%s`", p, doctype, name, p, doctype))
		}
	}
	everyone := flag(args, "everyone") == 1
	user := argString(args, "user")
	if user == "" {
		user = s.sessionUser() // add_docshare; DocShare.validate_user drops it for everyone
	}
	if !everyone && !s.userExists(user) {
		return nil, linkError("User", user)
	}
	if flag(args, "submit") == 1 && !isSet(s.metaFlag[doctype]["is_submittable"]) {
		return nil, Validation(fmt.Sprintf("Cannot share %s with submit permission as the doctype %s is not submittable", name, doctype))
	}
	d := s.findShare(doctype, name, user, everyone)
	if d == nil {
		d = map[string]interface{}{"share_doctype": doctype, "share_name": name}
		if everyone {
			d["user"] = nil
		} else {
			d["user"] = user
		}
		key := s.insert("DocShare", d)["name"].(string)
		d = s.doctypes["DocShare"][key]
	}
	d["everyone"] = json.Number(fmt.Sprint(flag(args, "everyone")))
	d["read"] = json.Number("1")
	for _, p := range []string{"write", "submit", "share"} {
		d[p] = json.Number(fmt.Sprint(flag(args, p)))
	}
	return copyDoc(d), nil
}

func (s *Site) shareSetPermission(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	for _, k := range []string{"doctype", "name", "user", "permission_to"} {
		if _, ok := args[k]; !ok {
			return nil, &Error{http.StatusInternalServerError, "TypeError", fmt.Sprintf("set_permission() missing 1 required positional argument: '%s'", k)}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doctype, name := argString(args, "doctype"), argString(args, "name")
	if s.denied[doctype]["share"] {
		return nil, Permission(fmt.Sprintf("No permission to share %s %s", doctype, name))
	}
	perm := argString(args, "permission_to")
	everyone := flag(args, "everyone") == 1
	user := argString(args, "user")
	value := 1 // Frappe's default
	if _, ok := args["value"]; ok {
		value = flag(args, "value")
	}
	if value == 1 && (perm == "write" || perm == "submit") && s.denied[doctype][perm] {
		return nil, Permission(fmt.Sprintf("You cannot share `%s` on %s `%s` as you do not have `%s` permission on `%s`", perm, doctype, name, perm, doctype))
	}
	d := s.findShare(doctype, name, user, everyone)
	switch {
	case d == nil && value == 0:
		return nil, nil // nothing to remove
	case d == nil:
		if !everyone && !s.userExists(user) {
			return nil, linkError("User", user)
		}
		d = map[string]interface{}{"share_doctype": doctype, "share_name": name, "user": user, "everyone": json.Number("0"),
			"read": json.Number("1"), "write": json.Number("0"), "submit": json.Number("0"), "share": json.Number("0")}
		if everyone {
			d["user"], d["everyone"] = nil, json.Number("1")
		}
		key := s.insert("DocShare", d)["name"].(string)
		d = s.doctypes["DocShare"][key]
	}
	d[perm] = json.Number(fmt.Sprint(value))
	if perm == "read" && value == 0 {
		delete(s.doctypes["DocShare"], fmt.Sprint(d["name"]))
	}
	return copyDoc(d), nil
}

func (s *Site) shareGetUsers(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doctype, name := argString(args, "doctype"), argString(args, "name")
	if _, ok := s.doctypes[doctype][name]; !ok {
		return nil, NotFound(fmt.Sprintf("%s %s not found", doctype, name))
	}
	out := []interface{}{}
	if s.denied[doctype]["read"] {
		return out, nil
	}
	for _, d := range s.doctypes["DocShare"] {
		if d["share_doctype"] == doctype && d["share_name"] == name {
			out = append(out, copyDoc(d))
		}
	}
	return out, nil
}
