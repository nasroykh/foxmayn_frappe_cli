//go:build contract

package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractExport pins T3.1 on a real site: frappe.client.get_list with
// `parent` returns a child table's rows for the parents named in an `in`
// filter, the CSV comes out in Data Import's layout (continuation rows with
// blank document columns, formula escaping), JSON nests the rows, the
// default columns include the Custom Field getdoctype merges, and
// download_template answers an .xlsx workbook.
func contractExport(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	cfg := contractConfig(t, sc)
	marker := "ffc-export"
	a := createContractDoc(t, c, map[string]interface{}{
		"title": "=sum", "ref_no": marker, "n_int": 7,
		"items": []interface{}{
			map[string]interface{}{"item": "a", "qty": 1},
			map[string]interface{}{"item": "b", "qty": 2},
		},
	})
	b := createContractDoc(t, c, map[string]interface{}{"title": "plain", "ref_no": marker})
	doc, err := c.GetDoc(ctx, contractDT, a)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := doc["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	rowName := func(i int) string { return fmt.Sprint(items[i].(map[string]interface{})["name"]) }
	filters := `{"ref_no":"` + marker + `"}`
	run := func(args ...string) string {
		t.Helper()
		r := runFFC(t, cfg, "", args...)
		if r.Code != 0 {
			t.Fatalf("%s: exit %d\n%s", strings.Join(args, " "), r.Code, r.Stderr)
		}
		return r.Stdout
	}

	// --page-size 1: the two documents come in two pages.
	out := run("export", "-d", contractDT, "--filters", filters, "--fields", "title,n_int,items.item,items.qty", "--page-size", "1")
	recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := [][]string{
		{"name", "title", "n_int", "items.name", "items.item", "items.qty"},
		{a, "'=sum", "7", rowName(0), "a", "1"},
		{"", "", "", rowName(1), "b", "2"},
		{b, "plain", "0", "", "", ""},
	}
	if !reflect.DeepEqual(recs, want) {
		t.Errorf("csv =\n%v\nwant\n%v", recs, want)
	}

	// Default columns: name, the fields in form order (the Custom Field
	// after title), then the table.
	head := strings.SplitN(run("export", "-d", contractDT, "--filters", filters, "--limit", "1"), "\n", 2)[0]
	if !strings.HasPrefix(head, "name,title,custom_note,") || !strings.HasSuffix(head, ",items.name,items.item,items.qty") {
		t.Errorf("default header = %s", head)
	}
	if tmpl := run("import-template", "-d", contractDT); strings.TrimSpace(tmpl) != head {
		t.Errorf("template header = %s, export header = %s", tmpl, head)
	}

	var docs []map[string]interface{}
	out = run("--json", "export", "-d", contractDT, "--filters", filters, "--fields", "title,items.qty")
	if err := json.Unmarshal([]byte(out), &docs); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(docs) != 2 || docs[0]["name"] != a || docs[0]["title"] != "=sum" {
		t.Fatalf("json = %s", out)
	}
	rows, _ := docs[0]["items"].([]interface{})
	if len(rows) != 2 || !reflect.DeepEqual(rows[1], map[string]interface{}{"name": rowName(1), "qty": float64(2)}) {
		t.Errorf("json items = %v", rows)
	}
	if rows, _ := docs[1]["items"].([]interface{}); rows == nil || len(rows) != 0 {
		t.Errorf("json items of %s = %v", b, docs[1]["items"])
	}

	// Frappe's own file: an .xlsx (a zip) with data, and the blank template.
	dir := t.TempDir()
	for _, args := range [][]string{
		{"export", "-d", contractDT, "--filters", filters, "--xlsx", "-o", filepath.Join(dir, "data.xlsx")},
		{"import-template", "-d", contractDT, "--xlsx", "-o", filepath.Join(dir, "blank.xlsx")},
	} {
		run(args...)
		got, err := os.ReadFile(args[len(args)-1])
		if err != nil || !strings.HasPrefix(string(got), "PK") {
			t.Errorf("%s: %d bytes, %q…, %v", args[0], len(got), got[:min(len(got), 8)], err)
		}
	}
}
