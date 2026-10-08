//go:build contract

package cmd

import (
	"encoding/json"
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
// byte for byte, the Workflow with its states and action, no stamps in any
// file, and a second pull that writes nothing.
func contractCustomizePull(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	script := "frappe.ui.form.on(\"" + contractDT + "\", {\n\trefresh(frm) {\n\t\t// <b>&</b>\n\t},\n});\n"
	if _, err := c.CreateDoc(ctx, "Client Script", map[string]interface{}{
		"name": contractDT + " Form", "dt": contractDT, "view": "Form", "enabled": 0, "script": script,
	}); err != nil {
		t.Fatalf("create Client Script: %v", err)
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
	if again.Err != nil || !strings.Contains(again.Stdout, `"written": 0`) {
		t.Errorf("second pull changed files: %v\n%s", again.Err, again.Stdout)
	}
}
