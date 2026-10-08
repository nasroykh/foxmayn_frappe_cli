package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func TestCustomFileBase(t *testing.T) {
	for name, want := range map[string]string{
		"ToDo-custom_x":  "ToDo-custom_x",
		"a/b\\c:d":       "a%2Fb%5Cc%3Ad",
		`q?"*<>|`:        "q%3F%22%2A%3C%3E%7C",
		"100%":           "100%25",
		"tab\there":      "tab%09here",
		".hidden":        "%2Ehidden",
		"ends.":          "ends%2E",
		"ends ":          "ends%20",
		"CON":            "%43ON",
		"con.txt":        "%63on.txt",
		"COM1":           "%43OM1",
		"lpt9.x":         "%6Cpt9.x",
		"COMX":           "COMX",
		"CONSOLE":        "CONSOLE",
		"Facture été 日本": "Facture été 日本",
	} {
		if got := customFileBase(name); got != want {
			t.Errorf("customFileBase(%q) = %q, want %q", name, got, want)
		}
	}
}

// customTSite is a site with one customization of each kind on ToDo, plus
// what pull must leave out.
func customTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	for _, ct := range append(customTypes, workflowStateType, workflowActionType) {
		fields := []string{"name"}
		if ct.selector != "" {
			fields = append(fields, ct.selector)
		}
		if ct.module != "" {
			fields = append(fields, ct.module)
		}
		if ct.exclude != nil {
			fields = append(fields, ct.exclude[0].(string))
		}
		s.AddDocType(ct.doctype, fields...)
	}
	s.DocField("Webhook", "webhook_secret", "Password")
	s.ChildTable("Webhook", "webhook_headers", "Webhook Header")
	s.DocField("Webhook Header", "token_value", "Password")
	s.Add("DocType",
		map[string]interface{}{"name": "ToDo", "module": "Desk"},
		map[string]interface{}{"name": "Note", "module": "Desk"},
		map[string]interface{}{"name": "Customer", "module": "Selling"})
	s.Add("Custom Field",
		map[string]interface{}{"name": "ToDo-custom_ref", "dt": "ToDo", "fieldname": "custom_ref", "fieldtype": "Data", "is_system_generated": 0, "insert_after": "status", "description": nil, "_user_tags": ""},
		map[string]interface{}{"name": "ToDo-app_field", "dt": "ToDo", "fieldname": "app_field", "is_system_generated": 1},
		map[string]interface{}{"name": "Customer-custom_x", "dt": "Customer", "fieldname": "custom_x", "is_system_generated": 0})
	s.Add("Property Setter",
		map[string]interface{}{"name": "ToDo-status-default", "doc_type": "ToDo", "field_name": "status", "property": "default", "value": "Open", "is_system_generated": 0})
	s.Add("Client Script",
		map[string]interface{}{"name": "ToDo Form", "dt": "ToDo", "module": "", "script": "frappe.ui.form.on('ToDo', {\n\trefresh(frm) {}\n});\n", "enabled": 1})
	s.Add("Server Script",
		map[string]interface{}{"name": "Nightly", "reference_doctype": "", "module": "Desk", "script_type": "Scheduler Event", "script": "x = 1"})
	s.Add("Report",
		map[string]interface{}{"name": "Open ToDos", "ref_doctype": "ToDo", "is_standard": "No", "module": "Desk", "query": "select name\nfrom `tabToDo`"},
		map[string]interface{}{"name": "ToDo Standard", "ref_doctype": "ToDo", "is_standard": "Yes", "module": "Desk"})
	s.Add("Webhook",
		map[string]interface{}{"name": "HOOK-1", "webhook_doctype": "ToDo", "request_url": "https://example.com/h", "webhook_secret": "*****",
			"webhook_headers": []interface{}{map[string]interface{}{"name": "row1", "parent": "HOOK-1", "parenttype": "Webhook", "parentfield": "webhook_headers", "idx": 1, "doctype": "Webhook Header", "key": "X-A", "value": "1"},
				map[string]interface{}{"key": "Authorization", "value": "Bearer abc", "token_value": "s3cret"}}})
	s.Add("Print Format",
		map[string]interface{}{"name": "ToDo Card", "doc_type": "ToDo", "standard": "No", "module": "Desk"},
		map[string]interface{}{"name": "ToDo Std", "doc_type": "ToDo", "standard": "Yes", "module": "Desk"})
	s.Add("Notification",
		map[string]interface{}{"name": "ToDo Due", "document_type": "ToDo", "is_standard": 0, "message_type": "Markdown", "message": "Due:\n{{ doc.date }}"},
		map[string]interface{}{"name": "ToDo Std Alert", "document_type": "ToDo", "is_standard": 1})
	s.Add("Property Setter", map[string]interface{}{"name": "ToDo-priority-hidden", "doc_type": "ToDo", "field_name": "priority", "property": "hidden", "value": "1", "is_system_generated": 1})
	s.Add("Workflow",
		map[string]interface{}{"name": "ToDo Flow", "document_type": "ToDo", "workflow_state_field": "workflow_state",
			"states":      []interface{}{map[string]interface{}{"state": "Draft", "doc_status": "0"}, map[string]interface{}{"state": "Done", "doc_status": "0"}},
			"transitions": []interface{}{map[string]interface{}{"state": "Draft", "action": "Finish", "next_state": "Done", "allowed": "All"}}})
	s.Add("Workflow State",
		map[string]interface{}{"name": "Draft", "workflow_state_name": "Draft"},
		map[string]interface{}{"name": "Done", "workflow_state_name": "Done", "style": "Success"},
		map[string]interface{}{"name": "Unused", "workflow_state_name": "Unused"})
	s.Add("Workflow Action Master", map[string]interface{}{"name": "Finish", "workflow_action_name": "Finish"})
	return s
}

func customTFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func customTRead(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCustomizePull(t *testing.T) {
	s := customTSite(t)
	dir := filepath.Join(t.TempDir(), "custom")
	r := cmdTOK(t, cmdTRun(t, s, "customize", "pull", "-d", "ToDo", "--out", dir))
	want := []string{
		".gitattributes",
		"client_script/ToDo Form.json", "client_script/ToDo Form.script.js",
		"custom_field/ToDo-custom_ref.json",
		"notification/ToDo Due.json", "notification/ToDo Due.message.md",
		"print_format/ToDo Card.json",
		"property_setter/ToDo-status-default.json",
		"report/Open ToDos.json", "report/Open ToDos.query.sql",
		"webhook/HOOK-1.json",
		"workflow/ToDo Flow.json",
		"workflow_action_master/Finish.json",
		"workflow_state/Done.json", "workflow_state/Draft.json",
	}
	if got := customTFiles(t, dir); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(r.Stdout, "11 written, 0 unchanged") {
		t.Errorf("stdout %q", r.Stdout)
	}

	// The file format, byte for byte: sorted keys, no nulls, stamps or "_"
	// keys, LF and a final newline.
	if got := customTRead(t, dir, "custom_field/ToDo-custom_ref.json"); got != `{
  "dt": "ToDo",
  "fieldname": "custom_ref",
  "fieldtype": "Data",
  "insert_after": "status",
  "is_system_generated": 0,
  "name": "ToDo-custom_ref"
}
` {
		t.Errorf("custom field file:\n%s", got)
	}
	if got := customTRead(t, dir, "client_script/ToDo Form.json"); !strings.Contains(got, `"script": {
    "$file": "ToDo Form.script.js"
  }`) {
		t.Errorf("client script file:\n%s", got)
	}
	if got := customTRead(t, dir, "client_script/ToDo Form.script.js"); got != "frappe.ui.form.on('ToDo', {\n\trefresh(frm) {}\n});\n" {
		t.Errorf("sidecar %q", got)
	}
	// Child rows lose their identity; the secret is never written.
	hook := customTRead(t, dir, "webhook/HOOK-1.json")
	for _, bad := range []string{"secret", "*****", "s3cret", "token_value", "parent", "row1", `"idx"`} {
		if strings.Contains(hook, bad) {
			t.Errorf("webhook file has %q:\n%s", bad, hook)
		}
	}
	if !strings.Contains(hook, `"key": "X-A"`) || !strings.Contains(r.Stderr, "webhook_headers.token_value, webhook_secret not written") ||
		!strings.Contains(r.Stderr, "header Authorization looks like a credential") {
		t.Errorf("webhook file:\n%s\nstderr %q", hook, r.Stderr)
	}
	if got := customTRead(t, dir, ".gitattributes"); !strings.Contains(got, "* text eol=lf") {
		t.Errorf(".gitattributes %q", got)
	}

	// Pulling again changes nothing.
	again := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "customize", "pull", "-d", "ToDo", "--out", dir)))
	if again["written"].(float64) != 0 || again["unchanged"].(float64) != 11 {
		t.Fatalf("second pull %v", again)
	}

	// A script that became one line drops its sidecar; a file of a document
	// gone from the site is kept and listed.
	s.Add("Client Script", map[string]interface{}{"name": "ToDo Form", "dt": "ToDo", "script": "x()", "enabled": 1})
	if err := os.WriteFile(filepath.Join(dir, "custom_field", "ToDo-gone.json"), []byte(`{"dt": "ToDo", "name": "ToDo-gone"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom_field", "Note-other.json"), []byte(`{"dt": "Note", "name": "Note-other"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "customize", "pull", "-d", "ToDo", "--out", dir)))
	if st := res["stale"].([]interface{}); len(st) != 1 || st[0] != "custom_field/ToDo-gone.json" {
		t.Errorf("stale %v", res["stale"])
	}
	if _, err := os.Stat(filepath.Join(dir, "client_script", "ToDo Form.script.js")); !os.IsNotExist(err) {
		t.Errorf("old sidecar kept: %v", err)
	}
	if got := customTRead(t, dir, "client_script/ToDo Form.json"); !strings.Contains(got, `"script": "x()"`) {
		t.Errorf("client script file:\n%s", got)
	}
}

func TestCustomizePullModule(t *testing.T) {
	s := customTSite(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "custom_field"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom_field", "ToDo-gone.json"), []byte(`{"dt": "ToDo", "name": "ToDo-gone"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "customize", "pull", "--module", "Desk", "--types", "custom_field,property_setter,server_script,custom_field", "--include-system", "--out", dir)))
	// A file of a DocType in the module counts as the module's.
	if st := res["stale"].([]interface{}); len(st) != 1 || st[0] != "custom_field/ToDo-gone.json" {
		t.Errorf("stale %v", res["stale"])
	}
	// By the DocType's module (ToDo is in Desk) and by its own module (the
	// scheduler script names no DocType).
	want := "custom_field/ToDo-app_field.json\ncustom_field/ToDo-custom_ref.json\ncustom_field/ToDo-gone.json\n" +
		"property_setter/ToDo-priority-hidden.json\nproperty_setter/ToDo-status-default.json\nserver_script/Nightly.json"
	got := customTFiles(t, dir)
	if strings.Join(got[1:], "\n") != want {
		t.Fatalf("files %v", got)
	}
}

func TestCustomizePullUsage(t *testing.T) {
	s := customTSite(t)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	before := len(s.Requests())
	for args, want := range map[string]string{
		"":                               "pass -d/--doctype or --module",
		"-d ToDo --module Desk --out x":  "cannot be combined",
		"-d ToDo":                        "--out is required",
		"-d ToDo --types widget --out x": `unknown kind "widget"`,
		"-d ToDo --out " + filepath.ToSlash(file): "is a file",
	} {
		r := cmdTRun(t, s, append([]string{"customize", "pull"}, strings.Fields(args)...)...)
		if cmdTFail(t, r, want); r.Code != exitUsage {
			t.Errorf("%s: exit %d", args, r.Code)
		}
	}
	if n := len(s.Requests()); n != before {
		t.Errorf("usage errors sent %d requests", n-before)
	}
}

// An edited file cannot make pull delete another document's files: only
// names this document's sidecars could have are removed.
func TestCustomizePullSidecarGuard(t *testing.T) {
	s := customTSite(t)
	dir := t.TempDir()
	args := []string{"customize", "pull", "-d", "ToDo", "--types", "client_script", "--out", dir}
	cmdTOK(t, cmdTRun(t, s, args...))
	folder := filepath.Join(dir, "client_script")
	victims := []string{"ToDo Form.b.script.js", "ToDo Form.x.json", "Other.script.js"}
	for _, v := range append(victims, "ToDo Form.notes.txt") {
		if err := os.WriteFile(filepath.Join(folder, v), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refs := `"a": {"$file": "ToDo Form.b.script.js"}, "b": {"$file": "ToDo Form.x.json"}, "c": {"$file": "../client_script/Other.script.js"}, "d": {"$file": "ToDo Form.notes.txt"}, `
	path := filepath.Join(folder, "ToDo Form.json")
	old := customTRead(t, dir, "client_script/ToDo Form.json")
	if err := os.WriteFile(path, []byte("{"+refs+old[1:]), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdTOK(t, cmdTRun(t, s, args...))
	for _, v := range victims {
		if _, err := os.Stat(filepath.Join(folder, v)); err != nil {
			t.Errorf("%s removed: %v", v, err)
		}
	}
	if _, err := os.Stat(filepath.Join(folder, "ToDo Form.notes.txt")); !os.IsNotExist(err) {
		t.Errorf("unreferenced sidecar kept: %v", err)
	}
	if customTRead(t, dir, "client_script/ToDo Form.json") != old {
		t.Error("document file not restored")
	}
}

// Two names whose files differ only in case stop the pull before any
// file is written.
func TestCustomizePullCaseClash(t *testing.T) {
	s := customTSite(t)
	s.Add("Client Script", map[string]interface{}{"name": "todo form", "dt": "ToDo", "script": "y()"})
	dir := filepath.Join(t.TempDir(), "out")
	r := cmdTRun(t, s, "customize", "pull", "-d", "ToDo", "--out", dir)
	cmdTFail(t, r, `would both be written to client_script/`)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("files written before the clash was found: %v", err)
	}
}

// A child row's password is dropped by the table's DocType from the meta,
// even when the row does not say its DocType.
func TestNormalizeCustomDocChildSecret(t *testing.T) {
	doc := map[string]interface{}{"name": "H", "rows": []interface{}{map[string]interface{}{"key": "a", "pw": "x", "name": "r1"}}}
	secret := map[string]map[string]bool{"Row": {"pw": true}}
	out, dropped := normalizeCustomDoc(doc, "Hook", secret, map[string]string{"rows": "Row"})
	row := out["rows"].([]interface{})[0].(map[string]interface{})
	if _, ok := row["pw"]; ok || row["name"] != nil || strings.Join(dropped, ",") != "rows.pw" {
		t.Errorf("row %v dropped %v", row, dropped)
	}
}
