package frappetest_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const insertManyPath = "/api/method/frappe.client.insert_many"

func TestInsertMany(t *testing.T) {
	s := fkSeed(t)
	r := fkDo(t, s, "POST", insertManyPath,
		`{"docs":[{"doctype":"Item","qty":1},{"doctype":"Item","name":"z","qty":2},{"doctype":"Item","qty":3}]}`, fkKey)
	names, _ := r.Body["message"].([]interface{})
	if r.Status != 200 || len(names) != 3 || names[1] != "z" {
		t.Fatalf("insert_many = %d %s", r.Status, r.Raw)
	}
	if s.Count("Item") != 6 {
		t.Errorf("count = %d, want 6", s.Count("Item"))
	}
	// The docs argument may also be a JSON string.
	r = fkDo(t, s, "POST", insertManyPath, `{"docs":"[{\"doctype\":\"Item\",\"name\":\"y\"}]"}`, fkKey)
	if names, _ := r.Body["message"].([]interface{}); r.Status != 200 || len(names) != 1 || names[0] != "y" {
		t.Fatalf("string docs = %d %s", r.Status, r.Raw)
	}
}

func TestInsertManyAllOrNothing(t *testing.T) {
	s := fkSeed(t)
	// The last document clashes with an existing one: the two before it go.
	r := fkDo(t, s, "POST", insertManyPath,
		`{"docs":[{"doctype":"Item","name":"n1"},{"doctype":"Item","name":"n2"},{"doctype":"Item","name":"a"}]}`, fkKey)
	if r.Status != http.StatusConflict || r.Body["exc_type"] != "DuplicateEntryError" {
		t.Fatalf("duplicate = %d %s", r.Status, r.Raw)
	}
	if s.Count("Item") != 3 {
		t.Errorf("count = %d, a failed batch must leave the site as it was", s.Count("Item"))
	}
	// The name counter went back too: an unnamed document of the failed
	// batch does not use up a name.
	fkDo(t, s, "POST", insertManyPath, `{"docs":[{"doctype":"Item"},{"doctype":"Item","name":"a"}]}`, fkKey)
	r = fkDo(t, s, "POST", insertManyPath, `{"docs":[{"doctype":"Item"}]}`, fkKey)
	if names, _ := r.Body["message"].([]interface{}); len(names) != 1 || names[0] != "Item-0001" {
		t.Errorf("names after a rollback = %s", r.Raw)
	}
}

func TestInsertManyLimitAndErrors(t *testing.T) {
	s := fkSeed(t)
	docs := make([]string, 201)
	for i := range docs {
		docs[i] = fmt.Sprintf(`{"doctype":"Item","name":"m%d"}`, i)
	}
	r := fkDo(t, s, "POST", insertManyPath, `{"docs":[`+strings.Join(docs, ",")+`]}`, fkKey)
	if r.Status != http.StatusExpectationFailed || !strings.Contains(r.Raw, "Only 200 inserts allowed in one request") {
		t.Fatalf("201 docs = %d %s", r.Status, r.Raw)
	}
	if s.Count("Item") != 3 {
		t.Errorf("count = %d", s.Count("Item"))
	}
	r = fkDo(t, s, "POST", insertManyPath, `{"docs":[`+strings.Join(docs[:200], ",")+`]}`, fkKey)
	if r.Status != 200 || s.Count("Item") != 203 {
		t.Errorf("200 docs = %d, count %d", r.Status, s.Count("Item"))
	}

	// Each document is inserted as its own doctype.
	s.AddDocType("Note")
	r = fkDo(t, s, "POST", insertManyPath, `{"docs":[{"doctype":"Note","name":"nn"},{"doctype":"Item","name":"ii"}]}`, fkKey)
	if r.Status != 200 {
		t.Fatalf("mixed doctypes = %d %s", r.Status, r.Raw)
	}
	if _, ok := s.Doc("Note", "nn"); !ok {
		t.Error("Note nn missing")
	}
	r = fkDo(t, s, "POST", insertManyPath, `{"docs":[{"doctype":"Nope","name":"x"}]}`, fkKey)
	if r.Status != http.StatusInternalServerError || r.Body["exc_type"] != "ImportError" {
		t.Errorf("unknown doctype = %d %s", r.Status, r.Raw)
	}
	for _, body := range []string{`{}`, `{"docs":"nope"}`, `{"docs":[1]}`} {
		if r := fkDo(t, s, "POST", insertManyPath, body, fkKey); r.Status != http.StatusExpectationFailed {
			t.Errorf("%s = %d %s", body, r.Status, r.Raw)
		}
	}
}
