//go:build contract

package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractImport pins T3.1's import on a real site: what export writes
// (CSV and JSON) imports back in update mode as unchanged; a changed value
// and a changed table row update the document and keep the row's
// identity; an insert with two table rows across a continuation row
// creates the document, and --submit submits it; a bad Select value stops
// the whole file before anything is written.
func contractImport(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	cfg := contractConfig(t, sc)
	marker := "ffc-import"
	t.Cleanup(func() { cleanupContractImport(t, c, marker) })
	a := createContractDoc(t, c, map[string]interface{}{
		"title": "=sum", "ref_no": marker, "status": "Open", "n_int": 7, "n_float": 1.5, "n_cur": 1234567, "n_check": 1,
		"items": []interface{}{
			map[string]interface{}{"item": "a", "qty": 1},
			map[string]interface{}{"item": "b", "qty": 2},
		},
	})
	b := createContractDoc(t, c, map[string]interface{}{"title": "plain", "ref_no": marker})
	filters := `{"ref_no":"` + marker + `"}`
	dir := t.TempDir()
	run := func(args ...string) cliResult {
		t.Helper()
		return runFFC(t, cfg, "", args...)
	}
	statuses := func(r cliResult) []string {
		t.Helper()
		var out struct {
			Results []importResult `json:"results"`
		}
		if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
			t.Fatalf("exit %d: %v\n%s\n%s", r.Code, err, r.Stdout, r.Stderr)
		}
		var st []string
		for _, res := range out.Results {
			st = append(st, res.Status+":"+res.Error)
		}
		return st
	}

	// Round trips: every default column, CSV then JSON.
	for _, f := range []struct{ name, format string }{{"docs.csv", "csv"}, {"docs.json", "json"}} {
		file := filepath.Join(dir, f.name)
		if r := run("--output", f.format, "export", "-d", contractDT, "--filters", filters, "-o", file); r.Code != 0 {
			t.Fatalf("export: %s", r.Stderr)
		}
		r := run("--json", "import", "-d", contractDT, file, "--mode", "update")
		if got := statuses(r); r.Code != 0 || !reflect.DeepEqual(got, []string{"unchanged:", "unchanged:"}) {
			t.Fatalf("%s round trip: exit %d %v\n%s", f.format, r.Code, got, r.Stderr)
		}
	}

	// Change a's title and its second row's qty in the exported CSV.
	before, err := c.GetDoc(ctx, contractDT, a)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "docs.csv")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, h := range recs[0] {
		col[h] = i
	}
	for i, rec := range recs {
		if rec[col["name"]] == a {
			recs[i][col["title"]] = "changed"
			recs[i+1][col["items.qty"]] = "5"
		}
	}
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.WriteAll(recs)
	if err := os.WriteFile(file, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run("--json", "import", "-d", contractDT, file, "--mode", "update")
	if got := statuses(r); r.Code != 0 || !reflect.DeepEqual(got, []string{"updated:", "unchanged:"}) {
		t.Fatalf("update: exit %d %v\n%s", r.Code, got, r.Stderr)
	}
	after, err := c.GetDoc(ctx, contractDT, a)
	if err != nil {
		t.Fatal(err)
	}
	rowsBefore, rowsAfter := rowsOf(before["items"]), rowsOf(after["items"])
	if after["title"] != "changed" || len(rowsAfter) != 2 || fmt.Sprint(rowsAfter[1]["qty"]) != "5" || rowsAfter[1]["item"] != "b" ||
		rowsAfter[0]["name"] != rowsBefore[0]["name"] || rowsAfter[1]["name"] != rowsBefore[1]["name"] {
		t.Errorf("after update: %v\nitems before %v", after, rowsBefore)
	}
	if other, err := c.GetDoc(ctx, contractDT, b); err != nil || other["modified"] == nil {
		t.Errorf("b: %v", err)
	}

	// Insert one document with two rows (a continuation row), then one
	// more with --submit.
	newMarker := marker + "-new"
	ins := filepath.Join(dir, "new.csv")
	content := "title,ref_no,status,n_check,n_cur,items.item,items.qty\n" +
		"New,%s,Closed,yes,12.50,x,1\n" +
		",,,,,y,2\n"
	if err := os.WriteFile(ins, []byte(fmt.Sprintf(content, newMarker)), 0o600); err != nil {
		t.Fatal(err)
	}
	r = run("--json", "import", "-d", contractDT, ins, "--mode", "insert")
	var out struct {
		Results []importResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); r.Code != 0 || err != nil || len(out.Results) != 1 || out.Results[0].Status != "created" {
		t.Fatalf("insert: exit %d %s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	created, err := c.GetDoc(ctx, contractDT, out.Results[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(created["items"])
	if created["ref_no"] != newMarker || created["status"] != "Closed" || fmt.Sprint(created["n_check"]) != "1" ||
		fmt.Sprint(created["n_cur"]) != "12.5" || len(rows) != 2 || rows[0]["item"] != "x" || rows[1]["item"] != "y" ||
		fmt.Sprint(rows[1]["qty"]) != "2" || fmt.Sprint(created["docstatus"]) != "0" {
		t.Errorf("created %v", created)
	}
	r = run("--json", "import", "-d", contractDT, ins, "--mode", "insert", "--submit")
	out.Results = nil
	if err := json.Unmarshal([]byte(r.Stdout), &out); r.Code != 0 || err != nil || len(out.Results) != 1 || !out.Results[0].Submitted {
		t.Fatalf("insert --submit: exit %d %s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if d, err := c.GetDoc(ctx, contractDT, out.Results[0].Name); err != nil || fmt.Sprint(d["docstatus"]) != "1" {
		t.Errorf("submitted doc: %v %v", d["docstatus"], err)
	}

	// A bad Select value: nothing of the file is written.
	count := func() int {
		n, err := c.GetCount(ctx, contractDT, `{"ref_no":"`+newMarker+`"}`)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	n := count()
	bad := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(bad, []byte(fmt.Sprintf("title,ref_no,status\nok,%s,Open\nbad,%s,Bogus\n", newMarker, newMarker)), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := run("import", "-d", contractDT, bad, "--mode", "insert"); r.Code != exitValidation || !strings.Contains(r.Stderr, `status "Bogus"`) || count() != n {
		t.Errorf("bad select: exit %d, %d → %d docs\n%s", r.Code, n, count(), r.Stderr)
	}
}

// cleanupContractImport removes the documents the import test made,
// found by their ref_no marker (submitted ones are cancelled first).
func cleanupContractImport(t *testing.T, c *client.FrappeClient, marker string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rows, err := c.GetList(ctx, contractDT, client.ListOptions{Fields: []string{"name", "docstatus", "ref_no"},
		Filters: `[["ref_no","like","` + marker + `%"]]`, Limit: -1})
	if err != nil {
		t.Logf("cleanup: %v", err)
		return
	}
	for _, r := range rows {
		ref := fmt.Sprint(r["ref_no"])
		if ref != marker && ref != marker+"-new" {
			continue
		}
		name := fmt.Sprint(r["name"])
		if fmt.Sprint(r["docstatus"]) == "1" {
			_, _ = c.CallMethod(ctx, "frappe.client.cancel", map[string]interface{}{"doctype": contractDT, "name": name}, false)
		}
		if err := c.DeleteDoc(ctx, contractDT, name); err != nil {
			t.Logf("cleanup: delete %s: %v", name, err)
		}
	}
}
