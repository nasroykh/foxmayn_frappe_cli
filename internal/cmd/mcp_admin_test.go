package cmd

import (
	"net/http"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func TestMCPSiteHealth(t *testing.T) {
	s, site := newMCPFake(t, true)
	adminTReport(site, false)
	rep := mcpTObj(t, mcpTOK(t, s, "site_health", nil))
	att, _ := rep["attention"].([]interface{})
	if len(att) != 5 || rep["name"] != nil || rep["scheduler_status"] != "Inactive" {
		t.Errorf("report %v", rep)
	}
}

func TestMCPListJobs(t *testing.T) {
	s, site := newMCPFake(t, true)
	got := adminTJobs(site)
	out := mcpTObj(t, mcpTOK(t, s, "list_jobs", map[string]interface{}{"status": "failed", "limit": 5}))
	jobs := out["jobs"].([]interface{})
	j := jobs[0].(map[string]interface{})
	if len(jobs) != 2 || j["error"] != "KeyError: 'thumb'" || j["exc_info"] != nil || j["arguments"] != nil || out["warning"] != nil {
		t.Errorf("jobs %v", out)
	}
	if args := (*got)[0]; args["limit_page_length"] == nil || mustJSON(t, args["filters"]) != `[["RQ Job","status","=","failed"]]` {
		t.Errorf("get_list args %v", args)
	}
	mcpTErr(t, s, "list_jobs", map[string]interface{}{"limit": 101}, "limit: between 1 and 100")
	mcpTErr(t, s, "list_jobs", map[string]interface{}{"status": "broken"}, "status")
	mcpTErr(t, s, "list_jobs", map[string]interface{}{"queue": 1}, "queue: expected a string")

	site.Add("RQ Job", map[string]interface{}{"name": "site||j2", "job_id": "site||j2", "status": "failed", "exc_info": "Traceback\nKeyError: 'thumb'", "owner": "Administrator"})
	job := mcpTObj(t, mcpTOK(t, s, "list_jobs", map[string]interface{}{"name": "site||j2"}))
	if job["exc_info"] != "Traceback\nKeyError: 'thumb'" || job["owner"] != nil || job["name"] != nil {
		t.Errorf("job %v", job)
	}
}

func TestMCPListErrors(t *testing.T) {
	s, site := newMCPFake(t, true)
	adminTClock(t)
	site.Add("System Settings", map[string]interface{}{"name": "System Settings", "time_zone": "UTC"})
	site.AddDocType("Error Log", "name", "creation", "method", "reference_doctype", "reference_name", "seen", "error")
	site.Add("Error Log",
		map[string]interface{}{"name": "e1", "creation": "2026-10-08 08:30:00", "method": "ValidationError", "seen": 0, "error": "Traceback\n  File x\nValidationError: bad"},
		map[string]interface{}{"name": "e0", "creation": "2026-10-01 08:00:00", "method": "old", "seen": 1, "error": "x"})
	out := mcpTObj(t, mcpTOK(t, s, "list_errors", map[string]interface{}{"since": "1h"}))
	rows := out["errors"].([]interface{})
	if row := rows[0].(map[string]interface{}); len(rows) != 1 || row["name"] != "e1" || row["error"] != "ValidationError: bad" || out["hidden_by_policy"] != float64(0) {
		t.Errorf("errors %v", out)
	}
	if n := len(mcpTObj(t, mcpTOK(t, s, "list_errors", map[string]interface{}{"since": "0"}))["errors"].([]interface{})); n != 2 {
		t.Errorf("since 0: %d rows", n)
	}
	mcpTErr(t, s, "list_errors", map[string]interface{}{"since": "yesterday"}, `since "yesterday": use a duration`)
	mcpTErr(t, s, "list_errors", map[string]interface{}{"since": 0}, "since: expected a string")
	rec := mcpTObj(t, mcpTOK(t, s, "list_errors", map[string]interface{}{"name": "e1"}))
	if rec["error"] != "Traceback\n  File x\nValidationError: bad" || rec["creation"] == nil {
		t.Errorf("entry %v", rec)
	}
}

func TestMCPSchedulerStatus(t *testing.T) {
	s, site := newMCPFake(t, true)
	adminTClock(t)
	site.Add("System Settings", map[string]interface{}{"name": "System Settings", "time_zone": "UTC"})
	site.HandleMethod("frappe.utils.scheduler.get_scheduler_status", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"status": "active"}, nil
	})
	site.Add("Scheduled Job Type", map[string]interface{}{"name": "h1", "method": "app.a", "stopped": 0})
	site.AddDocType("Scheduled Job Log", "status", "creation", "scheduled_job_type", "details")
	site.Add("Scheduled Job Log", map[string]interface{}{"name": "l1", "status": "Failed", "scheduled_job_type": "h1", "creation": "2026-10-08 08:00:00", "details": "Traceback\n  File y\nboom"})
	rep := mcpTObj(t, mcpTOK(t, s, "scheduler_status", map[string]interface{}{"since": "7d"}))
	f := rep["failures"].([]interface{})
	if rep["status"] != "active" || rep["enabled"] != float64(1) || len(f) != 1 || f[0].(map[string]interface{})["method"] != "app.a" ||
		f[0].(map[string]interface{})["error"] != "boom" {
		t.Errorf("report %v", rep)
	}
}

// The DocTypes each view reads are in its scope: a denied one refuses the
// call before any request.
func TestMCPAdminPolicy(t *testing.T) {
	for _, c := range []struct{ tool, dt string }{
		{"site_health", "System Health Report"}, {"site_health", "Error Log"}, {"site_health", "User"}, {"site_health", "Scheduled Job Type"},
		{"list_jobs", "RQ Job"}, {"list_errors", "Error Log"}, {"list_errors", "System Settings"},
		{"scheduler_status", "Scheduled Job Log"}, {"scheduler_status", "System Settings"},
	} {
		s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{c.dt}}, config.MCPPolicy{})
		mcpTErr(t, s, c.tool, nil, `DocType "`+c.dt+`" is denied`)
		if n := len(site.Requests()); n != 0 {
			t.Errorf("%s: %d requests sent", c.tool, n)
		}
	}
}

// An Error Log entry about a DocType the policy hides is left out, and
// reading it by name is refused.
func TestMCPListErrorsPolicy(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{})
	site.AddDocType("Error Log", "name", "creation", "method", "reference_doctype", "reference_name", "seen", "error")
	site.Add("Error Log",
		map[string]interface{}{"name": "e1", "creation": "2026-10-08 08:30:00", "method": "a", "reference_doctype": "ToDo", "reference_name": "TD-1", "error": "x"},
		map[string]interface{}{"name": "e2", "creation": "2026-10-08 08:20:00", "method": "b", "reference_doctype": "Note", "reference_name": "N1", "error": "y"},
		map[string]interface{}{"name": "e3", "creation": "2026-10-08 08:10:00", "method": "c", "error": "z"})
	out := mcpTObj(t, mcpTOK(t, s, "list_errors", map[string]interface{}{"since": "0"}))
	rows := out["errors"].([]interface{})
	if len(rows) != 2 || rows[0].(map[string]interface{})["name"] != "e2" || out["hidden_by_policy"] != float64(1) {
		t.Errorf("errors %v", out)
	}
	mcpTErr(t, s, "list_errors", map[string]interface{}{"name": "e1"}, `DocType "ToDo" is denied`)
	mcpTErr(t, s, "list_errors", map[string]interface{}{"doctype": "ToDo"}, `DocType "ToDo" is denied`)
}
