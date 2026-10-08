//go:build contract

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractImportServer pins T3.1's --server on a real site: --preview
// leaves a Data Import ready (Frappe's own row and document counts),
// --resume starts it and waits for the background job, and the two
// documents with their child rows (one across a continuation row) are
// created. A bad Select value is a blocking preview warning: nothing is
// imported and the Data Import is deleted. Skipped when the site's
// scheduler is inactive (form_start_import refuses then).
func contractImportServer(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	res, err := c.CallMethod(ctx, "frappe.utils.scheduler.get_scheduler_status", nil, true)
	if m, _ := res.(map[string]interface{}); err != nil || m["status"] != "active" {
		t.Skipf("the site's scheduler is not active (%v, %v): Frappe does not start a Data Import; run bench --site <site> enable-scheduler", m, err)
	}
	cfg := contractConfig(t, sc)
	marker := "ffc-import-server"
	t.Cleanup(func() {
		cleanupContractImport(t, c, marker)
		cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cleanupContractDataImports(cctx, t, c)
	})
	run := func(args ...string) cliResult {
		t.Helper()
		return runFFC(t, cfg, "", args...)
	}
	count := func() int {
		t.Helper()
		rows, err := c.GetList(ctx, contractDT, client.ListOptions{Filters: `{"ref_no":"` + marker + `"}`, Limit: -1})
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "server.csv")
	csv := "title,ref_no,status,n_int,items.item,items.qty\n" +
		"Server A," + marker + ",Open,3,a,1\n" +
		",,,,b,2\n" +
		"Server B," + marker + ",Closed,4,c,5\n"
	if err := os.WriteFile(file, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}

	r := run("--json", "import", "-d", contractDT, file, "--mode", "insert", "--server", "--preview")
	var pv map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &pv); err != nil || r.Code != exitOK {
		t.Fatalf("preview: exit %d %v\n%s\n%s", r.Code, err, r.Stdout, r.Stderr)
	}
	name := fmt.Sprint(pv["data_import"])
	if fmt.Sprint(pv["rows"]) != "3" || fmt.Sprint(pv["documents"]) != "2" {
		t.Errorf("preview %v", pv)
	}
	if n := count(); n != 0 {
		t.Fatalf("--preview imported %d documents", n)
	}
	di, err := c.GetDoc(ctx, "Data Import", name)
	if err != nil || di["status"] != "Pending" || di["import_type"] != client.DataImportInsert {
		t.Fatalf("Data Import %v: %v", di, err)
	}

	r = run("--json", "import", "--server", "--resume", name, "--wait", "3m")
	var out dataImportResult
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || r.Code != exitOK {
		t.Fatalf("resume: exit %d %v\n%s\n%s", r.Code, err, r.Stdout, r.Stderr)
	}
	if out.Status != "Success" || out.Success != 2 || out.Failed != 0 || out.Total != 2 || len(out.Rows) != 2 {
		t.Fatalf("result %+v", out)
	}
	want := map[string]struct {
		status, items string
		nInt          string
	}{
		"Server A": {"Open", "a:1,b:2", "3"},
		"Server B": {"Closed", "c:5", "4"},
	}
	for i, row := range out.Rows {
		if row.Status != "created" || row.Name == "" {
			t.Errorf("row %d %+v", i, row)
			continue
		}
		d, err := c.GetDoc(ctx, contractDT, row.Name)
		if err != nil {
			t.Fatal(err)
		}
		w, ok := want[fmt.Sprint(d["title"])]
		if !ok || d["ref_no"] != marker || d["status"] != w.status || fmt.Sprint(d["n_int"]) != w.nInt || fmt.Sprint(d["docstatus"]) != "0" {
			t.Errorf("%s: %v", row.Name, d)
		}
		var items []string
		for _, it := range d["items"].([]interface{}) {
			m := it.(map[string]interface{})
			items = append(items, fmt.Sprintf("%v:%v", m["item"], m["qty"]))
		}
		if got := strings.Join(items, ","); got != w.items {
			t.Errorf("%s items %s, want %s", row.Name, got, w.items)
		}
	}
	if fmt.Sprint(out.Rows[0].Rows) != "[2 3]" || fmt.Sprint(out.Rows[1].Rows) != "[4]" {
		t.Errorf("log rows %v %v", out.Rows[0].Rows, out.Rows[1].Rows)
	}
	// A finished import is reported again, not restarted.
	if r := run("--json", "import", "--resume", name); r.Code != exitOK || !strings.Contains(r.Stdout, `"success": 2`) {
		t.Errorf("second resume: exit %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}

	// A bad Select value: Frappe's preview warns, nothing is imported and
	// the Data Import is deleted.
	bad := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(bad, []byte("title,ref_no,status\nBad,"+marker+",Bogus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(contractDataImports(ctx, t, c))
	r = run("import", "-d", contractDT, bad, "--mode", "insert", "--server")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, "row 2") || count() != 2 {
		t.Errorf("bad Select: exit %d, %d documents\n%s", r.Code, count(), r.Stderr)
	}
	if after := len(contractDataImports(ctx, t, c)); after != before {
		t.Errorf("the refused Data Import was kept (%d → %d)", before, after)
	}
}

// contractDataImports lists the Data Imports of the fixture DocType.
func contractDataImports(ctx context.Context, t *testing.T, c *client.FrappeClient) []map[string]interface{} {
	t.Helper()
	rows, err := c.GetList(ctx, "Data Import", client.ListOptions{Filters: `{"reference_doctype":"` + contractDT + `"}`, Limit: -1})
	if err != nil {
		t.Logf("Data Import list: %v", err)
	}
	return rows
}

// cleanupContractDataImports deletes the fixture DocType's Data Imports
// (Frappe deletes their logs and files with them); they link the DocType,
// which could not be deleted otherwise.
func cleanupContractDataImports(ctx context.Context, t *testing.T, c *client.FrappeClient) {
	t.Helper()
	for _, r := range contractDataImports(ctx, t, c) {
		name := fmt.Sprint(r["name"])
		if err := c.DeleteDoc(ctx, "Data Import", name); err != nil {
			t.Logf("cleanup: delete Data Import %s: %v", name, err)
		}
	}
}
