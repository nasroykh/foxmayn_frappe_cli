//go:build contract

package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractPreparedReport is a non-standard Query Report on the fixture
// DocType, marked prepared, so it runs on the site's "long" queue worker
// (frappe_docker's pwd.yml has one).
const contractPreparedReport = "FFC Contract Prepared"

// contractPrepared pins run-report --prepared: query_report.run answers
// {prepared_report: true, doc: null} until a job finished, make_prepared_report
// queues one that get_reports_in_queued_state lists until the worker is done,
// and the finished result is reused for the same filters.
func contractPrepared(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	teardownPrepared(ctx, t, c)
	if _, err := c.CreateDoc(ctx, "Report", map[string]interface{}{
		"report_name": contractPreparedReport, "ref_doctype": contractDT, "report_type": "Query Report",
		"is_standard": "No", "prepared_report": 1,
		"query": "select name, title from `tab" + contractDT + "` order by name",
	}); err != nil {
		t.Fatalf("create report: %v", err)
	}
	t.Cleanup(func() { teardownPrepared(context.Background(), t, c) })
	createContractDoc(t, c, map[string]interface{}{"title": "prepared"})

	live, err := c.RunReport(ctx, contractPreparedReport, nil)
	if err != nil || live["prepared_report"] != nil {
		t.Fatalf("run with ignore_prepared_report: %v, prepared_report %v", err, live["prepared_report"])
	}

	opt := client.PreparedOptions{Wait: 3 * time.Minute}
	res, err := c.RunPreparedReport(ctx, contractPreparedReport, nil, opt)
	if err != nil {
		t.Fatalf("prepared run (is the long-queue worker running?): %v", err)
	}
	doc, _ := res["doc"].(map[string]interface{})
	rows, _ := res["result"].([]interface{})
	if res["prepared_report"] != true || doc["status"] != "Completed" || len(rows) == 0 {
		t.Fatalf("prepared result: prepared_report %v, doc %v, %d rows", res["prepared_report"], doc, len(rows))
	}
	if !strings.Contains(fmt.Sprint(rows), "prepared") {
		t.Errorf("rows %v lack the fixture document", rows)
	}

	// Same filters: the finished one comes back without a new job.
	again, err := c.RunPreparedReport(ctx, contractPreparedReport, nil, opt)
	if d, _ := again["doc"].(map[string]interface{}); err != nil || d["name"] != doc["name"] {
		t.Errorf("second run: %v, doc %v; want %v reused", err, again["doc"], doc["name"])
	}
	r := runFFC(t, contractConfig(t, sc), "", "--json", "run-report", "-n", contractPreparedReport, "--prepared", "--fresh", "--wait", "3m")
	if r.Err != nil || !strings.Contains(r.Stderr, "From prepared report") || strings.Contains(r.Stderr, fmt.Sprint(doc["name"])+",") {
		t.Errorf("run-report --prepared --fresh: %v\n%s", r.Err, r.Stderr)
	}
}

// teardownPrepared removes the fixture report and its Prepared Reports, but
// only a report on the fixture DocType (anything else is not ours).
func teardownPrepared(ctx context.Context, t *testing.T, c *client.FrappeClient) {
	t.Helper()
	rep, err := c.GetDoc(ctx, "Report", contractPreparedReport)
	if err != nil {
		return
	}
	if rep["ref_doctype"] != contractDT || rep["is_standard"] != "No" {
		t.Fatalf("Report %q exists but was not created by these tests; refusing to delete it", contractPreparedReport)
	}
	if rows, err := c.GetList(ctx, "Prepared Report", client.ListOptions{Filters: `{"report_name":"` + contractPreparedReport + `"}`, Limit: -1}); err == nil {
		for _, r := range rows {
			if err := c.DeleteDoc(ctx, "Prepared Report", fmt.Sprint(r["name"])); err != nil {
				t.Logf("teardown: delete Prepared Report %v: %v", r["name"], err)
			}
		}
	}
	if err := c.DeleteDoc(ctx, "Report", contractPreparedReport); err != nil {
		t.Logf("teardown: delete Report: %v", err)
	}
}
