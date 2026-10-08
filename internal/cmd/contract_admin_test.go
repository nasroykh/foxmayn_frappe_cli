//go:build contract

package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractAdmin pins the admin views on a real site: the System Health
// Report loads as a document, RQ Job lists through a POST of
// frappe.client.get_list (a GET list fails with a TypeError), the Error Log
// filter uses the System Settings time zone, and get_scheduler_status
// answers active or inactive.
func contractAdmin(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	cfg := contractConfig(t, sc)
	run := func(args ...string) string {
		t.Helper()
		r := runFFC(t, cfg, "", append([]string{"--json"}, args...)...)
		if r.Err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), r.Err, r.Stderr)
		}
		return r.Stdout
	}

	var health map[string]interface{}
	if err := json.Unmarshal([]byte(run("health")), &health); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"attention", "scheduler_status", "total_users", "database"} {
		if _, ok := health[k]; !ok {
			t.Errorf("health lacks %q: %v", k, health)
		}
	}
	if _, ok := health["name"]; ok {
		t.Error("health keeps name")
	}
	t.Logf("health attention: %v", health["attention"])

	// The health report queued a frappe.ping job, so the queue has one.
	// v15 lists only the first 20 job ids it meets (and warns), so the
	// ping may be missing there; any job then stands in for it.
	jr := runFFC(t, cfg, "", "--json", "jobs", "-l", "50")
	if jr.Err != nil {
		t.Fatalf("jobs: %v\n%s", jr.Err, jr.Stderr)
	}
	var jobs []map[string]interface{}
	if err := json.Unmarshal([]byte(jr.Stdout), &jobs); err != nil {
		t.Fatal(err)
	}
	var target map[string]interface{}
	for _, j := range jobs {
		if j["job_name"] == "frappe.ping" {
			target = j
			break
		}
	}
	v15 := strings.Contains(jr.Stderr, "lists only 20 jobs")
	switch {
	case target == nil && v15 && len(jobs) > 0:
		t.Logf("v15: no frappe.ping among the %d jobs listed", len(jobs))
		target = jobs[0]
	case target == nil:
		t.Fatalf("no frappe.ping job among %d\n%s", len(jobs), jr.Stderr)
	}
	if _, err := c.GetDoc(contractCtx(t), "RQ Job", target["job_id"].(string)); err != nil {
		t.Errorf("jobs -n target: %v", err)
	}
	if out := run("jobs", "--status", "failed", "-l", "1"); !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("jobs --status: %s", out)
	}

	var errs []map[string]interface{}
	if err := json.Unmarshal([]byte(run("errors", "--since", "7d", "-l", "5")), &errs); err != nil {
		t.Fatal(err)
	}
	for _, e := range errs {
		if e["name"] == nil || e["creation"] == nil {
			t.Errorf("error row %v", e)
		}
	}

	var sched schedulerReport
	if err := json.Unmarshal([]byte(run("scheduler", "--since", "7d")), &sched); err != nil {
		t.Fatal(err)
	}
	if (sched.Status != "active" && sched.Status != "inactive") || sched.Enabled == 0 || sched.Since == "" {
		t.Errorf("scheduler %+v", sched)
	}
}
