package frappetest

import (
	"encoding/json"
	"fmt"
)

// Prepared reports, as Frappe v15/v16 run them (query_report.py,
// prepared_report.py). A Prepared Report is a document of that DocType
// (GET /api/resource/Prepared Report/<name> works); the background worker
// is simulated by get_reports_in_queued_state: each call counts down the
// listed jobs, which finish when their count reaches zero.

// preparedJob is how the simulated worker treats a report's jobs.
type preparedJob struct {
	polls   int    // queued-state calls a new job lasts; < 0: no worker
	failure string // error_message when the job fails; "" succeeds
}

// PrepareReport marks a report added with AddReport as prepared: run
// (without ignore_prepared_report) answers only finished Prepared Reports.
// A new job finishes during the polls-th get_reports_in_queued_state call
// that lists it (0 and 1: the first; polls < 0: never, as without a
// worker), as Error with failure as its error_message when failure is not
// "". Calling it again changes how jobs already queued end too: polls < 0
// stops the worker, anything else lets their countdown run out.
func (s *Site) PrepareReport(name string, polls int, failure string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepared[name] = preparedJob{polls: polls, failure: failure}
}

// normFilters writes report filters as Frappe matches them: JSON, keys
// sorted, no spaces.
func normFilters(v interface{}) string {
	if str, ok := v.(string); ok {
		var parsed interface{}
		if json.Unmarshal([]byte(str), &parsed) == nil {
			v = parsed
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// preparedRun is query_report.run for a prepared report: the named
// Prepared Report (filters["prepared_report_name"]), else the user's newest
// Completed one with these filters; its result plus prepared_report and doc,
// or doc null.
func (s *Site) preparedRun(report string, result map[string]interface{}, filtersArg interface{}, user string) map[string]interface{} {
	filters, _ := filtersArg.(string)
	var m map[string]interface{}
	_ = json.Unmarshal([]byte(filters), &m)
	dn, _ := m["prepared_report_name"].(string)
	delete(m, "prepared_report_name")
	norm := normFilters(m)
	if m == nil {
		norm = normFilters(filtersArg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var doc map[string]interface{}
	if dn != "" {
		doc = s.doctypes["Prepared Report"][dn]
	} else {
		for _, d := range s.doctypes["Prepared Report"] {
			if d["report_name"] == report && d["filters"] == norm && d["owner"] == user && d["status"] == "Completed" &&
				(doc == nil || fmt.Sprint(d["creation"]) > fmt.Sprint(doc["creation"])) {
				doc = d
			}
		}
	}
	out := map[string]interface{}{"prepared_report": true, "doc": nil}
	if doc == nil || doc["status"] != "Completed" {
		// Frappe answers a Prepared Report without its result file with
		// doc null too (it logs "Prepared report render failed").
		return out
	}
	for k, v := range result {
		out[k] = v
	}
	out["doc"] = copyDoc(doc)
	return out
}

// makePrepared is make_prepared_report: a new Queued Prepared Report.
func (s *Site) makePrepared(args map[string]interface{}, user string) (interface{}, error) {
	report, _ := args["report_name"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reports[report]; !ok {
		return nil, NotFound(fmt.Sprintf("Report %s not found", report))
	}
	s.seq++
	name := fmt.Sprintf("prep%06d", s.seq)
	if s.doctypes["Prepared Report"] == nil {
		s.doctypes["Prepared Report"] = map[string]map[string]interface{}{}
	}
	s.doctypes["Prepared Report"][name] = s.stamp("Prepared Report", map[string]interface{}{
		"name": name, "report_name": report, "filters": normFilters(args["filters"]),
		"status": "Queued", "owner": user,
	}, true)
	s.jobs[name] = s.prepared[report].polls
	return map[string]interface{}{"name": name}, nil
}

// queuedPrepared is get_reports_in_queued_state; each call is one step of
// the simulated worker for the jobs it lists.
func (s *Site) queuedPrepared(args map[string]interface{}, user string) (interface{}, error) {
	report, _ := args["report_name"].(string)
	norm := normFilters(args["filters"])
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]interface{}{}
	for name, d := range s.doctypes["Prepared Report"] {
		if d["report_name"] != report || d["filters"] != norm || d["owner"] != user || (d["status"] != "Queued" && d["status"] != "Started") {
			continue
		}
		out = append(out, map[string]interface{}{"name": name})
		cfg := s.prepared[report]
		if cfg.polls < 0 {
			continue
		}
		s.jobs[name]--
		switch {
		case s.jobs[name] > 0:
			d["status"] = "Started"
		case cfg.failure != "":
			d["status"], d["error_message"] = "Error", cfg.failure
		default:
			d["status"], d["report_end_time"] = "Completed", s.clock.Format("2006-01-02 15:04:05.000000")
		}
	}
	return out, nil
}
