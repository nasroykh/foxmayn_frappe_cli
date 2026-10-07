package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Prepared reports (Frappe v15/v16, frappe/desk/query_report.py and
// core/doctype/prepared_report/prepared_report.py). A report marked
// "prepared" is not run in the request: query_report.run (without
// ignore_prepared_report) answers the newest Completed Prepared Report the
// user made with the same filters, or {"prepared_report": true, "doc": null}
// when there is none. make_prepared_report (permission-checked through
// get_report_doc) inserts a Prepared Report, and a worker on the "long"
// queue runs the report and attaches the result. get_reports_in_queued_state
// lists the user's Queued/Started ones for a report and filters; it needs no
// role, unlike reading Prepared Report documents (System Manager or Prepared
// Report User). Frappe matches filters after its own json.loads and
// as_json on both sides, so how ffc encodes them does not matter (a number
// keeps its type, though: 1.0 is not 1). A Custom Report's saved filters
// are not applied here (are_default_filters=0): ours are what is matched.

// PreparedOptions controls RunPreparedReport.
type PreparedOptions struct {
	// Fresh ignores a completed result and prepares a new one.
	Fresh bool
	// Name resumes waiting for this Prepared Report (made with the same
	// filters) instead of looking for one.
	Name string
	// Wait bounds the time spent waiting for the background job.
	Wait time.Duration
	// OnWait, when set, is called with the Prepared Report's name before
	// each check while it is queued or running.
	OnWait func(name string)
}

// PreparedReportError is a prepared report that has no result: still queued
// or running when the wait ran out (Pending), or finished without one (the
// job failed or the name belongs to other filters).
type PreparedReportError struct {
	Report, Name string
	Pending      bool
	Waited       time.Duration
	// Status and Message come from the Prepared Report when the user may
	// read it.
	Status, Message string
}

func (e *PreparedReportError) Error() string {
	if e.Pending {
		return fmt.Sprintf("prepared report %s for %q is still queued or running after %s", e.Name, e.Report, e.Waited)
	}
	msg := fmt.Sprintf("prepared report %s for %q has no result", e.Name, e.Report)
	if e.Status != "" {
		msg += " (status " + e.Status + ")"
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// PreparedPollStart is the first pause between two checks of a queued job;
// it doubles up to preparedPollMax. Tests lower it.
var PreparedPollStart = time.Second

// preparedPollMax is the longest pause between two checks of a queued job.
var preparedPollMax = 5 * time.Second

// RunPreparedReport returns a prepared report's result, preparing it in the
// background when needed: run first (the permission check, and the live
// result for a report that is not prepared), then the completed result for
// these filters unless opt.Fresh, else the user's queued job for them or a
// new one, waited for at most opt.Wait. The answer is query_report.run's,
// with "doc" the Prepared Report it came from.
func (c *FrappeClient) RunPreparedReport(ctx context.Context, reportName string, filters map[string]interface{}, opt PreparedOptions) (map[string]interface{}, error) {
	filtersJSON, err := reportFilters(filters)
	if err != nil {
		return nil, err
	}
	name := opt.Name
	if name != "" {
		if err := c.checkPrepared(ctx, reportName, filtersJSON, name); err != nil {
			return nil, err
		}
	} else {
		res, err := c.runReport(ctx, reportName, filtersJSON, false)
		if err != nil {
			return nil, err
		}
		if res["prepared_report"] != true || (!opt.Fresh && res["doc"] != nil) {
			return res, nil
		}
		queued, err := c.queuedPrepared(ctx, reportName, filtersJSON)
		if err != nil {
			return nil, err
		}
		if len(queued) > 0 {
			name = queued[0]
		} else if name, err = c.makePrepared(ctx, reportName, filtersJSON); err != nil {
			return nil, err
		}
	}
	if err := c.waitPrepared(ctx, reportName, filtersJSON, name, opt); err != nil {
		return nil, err
	}
	withName := make(map[string]interface{}, len(filters)+1)
	for k, v := range filters {
		withName[k] = v
	}
	withName["prepared_report_name"] = name
	filtersJSON, err = reportFilters(withName)
	if err != nil {
		return nil, err
	}
	res, err := c.runReport(ctx, reportName, filtersJSON, false)
	if err != nil {
		return nil, err
	}
	if res["doc"] == nil {
		// Failed (no attachment: Frappe logs it and answers without a doc).
		e := &PreparedReportError{Report: reportName, Name: name}
		var pr dataEnvelope
		if c.do(ctx, http.MethodGet, resourcePath("Prepared Report", name), nil, nil, nil, &pr) == nil {
			e.Status, _ = pr.Data["status"].(string)
			msg, _ := pr.Data["error_message"].(string)
			e.Message = lastLine(msg)
		}
		return nil, e
	}
	return res, nil
}

// waitPrepared waits until name is no longer among the user's queued or
// running Prepared Reports for these filters.
func (c *FrappeClient) waitPrepared(ctx context.Context, reportName, filtersJSON, name string, opt PreparedOptions) error {
	start := time.Now()
	pause := PreparedPollStart
	for {
		if opt.OnWait != nil {
			opt.OnWait(name)
		}
		queued, err := c.queuedPrepared(ctx, reportName, filtersJSON)
		if err != nil {
			return err
		}
		if !slices.Contains(queued, name) {
			return nil
		}
		left := opt.Wait - time.Since(start)
		if left <= 0 {
			return &PreparedReportError{Report: reportName, Name: name, Pending: true, Waited: opt.Wait}
		}
		t := time.NewTimer(min(pause, left))
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("stopped waiting for prepared report %s, which keeps running (--prepared-name %s picks it up): %w", name, name, ctx.Err())
		case <-t.C:
		}
		pause = min(pause*2, preparedPollMax)
	}
}

// queuedPrepared lists the names of the user's Queued or Started Prepared
// Reports for a report and filters, newest first.
func (c *FrappeClient) queuedPrepared(ctx context.Context, reportName, filtersJSON string) ([]string, error) {
	var result struct {
		Message []struct {
			Name string `json:"name"`
		} `json:"message"`
	}
	query := map[string]string{"report_name": reportName, "filters": filtersJSON}
	if err := c.do(ctx, http.MethodGet, "/api/method/frappe.core.doctype.prepared_report.prepared_report.get_reports_in_queued_state", nil, query, reportHints(reportName), &result); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Message))
	for _, m := range result.Message {
		names = append(names, m.Name)
	}
	return names, nil
}

// makePrepared queues a new Prepared Report and returns its name.
func (c *FrappeClient) makePrepared(ctx context.Context, reportName, filtersJSON string) (string, error) {
	var result struct {
		Message struct {
			Name string `json:"name"`
		} `json:"message"`
	}
	body := map[string]interface{}{"report_name": reportName, "filters": filtersJSON}
	if err := c.do(ctx, http.MethodPost, "/api/method/frappe.core.doctype.prepared_report.prepared_report.make_prepared_report", body, nil, reportHints(reportName), &result); err != nil {
		return "", err
	}
	if result.Message.Name == "" {
		return "", fmt.Errorf("unexpected response: make_prepared_report returned no name")
	}
	return result.Message.Name, nil
}

// checkPrepared refuses a Prepared Report made for another report or other
// filters: run would return its result anyway (it only checks the owner),
// and the wait, which looks for the job among those for our filters, would
// not see it. When the user may not read Prepared Report documents the
// check is skipped.
func (c *FrappeClient) checkPrepared(ctx context.Context, reportName, filtersJSON, name string) error {
	var pr dataEnvelope
	if c.do(ctx, http.MethodGet, resourcePath("Prepared Report", name), nil, nil, nil, &pr) != nil {
		return nil
	}
	if r, _ := pr.Data["report_name"].(string); r != reportName {
		return &StateError{Message: fmt.Sprintf("prepared report %s is for report %q, not %q", name, r, reportName)}
	}
	var got, want interface{}
	stored, _ := pr.Data["filters"].(string)
	if json.Unmarshal([]byte(stored), &got) != nil || json.Unmarshal([]byte(filtersJSON), &want) != nil || !reflect.DeepEqual(got, want) {
		return &StateError{Message: fmt.Sprintf("prepared report %s was made with the filters %s; pass the same --filters", name, stored)}
	}
	return nil
}

// lastLine keeps the last non-empty line of a Prepared Report's
// error_message (Frappe stores the whole traceback), at most 300 runes.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := []rune(strings.TrimSpace(lines[len(lines)-1]))
	if len(l) > 300 {
		l = append(l[:299], '…')
	}
	return string(l)
}
