//go:build contract

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractCustomizePull pins customize pull on a real site: the fixture's
// Custom Field and Property Setter (made through /api/resource, so not
// system generated), a Client Script whose script lands in a sidecar file
// byte for byte, a Workflow with its states and action, no stamps in any
// file, and a second pull that writes nothing. Then push: the folder
// pushed back changes nothing, an edited script and Workflow transition are
// updated (the table replaced), and a deleted Client Script is created
// again.
func contractCustomizePull(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	script := "frappe.ui.form.on(\"" + contractDT + "\", {\n\trefresh(frm) {\n\t\t// <b>&</b>\n\t},\n});\n"
	if _, err := c.CreateDoc(ctx, "Client Script", map[string]interface{}{
		"name": contractDT + " Form", "dt": contractDT, "view": "Form", "enabled": 0, "script": script,
	}); err != nil {
		t.Fatalf("create Client Script: %v", err)
	}
	// An inactive Workflow (the workflow subtest removes its own), so the
	// DocType keeps submitting as before.
	draft, done, action := contractWF+" Draft", contractWF+" Done", contractWF+" Finish"
	t.Cleanup(func() { teardownWorkflow(contractCtx(t), t, c) })
	// The workflow subtest's states may still exist: fixture documents
	// link to them until the final teardown.
	exists := func(err error) bool {
		var api *client.APIError
		return errors.As(err, &api) && api.Status == http.StatusConflict
	}
	for _, st := range []string{draft, done} {
		if _, err := c.CreateDoc(ctx, "Workflow State", map[string]interface{}{"workflow_state_name": st}); err != nil && !exists(err) {
			t.Fatalf("Workflow State: %v", err)
		}
	}
	if _, err := c.CreateDoc(ctx, "Workflow Action Master", map[string]interface{}{"workflow_action_name": action}); err != nil && !exists(err) {
		t.Fatalf("Workflow Action Master: %v", err)
	}
	if _, err := c.CreateDoc(ctx, "Workflow", map[string]interface{}{
		"workflow_name": contractWF, "document_type": contractDT, "is_active": 0, "send_email_alert": 0,
		"states": []interface{}{
			map[string]interface{}{"state": draft, "doc_status": "0", "allow_edit": "System Manager"},
			map[string]interface{}{"state": done, "doc_status": "0", "allow_edit": "System Manager"},
		},
		"transitions": []interface{}{
			map[string]interface{}{"state": draft, "action": action, "next_state": done, "allowed": "System Manager", "allow_self_approval": 1},
		},
	}); err != nil {
		t.Fatalf("Workflow: %v", err)
	}
	dir := t.TempDir()
	cfg := contractConfig(t, sc)
	r := runFFC(t, cfg, "", "--json", "customize", "pull", "-d", contractDT, "--out", dir)
	if r.Err != nil {
		t.Fatalf("pull: %v\n%s", r.Err, r.Stderr)
	}
	for _, f := range []string{
		"custom_field/" + contractDT + "-custom_note.json",
		"property_setter/" + contractDT + "-title-label.json",
		"client_script/" + contractDT + " Form.json",
		"workflow/" + contractWF + ".json",
		"workflow_state/" + contractWF + " Draft.json",
		"workflow_action_master/" + contractWF + " Finish.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "client_script", contractDT+" Form.script.js")); err != nil || string(b) != script {
		t.Errorf("sidecar: %v %q", err, b)
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		b, _ := os.ReadFile(p)
		var doc map[string]interface{}
		if json.Unmarshal(b, &doc) != nil {
			t.Errorf("%s: not JSON", p)
			return nil
		}
		for _, k := range []string{"modified", "creation", "owner", "modified_by", "docstatus", "idx"} {
			if _, ok := doc[k]; ok {
				t.Errorf("%s keeps %q", p, k)
			}
		}
		if strings.Contains(string(b), `"parent"`) || strings.Contains(string(b), "\r") {
			t.Errorf("%s keeps row identity or CR", p)
		}
		return nil
	})
	again := runFFC(t, cfg, "", "--json", "customize", "pull", "-d", contractDT, "--out", dir)
	var res struct{ Written, Unchanged int }
	if again.Err != nil || json.Unmarshal([]byte(again.Stdout), &res) != nil || res.Written != 0 || res.Unchanged < 6 {
		t.Errorf("second pull changed files: %v\n%s", again.Err, again.Stdout)
	}
	contractCustomizePush(t, c, cfg, dir)
}

func contractCustomizePush(t *testing.T, c *client.FrappeClient, cfg, dir string) {
	ctx := contractCtx(t)
	push := func(want map[string]float64) map[string]interface{} {
		t.Helper()
		r := runFFC(t, cfg, "", "--json", "customize", "push", dir, "-d", contractDT, "--yes")
		var res map[string]interface{}
		if r.Err != nil || json.Unmarshal([]byte(r.Stdout), &res) != nil {
			t.Fatalf("push: %v\n%s\n%s", r.Err, r.Stdout, r.Stderr)
		}
		if w, ok := res["warnings"]; ok {
			t.Logf("push warnings: %v", w)
		}
		for k, v := range want {
			if res[k] != v {
				t.Errorf("push %s = %v, want %v\n%s", k, res[k], v, r.Stdout)
			}
		}
		return res
	}
	push(map[string]float64{"create": 0, "update": 0})

	name := contractDT + " Form"
	script := "frappe.ui.form.on(\"" + contractDT + "\", {});\n"
	if err := os.WriteFile(filepath.Join(dir, "client_script", name+".script.js"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(dir, "workflow", contractWF+".json")
	wf, err := readCustomFile(filepath.Dir(wfPath), filepath.Base(wfPath))
	if err != nil {
		t.Fatal(err)
	}
	rows := wf["transitions"].([]interface{})
	rows[0].(map[string]interface{})["allow_self_approval"] = json.Number("0")
	b, err := marshalCustomDoc(wf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wfPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	res := push(map[string]float64{"create": 0, "update": 2, "applied": 2})
	for _, it := range res["plan"].([]interface{}) {
		m := it.(map[string]interface{})
		if m["action"] != "update" {
			continue
		}
		if f := fmt.Sprint(m["fields"]); f != "[script]" && f != "[transitions]" {
			t.Errorf("%s/%s changed %s", m["type"], m["name"], f)
		}
	}
	if doc, err := c.GetDoc(ctx, "Client Script", name); err != nil || doc["script"] != script {
		t.Errorf("script after push: %v %q", err, doc["script"])
	}
	doc, err := c.GetDoc(ctx, "Workflow", contractWF)
	if err != nil {
		t.Fatal(err)
	}
	if tr, _ := doc["transitions"].([]interface{}); len(tr) != 1 || fmt.Sprint(tr[0].(map[string]interface{})["allow_self_approval"]) != "0" {
		t.Errorf("transitions after push: %v", doc["transitions"])
	}

	if err := c.DeleteDoc(ctx, "Client Script", name); err != nil {
		t.Fatal(err)
	}
	push(map[string]float64{"create": 1, "update": 0, "applied": 1})
	if doc, err := c.GetDoc(ctx, "Client Script", name); err != nil || doc["script"] != script || doc["dt"] != contractDT {
		t.Errorf("recreated script: %v %v", err, doc)
	}
	push(map[string]float64{"create": 0, "update": 0})
}
