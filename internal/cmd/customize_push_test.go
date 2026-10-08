package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// customTMeta declares the fields customTSite's documents use, as a real
// site's meta would list them.
func customTMeta(s *frappetest.Site) {
	for dt, fields := range map[string][]string{
		"Custom Field":            {"dt", "fieldname", "fieldtype", "insert_after", "label", "is_system_generated", "module", "description"},
		"Property Setter":         {"doc_type", "field_name", "property", "value", "is_system_generated", "module"},
		"Client Script":           {"dt", "module", "script", "enabled"},
		"Server Script":           {"reference_doctype", "module", "script_type", "script"},
		"Report":                  {"ref_doctype", "is_standard", "module", "query"},
		"Print Format":            {"doc_type", "standard", "module"},
		"Notification":            {"document_type", "is_standard", "message_type", "message", "module"},
		"Webhook":                 {"webhook_doctype", "request_url", "enable_security"},
		"Webhook Header":          {"key", "value"},
		"Workflow":                {"document_type", "workflow_state_field"},
		"Workflow Document State": {"state", "doc_status"},
		"Workflow Transition":     {"state", "action", "next_state", "allowed"},
		"Workflow State":          {"workflow_state_name", "style"},
		"Workflow Action Master":  {"workflow_action_name"},
	} {
		for _, f := range fields {
			s.DocField(dt, f, "Data")
		}
	}
	s.ChildTable("Workflow", "states", "Workflow Document State")
	s.ChildTable("Workflow", "transitions", "Workflow Transition")
}

// customTPulled is customTSite with its meta, pulled for ToDo.
func customTPulled(t *testing.T) (*frappetest.Site, string) {
	t.Helper()
	s := customTSite(t)
	customTMeta(s)
	dir := filepath.Join(t.TempDir(), "custom")
	cmdTOK(t, cmdTRun(t, s, "customize", "pull", "-d", "ToDo", "--out", dir))
	return s, dir
}

func customTWrites(s *frappetest.Site) []frappetest.Request {
	var out []frappetest.Request
	for _, r := range s.Requests() {
		if (r.Method == http.MethodPost || r.Method == http.MethodPut) && strings.HasPrefix(r.Path, "/api/resource/") {
			out = append(out, r)
		}
	}
	return out
}

func customTWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Pushing what was just pulled changes nothing; an edit is sent as the
// changed fields with the modification time read.
func TestCustomizePush(t *testing.T) {
	s, dir := customTPulled(t)
	r := cmdTOK(t, cmdTRun(t, s, "customize", "push", dir))
	if !strings.Contains(r.Stdout, "0 to create, 0 to update, 11 unchanged") {
		t.Errorf("stdout:\n%s", r.Stdout)
	}
	if w := customTWrites(s); len(w) != 0 {
		t.Fatalf("round trip wrote %v", w)
	}

	customTWrite(t, dir, "client_script/ToDo Form.script.js", "frappe.ui.form.on('ToDo', {});\n")
	ps := strings.Replace(customTRead(t, dir, "property_setter/ToDo-status-default.json"), `"Open"`, `"Closed"`, 1)
	customTWrite(t, dir, "property_setter/ToDo-status-default.json", ps)
	before, _ := s.Doc("Client Script", "ToDo Form")

	r = cmdTOK(t, cmdTRun(t, s, "customize", "push", dir, "--dry-run"))
	for _, want := range []string{"update  client_script/ToDo Form: script", "update  property_setter/ToDo-status-default: value", "0 to create, 2 to update, 9 unchanged"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("dry run lacks %q:\n%s", want, r.Stdout)
		}
	}
	if w := customTWrites(s); len(w) != 0 {
		t.Fatalf("dry run wrote %v", w)
	}

	r = cmdTRun(t, s, "customize", "push", dir)
	cmdTFail(t, r, "pass --yes")

	res := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "customize", "push", dir, "--yes")))
	if res["applied"] != float64(2) || res["update"] != float64(2) || res["unchanged"] != float64(9) {
		t.Errorf("result %v", res)
	}
	w := customTWrites(s)
	if len(w) != 2 || w[0].Path != "/api/resource/Property Setter/ToDo-status-default" || w[1].Path != "/api/resource/Client Script/ToDo Form" {
		t.Fatalf("writes %v", w)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(w[1].Body), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || body["script"] != "frappe.ui.form.on('ToDo', {});\n" || body["modified"] != before["modified"] {
		t.Errorf("update body %v", body)
	}
	if doc, _ := s.Doc("Property Setter", "ToDo-status-default"); doc["value"] != "Closed" {
		t.Errorf("value %v", doc["value"])
	}
}

// Into an empty site everything is created, in dependency order, without
// passwords.
func TestCustomizePushCreate(t *testing.T) {
	_, dir := customTPulled(t)
	// A second Custom Field inserted after the first one, named so that it
	// sorts first.
	customTWrite(t, dir, "custom_field/ToDo-a_after.json",
		`{"dt": "ToDo", "fieldname": "a_after", "fieldtype": "Data", "insert_after": "custom_ref", "name": "ToDo-a_after"}`)
	if err := os.MkdirAll(filepath.Join(dir, "server_script"), 0o755); err != nil {
		t.Fatal(err)
	}
	customTWrite(t, dir, "server_script/ToDo Check.json",
		`{"name": "ToDo Check", "reference_doctype": "ToDo", "script_type": "DocType Event", "script": "pass"}`)
	target := frappetest.New(t)
	for _, ct := range append(customTypes, workflowStateType, workflowActionType) {
		target.AddDocType(ct.doctype)
	}
	customTMeta(target)
	target.DocField("Webhook", "webhook_secret", "Password")
	target.ChildTable("Webhook", "webhook_headers", "Webhook Header")
	target.DocField("Webhook Header", "token_value", "Password")
	target.HandleMethod("frappe.core.doctype.server_script.server_script.enabled", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return false, nil
	})
	// The pulled webhook had security on.
	hook := strings.Replace(customTRead(t, dir, "webhook/HOOK-1.json"), `"name": "HOOK-1"`, `"enable_security": 1, "name": "HOOK-1", "webhook_secret": "x"`, 1)
	customTWrite(t, dir, "webhook/HOOK-1.json", hook)

	r := cmdTOK(t, cmdTRun(t, target, "customize", "push", dir, "--yes"))
	var paths []string
	for _, w := range customTWrites(target) {
		if w.Method != http.MethodPost {
			t.Errorf("%s %s", w.Method, w.Path)
		}
		paths = append(paths, strings.TrimPrefix(w.Path, "/api/resource/"))
		if strings.Contains(w.Body, "webhook_secret") || strings.Contains(w.Body, "s3cret") {
			t.Errorf("password sent: %s", w.Body)
		}
	}
	want := "Workflow State,Workflow State,Workflow Action Master,Custom Field,Custom Field,Property Setter,Client Script," +
		"Server Script,Print Format,Report,Workflow,Notification,Webhook"
	if strings.Join(paths, ",") != want {
		t.Errorf("order %v", paths)
	}
	cf := customTWrites(target)[3]
	if !strings.Contains(cf.Body, `"fieldname":"custom_ref"`) {
		t.Errorf("custom_ref not created before a_after: %s", cf.Body)
	}
	for _, want := range []string{"password fields are never sent, left out: Webhook.webhook_secret",
		"Webhook HOOK-1: security is on", "Server Scripts are disabled", "All 13 customization documents applied"} {
		if !strings.Contains(r.Stderr+r.Stdout, want) {
			t.Errorf("output lacks %q:\n%s%s", want, r.Stdout, r.Stderr)
		}
	}
	if doc, ok := target.Doc("Client Script", "ToDo Form"); !ok || !strings.Contains(doc["script"].(string), "refresh(frm)") {
		t.Errorf("client script %v", doc)
	}
	// Pushing again changes nothing.
	r = cmdTOK(t, cmdTRun(t, target, "customize", "push", dir, "--yes"))
	if !strings.Contains(r.Stdout, "0 to create, 0 to update, 13 unchanged") {
		t.Errorf("second push:\n%s", r.Stdout)
	}
}

// -d keeps the documents of those DocTypes (and the Workflow States and
// Actions their Workflows use) and lists what the folder lacks.
func TestCustomizePushSelection(t *testing.T) {
	s, dir := customTPulled(t)
	if err := os.MkdirAll(filepath.Join(dir, "custom_field"), 0o755); err != nil {
		t.Fatal(err)
	}
	customTWrite(t, dir, "custom_field/Note-x.json", `{"dt": "Note", "fieldname": "x", "name": "Note-x"}`)
	s.Add("Client Script", map[string]interface{}{"name": "Only On Site", "dt": "ToDo", "script": "y()"})
	if err := os.MkdirAll(filepath.Join(dir, "stuff"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "customize", "push", dir, "-d", "ToDo", "--types", "client_script,workflow", "--dry-run")))
	var got []string
	for _, it := range res["plan"].([]interface{}) {
		m := it.(map[string]interface{})
		got = append(got, m["type"].(string)+"/"+m["name"].(string)+"="+m["action"].(string))
	}
	want := "workflow_state/Done=unchanged,workflow_state/Draft=unchanged,workflow_action_master/Finish=unchanged,client_script/ToDo Form=unchanged,workflow/ToDo Flow=unchanged"
	if strings.Join(got, ",") != want {
		t.Errorf("plan %v", got)
	}
	if ex := res["extras"].([]interface{}); len(ex) != 1 || ex[0] != "client_script/Only On Site" {
		t.Errorf("extras %v", res["extras"])
	}
	if w := res["warnings"].([]interface{}); len(w) != 1 || !strings.Contains(w[0].(string), "folder stuff skipped") {
		t.Errorf("warnings %v", w)
	}
}

// Fields the site does not have are left out with a warning; a table
// counts as unchanged when the file's columns match.
func TestCustomizePushUnknownFields(t *testing.T) {
	s, dir := customTPulled(t)
	cf := strings.Replace(customTRead(t, dir, "custom_field/ToDo-custom_ref.json"), `"dt": "ToDo"`, `"dt": "ToDo", "zz_new": 1`, 1)
	customTWrite(t, dir, "custom_field/ToDo-custom_ref.json", cf)
	// A column the site added to its rows does not count as a change.
	doc, _ := s.Doc("Workflow", "ToDo Flow")
	for _, r := range doc["transitions"].([]interface{}) {
		r.(map[string]interface{})["condition_v16"] = "x"
	}
	s.Add("Workflow", doc)
	r := cmdTOK(t, cmdTRun(t, s, "customize", "push", dir, "--yes"))
	if !strings.Contains(r.Stderr, "not fields on this site, left out: Custom Field.zz_new") || !strings.Contains(r.Stdout, "11 unchanged") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", r.Stdout, r.Stderr)
	}
}

// A document changed on the site after the plan is refused, not
// overwritten, and the run reports it (exit 8).
func TestCustomizePushConflict(t *testing.T) {
	s, dir := customTPulled(t)
	customTWrite(t, dir, "client_script/ToDo Form.script.js", "x()\n")
	s.Handle("PUT /api/resource/Client Script/ToDo Form", frappetest.ErrorHandler(&frappetest.Error{
		Status: http.StatusExpectationFailed, ExcType: "TimestampMismatchError", Message: "Document has been modified after you have opened it"}))
	r := cmdTRun(t, s, "customize", "push", dir, "--yes")
	if r.Code != 8 || !strings.Contains(r.Stdout, "was changed on the server since the plan was made") {
		t.Errorf("code %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
}

// A folder push cannot trust stops it before any request.
func TestCustomizePushBadFiles(t *testing.T) {
	for name, tc := range map[string]struct{ file, content, want string }{
		"name":    {"custom_field/Wrong.json", `{"name": "ToDo-x"}`, `the name "ToDo-x" belongs in ToDo-x.json`},
		"no name": {"custom_field/x.json", `{"dt": "ToDo"}`, `no "name"`},
		"json":    {"custom_field/y.json", `{"name": `, "not a JSON object"},
		"sidecar": {"client_script/A.json", `{"name": "A", "script": {"$file": "../custom_field/ToDo-custom_ref.json"}}`, "is not a sidecar of this document"},
		"missing": {"client_script/B.json", `{"name": "B", "script": {"$file": "B.script.js"}}`, "B.script.js"},
	} {
		t.Run(name, func(t *testing.T) {
			s, dir := customTPulled(t)
			n := len(s.Requests())
			customTWrite(t, dir, tc.file, tc.content)
			r := cmdTRun(t, s, "customize", "push", dir, "--yes")
			cmdTFail(t, r, "nothing was sent", tc.want)
			if r.Code != 2 || len(s.Requests()) != n {
				t.Errorf("code %d, %d requests", r.Code, len(s.Requests())-n)
			}
		})
	}
}

func TestSameCustomValue(t *testing.T) {
	row := func(kv ...interface{}) map[string]interface{} {
		m := map[string]interface{}{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	for i, tc := range []struct {
		local, site interface{}
		same        bool
	}{
		{json.Number("1"), json.Number("1.0"), true},
		{"a", "b", false},
		{[]interface{}{row("a", "1")}, []interface{}{row("a", "1", "b", "2")}, true},
		{[]interface{}{row("a", "1", "b", "2")}, []interface{}{row("a", "1")}, false},
		{[]interface{}{row("a", "1")}, []interface{}{row("a", "1"), row("a", "2")}, false},
		{[]interface{}{}, nil, true},
		{[]interface{}{row("a", "1")}, nil, false},
	} {
		if got := sameCustomValue(tc.local, tc.site); got != tc.same {
			t.Errorf("%d: sameCustomValue(%v, %v) = %v", i, tc.local, tc.site, got)
		}
	}
}
