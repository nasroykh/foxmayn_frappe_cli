package frappetest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// frappe.client.insert_many (frappe/client.py:231): the documents come as a
// list or a JSON string, more than 200 are refused (417), each is inserted
// as its own "doctype", and the names come back in order. Frappe runs the
// request in one transaction, so a failing document rolls back the ones
// before it: here the documents already inserted are removed again, and the
// name counter and the clock go back with them.

const maxInsertMany = 200

func (s *Site) registerInsertMany() {
	s.methods["frappe.client.insert_many"] = s.insertMany
}

func (s *Site) insertMany(r *http.Request, args map[string]interface{}) (interface{}, error) {
	docs, err := insertManyDocs(args["docs"])
	if err != nil {
		return nil, err
	}
	if len(docs) > maxInsertMany {
		return nil, Validation("Only 200 inserts allowed in one request")
	}

	user := s.authenticate(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, clock := s.seq, s.clock
	type ref struct{ doctype, name string }
	var made []ref
	names := make([]interface{}, 0, len(docs))
	for _, d := range docs {
		doctype, _ := d["doctype"].(string)
		var doc map[string]interface{}
		var e *Error
		if _, known := s.doctypes[doctype]; !known {
			e = &Error{http.StatusInternalServerError, "ImportError", fmt.Sprintf("Module import failed for %s", doctype)}
		} else {
			doc, e = s.insertLocked(doctype, d, user)
		}
		if e != nil {
			for _, m := range made {
				delete(s.doctypes[m.doctype], m.name)
			}
			s.seq, s.clock = seq, clock
			return nil, e
		}
		made = append(made, ref{doctype, fmt.Sprint(doc["name"])})
		names = append(names, doc["name"])
	}
	return names, nil
}

// insertManyDocs reads the docs argument: a list of objects or a JSON
// string of one.
func insertManyDocs(v interface{}) ([]map[string]interface{}, *Error) {
	if raw, ok := v.(string); ok {
		dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
		dec.UseNumber()
		var list []interface{}
		if err := dec.Decode(&list); err != nil {
			return nil, Validation("docs is not a JSON list")
		}
		v = list
	}
	list, ok := v.([]interface{})
	if !ok {
		return nil, Validation("docs is required")
	}
	docs := make([]map[string]interface{}, len(list))
	for i, e := range list {
		d, ok := e.(map[string]interface{})
		if !ok {
			return nil, Validation(fmt.Sprintf("docs item %d is not an object", i+1))
		}
		docs[i] = d
	}
	return docs, nil
}
