package frappetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Search methods, shaped like Frappe's (v15 and v16, checked by the contract
// tests): frappe.desk.search.search_link answers a list of
// {value, description, label}, frappe.utils.global_search.search a list of
// {doctype, name, content, rank} with content "Label : value ||| ...".
// HandleMethod replaces either.

// pageLengthForLinkValidation is what search_widget uses for a page_length
// of 0 or less.
const pageLengthForLinkValidation = 25000

// SearchFields declares the fields search_link also matches and shows in
// description, like a DocType's search_fields.
func (s *Site) SearchFields(doctype string, fields ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.searchFields == nil {
		s.searchFields = map[string][]string{}
	}
	s.searchFields[doctype] = fields
}

// GlobalSearch puts a DocType into the global search index, indexing the
// given fields (the DocType's "in global search" fields) next to its name.
// DocTypes never passed here are not searched, as on a real site where they
// are missing from Global Search Settings.
func (s *Site) GlobalSearch(doctype string, fields ...string) {
	s.AddDocType(doctype)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.globalSearch == nil {
		s.globalSearch = map[string][]string{}
	}
	s.globalSearch[doctype] = fields
}

func (s *Site) registerSearch() {
	s.methods["frappe.desk.search.search_link"] = s.searchLink
	s.methods["frappe.utils.global_search.search"] = s.globalSearchMethod
}

func (s *Site) searchLink(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	doctype := argString(args, "doctype")
	if doctype == "" {
		return nil, Validation("doctype is required")
	}
	txt, has := args["txt"]
	if !has {
		return nil, Validation("txt is required")
	}
	needle := strings.ToLower(strings.TrimSpace(fmt.Sprint(txt)))
	limit := pageLength(args["page_length"], 10)

	s.mu.Lock()
	defer s.mu.Unlock()
	docs, ok := s.doctypes[doctype]
	if !ok {
		return nil, NotFound(fmt.Sprintf("DocType %s not found", doctype))
	}
	fields := s.searchFields[doctype]
	type hit struct {
		name, desc string
		prefix     bool
	}
	var hits []hit
	for name, doc := range docs {
		var parts []string
		for _, f := range fields {
			if v := argString(doc, f); v != "" {
				parts = append(parts, v)
			}
		}
		match := strings.Contains(strings.ToLower(name), needle)
		for _, p := range parts {
			match = match || strings.Contains(strings.ToLower(p), needle)
		}
		if match {
			hits = append(hits, hit{name, strings.Join(parts, ", "), strings.HasPrefix(strings.ToLower(name), needle)})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].prefix != hits[j].prefix {
			return hits[i].prefix
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]map[string]interface{}, len(hits))
	for i, h := range hits {
		out[i] = map[string]interface{}{"value": h.name, "description": h.desc, "label": h.name}
	}
	return out, nil
}

func (s *Site) globalSearchMethod(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	limit := pageLength(args["limit"], 20)
	only := argString(args, "doctype")
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]interface{}
	seen := map[string]bool{}
	for _, word := range strings.Split(argString(args, "text"), "&") {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" {
			continue
		}
		if seen[word] {
			continue
		}
		seen[word] = true
		var hits []map[string]interface{}
		for dt, fields := range s.globalSearch {
			if only != "" && dt != only {
				continue
			}
			for name, doc := range s.doctypes[dt] {
				content := []string{"Name : " + name}
				for _, f := range fields {
					if v := argString(doc, f); v != "" {
						content = append(content, f+" : "+v)
					}
				}
				joined := strings.Join(content, " ||| ")
				if strings.Contains(strings.ToLower(joined), word) {
					hits = append(hits, map[string]interface{}{"doctype": dt, "name": name, "content": joined, "rank": json.Number("1.0")})
				}
			}
		}
		sort.Slice(hits, func(i, j int) bool {
			if hits[i]["doctype"] != hits[j]["doctype"] {
				return hits[i]["doctype"].(string) < hits[j]["doctype"].(string)
			}
			return hits[i]["name"].(string) < hits[j]["name"].(string)
		})
		if len(hits) > limit {
			hits = hits[:limit]
		}
		out = append(out, hits...)
	}
	if out == nil {
		out = []map[string]interface{}{}
	}
	return out, nil
}

// pageLength reads a numeric argument; 0 or less means "no limit" for
// search_link (a large page) and an empty page for global search, which the
// callers never send.
func pageLength(v interface{}, def int) int {
	if v == nil {
		return def
	}
	n, err := strconv.Atoi(fmt.Sprint(v))
	if err != nil {
		return def
	}
	if n <= 0 {
		return pageLengthForLinkValidation
	}
	return n
}
