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
			"webhook_headers": []interface{}{map[string]interface{}{"name": "row1", "parent": "HOOK-1", "parenttype": "Webhook", "parentfield": "webhook_headers", "idx": 1, "doctype": "Webhook Header", "key": "X-A", "value": "1"}}})
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
	if !strings.Contains(r.Stdout, "9 written, 0 unchanged") {
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
	for _, bad := range []string{"secret", "*****", "parent", "row1", `"idx"`} {
		if strings.Contains(hook, bad) {
			t.Errorf("webhook file has %q:\n%s", bad, hook)
		}
	}
	if !strings.Contains(hook, `"key": "X-A"`) || !strings.Contains(r.Stderr, "webhook_secret not written") {
		t.Errorf("webhook file:\n%s\nstderr %q", hook, r.Stderr)
	}
	if got := customTRead(t, dir, ".gitattributes"); !strings.Contains(got, "* text eol=lf") {
		t.Errorf(".gitattributes %q", got)
	}

	// Pulling again changes nothing.
	again := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "customize", "pull", "-d", "ToDo", "--out", dir)))
	if again["written"].(float64) != 0 || again["unchanged"].(float64) != 9 {
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
	cmdTOK(t, cmdTRun(t, s, "customize", "pull", "--module", "Desk", "--types", "custom_field,server_script", "--include-system", "--out", dir))
	// By the DocType's module (ToDo is in Desk) and by its own module (the
	// scheduler script names no DocType).
	want := "custom_field/ToDo-app_field.json\ncustom_field/ToDo-custom_ref.json\nserver_script/Nightly.json"
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
