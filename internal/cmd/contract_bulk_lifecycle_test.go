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

// contractBulkLifecycle pins bulk-submit and bulk-cancel on the fixture
// DocType: a list is submitted and cancelled one document at a time, a
// document already in the target state is a failed item with ffc's own
// message (exit 8) and the others still go through, and --filters selects
// by docstatus. Teardown cancels and deletes whatever is left.
func contractBulkLifecycle(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	cfg := contractConfig(t, sc)
	bulk := func(wantCode int, args ...string) blcReport {
		t.Helper()
		r := runFFC(t, cfg, "", append(args, "--yes", "--json")...)
		if r.Code != wantCode {
			t.Fatalf("%s: exit %d, want %d (%v)\n%s", args[0], r.Code, wantCode, r.Err, r.Stderr)
		}
		var rep blcReport
		if err := json.Unmarshal([]byte(r.Stdout), &rep); err != nil {
			t.Fatalf("%s: %v\n%s", args[0], err, r.Stdout)
		}
		return rep
	}
	docstatus := func(name string) string {
		t.Helper()
		d, err := c.GetDoc(contractCtx(t), contractDT, name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return fmt.Sprint(d["docstatus"])
	}
	a := createContractDoc(t, c, map[string]interface{}{"title": "bulk lifecycle"})
	b := createContractDoc(t, c, map[string]interface{}{"title": "bulk lifecycle"})
	d := createContractDoc(t, c, map[string]interface{}{"title": "bulk lifecycle"})

	rep := bulk(0, "bulk-submit", "-d", contractDT, "--names", a+","+b)
	if rep.Submitted != 2 || rep.Failed != 0 || docstatus(a) != "1" || docstatus(b) != "1" {
		t.Fatalf("bulk-submit: %+v", rep)
	}
	// Submitted again, a draft after it: the first fails, the second still goes.
	rep = bulk(exitPartial, "bulk-submit", "-d", contractDT, "--names", a+","+d)
	if rep.Submitted != 1 || rep.Failed != 1 || rep.Results[0].Status != "error" || !strings.Contains(rep.Results[0].Error, "already submitted") || docstatus(d) != "1" {
		t.Errorf("bulk-submit of a submitted document: %+v", rep)
	}

	rep = bulk(0, "bulk-cancel", "-d", contractDT, "--names", a+","+b)
	if rep.Cancelled != 2 || rep.Failed != 0 || docstatus(a) != "2" || docstatus(b) != "2" {
		t.Fatalf("bulk-cancel: %+v", rep)
	}
	rep = bulk(exitPartial, "bulk-cancel", "-d", contractDT, "--names", a+","+d)
	if rep.Cancelled != 1 || rep.Failed != 1 || !strings.Contains(rep.Results[0].Error, "already cancelled") || docstatus(d) != "2" {
		t.Errorf("bulk-cancel of a cancelled document: %+v", rep)
	}

	// --filters adds the docstatus the command needs.
	f1 := createContractDoc(t, c, map[string]interface{}{"title": "bulk filtered"})
	f2 := createContractDoc(t, c, map[string]interface{}{"title": "bulk filtered"})
	filter := `{"title":"bulk filtered"}`
	if rep = bulk(0, "bulk-cancel", "-d", contractDT, "--filters", filter); len(rep.Results) != 0 {
		t.Errorf("bulk-cancel --filters matched drafts: %+v", rep)
	}
	if rep = bulk(0, "bulk-submit", "-d", contractDT, "--filters", filter); rep.Submitted != 2 || docstatus(f1) != "1" || docstatus(f2) != "1" {
		t.Errorf("bulk-submit --filters: %+v", rep)
	}
	if rep = bulk(0, "bulk-submit", "-d", contractDT, "--filters", filter); len(rep.Results) != 0 {
		t.Errorf("bulk-submit --filters matched submitted documents: %+v", rep)
	}
	if rep = bulk(0, "bulk-cancel", "-d", contractDT, "--filters", filter); rep.Cancelled != 2 || docstatus(f1) != "2" || docstatus(f2) != "2" {
		t.Errorf("bulk-cancel --filters: %+v", rep)
	}

	// A draft cannot be cancelled.
	draft := createContractDoc(t, c, map[string]interface{}{"title": "bulk draft"})
	rep = bulk(exitPartial, "bulk-cancel", "-d", contractDT, "--names", draft)
	if rep.Failed != 1 || !strings.Contains(rep.Results[0].Error, "a draft, not submitted") || docstatus(draft) != "0" {
		t.Errorf("bulk-cancel of a draft: %+v", rep)
	}
}
