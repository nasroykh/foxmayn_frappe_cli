package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func mcpTAtomic(doctype string, data interface{}) map[string]interface{} {
	return map[string]interface{}{"doctype": doctype, "data": data, "atomic": true}
}

func TestMCPBulkCreateAtomic(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddDocType("ToDo")
	m := mcpTObj(t, mcpTOK(t, s, "bulk_create", mcpTAtomic("ToDo", []interface{}{
		map[string]interface{}{"description": "a"},
		map[string]interface{}{"name": "X", "description": "b"},
		map[string]interface{}{"description": "c"},
	})))
	// The shape is the one a per-item run returns.
	if m["created"] != 3.0 || m["failed"] != 0.0 || m["skipped"] != 0.0 {
		t.Fatalf("report = %v", m)
	}
	res := m["results"].([]interface{})
	if len(res) != 3 {
		t.Fatalf("results = %v", res)
	}
	var names []string
	for i, r := range res {
		got := r.(map[string]interface{})
		if got["index"] != float64(i+1) || got["status"] != "created" || got["name"] == "" {
			t.Errorf("result %d = %v", i+1, got)
		}
		names = append(names, got["name"].(string))
	}
	if names[1] != "X" {
		t.Errorf("names = %v, want the second to be X", names)
	}
	// Names are in input order: each is the document of its item.
	for i, want := range []string{"a", "b", "c"} {
		if d, ok := site.Doc("ToDo", names[i]); !ok || d["description"] != want {
			t.Errorf("result %d is %s, description %v, want %s", i+1, names[i], d["description"], want)
		}
	}
	if many, one := atomicTSent(site); many != 1 || one != 0 {
		t.Errorf("sent %d insert_many and %d single inserts, want 1 and 0", many, one)
	}
	body := mcpTBody(t, mcpTOnly(t, site, http.MethodPost, atomicPath))
	docs, _ := body["docs"].([]interface{})
	if len(docs) != 3 || docs[0].(map[string]interface{})["doctype"] != "ToDo" {
		t.Errorf("body docs = %v", body["docs"])
	}

	// A same-doctype item is accepted, and the flag may be the string "true".
	args := mcpTAtomic("ToDo", `[{"doctype":"ToDo","name":"T2"}]`)
	args["atomic"] = "true"
	m = mcpTObj(t, mcpTOK(t, s, "bulk_create", args))
	if m["created"] != 1.0 {
		t.Errorf("report = %v", m)
	}

	// atomic false (or null) is the per-item run.
	many0, one0 := atomicTSent(site)
	for _, v := range []interface{}{false, nil, "false"} {
		args := mcpTAtomic("ToDo", []interface{}{map[string]interface{}{"description": "z"}})
		args["atomic"] = v
		mcpTOK(t, s, "bulk_create", args)
	}
	if many, one := atomicTSent(site); many != many0 || one != one0+3 {
		t.Errorf("sent %d insert_many and %d single inserts more, want 0 and 3", many-many0, one-one0)
	}
}

func TestMCPBulkCreateAtomicRollsBack(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("ToDo", map[string]interface{}{"name": "dup"})
	// The third item clashes with an existing document: the two before it
	// must not stay.
	mcpTErr(t, s, "bulk_create", mcpTAtomic("ToDo", []interface{}{
		map[string]interface{}{"name": "keep1"},
		map[string]interface{}{"name": "keep2"},
		map[string]interface{}{"name": "dup"},
	}), "nothing was created")
	if site.Count("ToDo") != 1 {
		t.Errorf("count = %d, want 1", site.Count("ToDo"))
	}
	for _, n := range []string{"keep1", "keep2"} {
		if _, ok := site.Doc("ToDo", n); ok {
			t.Errorf("%s survived the failed batch", n)
		}
	}
	if many, one := atomicTSent(site); many != 1 || one != 0 {
		t.Errorf("sent %d insert_many and %d single inserts, want 1 and 0", many, one)
	}
}

func TestMCPBulkCreateAtomicOutcome(t *testing.T) {
	for _, tc := range []struct {
		err  *frappetest.Error
		want string
		not  string
	}{
		{&frappetest.Error{Status: http.StatusForbidden, ExcType: "PermissionError", Message: "No permission for ToDo"}, "nothing was created", "may or may not"},
		{&frappetest.Error{Status: http.StatusGatewayTimeout, Message: "gateway timeout"}, "may or may not have been created", "nothing was created"},
		{&frappetest.Error{Status: 524, Message: "origin timeout"}, "check the site before re-running", "nothing was created"},
	} {
		s, site := newMCPFake(t, false)
		site.Handle("POST "+atomicPath, frappetest.ErrorHandler(tc.err))
		args := mcpTAtomic("ToDo", []interface{}{map[string]interface{}{"name": "x"}})
		mcpTErr(t, s, "bulk_create", args, tc.want)
		if msg := resultText(t, callTool(t, s, "bulk_create", args)); strings.Contains(msg, tc.not) {
			t.Errorf("status %d: %q must not contain %q", tc.err.Status, msg, tc.not)
		}
	}

	// A success answer without the names means the batch was probably created.
	s, site := newMCPFake(t, false)
	site.HandleMethod("frappe.client.insert_many", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return []interface{}{}, nil
	})
	mcpTErr(t, s, "bulk_create", mcpTAtomic("ToDo", []interface{}{map[string]interface{}{"name": "x"}}), "probably created")
}

func TestMCPBulkCreateAtomicRefused(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddDocType("ToDo")
	one := []interface{}{map[string]interface{}{"description": "a"}}

	// concurrency, whatever its value, is a usage error with atomic.
	for _, c := range []interface{}{1, 2, "1", 0} {
		args := mcpTAtomic("ToDo", one)
		args["concurrency"] = c
		mcpTErr(t, s, "bulk_create", args, "atomic sends one request")
	}

	// An item of another DocType is refused, not rewritten; so is a row for
	// an existing parent.
	mcpTErr(t, s, "bulk_create", mcpTAtomic("ToDo", []interface{}{
		map[string]interface{}{"name": "a"},
		map[string]interface{}{"doctype": "Note", "name": "b"},
	}), "item 2")
	mcpTErr(t, s, "bulk_create", mcpTAtomic("ToDo", []interface{}{
		map[string]interface{}{"parent": "TD-1", "parenttype": "ToDo", "parentfield": "x"},
	}), "parent and parenttype")

	// More than 200 items.
	over := make([]interface{}, atomicLimit+1)
	for i := range over {
		over[i] = map[string]interface{}{"name": fmt.Sprintf("n%d", i)}
	}
	mcpTErr(t, s, "bulk_create", mcpTAtomic("ToDo", over), "too many items (201)")

	// A flag that is no boolean must not fall back to one-by-one creation.
	for _, v := range []interface{}{"yes", 1, []interface{}{true}} {
		args := mcpTAtomic("ToDo", one)
		args["atomic"] = v
		mcpTErr(t, s, "bulk_create", args, "atomic: expected true or false")
	}

	if n := len(site.Requests()); n != 0 {
		t.Errorf("%d requests reached the site: %+v", n, site.Requests())
	}

	// Exactly the limit is accepted, in one request.
	m := mcpTObj(t, mcpTOK(t, s, "bulk_create", mcpTAtomic("ToDo", over[:atomicLimit])))
	if m["created"] != float64(atomicLimit) || site.Count("ToDo") != atomicLimit {
		t.Errorf("at limit: %v count=%d", m["created"], site.Count("ToDo"))
	}
	if many, one := atomicTSent(site); many != 1 || one != 0 {
		t.Errorf("sent %d insert_many and %d single inserts, want 1 and 0", many, one)
	}
}

func TestMCPBulkCreateAtomicDescribed(t *testing.T) {
	s, _ := newMCPFake(t, false)
	tool := s.ListTools()["bulk_create"].Tool
	if _, ok := tool.InputSchema.Properties["atomic"]; !ok {
		t.Fatalf("bulk_create has no atomic argument: %v", tool.InputSchema.Properties)
	}
	for _, want := range []string{"atomic", "all or none", "one database transaction", "cannot be combined with concurrency", "200"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("description lacks %q: %s", want, tool.Description)
		}
	}
}
