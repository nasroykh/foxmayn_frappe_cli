package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func adminTReport(s *frappetest.Site, healthy bool) {
	rep := map[string]interface{}{
		"name": "System Health Report", "background_jobs_check": "queued", "total_background_workers": 3,
		"scheduler_status": "Active", "total_errors": 0, "failed_emails": 0, "database": "mariadb",
		"database_version": "10.11.6", "db_storage_usage": json.Number("120.53"), "total_users": 4,
		"_server_messages": "x", "test_job_id": "j1",
		"background_workers": []interface{}{map[string]interface{}{"name": "r1", "parent": "System Health Report", "queues": "default,short,long", "count": 3, "utilization": json.Number("2.5"), "failed_jobs": 0}},
		"queue_status":       []interface{}{map[string]interface{}{"queue": "default", "pending_jobs": 0}, map[string]interface{}{"queue": "long", "pending_jobs": 2}},
	}
	if !healthy {
		rep["scheduler_status"] = "Inactive"
		rep["total_errors"] = 7
		rep["failed_emails"] = 2
		rep["oldest_unscheduled_job"] = "abc123"
		rep["failing_scheduled_jobs"] = []interface{}{map[string]interface{}{"scheduled_job_type": "hash1", "failure_rate": json.Number("25.0")}}
		rep["top_errors"] = []interface{}{map[string]interface{}{"title": "frappe.exceptions.ValidationError: x", "occurrences": 5}}
	}
	s.Add("System Health Report", rep)
	s.Add("Scheduled Job Type", map[string]interface{}{"name": "hash1", "method": "app.tasks.nightly", "stopped": 0})
}

func TestHealth(t *testing.T) {
	s := frappetest.New(t)
	adminTReport(s, false)
	r := cmdTOK(t, cmdTRun(t, s, "health"))
	for _, want := range []string{
		"3 workers, test job queued", "default,short,long: 3 workers, 2.5% busy, 0 failed jobs", "pending: default 0, long 2",
		"Scheduler         Inactive", "failing: app.tasks.nightly (25% of runs, 7 days)", "5× frappe.exceptions.ValidationError: x",
		"mariadb 10.11.6, 120.5 MB", "Needs attention:", "  - scheduler: Inactive", "a scheduled job is overdue: abc123",
		"scheduled job app.tasks.nightly failed in 25% of its runs", "2 emails failed to send", "7 errors logged (24 hours): ffc errors",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("health lacks %q:\n%s", want, r.Stdout)
		}
	}
	rep := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "health")))
	for _, k := range []string{"name", "doctype", "modified", "_server_messages"} {
		if _, ok := rep[k]; ok {
			t.Errorf("JSON keeps %q", k)
		}
	}
	if row := rep["background_workers"].([]interface{})[0].(map[string]interface{}); row["parent"] != nil || row["name"] != nil {
		t.Errorf("row identity kept: %v", row)
	}
	if att := rep["attention"].([]interface{}); len(att) != 5 {
		t.Errorf("attention %v", att)
	}
}

func TestHealthClean(t *testing.T) {
	s := frappetest.New(t)
	adminTReport(s, true)
	r := cmdTOK(t, cmdTRun(t, s, "health"))
	if !strings.Contains(r.Stdout+r.Stderr, "Nothing needs attention.") || strings.Contains(r.Stdout, "Needs attention") {
		t.Errorf("stdout:\n%s", r.Stdout)
	}
	if len(s.RequestsTo(http.MethodGet, "/api/resource/Scheduled Job Type")) != 0 {
		t.Error("job types read without failing jobs")
	}
}

func TestHealthPermission(t *testing.T) {
	s := frappetest.New(t)
	s.Handle("GET /api/resource/System Health Report/System Health Report", frappetest.ErrorHandler(&frappetest.Error{
		Status: http.StatusForbidden, ExcType: "PermissionError", Message: "Not permitted"}))
	r := cmdTRun(t, s, "health")
	if r.Code != 5 {
		t.Errorf("code %d: %v", r.Code, r.Err)
	}
}

func adminTJobs(s *frappetest.Site) *[]map[string]interface{} {
	var got []map[string]interface{}
	jobs := []interface{}{
		map[string]interface{}{"name": "site||j2", "job_id": "site||j2", "queue": "long", "status": "failed", "job_name": "app.sync",
			"started_at": "2026-10-08 10:45:15.045749+01:00", "time_taken": json.Number("0.173927"), "_comment_count": 0, "owner": "Administrator",
			"exc_info": "Traceback (most recent call last):\n  File \"x\"\nKeyError: 'thumb'\n", "arguments": "{}"},
		map[string]interface{}{"name": "site||j1", "job_id": "site||j1", "queue": "default", "status": "finished", "job_name": "app.ping", "time_taken": json.Number("0.2")},
	}
	// RQ Job's get_list ignores fields and answers whole jobs.
	s.HandleMethod("frappe.client.get_list", func(r *http.Request, args map[string]interface{}) (interface{}, error) {
		got = append(got, args)
		if r.Method != http.MethodPost || args["order_by"] == nil {
			return nil, &frappetest.Error{Status: 500, ExcType: "TypeError", Message: "argument of type 'NoneType' is not a container or iterable"}
		}
		return jobs, nil
	})
	return &got
}

func TestJobs(t *testing.T) {
	s := frappetest.New(t)
	got := adminTJobs(s)
	r := cmdTOK(t, cmdTRun(t, s, "jobs", "--status", "failed", "--queue", "long", "-l", "5"))
	for _, want := range []string{"site||j2", "KeyError: 'thumb'", "2026-10-08 10:45:15", "0.2"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jobs lack %q:\n%s", want, r.Stdout)
		}
	}
	args := (*got)[0]
	if args["doctype"] != "RQ Job" || args["limit_page_length"] != json.Number("5") ||
		mustJSON(t, args["filters"]) != `[["RQ Job","status","=","failed"],["RQ Job","queue","=","long"]]` {
		t.Errorf("get_list args %v", args)
	}
	rows := cmdTRows(t, cmdTOK(t, cmdTRun(t, s, "--json", "jobs")))
	if len(rows) != 2 || rows[0]["_comment_count"] != nil || rows[0]["owner"] != nil || rows[0]["exc_info"] == nil {
		t.Errorf("rows %v", rows)
	}
	cmdTFail(t, cmdTRun(t, s, "jobs", "--status", "broken"), "--status must be one of")

	// Server text is printed without terminal escapes.
	s.Add("RQ Job", map[string]interface{}{"name": "site||j2", "job_id": "site||j2", "status": "failed",
		"exc_info": "Traceback\nKeyError: 'thumb'\x1b]0;title\a", "arguments": "{\"a\": 1}\x1b[2J"})
	r = cmdTOK(t, cmdTRun(t, s, "jobs", "-n", "site||j2"))
	if !strings.Contains(r.Stdout, "status             failed") || !strings.Contains(r.Stdout, "\nTraceback\nKeyError: 'thumb'") ||
		strings.ContainsAny(r.Stdout, "\x1b\a") {
		t.Errorf("job:\n%q", r.Stdout)
	}
}

// Frappe v15 lists only the first 20 matching jobs: a full page warns.
func TestJobsV15(t *testing.T) {
	for _, tc := range []struct {
		version string
		n       int
		warn    bool
	}{{"15.121.6", 20, true}, {"15.121.6", 19, false}, {"16.37.0", 20, false}} {
		s := frappetest.New(t)
		s.SetApps(map[string]frappetest.App{"frappe": {Title: "Frappe Framework", Version: tc.version}})
		jobs := make([]interface{}, tc.n)
		for i := range jobs {
			jobs[i] = map[string]interface{}{"name": fmt.Sprint(i), "job_id": fmt.Sprint(i), "status": "finished"}
		}
		s.HandleMethod("frappe.client.get_list", func(*http.Request, map[string]interface{}) (interface{}, error) { return jobs, nil })
		r := cmdTOK(t, cmdTRun(t, s, "--json", "jobs", "-l", "5"))
		if got := strings.Contains(r.Stderr, "lists only 20 jobs"); got != tc.warn || len(cmdTRows(t, r)) != 5 {
			t.Errorf("%s with %d jobs: stderr %q", tc.version, tc.n, r.Stderr)
		}
	}
}

// A check that failed on the site leaves its fields unset: no alarm.
func TestHealthAttentionUnset(t *testing.T) {
	if got := healthAttention(map[string]interface{}{}); len(got) != 0 {
		t.Errorf("unset report: %v", got)
	}
	got := healthAttention(map[string]interface{}{"background_jobs_check": "failed", "total_background_workers": json.Number("0")})
	if len(got) != 2 {
		t.Errorf("failed checks: %v", got)
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func adminTClock(t *testing.T) {
	t.Helper()
	old := adminNow
	adminNow = func() time.Time { return time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { adminNow = old })
}

// --since counts back in the System Settings time zone, not the
// defaults' one (frappe.client.get_time_zone).
func TestErrors(t *testing.T) {
	s := frappetest.New(t)
	adminTClock(t)
	s.Add("System Settings", map[string]interface{}{"name": "System Settings", "time_zone": "Africa/Algiers"})
	s.AddDocType("Error Log", "name", "creation", "method", "reference_doctype", "reference_name", "seen", "error")
	s.Add("Error Log",
		map[string]interface{}{"name": "e1", "creation": "2026-10-08 09:30:00.000000", "method": "frappe.exceptions.ValidationError: bad", "reference_doctype": "ToDo", "reference_name": "T1", "seen": 0, "error": "Traceback\nValidationError: bad"},
		map[string]interface{}{"name": "e0", "creation": "2026-10-07 08:00:00.000000", "method": "old", "seen": 1, "error": "x"})
	r := cmdTOK(t, cmdTRun(t, s, "errors"))
	if !strings.Contains(r.Stdout, "e1") || strings.Contains(r.Stdout, "e0") || !strings.Contains(r.Stdout, "ToDo T1") {
		t.Errorf("errors:\n%s", r.Stdout)
	}
	reqs := s.RequestsTo(http.MethodGet, "/api/resource/Error Log")
	// 09:00 UTC is 10:00 in Algiers; 24 h back.
	var filters interface{}
	if err := json.Unmarshal([]byte(reqs[0].Query.Get("filters")), &filters); err != nil || mustJSON(t, filters) != mustJSON(t, [][]string{{"creation", ">", "2026-10-07 10:00:00"}}) {
		t.Errorf("filters %v (%v)", filters, err)
	}
	r = cmdTOK(t, cmdTRun(t, s, "errors", "--since", "0", "-d", "ToDo", "--method", "Valid"))
	if f := s.RequestsTo(http.MethodGet, "/api/resource/Error Log")[1].Query.Get("filters"); f != `[["reference_doctype","=","ToDo"],["method","like","%Valid%"]]` {
		t.Errorf("filters %s", f)
	}
	r = cmdTOK(t, cmdTRun(t, s, "errors", "-n", "e1"))
	if !strings.Contains(r.Stdout, "reference_doctype  ToDo") || !strings.Contains(r.Stdout, "\nTraceback\nValidationError: bad") {
		t.Errorf("error:\n%s", r.Stdout)
	}
	cmdTFail(t, cmdTRun(t, s, "errors", "--since", "yesterday"), "use a duration such as")
	r = cmdTOK(t, cmdTRun(t, s, "errors", "--since", "20m"))
	if r.Stdout != "" || !strings.Contains(r.Stderr, "No errors logged in the last 20m.") {
		t.Errorf("empty: %q, %q", r.Stdout, r.Stderr)
	}
}

func TestScheduler(t *testing.T) {
	s := frappetest.New(t)
	adminTClock(t)
	s.Add("System Settings", map[string]interface{}{"name": "System Settings", "time_zone": "UTC"})
	s.HandleMethod("frappe.utils.scheduler.get_scheduler_status", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"status": "inactive"}, nil
	})
	s.Add("Scheduled Job Type",
		map[string]interface{}{"name": "h1", "method": "app.a", "stopped": 0},
		map[string]interface{}{"name": "h2", "method": "app.b", "stopped": 0},
		map[string]interface{}{"name": "h3", "method": "app.c", "stopped": 1})
	s.AddDocType("Scheduled Job Log", "status", "creation", "scheduled_job_type", "details")
	s.Add("Scheduled Job Log",
		map[string]interface{}{"name": "l1", "status": "Failed", "scheduled_job_type": "h1", "creation": "2026-10-08 08:00:00", "details": "Traceback\nOld error"},
		map[string]interface{}{"name": "l2", "status": "Failed", "scheduled_job_type": "h1", "creation": "2026-10-08 08:30:00", "details": "Traceback\nNew error"},
		map[string]interface{}{"name": "l3", "status": "Failed", "scheduled_job_type": "h2", "creation": "2026-10-08 08:10:00", "details": "x"},
		map[string]interface{}{"name": "l4", "status": "Complete", "scheduled_job_type": "h2", "creation": "2026-10-08 08:20:00"},
		map[string]interface{}{"name": "l5", "status": "Failed", "scheduled_job_type": "h2", "creation": "2026-10-01 08:20:00"})
	r := cmdTOK(t, cmdTRun(t, s, "scheduler"))
	for _, want := range []string{"Scheduler: inactive", "Job types: 2 enabled, 1 stopped", "Failed runs in the last 1d:", "app.a", "New error"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("scheduler lacks %q:\n%s", want, r.Stdout)
		}
	}
	rep := cmdTObj(t, cmdTOK(t, cmdTRun(t, s, "--json", "scheduler")))
	f := rep["failures"].([]interface{})
	if len(f) != 2 || f[0].(map[string]interface{})["method"] != "app.a" || f[0].(map[string]interface{})["count"] != float64(2) ||
		f[1].(map[string]interface{})["count"] != float64(1) || rep["since"] != "2026-10-07 09:00:00" {
		t.Errorf("report %v", rep)
	}
}

// At the cap the report says so; equal counts sort by method, not by the
// job type's hash name.
func TestSchedulerCap(t *testing.T) {
	s := frappetest.New(t)
	adminTClock(t)
	s.Add("System Settings", map[string]interface{}{"name": "System Settings", "time_zone": "UTC"})
	s.HandleMethod("frappe.utils.scheduler.get_scheduler_status", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"status": "active"}, nil
	})
	s.Add("Scheduled Job Type",
		map[string]interface{}{"name": "a1", "method": "app.z", "stopped": 0},
		map[string]interface{}{"name": "z1", "method": "app.a", "stopped": 0})
	s.AddDocType("Scheduled Job Log", "status", "creation", "scheduled_job_type", "details")
	for i := 0; i < schedulerFailureCap+2; i++ {
		s.Add("Scheduled Job Log", map[string]interface{}{"name": fmt.Sprintf("l%d", i), "status": "Failed",
			"scheduled_job_type": []string{"a1", "z1"}[i%2], "creation": fmt.Sprintf("2026-10-08 08:%02d:%02d", i/60%60, i%60)})
	}
	r := cmdTOK(t, cmdTRun(t, s, "--json", "scheduler"))
	rep := cmdTObj(t, r)
	f := rep["failures"].([]interface{})
	if rep["warning"] == nil || !strings.Contains(r.Stderr, "only the newest 1000 failed runs") || len(f) != 2 ||
		f[0].(map[string]interface{})["method"] != "app.a" || f[0].(map[string]interface{})["count"] != float64(500) {
		t.Errorf("report %v\nstderr %s", rep, r.Stderr)
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"0": 0, "": 0, "30s": 30 * time.Second, "30m": 30 * time.Minute, "1h": time.Hour, "7d": 7 * 24 * time.Hour} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("parseSince(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"-1h", "xd", "yesterday", "-2d", "200000d"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
	for d, want := range map[time.Duration]string{90 * time.Minute: " in the last 1h30m", time.Hour: " in the last 1h", 20 * time.Minute: " in the last 20m", 48 * time.Hour: " in the last 2d",
		30 * time.Second: " in the last 30s", 90 * time.Second: " in the last 1m30s", 10 * time.Second: " in the last 10s", 2 * time.Hour: " in the last 2h"} {
		if got := sinceText(d); got != want {
			t.Errorf("sinceText(%v) = %q", d, got)
		}
	}
}
