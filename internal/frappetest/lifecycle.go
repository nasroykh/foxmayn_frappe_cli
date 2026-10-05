package frappetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Lifecycle methods with Frappe's docstatus rules (0 draft, 1 submitted,
// 2 cancelled) and error shapes, checked against v15 and v16 by the contract
// tests. HandleMethod replaces any of them.

func docstatusTransitionError(from, to string) *Error {
	return &Error{http.StatusExpectationFailed, "DocstatusTransitionError", fmt.Sprintf("Cannot change docstatus from %s to %s", from, to)}
}

// setDocstatus moves a stored document from one docstatus to another.
func (s *Site) setDocstatus(doctype, name, from, to string) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.doctypes[doctype][name]
	if !ok {
		return nil, NotFound(fmt.Sprintf("%s %s not found", doctype, name))
	}
	if cur := fmt.Sprint(doc["docstatus"]); cur != from {
		return nil, docstatusTransitionError(cur, to)
	}
	doc["docstatus"] = json.Number(to)
	s.stamp(doctype, doc, false)
	return copyDoc(doc), nil
}

func argString(args map[string]interface{}, k string) string {
	if v, ok := args[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func (s *Site) submit(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	doc, ok := args["doc"].(map[string]interface{})
	if !ok {
		if raw, isStr := args["doc"].(string); isStr {
			_ = json.Unmarshal([]byte(raw), &doc)
		}
	}
	if doc == nil {
		return nil, Validation("doc is required")
	}
	doctype, name := argString(doc, "doctype"), argString(doc, "name")
	if stored, ok := s.Doc(doctype, name); ok && argString(doc, "modified") != argString(stored, "modified") {
		return nil, &Error{http.StatusExpectationFailed, "TimestampMismatchError", "Error: Document has been modified after you have opened it. Please refresh to get the latest document."}
	}
	return s.setDocstatus(doctype, name, "0", "1")
}

func (s *Site) cancel(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	return s.setDocstatus(argString(args, "doctype"), argString(args, "name"), "1", "2")
}

func (s *Site) discard(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	_, err := s.setDocstatus(argString(args, "doctype"), argString(args, "name"), "0", "2")
	return nil, err
}

func (s *Site) renameDoc(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	doctype, oldName, newName := argString(args, "doctype"), argString(args, "old_name"), argString(args, "new_name")
	merge := argString(args, "merge") == "true" || argString(args, "merge") == "1"
	s.mu.Lock()
	defer s.mu.Unlock()
	docs := s.doctypes[doctype]
	doc, ok := docs[oldName]
	if !ok {
		return nil, NotFound(fmt.Sprintf("%s %s not found", doctype, oldName))
	}
	_, exists := docs[newName]
	switch {
	case exists && !merge:
		return nil, Duplicate(fmt.Sprintf("Another %s with name %s exists, select another name", doctype, newName))
	case !exists && merge:
		return nil, Validation(fmt.Sprintf("%s %s does not exist, select a new target to merge", doctype, newName))
	}
	delete(docs, oldName)
	if !merge {
		doc["name"] = newName
		docs[newName] = doc
	}
	return newName, nil
}

func (s *Site) submittedLinkedDocs(_ *http.Request, _ map[string]interface{}) (interface{}, error) {
	return map[string]interface{}{"docs": []interface{}{}, "count": 0}, nil
}

// getdoctype answers frappe.desk.form.load.getdoctype like Frappe, with the
// metas in "docs" (the DocType's, then its child tables'), each listing the
// fields declared with DocField, NoCopy and ChildTable.
func (s *Site) getdoctype(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doctype := argString(args, "doctype")
	if _, ok := s.doctypes[doctype]; !ok {
		return nil, NotFound(fmt.Sprintf("DocType %s not found", doctype))
	}
	dts := []string{doctype}
	for _, child := range s.tables[doctype] {
		dts = append(dts, child)
	}
	var docs []interface{}
	for _, dt := range dts {
		fields := []interface{}{}
		for _, f := range s.meta[dt] {
			fields = append(fields, map[string]interface{}{"fieldname": f.name, "fieldtype": f.fieldtype, "no_copy": f.noCopy,
				"options": f.options, "permlevel": f.permlevel})
		}
		perms := []interface{}{}
		for _, row := range s.docPerms[dt] {
			perms = append(perms, row)
		}
		meta := map[string]interface{}{"name": dt, "fields": fields, "permissions": perms,
			"istable": 0, "is_submittable": 0, "allow_import": 0}
		for k, v := range s.metaFlag[dt] {
			meta[k] = v
		}
		docs = append(docs, meta)
	}
	return Response{"docs": docs}, nil
}

// metaField is a field of the fake meta.
type metaField struct {
	name, fieldtype string
	noCopy          int
	options         string // the child DocType of a Table field
	permlevel       int
}

func (s *Site) addField(doctype string, f metaField) {
	for i, old := range s.meta[doctype] {
		if old.name == f.name {
			s.meta[doctype][i] = f
			return
		}
	}
	s.meta[doctype] = append(s.meta[doctype], f)
}

// DocField declares a field of a DocType's meta with its fieldtype.
func (s *Site) DocField(doctype, fieldname, fieldtype string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addField(doctype, metaField{name: fieldname, fieldtype: fieldtype})
}

// Permlevel sets the permission level of a declared field.
func (s *Site) Permlevel(doctype, fieldname string, level int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, f := range s.meta[doctype] {
		if f.name == fieldname {
			s.meta[doctype][i].permlevel = level
			return
		}
	}
	panic("frappetest: Permlevel on undeclared field " + doctype + "." + fieldname)
}

// NoCopy declares fields of a DocType marked "no copy".
func (s *Site) NoCopy(doctype string, fields ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range fields {
		s.addField(doctype, metaField{name: f, fieldtype: "Data", noCopy: 1})
	}
}

// ChildTable declares the table field of parent whose rows are child
// documents: created rows get their identity (doctype, name, parent,
// parentfield, parenttype, idx) like on a real site.
func (s *Site) ChildTable(parent, field, child string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tables[parent] == nil {
		s.tables[parent] = map[string]string{}
	}
	s.tables[parent][field] = child
	s.addField(parent, metaField{name: field, fieldtype: "Table", options: child})
}

// AllowOnSubmit declares fields that a submitted document may still change.
func (s *Site) AllowOnSubmit(doctype string, fields ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onSubmit[doctype] == nil {
		s.onSubmit[doctype] = map[string]bool{}
	}
	for _, f := range fields {
		s.onSubmit[doctype][f] = true
	}
}

// stampRows gives the rows of declared child tables their identity; the
// caller holds s.mu.
func (s *Site) stampRows(doctype string, d map[string]interface{}) {
	for field, child := range s.tables[doctype] {
		rows, _ := d[field].([]interface{})
		for i, r := range rows {
			row, ok := r.(map[string]interface{})
			if !ok {
				continue
			}
			s.seq++
			row["name"] = fmt.Sprintf("row-%04d", s.seq)
			row["doctype"], row["parent"], row["parentfield"], row["parenttype"] = child, d["name"], field, doctype
			row["idx"] = json.Number(fmt.Sprint(i + 1))
		}
	}
}

// amendmentName is the name Frappe gives an amendment of orig: orig-1, or
// <prefix>-<n+1> when orig is itself the amendment <prefix>-<n>. The caller
// holds s.mu. A second amendment of the same document gets the same name
// and fails as a duplicate, like on a real site.
func (s *Site) amendmentName(doctype, orig string) string {
	if from, _ := s.doctypes[doctype][orig]["amended_from"].(string); from != "" {
		if i := strings.LastIndex(orig, "-"); i > 0 {
			var n int
			if _, err := fmt.Sscanf(orig[i+1:], "%d", &n); err == nil {
				return fmt.Sprintf("%s-%d", orig[:i], n+1)
			}
		}
	}
	return orig + "-1"
}

func (s *Site) registerLifecycle() {
	s.methods["frappe.client.submit"] = s.submit
	s.methods["frappe.client.cancel"] = s.cancel
	s.methods["frappe.client.rename_doc"] = s.renameDoc
	s.methods["frappe.desk.form.save.discard"] = s.discard
	s.methods["frappe.desk.form.linked_with.get_submitted_linked_docs"] = s.submittedLinkedDocs
	s.methods["frappe.desk.form.load.getdoctype"] = s.getdoctype
}
