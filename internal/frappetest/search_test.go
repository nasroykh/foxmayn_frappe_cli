package frappetest_test

import (
	"strings"
	"testing"
)

func TestSearchLink(t *testing.T) {
	s := fkSeed(t)
	s.SearchFields("Item", "color")
	get := func(q string) resp {
		return fkDo(t, s, "GET", "/api/method/frappe.desk.search.search_link?"+q, "", fkKey)
	}
	rows := func(r resp) []interface{} {
		t.Helper()
		m, ok := r.Body["message"].([]interface{})
		if !ok {
			t.Fatalf("message is not a list: %s", r.Raw)
		}
		return m
	}
	m := rows(get("doctype=Item&txt=red"))
	if len(m) != 2 {
		t.Fatalf("rows %v", m)
	}
	first := m[0].(map[string]interface{})
	if first["value"] != "a" || first["description"] != "red" || first["label"] != "a" {
		t.Fatalf("row %v", first)
	}
	if n := len(rows(get("doctype=Item&txt="))); n != 3 {
		t.Errorf("empty txt: %d rows", n)
	}
	if n := len(rows(get("doctype=Item&txt=&page_length=2"))); n != 2 {
		t.Errorf("page_length 2: %d rows", n)
	}
	if n := len(rows(get("doctype=Item&txt=&page_length=0"))); n != 3 {
		t.Errorf("page_length 0 means no limit: %d rows", n)
	}
	// A name prefix sorts before a plain substring.
	s.Add("Item", map[string]interface{}{"name": "xa"})
	if got := rows(get("doctype=Item&txt=a"))[0].(map[string]interface{})["value"]; got != "a" {
		t.Errorf("first = %v", got)
	}
	if r := get("doctype=Nope&txt=a"); r.Status != 404 || r.Body["exc_type"] != "DoesNotExistError" {
		t.Errorf("unknown DocType: %d %s", r.Status, r.Raw)
	}
	if r := get("doctype=Item"); r.Status != 417 {
		t.Errorf("missing txt: %d", r.Status)
	}
}

func TestGlobalSearch(t *testing.T) {
	s := fkSeed(t)
	s.Add("Note", map[string]interface{}{"name": "n1", "title": "red note"})
	s.GlobalSearch("Item", "color")
	raw := func(q string) resp {
		return fkDo(t, s, "GET", "/api/method/frappe.utils.global_search.search?"+q, "", fkKey)
	}
	get := func(q string) []interface{} {
		t.Helper()
		r := raw(q)
		m, ok := r.Body["message"].([]interface{})
		if !ok {
			t.Fatalf("message is not a list: %s", r.Raw)
		}
		return m
	}
	m := get("text=red")
	if len(m) != 2 {
		t.Fatalf("rows %v", m)
	}
	row := m[0].(map[string]interface{})
	if row["doctype"] != "Item" || row["name"] != "a" || row["content"] != "Name : a ||| color : red" {
		t.Fatalf("row %v", row)
	}
	if got := strings.Count(raw("text=red").Raw, `"rank":1.0`); got != 2 {
		t.Errorf("rank must keep its decimal point: %d in %s", got, raw("text=red").Raw)
	}
	if n := len(get("text=red&limit=1")); n != 1 {
		t.Errorf("limit: %d rows", n)
	}
	// "&" separates phrases whose hits are combined.
	if n := len(get("text=blue%20%26%20red")); n != 3 {
		t.Errorf("two phrases: %d rows", n)
	}
	if n := len(get("text=")); n != 0 {
		t.Errorf("empty text: %d rows", n)
	}
	if n := len(get("text=a&doctype=Note")); n != 0 {
		t.Errorf("Note is not indexed: %d rows", n)
	}
}
