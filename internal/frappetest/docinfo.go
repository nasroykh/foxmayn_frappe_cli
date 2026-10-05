package frappetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Document context methods, shaped like the v16 answers (checked by the
// contract tests): frappe.desk.form.load.get_docinfo and getdoc,
// frappe.desk.form.activity.get_activity_timeline and
// frappe.desk.notifications.get_open_count. The docinfo is built from the
// stored documents that reference the document, as Frappe builds it:
//
//	Version       ref_doctype, docname, data (JSON string)
//	Comment       reference_doctype, reference_name, comment_type, content
//	File          attached_to_doctype, attached_to_name, file_name, file_url, is_private
//	ToDo          reference_type, reference_name, allocated_to, status, description
//	DocShare      share_doctype, share_name, user, read, write, share, submit, everyone
//	Tag Link      document_type, document_name, tag
//	Communication reference_doctype, reference_name, subject, sender, communication_type
//
// HandleMethod replaces any of them; HandleMethod(name, nil) removes one, as
// on a site whose Frappe lacks it.

func (s *Site) registerDocInfo() {
	s.methods["frappe.desk.form.load.get_docinfo"] = s.getDocinfo
	s.methods["frappe.desk.form.load.getdoc"] = s.getdoc
	s.methods["frappe.desk.form.activity.get_activity_timeline"] = s.activityTimeline
	s.methods["frappe.desk.notifications.get_open_count"] = s.openCount
}

// Dashboard declares a DocType's Connections for get_open_count: each
// linked DocType and the field of it that holds this DocType's names.
func (s *Site) Dashboard(doctype string, links map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dashboards == nil {
		s.dashboards = map[string]map[string]string{}
	}
	s.dashboards[doctype] = links
}

// Onload sets the __onload values getdoc returns with a document.
func (s *Site) Onload(doctype, name string, values map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onload == nil {
		s.onload = map[string]map[string]interface{}{}
	}
	s.onload[doctype+"\x00"+name] = values
}

// refs returns copies of the stored docType documents whose fields equal
// the given values, oldest first; the caller holds s.mu.
func (s *Site) refs(doctype string, eq map[string]string) []map[string]interface{} {
	var out []map[string]interface{}
	for _, d := range s.doctypes[doctype] {
		match := true
		for k, v := range eq {
			if fmt.Sprint(d[k]) != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, copyDoc(d))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := fmt.Sprint(out[i]["creation"]), fmt.Sprint(out[j]["creation"])
		if a != b {
			return a < b
		}
		return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"])
	})
	return out
}

// target finds the document a context method names, failing like
// frappe.get_lazy_doc; the caller holds s.mu.
func (s *Site) target(args map[string]interface{}) (string, string, map[string]interface{}, error) {
	dt, name := argString(args, "doctype"), argString(args, "name")
	docs, ok := s.doctypes[dt]
	if !ok {
		return "", "", nil, NotFound(fmt.Sprintf("DocType %s not found", dt))
	}
	doc, ok := docs[name]
	if !ok {
		return "", "", nil, NotFound(fmt.Sprintf("%s %s not found", dt, name))
	}
	return dt, name, doc, nil
}

func pick(d map[string]interface{}, keys ...string) map[string]interface{} {
	out := make(map[string]interface{}, len(keys))
	for _, k := range keys {
		out[k] = d[k]
	}
	return out
}

// docinfo builds get_docinfo's object; the caller holds s.mu.
func (s *Site) docinfo(dt, name string) map[string]interface{} {
	lists := map[string][]interface{}{}
	for _, k := range []string{"comments", "assignment_logs", "attachment_logs", "info_logs", "like_logs", "workflow_logs"} {
		lists[k] = []interface{}{}
	}
	for _, c := range s.refs("Comment", map[string]string{"reference_doctype": dt, "reference_name": name}) {
		key := map[string]string{
			"Comment": "comments", "Assigned": "assignment_logs", "Assignment Completed": "assignment_logs",
			"Attachment": "attachment_logs", "Attachment Removed": "attachment_logs",
			"Info": "info_logs", "Edit": "info_logs", "Label": "info_logs", "Like": "like_logs", "Workflow": "workflow_logs",
		}[fmt.Sprint(c["comment_type"])]
		if key != "" {
			lists[key] = append(lists[key], pick(c, "name", "creation", "content", "owner", "comment_type", "published"))
		}
	}
	versions := []interface{}{}
	vs := s.refs("Version", map[string]string{"ref_doctype": dt, "docname": name})
	for i := len(vs) - 1; i >= 0 && len(versions) < 10; i-- { // newest first, at most 10
		versions = append(versions, pick(vs[i], "name", "owner", "creation", "data"))
	}
	attachments := []interface{}{}
	for _, f := range s.refs("File", map[string]string{"attached_to_doctype": dt, "attached_to_name": name}) {
		attachments = append(attachments, pick(f, "name", "file_name", "file_url", "file_type", "file_size", "is_private", "attached_to_field", "folder"))
	}
	assignments := []interface{}{}
	for _, t := range s.refs("ToDo", map[string]string{"reference_type": dt, "reference_name": name}) {
		if st := fmt.Sprint(t["status"]); st == "Cancelled" || st == "Closed" || t["allocated_to"] == nil {
			continue
		}
		assignments = append(assignments, map[string]interface{}{"name": t["name"], "owner": t["allocated_to"], "description": t["description"], "status": t["status"]})
	}
	shared := []interface{}{}
	for _, d := range s.refs("DocShare", map[string]string{"share_doctype": dt, "share_name": name}) {
		shared = append(shared, d)
	}
	var tags []string
	for _, t := range s.refs("Tag Link", map[string]string{"document_type": dt, "document_name": name}) {
		tags = append(tags, fmt.Sprint(t["tag"]))
	}
	comms, auto := []interface{}{}, []interface{}{}
	for _, c := range s.refs("Communication", map[string]string{"reference_doctype": dt, "reference_name": name}) {
		row := pick(c, "name", "communication_type", "communication_medium", "communication_date", "content", "sender", "subject", "creation")
		if c["communication_type"] == "Automated Message" {
			auto = append(auto, row)
		} else {
			comms = append(comms, row)
		}
	}
	perms := map[string]interface{}{}
	for _, p := range []string{"amend", "cancel", "create", "delete", "email", "export", "import", "print", "read", "report", "select", "share", "submit", "write"} {
		perms[p] = json.Number("1")
	}
	info := map[string]interface{}{
		"doctype": dt, "name": name,
		"attachments": attachments, "communications": comms, "automated_messages": auto,
		"versions": versions, "assignments": assignments, "permissions": perms, "shared": shared,
		"views": []interface{}{}, "additional_timeline_content": []interface{}{}, "milestones": []interface{}{},
		"is_document_followed": nil, "tags": strings.Join(tags, ","), "document_email": nil,
		"user_info": map[string]interface{}{Username: map[string]interface{}{"name": Username, "fullname": Username, "email": "admin@example.com", "image": nil}},
	}
	for k, v := range lists {
		info[k] = v
	}
	return info
}

func (s *Site) getDocinfo(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dt, name, _, err := s.target(args)
	if err != nil {
		return nil, err
	}
	return Response{"docinfo": s.docinfo(dt, name)}, nil
}

// getdoc answers {"message": []} for a missing document, as Frappe does.
func (s *Site) getdoc(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dt, name, doc, err := s.target(args)
	if err != nil {
		if fe, ok := err.(*Error); ok && strings.HasPrefix(fe.Message, "DocType ") {
			return nil, err
		}
		return []interface{}{}, nil
	}
	d := copyDoc(doc)
	onload := s.onload[dt+"\x00"+name]
	if onload == nil {
		onload = map[string]interface{}{}
	}
	d["__onload"] = onload
	return Response{"docs": []interface{}{d}, "docinfo": s.docinfo(dt, name)}, nil
}

func (s *Site) activityTimeline(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dt, name, doc, err := s.target(args)
	if err != nil {
		return nil, err
	}
	author := func(user interface{}) map[string]interface{} {
		return map[string]interface{}{"email": user, "fullname": user, "image": nil}
	}
	acts := []map[string]interface{}{{
		"type": "log", "key": "creation", "timestamp": doc["creation"], "author": author(doc["owner"]),
		"data": map[string]interface{}{"name": "creation", "subtype": "created", "text": fmt.Sprintf("%v created this document", doc["owner"])},
	}}
	for _, v := range s.refs("Version", map[string]string{"ref_doctype": dt, "docname": name}) {
		var data struct {
			Changed [][]interface{} `json:"changed"`
		}
		_ = json.Unmarshal([]byte(fmt.Sprint(v["data"])), &data)
		for i, c := range data.Changed {
			if len(c) < 3 {
				continue
			}
			acts = append(acts, map[string]interface{}{
				"type": "version", "key": fmt.Sprintf("version:%v-%d", v["name"], i), "timestamp": v["creation"], "author": author(v["owner"]),
				"data": map[string]interface{}{"fieldname": c[0], "type": "diff", "prefix": fmt.Sprintf("changed %v", c[0]),
					"from": fmt.Sprint(c[1]), "to": fmt.Sprint(c[2]), "name": fmt.Sprintf("%v-%d", v["name"], i)},
			})
		}
	}
	for _, c := range s.refs("Comment", map[string]string{"reference_doctype": dt, "reference_name": name}) {
		if c["comment_type"] == "Comment" {
			acts = append(acts, map[string]interface{}{
				"type": "comment", "key": fmt.Sprintf("comment:%v", c["name"]), "timestamp": c["creation"], "author": author(c["owner"]),
				"data": map[string]interface{}{"name": c["name"], "content": c["content"], "attachments": []interface{}{}},
			})
			continue
		}
		if act := logActivity(c); act != nil {
			act["timestamp"], act["author"] = c["creation"], author(c["owner"])
			acts = append(acts, act)
		}
	}
	sort.SliceStable(acts, func(i, j int) bool {
		a, b := fmt.Sprint(acts[i]["timestamp"]), fmt.Sprint(acts[j]["timestamp"])
		if a != b {
			return a < b
		}
		return fmt.Sprint(acts[i]["key"]) < fmt.Sprint(acts[j]["key"])
	})
	list := make([]interface{}, len(acts))
	for i, a := range acts {
		list[i] = a
	}
	return map[string]interface{}{"activities": list, "has_more_emails": false, "has_more_milestones": false, "next_milestone_start": json.Number("0")}, nil
}

// logActivity shapes a Comment that is not a plain comment as the timeline
// does (activity.py add_activity_record, attachment_log_activity): the
// comment_type picks the subtype, assignment logs name the assignee (the
// content's third word, as in "Administrator assigned jane@x: Task") and
// attachment logs the file. Types the timeline does not show give nil.
func logActivity(c map[string]interface{}) map[string]interface{} {
	content := fmt.Sprint(c["content"])
	switch ct := fmt.Sprint(c["comment_type"]); ct {
	case "Attachment", "Attachment Removed":
		action := "added"
		if ct == "Attachment Removed" {
			action = "removed"
		}
		return map[string]interface{}{"type": "attachment_log", "key": fmt.Sprintf("attachment:%v", c["name"]),
			"data": map[string]interface{}{"name": c["name"], "action": action, "fileName": content, "fileUrl": nil, "isPrivate": false}}
	case "Assigned", "Assignment Completed", "Like", "Workflow", "Info", "Edit", "Label", "Shared", "Unshared":
		subtype := map[string]string{"Assigned": "assigned", "Assignment Completed": "assignment_completed", "Like": "like",
			"Workflow": "workflow", "Info": "info", "Edit": "info", "Label": "info", "Shared": "shared", "Unshared": "shared"}[ct]
		data := map[string]interface{}{"name": c["name"], "subtype": subtype, "text": content}
		switch subtype {
		case "assigned", "assignment_completed":
			if f := strings.Fields(content); len(f) > 2 {
				data["assignee"] = strings.TrimSuffix(f[2], ":")
			}
		case "workflow", "info":
			data["text"] = fmt.Sprintf("%v %s", c["owner"], content)
		}
		return map[string]interface{}{"type": "log", "key": fmt.Sprintf("log:%v", c["name"]), "data": data}
	}
	return nil
}

// openCount counts, per DocType declared with Dashboard, the documents
// whose link field holds the name, at most 100 like get_doc_count.
func (s *Site) openCount(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dt, name, _, err := s.target(args)
	if err != nil {
		return nil, err
	}
	links := s.dashboards[dt]
	ext := []interface{}{}
	for _, linked := range sortedKeys(links) {
		n := len(s.refs(linked, map[string]string{links[linked]: name}))
		if n > 100 {
			n = 100
		}
		ext = append(ext, map[string]interface{}{"doctype": linked, "count": json.Number(fmt.Sprint(n)), "open_count": json.Number("0")})
	}
	return map[string]interface{}{"count": map[string]interface{}{"external_links_found": ext, "internal_links_found": []interface{}{}}}, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
