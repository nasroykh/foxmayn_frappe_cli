package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func cmdTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	s.Add("ToDo",
		map[string]interface{}{"name": "TD-1", "description": "alpha", "status": "Open", "priority": 1},
		map[string]interface{}{"name": "TD-2", "description": "beta", "status": "Closed", "priority": 2},
		map[string]interface{}{"name": "TD-3", "description": "gamma", "status": "Open", "priority": 3},
	)
	return s
}

// cmdTExec runs ffc through runFFC (which also captures table output).
func cmdTExec(t *testing.T, cfgPath, stdin string, args ...string) cliResult {
	t.Helper()
	return runFFC(t, cfgPath, stdin, args...)
}

// cmdTRun runs ffc against s with API-key auth.
func cmdTRun(t *testing.T, s *frappetest.Site, args ...string) cliResult {
	t.Helper()
	return cmdTExec(t, fakeConfig(t, s, "apikey"), "", args...)
}

func cmdTRunStdin(t *testing.T, s *frappetest.Site, stdin string, args ...string) cliResult {
	t.Helper()
	return cmdTExec(t, fakeConfig(t, s, "apikey"), stdin, args...)
}

func cmdTOK(t *testing.T, r cliResult) cliResult {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", r.Err, r.Stderr)
	}
	return r
}

func cmdTFail(t *testing.T, r cliResult, wantInErr ...string) {
	t.Helper()
	if r.Err == nil {
		t.Fatalf("expected an error; stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
	for _, w := range wantInErr {
		if !strings.Contains(r.Err.Error(), w) {
			t.Errorf("error %q does not contain %q", r.Err, w)
		}
	}
}

func cmdTJSON(t *testing.T, r cliResult) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.Stdout)
	}
	return v
}

func cmdTObj(t *testing.T, r cliResult) map[string]interface{} {
	t.Helper()
	m, ok := cmdTJSON(t, r).(map[string]interface{})
	if !ok {
		t.Fatalf("stdout is not a JSON object: %s", r.Stdout)
	}
	return m
}

func cmdTRows(t *testing.T, r cliResult) []map[string]interface{} {
	t.Helper()
	arr, ok := cmdTJSON(t, r).([]interface{})
	if !ok {
		t.Fatalf("stdout is not a JSON array: %s", r.Stdout)
	}
	out := make([]map[string]interface{}, len(arr))
	for i, a := range arr {
		out[i], _ = a.(map[string]interface{})
	}
	return out
}

func cmdTNames(rows []map[string]interface{}) []string {
	var out []string
	for _, r := range rows {
		out = append(out, fmt.Sprint(r["name"]))
	}
	return out
}

func cmdTEq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func cmdTHas(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("output does not contain %q:\n%s", n, haystack)
		}
	}
}

// ─── get-doc ─────────────────────────────────────────────────────────────────

func TestCmdGetDoc(t *testing.T) {
	s := cmdTSite(t)

	r := cmdTOK(t, cmdTRun(t, s, "get-doc", "-d", "ToDo", "-n", "TD-1"))
	cmdTHas(t, r.Stdout, "description", "alpha", "TD-1")

	r = cmdTOK(t, cmdTRun(t, s, "--json", "get-doc", "-d", "ToDo", "-n", "TD-2"))
	doc := cmdTObj(t, r)
	if doc["description"] != "beta" || doc["status"] != "Closed" {
		t.Fatalf("doc %v", doc)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "get-doc", "-d", "ToDo", "-n", "TD-2", "--keys", "name,status"))
	if doc = cmdTObj(t, r); len(doc) != 2 || doc["status"] != "Closed" {
		t.Fatalf("--keys: %v", doc)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "get-doc", "-d", "ToDo", "-n", "TD-2", "--fields", "name,priority"))
	if doc = cmdTObj(t, r); len(doc) != 2 || doc["priority"] == nil {
		t.Fatalf("--fields: %v", doc)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "get-doc", "-d", "ToDo", "-n", "TD-2", "--keys", "name,nope"))
	if !strings.Contains(r.Stderr, "nope") {
		t.Fatalf("missing-key warning absent: %q", r.Stderr)
	}

	cmdTFail(t, cmdTRun(t, s, "get-doc", "-d", "ToDo", "-n", "TD-2", "--fields", `{"a":1}`), "--fields")
	cmdTFail(t, cmdTRun(t, s, "get-doc", "-d", "ToDo", "-n", "nope"), "nope")
}

func TestCmdGetDocSingleDefaultsName(t *testing.T) {
	s := frappetest.New(t)
	s.Add("System Settings", map[string]interface{}{"name": "System Settings", "language": "en"})
	r := cmdTOK(t, cmdTRun(t, s, "--json", "get-doc", "-d", "System Settings"))
	if doc := cmdTObj(t, r); doc["language"] != "en" {
		t.Fatalf("doc %v", doc)
	}
	if got := s.RequestsTo("GET", "/api/resource/System Settings/System Settings"); len(got) != 1 {
		t.Fatalf("requests: %+v", s.Requests())
	}
}

// ─── list-docs ───────────────────────────────────────────────────────────────

func TestCmdListDocs(t *testing.T) {
	s := cmdTSite(t)

	r := cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--fields", "name,status"))
	cmdTHas(t, r.Stdout, "TD-1", "TD-2", "TD-3", "Open", "Closed")

	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "ToDo", "--fields", `["name","status"]`, "-o", "name asc"))
	rows := cmdTRows(t, r)
	cmdTEq(t, cmdTNames(rows), "TD-1", "TD-2", "TD-3")
	if len(rows[0]) != 2 {
		t.Fatalf("row %v", rows[0])
	}

	// default order is modified desc (last written first)
	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "ToDo"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "TD-3", "TD-2", "TD-1")

	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "ToDo", "-o", "priority desc"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "TD-3", "TD-2", "TD-1")

	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "ToDo", "--filters", `{"status":"Open"}`, "-o", "name asc"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "TD-1", "TD-3")

	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "ToDo", "--filters", `[["priority",">",1],["status","=","Open"]]`))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "TD-3")

	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "ToDo", "--filters", `[["status","=","Nope"]]`))
	if rows = cmdTRows(t, r); len(rows) != 0 {
		t.Fatalf("expected [], got %v", rows)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "ToDo", "--start", "1", "-l", "1", "-o", "name asc"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "TD-2")
}

func TestCmdListDocsLimit(t *testing.T) {
	s := frappetest.New(t)
	for i := 0; i < 25; i++ {
		s.Add("Note", map[string]interface{}{"name": fmt.Sprintf("N%02d", i)})
	}
	r := cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "Note"))
	if n := len(cmdTRows(t, r)); n != 20 {
		t.Fatalf("default limit: %d rows, want 20", n)
	}
	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "Note", "--limit", "0"))
	if n := len(cmdTRows(t, r)); n != 25 {
		t.Fatalf("--limit 0: %d rows, want 25", n)
	}
	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-docs", "-d", "Note", "--limit", "3"))
	if n := len(cmdTRows(t, r)); n != 3 {
		t.Fatalf("--limit 3: %d rows", n)
	}
}

func TestCmdListDocsRejectsBadInput(t *testing.T) {
	s := cmdTSite(t)
	cmdTFail(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--filters", "{nope"), "--filters", "invalid JSON")
	cmdTFail(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--filters", `"str"`), "--filters")
	cmdTFail(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--limit", "-1"), "--limit")
	cmdTFail(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--start", "-1"), "--start")
	cmdTFail(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--fields", "[bad"), "--fields")
	cmdTFail(t, cmdTRun(t, s, "list-docs"), "doctype")
	if n := len(s.RequestsTo("GET", "/api/resource/ToDo")); n != 0 {
		t.Fatalf("invalid input still reached the server (%d requests)", n)
	}
}

func TestCmdListDocsErrors(t *testing.T) {
	s := cmdTSite(t)
	cmdTFail(t, cmdTRun(t, s, "list-docs", "-d", "Missing"), "Missing")
	r := cmdTRun(t, s, "list-docs", "-d", "ToDo", "--fields", "name,bogus")
	cmdTFail(t, r, "bogus")
}

// ─── create-doc ──────────────────────────────────────────────────────────────

func TestCmdCreateDoc(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "create-doc", "-d", "ToDo", "--data", `{"description":"delta"}`))
	cmdTHas(t, r.Stderr, "Created ToDo")
	cmdTHas(t, r.Stdout, "delta")
	if s.Count("ToDo") != 4 {
		t.Fatalf("count %d", s.Count("ToDo"))
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "create-doc", "-d", "ToDo", "--data", `{"name":"TD-9","description":"nine"}`, "--keys", "name"))
	if doc := cmdTObj(t, r); len(doc) != 1 || doc["name"] != "TD-9" {
		t.Fatalf("doc %v", doc)
	}
	if d, ok := s.Doc("ToDo", "TD-9"); !ok || d["description"] != "nine" {
		t.Fatalf("stored %v", d)
	}
}

func TestCmdCreateDocErrors(t *testing.T) {
	s := cmdTSite(t)
	cmdTFail(t, cmdTRun(t, s, "create-doc", "-d", "ToDo", "--data", "[1]"), "--data")
	cmdTFail(t, cmdTRun(t, s, "create-doc", "-d", "ToDo", "--data", "null"), "--data")
	cmdTFail(t, cmdTRun(t, s, "create-doc", "-d", "ToDo", "--data", `{"name":"TD-1"}`), "already exists")
	cmdTFail(t, cmdTRun(t, s, "create-doc", "-d", "ToDo"), "data")
}

// ─── update-doc ──────────────────────────────────────────────────────────────

func TestCmdUpdateDoc(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"status":"Closed"}`))
	cmdTHas(t, r.Stderr, "Updated ToDo TD-1")
	if d, _ := s.Doc("ToDo", "TD-1"); d["status"] != "Closed" || d["description"] != "alpha" {
		t.Fatalf("doc %v", d)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "update-doc", "-d", "ToDo", "-n", "TD-2", "--data", `{"priority":9}`, "--keys", "priority"))
	if doc := cmdTObj(t, r); fmt.Sprint(doc["priority"]) != "9" || len(doc) != 1 {
		t.Fatalf("doc %v", doc)
	}
}

func TestCmdUpdateDocStripsName(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"name":"RENAMED","status":"Closed"}`))
	cmdTHas(t, r.Stderr, "warning", `"name"`)
	puts := s.RequestsTo("PUT", "/api/resource/ToDo/TD-1")
	if len(puts) != 1 {
		t.Fatalf("PUTs: %+v", s.Requests())
	}
	var sent map[string]interface{}
	if err := json.Unmarshal([]byte(puts[0].Body), &sent); err != nil {
		t.Fatal(err)
	}
	if _, has := sent["name"]; has || sent["status"] != "Closed" {
		t.Fatalf("sent body %v", sent)
	}
	if _, ok := s.Doc("ToDo", "RENAMED"); ok {
		t.Fatal("document was renamed")
	}
}

func TestCmdUpdateDocSingleAndErrors(t *testing.T) {
	s := frappetest.New(t)
	s.Add("System Settings", map[string]interface{}{"name": "System Settings", "language": "en"})
	cmdTOK(t, cmdTRun(t, s, "--json", "update-doc", "-d", "System Settings", "--data", `{"language":"fr"}`))
	if d, _ := s.Doc("System Settings", "System Settings"); d["language"] != "fr" {
		t.Fatalf("doc %v", d)
	}
	cmdTFail(t, cmdTRun(t, s, "update-doc", "-d", "System Settings", "-n", "nope", "--data", `{"a":1}`), "nope")
	cmdTFail(t, cmdTRun(t, s, "update-doc", "-d", "System Settings", "--data", "oops"), "--data")
}

// ─── delete-doc ──────────────────────────────────────────────────────────────

func TestCmdDeleteDoc(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "delete-doc", "-d", "ToDo", "-n", "TD-1", "--yes"))
	cmdTHas(t, r.Stderr, "Deleted ToDo TD-1")
	if _, ok := s.Doc("ToDo", "TD-1"); ok {
		t.Fatal("not deleted")
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "delete-doc", "-d", "ToDo", "-n", "TD-2", "-y"))
	if out := cmdTObj(t, r); out["deleted"] != true || out["name"] != "TD-2" || out["doctype"] != "ToDo" {
		t.Fatalf("out %v", out)
	}
	cmdTFail(t, cmdTRun(t, s, "delete-doc", "-d", "ToDo", "-n", "TD-2", "--yes"), "TD-2")
	cmdTFail(t, cmdTRun(t, s, "delete-doc", "-d", "ToDo", "--yes"), "name")
}

func TestCmdDeleteDocWithoutYesNonInteractive(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTRun(t, s, "delete-doc", "-d", "ToDo", "-n", "TD-1")
	if r.Err == nil {
		t.Fatalf("delete without --yes and without a terminal must fail; stderr=%q", r.Stderr)
	}
	if _, ok := s.Doc("ToDo", "TD-1"); !ok {
		t.Fatal("document was deleted without confirmation")
	}
	if n := len(s.RequestsTo("DELETE", "/api/resource/ToDo/TD-1")); n != 0 {
		t.Fatalf("%d DELETE requests sent", n)
	}
}

func TestCmdDeleteDocLinked(t *testing.T) {
	s := cmdTSite(t)
	s.Handle("DELETE /api/resource/ToDo/TD-1", frappetest.ErrorHandler(frappetest.LinkExists("Cannot delete TD-1 because it is linked to Event E-1")))
	r := cmdTRun(t, s, "delete-doc", "-d", "ToDo", "-n", "TD-1", "--yes")
	cmdTFail(t, r, "linked to Event E-1")
}

// ─── count-docs ──────────────────────────────────────────────────────────────

func TestCmdCountDocs(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "count-docs", "-d", "ToDo"))
	if strings.TrimSpace(r.Stdout) != "3" {
		t.Fatalf("stdout %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "count-docs", "-d", "ToDo", "--filters", `{"status":"Open"}`))
	if strings.TrimSpace(r.Stdout) != "2" {
		t.Fatalf("filtered stdout %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "--json", "count-docs", "-d", "ToDo", "--filters", `[["priority",">",1]]`))
	out := cmdTObj(t, r)
	if fmt.Sprint(out["count"]) != "2" || out["doctype"] != "ToDo" {
		t.Fatalf("out %v", out)
	}
	cmdTFail(t, cmdTRun(t, s, "count-docs", "-d", "ToDo", "--filters", "{bad"), "--filters")
	cmdTFail(t, cmdTRun(t, s, "count-docs", "-d", "ToDo", "--filters", `{"bogus":1}`), "bogus")
}

// ─── get-schema ──────────────────────────────────────────────────────────────

func cmdTSchemaSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	fields := []interface{}{
		map[string]interface{}{"fieldname": "title", "label": "Title", "fieldtype": "Data", "reqd": 1, "hidden": 0, "idx": 1},
		map[string]interface{}{"fieldname": "status", "label": "Status", "fieldtype": "Select", "options": "Open\nClosed", "idx": 2},
		map[string]interface{}{"fieldname": "notes", "label": "Notes", "fieldtype": "Text", "idx": 3},
	}
	s.Add("DocType", map[string]interface{}{
		"name": "Ticket", "module": "Support", "issingle": 0, "istable": 0, "track_changes": 1,
		"fields": fields, "creation_noise": "x",
		"permissions": []interface{}{map[string]interface{}{"role": "System Manager", "read": 1, "write": 1, "permlevel": 0}},
	})
	return s
}

func cmdTFieldNames(t *testing.T, doc map[string]interface{}) []string {
	t.Helper()
	var out []string
	for _, f := range doc["fields"].([]interface{}) {
		out = append(out, f.(map[string]interface{})["fieldname"].(string))
	}
	return out
}

func TestCmdGetSchemaWarnsWithoutCustomFieldAndPropertySetter(t *testing.T) {
	s := cmdTSchemaSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "get-schema", "-d", "Ticket"))
	doc := cmdTObj(t, r)
	cmdTEq(t, cmdTFieldNames(t, doc), "title", "status", "notes")
	warnings, _ := doc["_warnings"].([]interface{})
	if len(warnings) != 2 {
		t.Fatalf("warnings %v", doc["_warnings"])
	}
	cmdTHas(t, r.Stderr, "custom fields could not be merged", "Property Setter overrides could not be applied")
}

func TestCmdGetSchemaMergesCustomFieldsAndPropertySetters(t *testing.T) {
	s := cmdTSchemaSite(t)
	s.Add("Custom Field",
		map[string]interface{}{"name": "Ticket-region", "dt": "Ticket", "fieldname": "region", "label": "Region", "fieldtype": "Data", "insert_after": "title", "idx": 1},
		map[string]interface{}{"name": "Ticket-zone", "dt": "Ticket", "fieldname": "zone", "label": "Zone", "fieldtype": "Data", "insert_after": "region", "idx": 2},
		map[string]interface{}{"name": "Ticket-orphan", "dt": "Ticket", "fieldname": "orphan", "label": "Orphan", "fieldtype": "Data", "insert_after": "ghost", "idx": 3},
		map[string]interface{}{"name": "Other-x", "dt": "Other", "fieldname": "x", "label": "X", "fieldtype": "Data", "insert_after": "", "idx": 1},
	)
	s.Add("Property Setter",
		map[string]interface{}{"name": "ps1", "doc_type": "Ticket", "doctype_or_field": "DocField", "field_name": "notes", "property": "reqd", "property_type": "Check", "value": "1"},
		map[string]interface{}{"name": "ps2", "doc_type": "Ticket", "doctype_or_field": "DocField", "field_name": "status", "property": "options", "property_type": "Text", "value": "Open\nPending\nClosed"},
		map[string]interface{}{"name": "ps3", "doc_type": "Ticket", "doctype_or_field": "DocType", "field_name": "", "property": "track_changes", "property_type": "Check", "value": "0"},
		map[string]interface{}{"name": "ps4", "doc_type": "Other", "doctype_or_field": "DocField", "field_name": "title", "property": "label", "property_type": "Data", "value": "WRONG"},
	)

	r := cmdTOK(t, cmdTRun(t, s, "--json", "get-schema", "-d", "Ticket"))
	doc := cmdTObj(t, r)
	if doc["_warnings"] != nil || strings.Contains(r.Stderr, "warning") {
		t.Fatalf("unexpected warnings: %v / %q", doc["_warnings"], r.Stderr)
	}
	cmdTEq(t, cmdTFieldNames(t, doc), "title", "region", "zone", "status", "notes", "orphan")

	byName := map[string]map[string]interface{}{}
	for _, f := range doc["fields"].([]interface{}) {
		m := f.(map[string]interface{})
		byName[m["fieldname"].(string)] = m
	}
	if fmt.Sprint(byName["notes"]["reqd"]) != "1" {
		t.Fatalf("notes %v", byName["notes"])
	}
	if byName["status"]["options"] != "Open\nPending\nClosed" {
		t.Fatalf("status %v", byName["status"])
	}
	if byName["title"]["label"] != "Title" {
		t.Fatalf("setter for another doctype leaked: %v", byName["title"])
	}
	// compact view: track_changes set to 0 by the property setter is dropped,
	// noise keys are gone, the module is kept.
	if _, ok := doc["track_changes"]; ok {
		t.Fatalf("falsy track_changes kept: %v", doc)
	}
	if doc["module"] != "Support" || doc["creation_noise"] != nil || doc["owner"] != nil {
		t.Fatalf("compact view wrong: %v", doc)
	}
	if _, ok := byName["title"]["idx"]; ok {
		t.Fatalf("field idx kept in compact view: %v", byName["title"])
	}
	perms := doc["permissions"].([]interface{})
	if rights := perms[0].(map[string]interface{})["rights"].([]interface{}); len(rights) != 2 {
		t.Fatalf("rights %v", rights)
	}

	// --full keeps everything.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "get-schema", "-d", "Ticket", "--full"))
	full := cmdTObj(t, r)
	if full["owner"] == nil || full["creation_noise"] != "x" {
		t.Fatalf("--full is missing raw keys: %v", full)
	}
	// --keys filters top-level keys.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "get-schema", "-d", "Ticket", "--keys", "fields"))
	if only := cmdTObj(t, r); len(only) != 1 || only["fields"] == nil {
		t.Fatalf("--keys: %v", only)
	}

	// table output
	r = cmdTOK(t, cmdTRun(t, s, "get-schema", "-d", "Ticket"))
	cmdTHas(t, r.Stdout, "FIELDNAME", "region", "Select", "orphan")

	// the exact queries fetchSchema makes: the first call and --full (the
	// cache keeps only the compact view) fetch; --keys and the table are
	// answered from the cache.
	if n := len(s.RequestsTo("GET", "/api/resource/Custom Field")); n != 2 {
		t.Fatalf("Custom Field queries: %d", n)
	}
}

func TestCmdGetSchemaFieldOrderSetter(t *testing.T) {
	s := cmdTSchemaSite(t)
	s.Add("Custom Field", map[string]interface{}{"name": "c1", "dt": "Ticket", "fieldname": "c1", "label": "C1", "fieldtype": "Data", "insert_after": "", "idx": 1})
	s.Add("Property Setter", map[string]interface{}{"name": "ps", "doc_type": "Ticket", "doctype_or_field": "DocType", "field_name": "", "property": "field_order", "property_type": "Data", "value": `["notes","title","status"]`})
	r := cmdTOK(t, cmdTRun(t, s, "--json", "get-schema", "-d", "Ticket"))
	cmdTEq(t, cmdTFieldNames(t, cmdTObj(t, r)), "notes", "title", "status", "c1")
}

func TestCmdGetSchemaMissingDocType(t *testing.T) {
	s := cmdTSchemaSite(t)
	cmdTFail(t, cmdTRun(t, s, "get-schema", "-d", "Nope"), "Nope")
}

// ─── list-doctypes / list-reports ────────────────────────────────────────────

func TestCmdListDoctypes(t *testing.T) {
	s := frappetest.New(t)
	mk := func(name, module string) map[string]interface{} {
		return map[string]interface{}{"name": name, "module": module, "is_submittable": 0, "is_tree": 0, "description": "d-" + name}
	}
	s.Add("DocType", mk("Beta", "Accounts"), mk("Alpha", "Accounts"), mk("Gamma", "Stock"))

	r := cmdTOK(t, cmdTRun(t, s, "--json", "list-doctypes"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "Alpha", "Beta", "Gamma") // name asc

	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-doctypes", "--module", "Stock"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "Gamma")

	r = cmdTOK(t, cmdTRun(t, s, "list-doctypes", "-m", "Accounts"))
	cmdTHas(t, r.Stdout, "Alpha", "Beta", "Accounts")
	if strings.Contains(r.Stdout, "Gamma") {
		t.Fatalf("module filter ignored:\n%s", r.Stdout)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-doctypes", "--limit", "2"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "Alpha", "Beta")

	cmdTFail(t, cmdTRun(t, s, "list-doctypes", "--limit", "-3"), "limit")
}

func TestCmdListReports(t *testing.T) {
	s := frappetest.New(t)
	mk := func(name, module string) map[string]interface{} {
		return map[string]interface{}{"name": name, "report_type": "Query Report", "module": module, "is_standard": "Yes", "ref_doctype": "ToDo"}
	}
	s.Add("Report", mk("Zed", "Desk"), mk("Aye", "Desk"), mk("Mid", "Stock"))

	r := cmdTOK(t, cmdTRun(t, s, "--json", "list-reports"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "Aye", "Mid", "Zed")
	r = cmdTOK(t, cmdTRun(t, s, "--json", "list-reports", "--module", "Desk"))
	cmdTEq(t, cmdTNames(cmdTRows(t, r)), "Aye", "Zed")
	r = cmdTOK(t, cmdTRun(t, s, "list-reports", "--limit", "1"))
	cmdTHas(t, r.Stdout, "Aye", "Query Report")
	cmdTFail(t, cmdTRun(t, s, "list-reports", "--limit", "-1"), "limit")
}

// ─── run-report ──────────────────────────────────────────────────────────────

func cmdTReportSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	s.AddReport("Totals", map[string]interface{}{
		"columns": []interface{}{
			map[string]interface{}{"fieldname": "item", "label": "Item"},
			map[string]interface{}{"fieldname": "qty", "label": "Qty"},
		},
		"result": []interface{}{
			[]interface{}{"apple", 1},
			[]interface{}{"pear", 2},
			[]interface{}{"plum", 3},
		},
	})
	s.AddReport("Empty", map[string]interface{}{"columns": []interface{}{"A:Data"}, "result": []interface{}{}})
	return s
}

func TestCmdRunReport(t *testing.T) {
	s := cmdTReportSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "run-report", "-n", "Totals"))
	cmdTHas(t, r.Stdout, "ITEM", "apple", "pear", "plum")

	r = cmdTOK(t, cmdTRun(t, s, "run-report", "-n", "Totals", "--limit", "2"))
	cmdTHas(t, r.Stdout, "apple", "pear")
	if strings.Contains(r.Stdout, "plum") {
		t.Fatalf("--limit ignored in table output:\n%s", r.Stdout)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "run-report", "-n", "Totals", "--limit", "2"))
	out := cmdTObj(t, r)
	if n := len(out["result"].([]interface{})); n != 2 || out["truncated"] != true || fmt.Sprint(out["total_rows"]) != "3" {
		t.Fatalf("out %v", out)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "run-report", "-n", "Totals"))
	out = cmdTObj(t, r)
	if n := len(out["result"].([]interface{})); n != 3 || out["truncated"] != nil {
		t.Fatalf("out %v", out)
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "run-report", "-n", "Totals", "--keys", "columns"))
	if out = cmdTObj(t, r); len(out) != 1 || out["columns"] == nil {
		t.Fatalf("--keys: %v", out)
	}

	r = cmdTOK(t, cmdTRun(t, s, "run-report", "-n", "Empty"))
	cmdTHas(t, r.Stderr, "No results for report")

	r = cmdTOK(t, cmdTRun(t, s, "run-report", "-n", "Totals", "--filters", `{"company":"Acme"}`))
	posts := s.RequestsTo("POST", "/api/method/frappe.desk.query_report.run")
	last := posts[len(posts)-1]
	cmdTHas(t, last.Body, `company`, `Acme`)
}

func TestCmdRunReportErrors(t *testing.T) {
	s := cmdTReportSite(t)
	cmdTFail(t, cmdTRun(t, s, "run-report", "-n", "Missing"), "Missing")
	cmdTFail(t, cmdTRun(t, s, "run-report", "-n", "Totals", "--limit", "-1"), "--limit")
	cmdTFail(t, cmdTRun(t, s, "run-report", "-n", "Totals", "--filters", "[1]"), "--filters")
}

// ─── call-method ─────────────────────────────────────────────────────────────

func TestCmdCallMethod(t *testing.T) {
	s := frappetest.New(t)
	s.HandleMethod("my.app.echo", func(r *http.Request, args map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"verb": r.Method, "args": args}, nil
	})
	s.HandleMethod("my.app.fail", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Validation("Amount must be positive")
	})

	r := cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "my.app.echo", "--args", `{"x":"1","n":2}`))
	out := cmdTObj(t, r)
	args := out["args"].(map[string]interface{})
	if out["verb"] != "POST" || args["x"] != "1" || fmt.Sprint(args["n"]) != "2" {
		t.Fatalf("out %v", out)
	}

	r = cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "my.app.echo", "--args", `{"x":"7"}`, "--get"))
	out = cmdTObj(t, r)
	if out["verb"] != "GET" || out["args"].(map[string]interface{})["x"] != "7" {
		t.Fatalf("GET out %v", out)
	}

	// no args, plain ping: message printed as JSON even without --json
	r = cmdTOK(t, cmdTRun(t, s, "call-method", "--method", "frappe.ping"))
	if strings.TrimSpace(r.Stdout) != `"pong"` {
		t.Fatalf("stdout %q", r.Stdout)
	}

	cmdTFail(t, cmdTRun(t, s, "call-method", "--method", "my.app.fail"), "Amount must be positive")
	cmdTFail(t, cmdTRun(t, s, "call-method", "--method", "my.app.echo", "--args", "nope"), "--args")
	cmdTFail(t, cmdTRun(t, s, "call-method"), "method")
}

// ─── ping ────────────────────────────────────────────────────────────────────

func TestCmdPing(t *testing.T) {
	s := frappetest.New(t)
	r := cmdTOK(t, cmdTRun(t, s, "ping"))
	cmdTHas(t, r.Stderr, "pong", s.URL)

	r = cmdTOK(t, cmdTRun(t, s, "--json", "ping"))
	out := cmdTObj(t, r)
	if out["response"] != "pong" || out["url"] != s.URL || out["latency"] == "" {
		t.Fatalf("out %v", out)
	}

	r = cmdTOK(t, cmdTExec(t, fakeConfig(t, s, "password"), "", "ping"))
	if s.Logins() != 1 || s.Logouts() != 1 {
		t.Fatalf("logins=%d logouts=%d", s.Logins(), s.Logouts())
	}
	_ = r
}

func TestCmdPingUnreachable(t *testing.T) {
	s := frappetest.New(t)
	cfg := fakeConfig(t, s, "apikey")
	s.Handle("GET /api/method/frappe.ping", frappetest.HTMLPage(200))
	r := cmdTExec(t, cfg, "", "ping")
	cmdTFail(t, r)
}

// ─── bulk ────────────────────────────────────────────────────────────────────

func cmdTBulkJSON(t *testing.T, r cliResult) map[string]interface{} {
	t.Helper()
	return cmdTObj(t, r)
}

func cmdTResultStatuses(out map[string]interface{}) []string {
	var st []string
	for _, x := range out["results"].([]interface{}) {
		st = append(st, fmt.Sprint(x.(map[string]interface{})["status"]))
	}
	return st
}

func TestCmdBulkCreate(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", `[{"description":"n1"},{"name":"X","description":"n2"}]`))
	cmdTHas(t, r.Stderr, "All 2 ToDo documents created")
	cmdTHas(t, r.Stdout, "created", "X")
	if s.Count("ToDo") != 5 {
		t.Fatalf("count %d", s.Count("ToDo"))
	}

	r = cmdTOK(t, cmdTRunStdin(t, s, `[{"name":"S1"},{"name":"S2"}]`, "--json", "bulk-create", "-d", "ToDo", "--file", "-", "--concurrency", "2"))
	out := cmdTBulkJSON(t, r)
	if fmt.Sprint(out["created"]) != "2" || fmt.Sprint(out["failed"]) != "0" {
		t.Fatalf("out %v", out)
	}
	if _, ok := s.Doc("ToDo", "S2"); !ok {
		t.Fatal("S2 missing")
	}

	path := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(path, []byte(`[{"name":"F1"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmdTOK(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--file", path))
	if _, ok := s.Doc("ToDo", "F1"); !ok {
		t.Fatal("F1 missing")
	}
}

func TestCmdBulkCreatePartialFailure(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", `[{"name":"new1"},{"name":"TD-1"},{"name":"new2"}]`)
	cmdTFail(t, r, "1 of 3 items did not succeed")
	cmdTHas(t, r.Stdout, "error", "already exists")
	if _, ok := s.Doc("ToDo", "new2"); !ok {
		t.Fatal("processing did not continue after the failure")
	}

	r = cmdTRun(t, s, "--json", "bulk-create", "-d", "ToDo", "--data", `[{"name":"new3"},{"name":"TD-1"}]`)
	cmdTFail(t, r, "did not succeed")
	out := cmdTBulkJSON(t, r)
	if fmt.Sprint(out["created"]) != "1" || fmt.Sprint(out["failed"]) != "1" {
		t.Fatalf("out %v", out)
	}
	res := out["results"].([]interface{})[1].(map[string]interface{})
	if res["status"] != "error" || !strings.Contains(fmt.Sprint(res["error"]), "already exists") || fmt.Sprint(res["index"]) != "2" {
		t.Fatalf("result %v", res)
	}
}

func TestCmdBulkCreateFailFast(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTRun(t, s, "--json", "bulk-create", "-d", "ToDo", "--fail-fast", "--data", `[{"name":"ok1"},{"name":"TD-1"},{"name":"never1"},{"name":"never2"}]`)
	cmdTFail(t, r, "3 of 4 items did not succeed", "1 failed", "2 skipped")
	out := cmdTBulkJSON(t, r)
	if got := strings.Join(cmdTResultStatuses(out), ","); got != "created,error,skipped,skipped" {
		t.Fatalf("statuses %s", got)
	}
	if _, ok := s.Doc("ToDo", "never1"); ok {
		t.Fatal("item after the failure was created")
	}
}

func TestCmdBulkInputValidation(t *testing.T) {
	s := cmdTSite(t)
	cmdTFail(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo"), "--data or --file")
	cmdTFail(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", "{}"), "array")
	cmdTFail(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", "[]"), "empty")
	cmdTFail(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", `[{"a":1},3]`), "item 2")
	cmdTFail(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", "[{}]", "--file", "x"), "none of the others", "if any flags")
	cmdTFail(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--file", filepath.Join(t.TempDir(), "absent.json")), "reading file")
	cmdTFail(t, cmdTRun(t, s, "bulk-update", "-d", "ToDo", "--data", `[{"status":"x"}]`), "name")
	cmdTFail(t, cmdTRun(t, s, "bulk-delete", "-d", "ToDo"), "provide --names, --file or --filters")
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("validation failures still sent %d requests", n)
	}
}

func TestCmdBulkConcurrencyBounds(t *testing.T) {
	s := cmdTSite(t)
	for _, c := range []string{"0", "11", "-1"} {
		cmdTFail(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", `[{"name":"z"}]`, "--concurrency", c), "--concurrency must be between 1 and 10")
	}
	if _, ok := s.Doc("ToDo", "z"); ok {
		t.Fatal("created despite invalid concurrency")
	}

	var mu sync.Mutex
	inflight, peak := 0, 0
	s.Handle("POST /api/resource/ToDo", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		inflight++
		if inflight > peak {
			peak = inflight
		}
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		inflight--
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"name":"ok"}}`))
	}))
	items := make([]string, 8)
	for i := range items {
		items[i] = fmt.Sprintf(`{"description":"d%d"}`, i)
	}
	data := "[" + strings.Join(items, ",") + "]"

	cmdTOK(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", data, "--concurrency", "3"))
	if peak > 3 || peak < 2 {
		t.Fatalf("peak concurrency %d with --concurrency 3", peak)
	}
	peak = 0
	cmdTOK(t, cmdTRun(t, s, "bulk-create", "-d", "ToDo", "--data", data))
	if peak != 1 {
		t.Fatalf("peak concurrency %d with the default", peak)
	}
}

func TestCmdBulkUpdate(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "bulk-update", "-d", "ToDo", "--data", `[{"name":"TD-1","status":"Closed"},{"name":"TD-2","priority":9}]`))
	out := cmdTBulkJSON(t, r)
	if fmt.Sprint(out["updated"]) != "2" {
		t.Fatalf("out %v", out)
	}
	if d, _ := s.Doc("ToDo", "TD-1"); d["status"] != "Closed" {
		t.Fatalf("doc %v", d)
	}
	for _, p := range s.RequestsTo("PUT", "/api/resource/ToDo/TD-1") {
		if strings.Contains(p.Body, `"name"`) {
			t.Fatalf("name sent in payload: %s", p.Body)
		}
	}

	r = cmdTOK(t, cmdTRunStdin(t, s, `[{"name":"TD-3","status":"Closed"}]`, "bulk-update", "-d", "ToDo", "--file", "-"))
	cmdTHas(t, r.Stderr, "All 1 ToDo documents updated")

	r = cmdTRun(t, s, "bulk-update", "-d", "ToDo", "--data", `[{"name":"nope","status":"x"},{"name":"TD-1","status":"Open"}]`)
	cmdTFail(t, r, "1 of 2 items did not succeed")
	cmdTHas(t, r.Stdout, "nope", "error")
	if d, _ := s.Doc("ToDo", "TD-1"); d["status"] != "Open" {
		t.Fatalf("second item not applied: %v", d)
	}

	r = cmdTRun(t, s, "--json", "bulk-update", "-d", "ToDo", "--fail-fast", "--data", `[{"name":"nope","status":"x"},{"name":"TD-2","status":"Open"}]`)
	cmdTFail(t, r, "1 skipped")
	if got := strings.Join(cmdTResultStatuses(cmdTBulkJSON(t, r)), ","); got != "error,skipped" {
		t.Fatalf("statuses %s", got)
	}
}

func TestCmdBulkDelete(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "bulk-delete", "-d", "ToDo", "--names", "TD-1, TD-2", "--yes"))
	out := cmdTBulkJSON(t, r)
	if fmt.Sprint(out["deleted"]) != "2" || s.Count("ToDo") != 1 {
		t.Fatalf("out %v count %d", out, s.Count("ToDo"))
	}

	r = cmdTRun(t, s, "bulk-delete", "-d", "ToDo", "--names", "gone,TD-3", "-y")
	cmdTFail(t, r, "1 of 2 items did not succeed")
	cmdTHas(t, r.Stdout, "gone", "error")
	if s.Count("ToDo") != 0 {
		t.Fatalf("TD-3 not deleted")
	}

	s.Add("ToDo", map[string]interface{}{"name": "1"}, map[string]interface{}{"name": "b,c"})
	cmdTOK(t, cmdTRunStdin(t, s, `[1,"b,c"]`, "bulk-delete", "-d", "ToDo", "--file", "-", "--yes"))
	if s.Count("ToDo") != 0 {
		t.Fatalf("file names not deleted")
	}

	s.Add("ToDo", map[string]interface{}{"name": "k1"}, map[string]interface{}{"name": "k2"}, map[string]interface{}{"name": "k3"})
	r = cmdTRun(t, s, "--json", "bulk-delete", "-d", "ToDo", "--names", "missing,k1,k2", "--yes", "--fail-fast")
	cmdTFail(t, r, "2 skipped")
	if s.Count("ToDo") != 3 {
		t.Fatalf("fail-fast still deleted: %d left", s.Count("ToDo"))
	}
}

func TestCmdBulkDeleteWithoutYesNonInteractive(t *testing.T) {
	s := cmdTSite(t)
	r := cmdTRun(t, s, "bulk-delete", "-d", "ToDo", "--names", "TD-1,TD-2")
	if r.Err == nil {
		t.Fatalf("must not delete without confirmation; stderr=%q", r.Stderr)
	}
	if s.Count("ToDo") != 3 {
		t.Fatalf("documents deleted without confirmation: %d left", s.Count("ToDo"))
	}
}

// ─── site list ───────────────────────────────────────────────────────────────

func TestCmdSiteList(t *testing.T) {
	s := frappetest.New(t)
	cfg := fakeConfig(t, s, "oauth")
	r := cmdTOK(t, cmdTExec(t, cfg, "", "--json", "site", "list"))
	rows := cmdTRows(t, r)
	if len(rows) != 2 {
		t.Fatalf("rows %v", rows)
	}
	byName := map[string]map[string]interface{}{}
	for _, row := range rows {
		byName[row["name"].(string)] = row
	}
	if byName["t"]["auth"] != "OAuth 2.0" || byName["t"]["default"] != true || byName["t"]["url"] != s.URL {
		t.Fatalf("t: %v", byName["t"])
	}
	if byName["other"]["auth"] != "API Key" || byName["other"]["default"] != false {
		t.Fatalf("other: %v", byName["other"])
	}
	// No secrets in the listing.
	if strings.Contains(r.Stdout, frappetest.Token) || strings.Contains(r.Stdout, frappetest.APISecret) {
		t.Fatalf("secret leaked:\n%s", r.Stdout)
	}

	r = cmdTOK(t, cmdTExec(t, cfg, "", "site", "list"))
	cmdTHas(t, r.Stdout, "other", "API Key", "OAuth 2.0", "✓")

	pw := fakeConfig(t, s, "password")
	r = cmdTOK(t, cmdTExec(t, pw, "", "--json", "site", "list"))
	cmdTHas(t, r.Stdout, "Username/Password")
	if len(s.Requests()) != 0 {
		t.Fatal("site list must not touch the network")
	}
}

func TestCmdSiteSelection(t *testing.T) {
	s := cmdTSite(t)
	cfg := fakeConfig(t, s, "oauth")
	cmdTOK(t, cmdTExec(t, cfg, "", "--site", "other", "count-docs", "-d", "ToDo"))
	reqs := s.RequestsTo("POST", "/api/method/frappe.client.get_count")
	if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "token "+frappetest.APIKey+":"+frappetest.APISecret {
		t.Fatalf("--site other used the wrong credentials: %+v", reqs)
	}
	cmdTFail(t, cmdTExec(t, cfg, "", "--site", "ghost", "count-docs", "-d", "ToDo"), "ghost")
}

// ─── authentication per mode ─────────────────────────────────────────────────

func TestCmdAuthModesSendCredentials(t *testing.T) {
	t.Run("apikey", func(t *testing.T) {
		s := cmdTSite(t)
		cmdTOK(t, cmdTExec(t, fakeConfig(t, s, "apikey"), "", "get-doc", "-d", "ToDo", "-n", "TD-1"))
		reqs := s.RequestsTo("GET", "/api/resource/ToDo/TD-1")
		if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "token test-key:test-secret" {
			t.Fatalf("requests %+v", reqs)
		}
		if reqs[0].Header.Get("Cookie") != "" || s.Logins() != 0 {
			t.Fatalf("api key site used a session: cookie=%q logins=%d", reqs[0].Header.Get("Cookie"), s.Logins())
		}
	})
	t.Run("oauth", func(t *testing.T) {
		s := cmdTSite(t)
		cmdTOK(t, cmdTExec(t, fakeConfig(t, s, "oauth"), "", "get-doc", "-d", "ToDo", "-n", "TD-1"))
		reqs := s.RequestsTo("GET", "/api/resource/ToDo/TD-1")
		if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "Bearer "+frappetest.Token {
			t.Fatalf("requests %+v", reqs)
		}
		if s.Logins() != 0 {
			t.Fatalf("oauth site logged in")
		}
	})
	t.Run("password", func(t *testing.T) {
		s := cmdTSite(t)
		r := cmdTOK(t, cmdTExec(t, fakeConfig(t, s, "password"), "", "--json", "get-doc", "-d", "ToDo", "-n", "TD-1"))
		if cmdTObj(t, r)["name"] != "TD-1" {
			t.Fatalf("stdout %s", r.Stdout)
		}
		reqs := s.RequestsTo("GET", "/api/resource/ToDo/TD-1")
		if len(reqs) != 1 {
			t.Fatalf("requests %+v", s.Requests())
		}
		if got := reqs[0].Header.Get("Cookie"); got != "sid=sid-1" {
			t.Fatalf("cookie %q", got)
		}
		if reqs[0].Header.Get("Authorization") != "" {
			t.Fatalf("session site sent Authorization: %q", reqs[0].Header.Get("Authorization"))
		}
		if s.Logins() != 1 || s.Logouts() != 1 {
			t.Fatalf("logins=%d logouts=%d, want one of each", s.Logins(), s.Logouts())
		}
	})
	t.Run("bulk logs in once and out once", func(t *testing.T) {
		s := cmdTSite(t)
		cmdTOK(t, cmdTExec(t, fakeConfig(t, s, "password"), "", "bulk-create", "-d", "ToDo", "--data", `[{"name":"a"},{"name":"b"},{"name":"c"}]`, "--concurrency", "3"))
		if s.Logins() != 1 || s.Logouts() != 1 {
			t.Fatalf("logins=%d logouts=%d", s.Logins(), s.Logouts())
		}
	})
	t.Run("wrong credentials", func(t *testing.T) {
		s := cmdTSite(t)
		cfg := filepath.Join(t.TempDir(), "config.yaml")
		body := fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    api_key: test-key\n    api_secret: wrong\n", s.URL)
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cmdTFail(t, cmdTExec(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "TD-1"), "401")
	})
	t.Run("bad password", func(t *testing.T) {
		s := cmdTSite(t)
		cfg := filepath.Join(t.TempDir(), "config.yaml")
		body := fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n    username: Administrator\n    password: nope\n", s.URL)
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cmdTFail(t, cmdTExec(t, cfg, "", "ping"))
		if s.Logins() != 0 {
			t.Fatalf("logins %d", s.Logins())
		}
	})
}

// ─── error rendering ─────────────────────────────────────────────────────────

func TestCmdErrorRendering(t *testing.T) {
	t.Run("404 DoesNotExistError", func(t *testing.T) {
		s := cmdTSite(t)
		r := cmdTRun(t, s, "get-doc", "-d", "ToDo", "-n", "ghost")
		cmdTFail(t, r, "404", "ghost")
		if r.Stdout != "" {
			t.Fatalf("stdout must stay empty on failure: %q", r.Stdout)
		}
	})
	t.Run("417 with _server_messages", func(t *testing.T) {
		s := cmdTSite(t)
		s.Handle("POST /api/resource/ToDo", frappetest.ErrorHandler(frappetest.Validation("Description cannot be empty")))
		r := cmdTRun(t, s, "create-doc", "-d", "ToDo", "--data", `{"description":""}`)
		cmdTFail(t, r, "Description cannot be empty")
		if strings.Contains(r.Err.Error(), "Traceback") || strings.Contains(r.Err.Error(), "_server_messages") {
			t.Fatalf("raw server payload leaked: %v", r.Err)
		}
	})
	t.Run("417 on update", func(t *testing.T) {
		s := cmdTSite(t)
		s.Handle("PUT /api/resource/ToDo/TD-1", frappetest.ErrorHandler(frappetest.Validation("Status is not allowed")))
		cmdTFail(t, cmdTRun(t, s, "update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"status":"x"}`), "Status is not allowed")
	})
	t.Run("403 permission", func(t *testing.T) {
		s := cmdTSite(t)
		s.Handle("GET /api/resource/ToDo", frappetest.ErrorHandler(frappetest.Permission("No permission for ToDo")))
		cmdTFail(t, cmdTRun(t, s, "list-docs", "-d", "ToDo"), "403")
	})
	t.Run("403 on method", func(t *testing.T) {
		s := cmdTSite(t)
		s.HandleMethod("my.app.secret", func(*http.Request, map[string]interface{}) (interface{}, error) {
			return nil, frappetest.Permission("Not permitted")
		})
		cmdTFail(t, cmdTRun(t, s, "call-method", "--method", "my.app.secret"), "403")
	})
	t.Run("guest 403 without credentials", func(t *testing.T) {
		s := cmdTSite(t)
		cfg := filepath.Join(t.TempDir(), "config.yaml")
		body := fmt.Sprintf("default_site: t\nsites:\n  t:\n    url: %q\n", s.URL)
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cmdTFail(t, cmdTExec(t, cfg, "", "list-docs", "-d", "ToDo"))
	})
	t.Run("HTML page", func(t *testing.T) {
		s := cmdTSite(t)
		s.Handle("GET /api/resource/ToDo", frappetest.HTMLPage(200))
		r := cmdTRun(t, s, "list-docs", "-d", "ToDo")
		cmdTFail(t, r, "non-JSON", "HTTP 200")
		if len(r.Err.Error()) > 600 {
			t.Fatalf("error not bounded (%d bytes)", len(r.Err.Error()))
		}
	})
	t.Run("HTML error page", func(t *testing.T) {
		s := cmdTSite(t)
		s.Handle("GET /api/resource/ToDo/TD-1", frappetest.HTMLPage(502))
		r := cmdTRun(t, s, "get-doc", "-d", "ToDo", "-n", "TD-1")
		cmdTFail(t, r, "502")
		if len(r.Err.Error()) > 600 {
			t.Fatalf("error not bounded (%d bytes)", len(r.Err.Error()))
		}
	})
	t.Run("HTML on every data command fails", func(t *testing.T) {
		s := cmdTSite(t)
		for _, route := range []string{
			"GET /api/resource/ToDo/TD-1", "PUT /api/resource/ToDo/TD-1",
			"POST /api/resource/ToDo", "POST /api/method/frappe.client.get_count",
			"POST /api/method/frappe.desk.query_report.run", "POST /api/method/x.y",
		} {
			s.Handle(route, frappetest.HTMLPage(200))
		}
		for _, args := range [][]string{
			{"get-doc", "-d", "ToDo", "-n", "TD-1"},
			{"update-doc", "-d", "ToDo", "-n", "TD-1", "--data", `{"a":1}`},
			{"create-doc", "-d", "ToDo", "--data", `{"a":1}`},
			{"count-docs", "-d", "ToDo"},
			{"run-report", "-n", "R"},
			{"call-method", "--method", "x.y"},
		} {
			if r := cmdTRun(t, s, args...); r.Err == nil {
				t.Errorf("%v succeeded against an HTML page; stdout=%q", args, r.Stdout)
			}
		}
	})
}

// DeleteDoc passes a nil out to do(), so a 200 answer that is not Frappe's
// JSON (a login proxy page) is reported as a successful delete. Skipped until
// client.DeleteDoc checks the body.
func TestCmdDeleteDocHTMLSuccessIsNotDeleted(t *testing.T) {
	s := cmdTSite(t)
	s.Handle("DELETE /api/resource/ToDo/TD-1", frappetest.HTMLPage(200))
	if r := cmdTRun(t, s, "delete-doc", "-d", "ToDo", "-n", "TD-1", "-y"); r.Err == nil {
		t.Fatalf("delete-doc succeeded against an HTML page; stderr=%q", r.Stderr)
	}
}
