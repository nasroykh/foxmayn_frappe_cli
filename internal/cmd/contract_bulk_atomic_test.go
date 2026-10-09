//go:build contract

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractBulkCreateAtomic pins bulk-create --atomic (frappe.client.insert_many)
// on the fixture DocType: the names come back in input order, a batch with one
// invalid item creates nothing (one transaction) and fails with exit 6 and no
// report, and more than 200 items are refused before any request. Every
// document carries a title of its own, so the checks count only this test's
// documents; teardownContract removes whatever is left. The fixture has no
// Link field, so the invalid item is a Select value outside its options: the
// same ValidationError class as a bad Link.
func contractBulkCreateAtomic(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	cfg := contractConfig(t, sc)
	count := func(title string) int {
		t.Helper()
		rows, err := c.GetList(contractCtx(t), contractDT, client.ListOptions{Filters: `{"title":"` + title + `"}`, Limit: -1})
		if err != nil {
			t.Fatalf("list %q: %v", title, err)
		}
		return len(rows)
	}
	items := func(title string, n int) string {
		docs := make([]string, n)
		for i := range docs {
			docs[i] = fmt.Sprintf(`{"title":%q,"ref_no":"A-%d"}`, title, i+1)
		}
		return "[" + strings.Join(docs, ",") + "]"
	}

	// Success: one name per item, in the order of the input.
	r := runFFC(t, cfg, "", "bulk-create", "-d", contractDT, "--atomic", "--json", "--data", items("ffc atomic ok", 3))
	if r.Err != nil {
		t.Fatalf("bulk-create --atomic: %v\n%s", r.Err, r.Stderr)
	}
	var rep struct {
		Created int
		Failed  int
		Results []struct {
			Index  int
			Name   string
			Status string
		}
	}
	if err := json.Unmarshal([]byte(r.Stdout), &rep); err != nil {
		t.Fatalf("report: %v\n%s", err, r.Stdout)
	}
	if rep.Created != 3 || rep.Failed != 0 || len(rep.Results) != 3 {
		t.Fatalf("report %+v", rep)
	}
	for i, res := range rep.Results {
		d, err := c.GetDoc(contractCtx(t), contractDT, res.Name)
		if err != nil {
			t.Fatalf("result %d (%s): %v", i+1, res.Name, err)
		}
		if res.Index != i+1 || res.Status != "created" || d["ref_no"] != fmt.Sprintf("A-%d", i+1) {
			t.Errorf("result %d = %+v, document ref_no %v: names are not in input order", i+1, res, d["ref_no"])
		}
	}

	// Two valid items and one invalid: nothing is created, whatever the order.
	bad := `[{"title":"ffc atomic bad","ref_no":"B-1"},{"title":"ffc atomic bad","ref_no":"B-2"},{"title":"ffc atomic bad","status":"Not an option"}]`
	r = runFFC(t, cfg, "", "bulk-create", "-d", contractDT, "--atomic", "--json", "--data", bad)
	if r.Code != exitValidation || r.Err == nil || !strings.Contains(r.Err.Error(), "nothing was created") {
		t.Errorf("invalid item: exit %d, err %v, want %d and \"nothing was created\"", r.Code, r.Err, exitValidation)
	}
	if strings.TrimSpace(r.Stdout) != "" {
		t.Errorf("a failed batch printed a report: %q", r.Stdout)
	}
	if n := count("ffc atomic bad"); n != 0 {
		t.Errorf("%d documents of the failed batch were created, want 0", n)
	}

	// More than 200 items never reach the site.
	r = runFFC(t, cfg, "", "bulk-create", "-d", contractDT, "--atomic", "--data", items("ffc atomic over", 201))
	if r.Code != exitUsage || r.Err == nil || !strings.Contains(r.Err.Error(), "201 documents exceed the 200") {
		t.Errorf("201 items: exit %d, err %v, want %d", r.Code, r.Err, exitUsage)
	}
	if n := count("ffc atomic over"); n != 0 {
		t.Errorf("%d documents of the refused batch were created", n)
	}
}
