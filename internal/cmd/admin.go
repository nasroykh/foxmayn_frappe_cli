package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the site's time zone, also where the OS has no zone database (Windows)

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// T3.8 admin views: curated, read-only commands over what a System
// Manager can already read. Frappe facts (v16.36.1 source, v15 branch
// checked): see CLAUDE.md "Admin views".

// ─── ffc health ─────────────────────────────────────────────────────────────

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Show the site's System Health Report",
	Long: `Show Frappe's System Health Report: background workers and queues, the
scheduler, emails, errors, database, cache, storage and users, followed by
what needs attention.

Needs the System Manager role. Building the report takes a few seconds, and
it queues one "frappe.ping" background job to check that the queue works,
as the report's page in the desk does.

Examples:
  ffc health
  ffc health --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		rep, err := callSite(cmd, "Building the health report…", fetchHealth)
		if err != nil {
			return err
		}
		return render(rep, nil, func() error {
			printHealth(rep)
			return nil
		})
	},
}

// fetchHealth reads the System Health Report (a virtual Single: loading it
// runs every check, health_check-wrapped, so a failing step leaves its
// fields unset instead of failing the read) and adds the attention list.
func fetchHealth(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
	doc, err := c.GetDoc(ctx, "System Health Report", "System Health Report")
	if err != nil {
		return nil, err
	}
	rep, _ := normalizeCustomDoc(doc, "", nil, nil)
	delete(rep, "name")
	if failing := adminRows(rep["failing_scheduled_jobs"]); len(failing) > 0 {
		methods, err := jobTypeMethods(ctx, c)
		if err != nil {
			return nil, err
		}
		for _, r := range failing {
			if m := methods[fmt.Sprint(r["scheduled_job_type"])]; m != "" {
				r["method"] = m
			}
		}
	}
	rep["attention"] = healthAttention(rep)
	return rep, nil
}

// healthAttention lists what the report says is wrong, worst first. A
// check that failed on the site left its fields unset: no alarm then.
func healthAttention(rep map[string]interface{}) []string {
	out := []string{}
	if s := adminString(rep["background_jobs_check"]); s != "" && s != "queued" {
		out = append(out, "the test job could not be queued: the background job queue (Redis) may be down")
	}
	if _, ok := rep["total_background_workers"]; ok && adminNumber(rep["total_background_workers"]) == 0 {
		out = append(out, "no background workers are running: background jobs and emails wait")
	}
	if s := adminString(rep["scheduler_status"]); s != "" && s != "Active" {
		out = append(out, "scheduler: "+s)
	}
	if s := adminString(rep["oldest_unscheduled_job"]); s != "" {
		out = append(out, "a scheduled job is overdue: "+s)
	}
	for _, r := range adminRows(rep["failing_scheduled_jobs"]) {
		name := cmpString(r["method"], r["scheduled_job_type"])
		out = append(out, fmt.Sprintf("scheduled job %s failed in %s%% of its runs (7 days)", name, adminFormat(r["failure_rate"])))
	}
	for _, r := range adminRows(rep["background_workers"]) {
		if n := adminNumber(r["failed_jobs"]); n > 0 {
			out = append(out, fmt.Sprintf("%s failed jobs on the %s workers", adminFormat(r["failed_jobs"]), adminString(r["queues"])))
		}
	}
	if adminNumber(rep["failed_emails"]) > 0 {
		out = append(out, adminFormat(rep["failed_emails"])+" emails failed to send (7 days)")
	}
	if adminNumber(rep["total_errors"]) > 0 {
		out = append(out, adminFormat(rep["total_errors"])+" errors logged (24 hours): ffc errors")
	}
	return out
}

func printHealth(rep map[string]interface{}) {
	v := func(k string) string { return adminFormat(rep[k]) }
	line := func(label, value string) { fmt.Printf("%-17s %s\n", label, text.Sanitize(value)) }

	line("Background jobs", fmt.Sprintf("%s workers, test job %s", v("total_background_workers"), cmpString(rep["background_jobs_check"], "not checked")))
	for _, r := range adminRows(rep["background_workers"]) {
		line("", fmt.Sprintf("%s: %s workers, %s%% busy, %s failed jobs", adminString(r["queues"]), adminFormat(r["count"]), adminFormat(r["utilization"]), adminFormat(r["failed_jobs"])))
	}
	var queues []string
	for _, r := range adminRows(rep["queue_status"]) {
		queues = append(queues, fmt.Sprintf("%s %s", adminString(r["queue"]), adminFormat(r["pending_jobs"])))
	}
	if len(queues) > 0 {
		line("", "pending: "+strings.Join(queues, ", "))
	}
	line("Scheduler", cmpString(rep["scheduler_status"], "unknown"))
	for _, r := range adminRows(rep["failing_scheduled_jobs"]) {
		line("", fmt.Sprintf("failing: %s (%s%% of runs, 7 days)", cmpString(r["method"], r["scheduled_job_type"]), adminFormat(r["failure_rate"])))
	}
	line("Emails (7 days)", fmt.Sprintf("%s sent or queued, %s pending, %s failed, %s received, %s unhandled",
		v("total_outgoing_emails"), v("pending_emails"), v("failed_emails"), v("handled_emails"), v("unhandled_emails")))
	line("Errors (24 hours)", v("total_errors"))
	for _, r := range adminRows(rep["top_errors"]) {
		line("", fmt.Sprintf("%s× %s", adminFormat(r["occurrences"]), adminString(r["title"])))
	}
	line("Database", fmt.Sprintf("%s %s, %s MB", v("database"), v("database_version"), v("db_storage_usage")))
	for _, r := range adminRows(rep["top_db_tables"]) {
		line("", fmt.Sprintf("%s %s MB", adminString(r["table"]), adminFormat(r["size"])))
	}
	line("Cache", fmt.Sprintf("%s keys, %s", v("cache_keys"), v("cache_memory_usage")))
	line("Storage", fmt.Sprintf("backups %s MB (%s files), private files %s MB, public files %s MB",
		v("backups_size"), v("onsite_backups"), v("private_files_size"), v("public_files_size")))
	line("Users", fmt.Sprintf("%s enabled, %s new and %s failed logins (30 days), %s sessions",
		v("total_users"), v("new_users"), v("failed_logins"), v("active_sessions")))

	fmt.Println()
	att, _ := rep["attention"].([]string)
	if len(att) == 0 {
		output.PrintSuccess("Nothing needs attention.")
		return
	}
	fmt.Println("Needs attention:")
	for _, a := range att {
		fmt.Println("  - " + text.Sanitize(a))
	}
}

// ─── ffc jobs ───────────────────────────────────────────────────────────────

// rqJobStatuses are RQ's job states as Frappe lists them (rq_job.py).
var rqJobStatuses = []string{"queued", "started", "failed", "finished", "deferred", "scheduled", "canceled"}

var (
	jbStatus string
	jbQueue  string
	jbLimit  int
	jbName   string
)

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "List the site's background jobs",
	Long: `List the site's background jobs (RQ Job), newest first, or show one job
with its arguments and full traceback (-n).

Needs the System Manager role. Redis keeps finished and failed jobs only for
a while, so this is recent history, not a log. Frappe v15 lists at most 20
jobs, and not necessarily the newest; filter with --status or --queue.

Examples:
  ffc jobs
  ffc jobs --status failed
  ffc jobs --queue long -l 50
  ffc jobs -n <job id>
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if jbName != "" {
			doc, err := callSite(cmd, "Reading the job…", func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
				return c.GetDoc(ctx, "RQ Job", jbName)
			})
			if err != nil {
				return err
			}
			job := cleanJob(doc)
			return render(job, nil, func() error {
				printRecord(job, []string{"job_id", "job_name", "queue", "status", "creation", "started_at", "ended_at", "time_taken", "timeout", "arguments"}, "exc_info")
				return nil
			})
		}
		if jbStatus != "" && !slices.Contains(rqJobStatuses, jbStatus) {
			return usageErrorf("--status must be one of %s", strings.Join(rqJobStatuses, ", "))
		}
		if jbLimit < 1 || jbLimit > 500 {
			return usageErrorf("--limit must be between 1 and 500")
		}
		list, err := callSiteCfg(cmd, "Reading background jobs…", func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (jobList, error) {
			return fetchJobs(ctx, c, cfg, jbStatus, jbQueue, jbLimit)
		})
		if err != nil {
			return err
		}
		if list.warning != "" {
			output.PrintWarning(list.warning)
		}
		rows := list.rows
		return render(rows, nil, func() error {
			table := make([]map[string]interface{}, len(rows))
			for i, r := range rows {
				table[i] = map[string]interface{}{"job_id": r["job_id"], "queue": r["queue"], "status": r["status"], "job_name": r["job_name"],
					"started_at": shortTime(r["started_at"]), "seconds": adminFormat(r["time_taken"]), "error": lastTextLine(adminString(r["exc_info"]), 80)}
			}
			output.PrintTable(table, []string{"job_id", "queue", "status", "job_name", "started_at", "seconds", "error"})
			return nil
		})
	},
}

type jobList struct {
	rows    []map[string]interface{}
	warning string
}

// v15JobPage is what Frappe v15's RQ Job list returns at most.
const v15JobPage = 20

// fetchJobs lists RQ Jobs, newest first. RQ Job is a virtual DocType whose
// get_list (rq_job.py) ignores fields, needs an order_by ("desc" in
// order_by fails on None) and slices with page_length unconverted, so a
// GET list (string query values) fails with a TypeError: the query is a
// POST of frappe.client.get_list with a JSON number. v15's make_filter_dict
// reads four-element filters only (a shorter one is an IndexError), and its
// get_list reads page_length and start, which DatabaseQuery never passes:
// it takes the first 20 matching job ids in queue and registry order, then
// sorts those. So on v15 a full page is not the newest jobs (warning).
func fetchJobs(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, status, queue string, limit int) (jobList, error) {
	filters := []interface{}{}
	if status != "" {
		filters = append(filters, []interface{}{"RQ Job", "status", "=", status})
	}
	if queue != "" {
		filters = append(filters, []interface{}{"RQ Job", "queue", "=", queue})
	}
	res, err := c.CallMethod(ctx, "frappe.client.get_list", map[string]interface{}{
		"doctype": "RQ Job", "filters": filters, "order_by": "creation desc", "limit_page_length": limit,
	}, false)
	if err != nil {
		return jobList{}, err
	}
	list, _ := res.([]interface{})
	out := jobList{}
	if len(list) >= v15JobPage && cfg != nil {
		if info, _, err := serverInfo(ctx, c, cfg, false); err == nil {
			if m := info.FrappeMajor(); m > 0 && m < 16 {
				out.warning = fmt.Sprintf("Frappe v%d lists only %d jobs, not necessarily the newest: filter by status or queue", m, v15JobPage)
			}
		}
	}
	rows := make([]map[string]interface{}, 0, len(list))
	for _, r := range list {
		if m, ok := r.(map[string]interface{}); ok {
			rows = append(rows, cleanJob(m))
		}
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out.rows = rows
	return out, nil
}

// cleanJob drops what serialize_job fills in for the desk only.
func cleanJob(job map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range job {
		if strings.HasPrefix(k, "_") || k == "name" || k == "doctype" || k == "owner" || k == "modified_by" || k == "docstatus" || k == "idx" {
			continue
		}
		out[k] = v
	}
	return out
}

// ─── ffc errors ─────────────────────────────────────────────────────────────

var (
	erSince   string
	erLimit   int
	erDoctype string
	erMethod  string
	erName    string
)

var errorsCmd = &cobra.Command{
	Use:   "errors",
	Short: "List the site's recent Error Log entries",
	Long: `List the site's Error Log, newest first, or show one entry with its
traceback (-n).

--since counts back from now in the site's time zone (System Settings), as
Frappe stores the times. Needs the System Manager role. Reading an entry
here does not mark it seen.

Examples:
  ffc errors
  ffc errors --since 1h
  ffc errors --since 7d -d "Sales Invoice"
  ffc errors --method "TimestampMismatch"
  ffc errors -n <name>
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if erName != "" {
			doc, err := callSite(cmd, "Reading the error…", func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
				return c.GetDoc(ctx, "Error Log", erName)
			})
			if err != nil {
				return err
			}
			rec, _ := normalizeCustomDoc(doc, "", nil, nil)
			rec["creation"] = doc["creation"]
			return render(rec, nil, func() error {
				printRecord(rec, []string{"name", "creation", "method", "reference_doctype", "reference_name", "seen", "trace_id"}, "error")
				return nil
			})
		}
		since, err := parseSince(erSince)
		if err != nil {
			return err
		}
		if erLimit < 1 || erLimit > 500 {
			return usageErrorf("--limit must be between 1 and 500")
		}
		rows, err := callSite(cmd, "Reading the Error Log…", func(ctx context.Context, c *client.FrappeClient) ([]map[string]interface{}, error) {
			return fetchErrors(ctx, c, since, erDoctype, erMethod, erLimit)
		})
		if err != nil {
			return err
		}
		return render(rows, nil, func() error {
			if len(rows) == 0 {
				fmt.Fprintln(os.Stderr, "No errors logged"+sinceText(since)+".")
				return nil
			}
			table := make([]map[string]interface{}, len(rows))
			for i, r := range rows {
				ref := adminString(r["reference_doctype"])
				if n := adminString(r["reference_name"]); n != "" {
					ref += " " + n
				}
				table[i] = map[string]interface{}{"name": r["name"], "creation": shortTime(r["creation"]),
					"method": lastTextLine(adminString(r["method"]), 70), "reference": ref, "seen": r["seen"]}
			}
			output.PrintTable(table, []string{"name", "creation", "method", "reference", "seen"})
			return nil
		})
	},
}

// fetchErrors lists Error Log entries newer than since (0: all), newest
// first, with their tracebacks (error).
func fetchErrors(ctx context.Context, c *client.FrappeClient, since time.Duration, doctype, method string, limit int) ([]map[string]interface{}, error) {
	filters := [][]interface{}{}
	if since > 0 {
		from, err := siteTimeAgo(ctx, c, since)
		if err != nil {
			return nil, err
		}
		filters = append(filters, []interface{}{"creation", ">", from})
	}
	if doctype != "" {
		filters = append(filters, []interface{}{"reference_doctype", "=", doctype})
	}
	if method != "" {
		filters = append(filters, []interface{}{"method", "like", "%" + method + "%"})
	}
	return c.GetList(ctx, "Error Log", client.ListOptions{
		Fields:  []string{"name", "creation", "method", "reference_doctype", "reference_name", "seen", "error"},
		Filters: mustFilters(filters), OrderBy: "creation desc", Limit: limit,
	})
}

// ─── ffc scheduler ──────────────────────────────────────────────────────────

var scSince string

var schedulerCmd = &cobra.Command{
	Use:   "scheduler",
	Short: "Show the scheduler's status and its failed runs",
	Long: `Show whether the scheduler runs, how many scheduled job types are
enabled, and which ones failed recently (Scheduled Job Log), with the last
error of each.

The status is "inactive" when the scheduler is disabled in System Settings
or the site config, the site is in maintenance mode, or the scheduler is
paused. Reading the job types and logs needs the System Manager role.

Only job types with "Create Log" leave a log, of failed runs too. Frappe
sets it for every frequency except All and Cron; a failure of a job without
it shows only among the background jobs (ffc jobs --status failed).

Examples:
  ffc scheduler
  ffc scheduler --since 7d
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		since, err := parseSince(scSince)
		if err != nil {
			return err
		}
		rep, err := callSite(cmd, "Reading the scheduler…", func(ctx context.Context, c *client.FrappeClient) (*schedulerReport, error) {
			return fetchScheduler(ctx, c, since)
		})
		if err != nil {
			return err
		}
		if rep.Warning != "" {
			output.PrintWarning(rep.Warning)
		}
		return render(rep, nil, func() error {
			fmt.Printf("Scheduler: %s\n", text.Sanitize(rep.Status))
			fmt.Printf("Job types: %d enabled, %d stopped\n", rep.Enabled, rep.Stopped)
			if len(rep.Failures) == 0 {
				fmt.Printf("No failed runs%s.\n", sinceText(since))
				return nil
			}
			fmt.Printf("Failed runs%s:\n", sinceText(since))
			table := make([]map[string]interface{}, len(rep.Failures))
			for i, f := range rep.Failures {
				table[i] = map[string]interface{}{"job": cmpString(f.Method, f.JobType), "failures": f.Count, "last": shortTime(f.Last), "error": lastTextLine(f.Error, 70)}
			}
			output.PrintTable(table, []string{"job", "failures", "last", "error"})
			return nil
		})
	},
}

type schedulerFailure struct {
	JobType string `json:"job_type"`
	Method  string `json:"method,omitempty"`
	Count   int    `json:"count"`
	Last    string `json:"last_failure"`
	Error   string `json:"error,omitempty"` // the last failure's details (a traceback)
}

type schedulerReport struct {
	Status   string             `json:"status"` // active or inactive
	Enabled  int                `json:"enabled"`
	Stopped  int                `json:"stopped"`
	Since    string             `json:"since,omitempty"` // site time; absent without a bound
	Failures []schedulerFailure `json:"failures"`
	Warning  string             `json:"warning,omitempty"`
}

// schedulerFailureCap bounds the failed runs read.
const schedulerFailureCap = 1000

func fetchScheduler(ctx context.Context, c *client.FrappeClient, since time.Duration) (*schedulerReport, error) {
	res, err := c.CallMethod(ctx, "frappe.utils.scheduler.get_scheduler_status", nil, true)
	if err != nil {
		return nil, err
	}
	st, _ := res.(map[string]interface{})
	rep := &schedulerReport{Status: adminString(st["status"]), Failures: []schedulerFailure{}}
	types, err := c.GetList(ctx, "Scheduled Job Type", client.ListOptions{Fields: []string{"name", "method", "stopped"}, Limit: -1})
	if err != nil {
		return nil, err
	}
	methods := map[string]string{}
	for _, t := range types {
		methods[fmt.Sprint(t["name"])] = adminString(t["method"])
		if adminNumber(t["stopped"]) != 0 {
			rep.Stopped++
		} else {
			rep.Enabled++
		}
	}
	filters := [][]interface{}{{"status", "=", "Failed"}}
	if since > 0 {
		from, err := siteTimeAgo(ctx, c, since)
		if err != nil {
			return nil, err
		}
		rep.Since = from
		filters = append(filters, []interface{}{"creation", ">", from})
	}
	logs, err := c.GetList(ctx, "Scheduled Job Log", client.ListOptions{
		Fields:  []string{"scheduled_job_type", "creation", "details"},
		Filters: mustFilters(filters), OrderBy: "creation desc", Limit: schedulerFailureCap,
	})
	if err != nil {
		return nil, err
	}
	byType := map[string]*schedulerFailure{}
	for _, l := range logs { // newest first: the first of each type is its last failure
		t := fmt.Sprint(l["scheduled_job_type"])
		f := byType[t]
		if f == nil {
			f = &schedulerFailure{JobType: t, Method: methods[t], Last: adminString(l["creation"]), Error: adminString(l["details"])}
			byType[t] = f
		}
		f.Count++
	}
	for _, t := range slices.Sorted(maps.Keys(byType)) {
		rep.Failures = append(rep.Failures, *byType[t])
	}
	sort.SliceStable(rep.Failures, func(i, j int) bool {
		a, b := rep.Failures[i], rep.Failures[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return cmpString(a.Method, a.JobType) < cmpString(b.Method, b.JobType)
	})
	if len(logs) == schedulerFailureCap {
		rep.Warning = fmt.Sprintf("only the newest %d failed runs were counted", schedulerFailureCap)
	}
	return rep, nil
}

// jobTypeMethods maps Scheduled Job Type names (a hash on v16) to their
// methods.
func jobTypeMethods(ctx context.Context, c *client.FrappeClient) (map[string]string, error) {
	types, err := c.GetList(ctx, "Scheduled Job Type", client.ListOptions{Fields: []string{"name", "method"}, Limit: -1})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, t := range types {
		out[fmt.Sprint(t["name"])] = adminString(t["method"])
	}
	return out, nil
}

func init() {
	jobsCmd.Flags().StringVar(&jbStatus, "status", "", "Only jobs in this state: "+strings.Join(rqJobStatuses, ", "))
	jobsCmd.Flags().StringVar(&jbQueue, "queue", "", "Only jobs of this queue (default, short, long or a custom one)")
	jobsCmd.Flags().IntVarP(&jbLimit, "limit", "l", 20, "Most jobs to list (1-500)")
	jobsCmd.Flags().StringVarP(&jbName, "name", "n", "", "Show this job, with its arguments and traceback")

	errorsCmd.Flags().StringVar(&erSince, "since", "24h", `Only errors newer than this (e.g. 30m, 1h, 7d; "0" for all)`)
	errorsCmd.Flags().IntVarP(&erLimit, "limit", "l", 20, "Most errors to list (1-500)")
	errorsCmd.Flags().StringVarP(&erDoctype, "doctype", "d", "", "Only errors about this DocType (reference_doctype)")
	errorsCmd.Flags().StringVar(&erMethod, "method", "", "Only errors whose title contains this text (% and _ are wildcards)")
	errorsCmd.Flags().StringVarP(&erName, "name", "n", "", "Show this Error Log entry with its traceback")

	schedulerCmd.Flags().StringVar(&scSince, "since", "24h", `Count failed runs newer than this (e.g. 1h, 7d; "0" for all)`)

	rootCmd.AddCommand(healthCmd, jobsCmd, errorsCmd, schedulerCmd)
}

// ─── Shared ─────────────────────────────────────────────────────────────────

// parseSince reads a duration with an extra day unit (7d); 0 means no
// bound.
func parseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "0" || s == "" {
		return 0, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 || n > int(math.MaxInt64/(24*time.Hour)) {
			return 0, usageErrorf("since %q: use a duration such as 30m, 1h or 7d", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, usageErrorf("since %q: use a duration such as 30m, 1h or 7d", s)
	}
	return d, nil
}

func sinceText(d time.Duration) string {
	switch {
	case d == 0:
		return ""
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf(" in the last %dd", d/(24*time.Hour))
	}
	out := d.String() // 1h30m0s → 1h30m, 2h0m0s → 2h; 30s and 1m30s stay
	if m, ok := strings.CutSuffix(out, "m0s"); ok {
		out = m + "m"
	}
	if h, ok := strings.CutSuffix(out, "h0m"); ok {
		out = h + "h"
	}
	return " in the last " + out
}

// siteTimeAgo is now minus d as Frappe stores times: naive, in the time
// zone of System Settings (get_system_timezone, utils/data.py; Asia/Kolkata
// when unset). frappe.client.get_time_zone answers the defaults' zone,
// which can differ (seen on a v16 site), so it is not used.
func siteTimeAgo(ctx context.Context, c *client.FrappeClient, d time.Duration) (string, error) {
	doc, err := c.GetDoc(ctx, "System Settings", "System Settings")
	if err != nil {
		return "", fmt.Errorf("reading the site's time zone: %w", err)
	}
	zone := cmpString(doc["time_zone"], "Asia/Kolkata")
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", fmt.Errorf("the site's time zone %q is unknown: %w", zone, err)
	}
	return adminNow().Add(-d).In(loc).Format("2006-01-02 15:04:05"), nil
}

// adminNow is the clock (a seam for tests).
var adminNow = time.Now

// printRecord prints one document's fields, then a long text field whole.
func printRecord(rec map[string]interface{}, fields []string, long string) {
	for _, f := range fields {
		if v, ok := rec[f]; ok && adminString(v) != "" {
			fmt.Printf("%-18s %s\n", f, text.Sanitize(strings.TrimSpace(adminFormat(v))))
		}
	}
	if s := adminString(rec[long]); s != "" {
		fmt.Printf("\n%s\n", text.Sanitize(strings.TrimRight(s, "\n")))
	}
}

func adminRows(v interface{}) []map[string]interface{} {
	list, _ := v.([]interface{})
	var out []map[string]interface{}
	for _, r := range list {
		if m, ok := r.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func adminString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// adminFormat prints a value, a number without a long fraction.
func adminFormat(v interface{}) string {
	if n, ok := v.(json.Number); ok {
		if f, err := n.Float64(); err == nil && f != float64(int64(f)) {
			return strconv.FormatFloat(f, 'f', 1, 64)
		}
		if f, err := n.Float64(); err == nil {
			return strconv.FormatInt(int64(f), 10)
		}
	}
	if v == nil {
		return "-"
	}
	return adminString(v)
}

func adminNumber(v interface{}) float64 {
	switch n := v.(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

// cmpString is the first non-empty string of the values.
func cmpString(vals ...interface{}) string {
	for _, v := range vals {
		if s := adminString(v); s != "" {
			return s
		}
	}
	return ""
}

// shortTime cuts a Frappe datetime to the second.
func shortTime(v interface{}) string {
	s := adminString(v)
	if len(s) >= 19 && s[10] == ' ' {
		return s[:19]
	}
	return s
}

// lastTextLine is the last non-empty line of s (a traceback's exception),
// at most max runes.
func lastTextLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	l := []rune(strings.TrimSpace(lines[len(lines)-1]))
	if len(l) > max {
		l = append(l[:max-1], '…')
	}
	return string(l)
}
