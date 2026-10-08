package frappetest

import (
	"encoding/json"
	"fmt"
	"sort"
)

// childList answers frappe.client.get_list on a child DocType with
// `parent` (the parent DocType), as Frappe does for a child table query
// (parent_doctype): the rows of every table of that child DocType in the
// parent's documents, narrowed by filters (parent, parentfield, parenttype
// and the rows' own fields), ordered by order_by, with the requested
// fields. limit_page_length 0 means every row; the default is 20.
func (s *Site) childList(doctype, parent string, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	var tables []string
	for field, child := range s.tables[parent] {
		if child == doctype {
			tables = append(tables, field)
		}
	}
	sort.Strings(tables)
	var rows []map[string]interface{}
	for _, d := range s.doctypes[parent] {
		for _, field := range tables {
			list, _ := d[field].([]interface{})
			for _, r := range list {
				if m, ok := r.(map[string]interface{}); ok {
					rows = append(rows, copyDoc(m))
				}
			}
		}
	}
	s.mu.Unlock()
	if len(tables) == 0 {
		return nil, Permission(fmt.Sprintf("%s is not a child table of %s", doctype, parent))
	}

	filters := args["filters"]
	if str, ok := filters.(string); ok && str != "" {
		var v interface{}
		if err := json.Unmarshal([]byte(str), &v); err != nil {
			return nil, Validation("filters must be JSON")
		}
		filters = v
	}
	conds, err := parseFilters(filters)
	if err != nil {
		return nil, err
	}
	var matched []map[string]interface{}
	for _, r := range rows {
		ok := true
		for _, c := range conds {
			if !c.match(r[c.field]) {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, r)
		}
	}
	if err := sortRows(matched, argString(args, "order_by")); err != nil {
		return nil, err
	}
	limit := 20
	if v, ok := args["limit_page_length"]; ok {
		if n, isNum := number(v); isNum {
			limit = int(n)
		} else if _, err := fmt.Sscan(fmt.Sprint(v), &limit); err != nil {
			return nil, Validation("limit_page_length must be a number")
		}
	}
	if limit > 0 && limit < len(matched) {
		matched = matched[:limit]
	}
	fields := toStrings(args["fields"])
	if len(fields) == 0 {
		fields = []string{"name"}
	}
	out := []interface{}{}
	for _, r := range matched {
		row := map[string]interface{}{}
		for _, f := range fields {
			row[f] = r[f]
		}
		out = append(out, row)
	}
	return out, nil
}
