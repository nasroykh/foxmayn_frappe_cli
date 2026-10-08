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
	rows := mcpTRows(t, mcpTOK(t, s, "list_errors", map[string]interface{}{"since": "1h"}))
	if len(rows) != 1 || rows[0]["name"] != "e1" || rows[0]["error"] != "ValidationError: bad" {
		t.Errorf("rows %v", rows)
	}
	if n := len(mcpTRows(t, mcpTOK(t, s, "list_errors", map[string]interface{}{"since": "0"}))); n != 2 {
		t.Errorf("since 0: %d rows", n)
	}
	mcpTErr(t, s, "list_errors", map[string]interface{}{"since": "yesterday"}, "use a duration")
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
	site.Add("Scheduled Job Log", map[string]interface{}{"name": "l1", "status": "Failed", "scheduled_job_type": "h1", "creation": "2026-10-08 08:00:00", "details": "boom"})
	rep := mcpTObj(t, mcpTOK(t, s, "scheduler_status", map[string]interface{}{"since": "7d"}))
	f := rep["failures"].([]interface{})
	if rep["status"] != "active" || rep["enabled"] != float64(1) || len(f) != 1 || f[0].(map[string]interface{})["method"] != "app.a" {
		t.Errorf("report %v", rep)
	}
}

// The DocTypes each view reads are in its scope: a denied one refuses the
// call before any request.
func TestMCPAdminPolicy(t *testing.T) {
	for tool, dt := range map[string]string{"site_health": "System Health Report", "list_jobs": "RQ Job",
		"list_errors": "Error Log", "scheduler_status": "Scheduled Job Log"} {
		s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{dt}}, config.MCPPolicy{})
		mcpTErr(t, s, tool, nil, `DocType "`+dt+`" is denied`)
		if n := len(site.Requests()); n != 0 {
			t.Errorf("%s: %d requests sent", tool, n)
		}
	}
	// list_errors reads System Settings for the time zone.
	s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"System Settings"}}, config.MCPPolicy{})
	mcpTErr(t, s, "list_errors", nil, `"System Settings" is denied`)
	if n := len(site.Requests()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}
