//go:build contract

package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// contractEdit pins what edit-doc and update-doc --if-unmodified rely on: a
// PUT with a stale "modified" is a 417 TimestampMismatchError (exit 6, as
// in the fake), a full table keeps the rows sent with their names and drops
// the others, and a submitted document without "allow on submit" fields
// has nothing to edit.
func contractEdit(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	cfg := contractConfig(t, sc)
	name := createContractDoc(t, c, map[string]interface{}{
		"title": "edit", "items": []interface{}{
			map[string]interface{}{"item": "a", "qty": 1},
			map[string]interface{}{"item": "b", "qty": 2},
		},
	})
	doc, err := c.GetDoc(ctx, contractDT, name)
	if err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(doc["items"])
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	rowA, rowB := fmt.Sprint(rows[0]["name"]), fmt.Sprint(rows[1]["name"])

	// The fake and the site refuse a stale "modified" alike.
	stale := map[string]interface{}{"title": "stale", "modified": "2000-01-01 00:00:00.000000"}
	_, realErr := c.UpdateDoc(ctx, contractDT, name, stale)
	fake := frappetest.New(t)
	fake.Add(contractDT, map[string]interface{}{"name": "x", "title": "t"})
	fc, err := client.New(ctx, &config.SiteConfig{URL: fake.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	_, fakeErr := fc.UpdateDoc(ctx, contractDT, "x", stale)
	var re, fe *client.APIError
	if !errors.As(realErr, &re) || !errors.As(fakeErr, &fe) || re.Status != 417 || re.ExcType != "TimestampMismatchError" ||
		fe.Status != re.Status || fe.ExcType != re.ExcType {
		t.Errorf("stale modified: real %v, fake %v", realErr, fakeErr)
	}
	if code, _ := classify(realErr); code != exitValidation {
		t.Errorf("stale modified: exit %d", code)
	}

	// edit-doc: a changed row keeps its name and its other columns, a
	// removed row goes, a new row is added.
	oldRun, oldIn := runEditor, editInputDisabled
	t.Cleanup(func() { runEditor, editInputDisabled = oldRun, oldIn })
	editInputDisabled = func() bool { return false }
	runEditor = func(path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f := string(b)
		cut := strings.Index(f, "  - name: "+rowB)
		if cut < 0 || !strings.Contains(f, "title: edit") || !strings.Contains(f, "qty: 1\n") {
			t.Fatalf("file:\n%s", f)
		}
		f = strings.Replace(f[:cut], "qty: 1\n", "qty: 5\n", 1) + "  - item: c\n    qty: 3\n"
		f = strings.Replace(f, "title: edit", "title: edited", 1)
		return os.WriteFile(path, []byte(f), 0o600)
	}
	r := runFFC(t, cfg, "", "edit-doc", "-d", contractDT, "-n", name, "--yes", "--json")
	if r.Err != nil {
		t.Fatalf("edit-doc: %v\n%s", r.Err, r.Stderr)
	}
	doc, err = c.GetDoc(ctx, contractDT, name)
	if err != nil {
		t.Fatal(err)
	}
	rows = rowsOf(doc["items"])
	if doc["title"] != "edited" || len(rows) != 2 || rows[0]["name"] != rowA || fmt.Sprint(rows[0]["qty"]) != "5" ||
		rows[0]["item"] != "a" || rows[1]["item"] != "c" || rows[1]["name"] == rowB {
		t.Errorf("after edit-doc: title %v, rows %v", doc["title"], rows)
	}

	// Someone saves while the editor is open: nothing is saved, exit 6.
	runEditor = func(path string) error {
		if _, err := c.UpdateDoc(ctx, contractDT, name, map[string]interface{}{"title": "concurrent"}); err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(strings.Replace(string(b), "title: edited", "title: mine", 1)), 0o600)
	}
	r = runFFC(t, cfg, "", "edit-doc", "-d", contractDT, "-n", name, "--yes")
	if r.Code != exitValidation || !strings.Contains(fmt.Sprint(r.Err), "changed on the server since you opened it") {
		t.Errorf("concurrent edit: exit %d, %v", r.Code, r.Err)
	}
	if d, _ := c.GetDoc(ctx, contractDT, name); d["title"] != "concurrent" {
		t.Errorf("title = %v, want the concurrent save kept", d["title"])
	}

	// update-doc --if-unmodified with the current value saves, with an old
	// one fails.
	d, _ := c.GetDoc(ctx, contractDT, name)
	mod := fmt.Sprint(d["modified"])
	if r := runFFC(t, cfg, "", "update-doc", "-d", contractDT, "-n", name, "--data", `{"title":"t1"}`, "--if-unmodified", mod); r.Err != nil {
		t.Errorf("--if-unmodified current: %v", r.Err)
	}
	if r := runFFC(t, cfg, "", "update-doc", "-d", contractDT, "-n", name, "--data", `{"title":"t2"}`, "--if-unmodified", mod); r.Code != exitValidation {
		t.Errorf("--if-unmodified stale: exit %d, %v", r.Code, r.Err)
	}

	// The fixture has no "allow on submit" field: a submitted document has
	// nothing to edit.
	sub := createContractDoc(t, c, map[string]interface{}{"title": "submitted"})
	if _, err := c.SubmitDoc(ctx, contractDT, sub); err != nil {
		t.Fatal(err)
	}
	if r := runFFC(t, cfg, "", "edit-doc", "-d", contractDT, "-n", sub, "--yes"); r.Code != exitValidation ||
		!strings.Contains(fmt.Sprint(r.Err), "no fields that may change after submit") {
		t.Errorf("submitted: exit %d, %v", r.Code, r.Err)
	}
}
