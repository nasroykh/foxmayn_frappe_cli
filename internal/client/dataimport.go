package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Data Import (Frappe v15 and v16, frappe/core/doctype/data_import): the
// desk's importer, run on the site. A Data Import document names the
// DocType and the import type; its import_file (an Attach field) is a File
// uploaded to it, and saving the field parses the file (validate). The
// preview reports the parser's warnings; form_start_import enqueues the
// import on the "default" queue (job id data_import||<name>, not enqueued
// twice) and refuses while the scheduler is inactive, unless the site is in
// developer mode (then it runs inside the request). The worker writes one
// Data Import Log per document and sets the status. Only System Manager may
// create or read Data Imports.

// Data Import's import_type values for ffc's --mode.
const (
	DataImportInsert = "Insert New Records"
	DataImportUpdate = "Update Existing Records"
)

const dataImportDoctype = "Data Import"

const dataImportMethods = "/api/method/frappe.core.doctype.data_import.data_import."

// dataImportHints names the role Data Import needs on a 403.
func dataImportHints(doctype, name string) map[int]string {
	return map[int]string{
		http.StatusUnauthorized: authHint,
		http.StatusForbidden: fmt.Sprintf("permission denied (403): Frappe's Data Import needs the System Manager role, and import permission on %s; "+
			"without them, import without --server", doctype),
		http.StatusNotFound: fmt.Sprintf("Data Import %q not found (404)", name),
	}
}

// NewDataImport creates a Data Import for doctype and returns it. Frappe
// refuses (417) a DocType without "Allow Import" or a core DocType.
func (c *FrappeClient) NewDataImport(ctx context.Context, doctype, importType string, submit, muteEmails bool) (map[string]interface{}, error) {
	data := map[string]interface{}{
		"reference_doctype":   doctype,
		"import_type":         importType,
		"submit_after_import": boolInt(submit),
		"mute_emails":         boolInt(muteEmails),
	}
	var env dataEnvelope
	if err := c.do(ctx, http.MethodPost, resourcePath(dataImportDoctype), data, nil, dataImportHints(doctype, ""), &env); err != nil {
		return nil, err
	}
	return env.doc()
}

// GetDataImport reads a Data Import.
func (c *FrappeClient) GetDataImport(ctx context.Context, name string) (map[string]interface{}, error) {
	var env dataEnvelope
	if err := c.do(ctx, http.MethodGet, resourcePath(dataImportDoctype, name), nil, nil, dataImportHints("the DocType", name), &env); err != nil {
		return nil, err
	}
	return env.doc()
}

// SetDataImportFile points the Data Import at an uploaded file. Frappe
// parses the file while saving: a file it cannot read as a template fails
// here (417), and payload_count of the answer is the number of documents.
func (c *FrappeClient) SetDataImportFile(ctx context.Context, name, fileURL string) (map[string]interface{}, error) {
	var env dataEnvelope
	if err := c.do(ctx, http.MethodPut, resourcePath(dataImportDoctype, name), map[string]interface{}{"import_file": fileURL},
		nil, dataImportHints("the DocType", name), &env); err != nil {
		return nil, err
	}
	return env.doc()
}

// DataImportWarning is one of the importer's warnings about the file. Only
// Type "info" (a skipped column, a guessed date format) lets the import run.
type DataImportWarning struct {
	Row     int    `json:"row,omitempty"`
	Rows    []int  `json:"rows,omitempty"`
	Col     int    `json:"col,omitempty"`
	Type    string `json:"type,omitempty"`
	Message string `json:"message"`
}

// Blocking reports whether the warning stops the import (importer.py
// import_data: every type but "info").
func (w DataImportWarning) Blocking() bool { return w.Type != "info" }

// DataImportPreview is get_preview_from_template's answer, reduced to what
// ffc shows: the number of file rows (not documents) and the warnings.
type DataImportPreview struct {
	Rows     int
	Warnings []DataImportWarning
}

// DataImportPreview parses the Data Import's file again and returns the
// warnings Frappe would act on. The preview's data is cut to 10 rows;
// total_number_of_rows is sent when it was cut (always from v16.51).
func (c *FrappeClient) DataImportPreview(ctx context.Context, name string) (*DataImportPreview, error) {
	var res struct {
		Message struct {
			Data      []json.RawMessage `json:"data"`
			Warnings  []interface{}     `json:"warnings"`
			TotalRows json.Number       `json:"total_number_of_rows"`
		} `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, dataImportMethods+"get_preview_from_template", nil,
		map[string]string{"data_import": name}, dataImportHints("the DocType", name), &res); err != nil {
		return nil, err
	}
	p := &DataImportPreview{Rows: len(res.Message.Data), Warnings: dataImportWarnings(res.Message.Warnings)}
	if n, err := res.Message.TotalRows.Int64(); err == nil && n > 0 {
		p.Rows = int(n)
	}
	return p, nil
}

// DataImportWarnings decodes a template_warnings value (a JSON string).
func DataImportWarnings(stored string) []DataImportWarning {
	var v []interface{}
	if json.Unmarshal([]byte(stored), &v) != nil {
		if strings.TrimSpace(stored) == "" {
			return nil
		}
		return []DataImportWarning{{Message: htmlText(stored)}}
	}
	return dataImportWarnings(v)
}

func dataImportWarnings(raw []interface{}) []DataImportWarning {
	out := make([]DataImportWarning, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			out = append(out, DataImportWarning{Message: htmlText(fmt.Sprint(item))})
			continue
		}
		w := DataImportWarning{Row: intOf(m["row"]), Col: intOf(m["col"]), Message: htmlText(fmt.Sprint(m["message"]))}
		w.Type, _ = m["type"].(string)
		if rows, ok := m["rows"].([]interface{}); ok {
			for _, r := range rows {
				w.Rows = append(w.Rows, intOf(r))
			}
		}
		out = append(out, w)
	}
	return out
}

// intOf reads a JSON number (json.Number, float64 or a numeric string).
func intOf(v interface{}) int {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			f, _ := n.Float64()
			return int(f)
		}
		return int(i)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

// ErrSchedulerInactive is form_start_import's refusal while the site's
// scheduler is inactive (disabled, paused or in maintenance mode).
var ErrSchedulerInactive = errors.New("the site's scheduler is inactive, so Frappe does not start a Data Import")

// StartDataImport enqueues the import. It returns false when the job was
// already queued or running (Frappe does not enqueue it twice). A refusal
// is checked against the scheduler status (the message may be translated):
// when it is inactive the error wraps ErrSchedulerInactive.
func (c *FrappeClient) StartDataImport(ctx context.Context, name string) (bool, error) {
	var res struct {
		Message interface{} `json:"message"`
	}
	err := c.do(ctx, http.MethodPost, dataImportMethods+"form_start_import", map[string]interface{}{"data_import": name},
		nil, dataImportHints("the DocType", name), &res)
	var api *APIError
	if errors.As(err, &api) && api.Status == http.StatusExpectationFailed && c.schedulerInactive(ctx) {
		return false, &StateError{Message: fmt.Sprintf("%v (%s)", ErrSchedulerInactive, api.Message)}
	}
	if err != nil {
		return false, err
	}
	started, _ := res.Message.(bool)
	return started, nil
}

// IsSchedulerInactive reports whether err is StartDataImport's refusal
// for an inactive scheduler.
func IsSchedulerInactive(err error) bool {
	var st *StateError
	return errors.As(err, &st) && strings.HasPrefix(st.Message, ErrSchedulerInactive.Error())
}

// schedulerInactive asks frappe.utils.scheduler.get_scheduler_status
// (whitelisted, no role needed).
func (c *FrappeClient) schedulerInactive(ctx context.Context) bool {
	var res struct {
		Message struct {
			Status string `json:"status"`
		} `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/method/frappe.utils.scheduler.get_scheduler_status", nil, nil, nil, &res); err != nil {
		return false
	}
	return res.Message.Status == "inactive"
}

// DataImportStatus is get_import_status's answer plus, when Frappe stopped
// the run on the file's warnings, those warnings.
type DataImportStatus struct {
	Status  string `json:"status"`
	Success int    `json:"success"`
	Failed  int    `json:"failed"`
	Total   int    `json:"total_records"`
	// Processed is only sent by releases that mark a running import "In
	// Progress" (v16.51); there "Partial Success" is a final status.
	Processed *int `json:"processed_records,omitempty"`
	// Blocked: the worker found warnings and imported nothing (status stays
	// Pending, template_warnings holds them).
	Blocked  bool                `json:"-"`
	Warnings []DataImportWarning `json:"-"`
}

// DataImportStatus reads the status and the log counts.
func (c *FrappeClient) DataImportStatus(ctx context.Context, name string) (DataImportStatus, error) {
	var res struct {
		Message DataImportStatus `json:"message"`
	}
	err := c.do(ctx, http.MethodGet, dataImportMethods+"get_import_status", nil,
		map[string]string{"data_import_name": name}, dataImportHints("the DocType", name), &res)
	return res.Message, err
}

// finished reports whether the run is over. Success, Error and Timed Out
// are set only at the end. "Partial Success" is also set during the run on
// releases before "In Progress" existed (after the first imported
// document; importer.py), so there the counts decide: every document has a
// log. A run blocked by warnings is checked by the caller (status Pending).
func (s DataImportStatus) finished() bool {
	switch s.Status {
	case "Success", "Error", "Timed Out":
		return true
	case "Partial Success":
		if s.Processed != nil {
			return true
		}
	}
	return s.Total > 0 && s.Success+s.Failed >= s.Total && s.Status != "In Progress" && s.Status != "Pending"
}

// Final is the run's status: the site's when it has set a final one, else
// derived from the counts (a v15 site may still read "Partial Success" or
// "Pending" while it writes the last logs).
func (s DataImportStatus) Final() string {
	switch {
	case s.Blocked:
		return "Blocked"
	case s.Status == "Success", s.Status == "Error", s.Status == "Timed Out":
		return s.Status
	case s.Total > 0 && s.Success+s.Failed >= s.Total:
		switch {
		case s.Failed == 0:
			return "Success"
		case s.Success == 0:
			return "Error"
		}
		return "Partial Success"
	}
	return s.Status
}

// DataImportPollStart is the first pause between two status checks; it
// doubles up to PreparedPollStart's maximum (5 s). Tests lower it.
var DataImportPollStart = time.Second

// DataImportWaitError is a Data Import still queued or running when the
// wait ran out.
type DataImportWaitError struct {
	Name   string
	Waited time.Duration
	Status DataImportStatus
}

func (e *DataImportWaitError) Error() string {
	done := ""
	if e.Status.Total > 0 && e.Status.Success+e.Status.Failed > 0 {
		done = fmt.Sprintf(", %d of %d documents done", e.Status.Success+e.Status.Failed, e.Status.Total)
	}
	if e.Status.Status == "Pending" && done != "" {
		// importer.py also ends a run Pending when some documents have no
		// log and either none failed or none succeeded; it then stays so.
		return fmt.Sprintf("Data Import %s still reads Pending after %s%s: either it is still running, or Frappe finished it without logging every document (check it in the desk)",
			e.Name, e.Waited, done)
	}
	return fmt.Sprintf("Data Import %s is still queued or running after %s (status %s%s)", e.Name, e.Waited, e.Status.Status, done)
}

// WaitDataImport polls the status until the run is over or wait runs out
// (*DataImportWaitError). While the status is Pending without logs it also
// reads template_warnings: a run blocked by them never leaves Pending.
// onWait, when set, is called with each status read.
func (c *FrappeClient) WaitDataImport(ctx context.Context, name string, wait time.Duration, onWait func(DataImportStatus)) (DataImportStatus, error) {
	start := time.Now()
	pause := DataImportPollStart
	for {
		st, err := c.DataImportStatus(ctx, name)
		if err != nil {
			return st, err
		}
		if onWait != nil {
			onWait(st)
		}
		if st.finished() {
			return st, nil
		}
		if (st.Status == "Pending" || st.Status == "") && st.Success+st.Failed == 0 {
			doc, err := c.GetDataImport(ctx, name)
			if err != nil {
				return st, err
			}
			if tw, _ := doc["template_warnings"].(string); strings.TrimSpace(tw) != "" {
				st.Blocked, st.Warnings = true, DataImportWarnings(tw)
				return st, nil
			}
		}
		left := wait - time.Since(start)
		if left <= 0 {
			return st, &DataImportWaitError{Name: name, Waited: wait, Status: st}
		}
		t := time.NewTimer(min(pause, left))
		select {
		case <-ctx.Done():
			t.Stop()
			return st, fmt.Errorf("stopped waiting for Data Import %s, which keeps running (ffc import --server --resume %s picks it up): %w", name, name, ctx.Err())
		case <-t.C:
		}
		pause = min(pause*2, preparedPollMax)
	}
}

// DataImportLog is one Data Import Log: the outcome of one document.
type DataImportLog struct {
	Rows     []int    `json:"rows"` // file rows of the document (1 = the header)
	Success  bool     `json:"success"`
	Docname  string   `json:"docname,omitempty"`
	Messages []string `json:"messages,omitempty"`
	// Exception is the last line of the traceback of a failure.
	Exception string `json:"exception,omitempty"`
}

// DataImportLogs reads the run's logs in order (get_import_logs: at most
// 5000 rows before v16.51, 1000 from it).
func (c *FrappeClient) DataImportLogs(ctx context.Context, name string) ([]DataImportLog, error) {
	var res struct {
		Message []map[string]interface{} `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, dataImportMethods+"get_import_logs", nil,
		map[string]string{"data_import": name}, dataImportHints("the DocType", name), &res); err != nil {
		return nil, err
	}
	out := make([]DataImportLog, 0, len(res.Message))
	for _, m := range res.Message {
		l := DataImportLog{Success: intOf(m["success"]) == 1 || m["success"] == true}
		l.Docname, _ = m["docname"].(string)
		if rows, _ := m["row_indexes"].(string); rows != "" {
			var idx []interface{}
			if json.Unmarshal([]byte(rows), &idx) == nil {
				for _, r := range idx {
					l.Rows = append(l.Rows, intOf(r))
				}
			}
		}
		sort.Ints(l.Rows)
		l.Messages = logMessages(m["messages"])
		if exc, _ := m["exception"].(string); strings.TrimSpace(exc) != "" {
			l.Exception = lastLine(exc)
		}
		out = append(out, l)
	}
	return out, nil
}

// logMessages decodes a Data Import Log's messages: json.dumps of
// frappe.local.message_log (msgprint dicts, or older JSON-encoded dicts);
// a log without messages stores json.dumps("[]"), a JSON string.
func logMessages(v interface{}) []string {
	var out []string
	var walk func(v interface{}, depth int)
	walk = func(v interface{}, depth int) {
		if depth > 3 {
			return
		}
		switch x := v.(type) {
		case string:
			var inner interface{}
			if t := strings.TrimSpace(x); t != "" && (t[0] == '[' || t[0] == '{' || t[0] == '"') && json.Unmarshal([]byte(t), &inner) == nil {
				walk(inner, depth+1)
				return
			}
			if s := htmlText(x); s != "" {
				out = append(out, s)
			}
		case []interface{}:
			for _, e := range x {
				walk(e, depth+1)
			}
		case map[string]interface{}:
			if msg, ok := x["message"]; ok {
				walk(msg, depth+1)
			}
		}
	}
	walk(v, 0)
	return out
}

// htmlBreakRE matches the tags that end a line in Frappe's messages
// (value_mapping joins its lines with <br>).
var htmlBreakRE = regexp.MustCompile(`(?i)(\s*(<br\s*/?>|</p>|</li>|</div>)\s*)+`)

// htmlText is stripHTML with line breaks kept apart as "; ".
func htmlText(s string) string {
	s = strings.Trim(strings.TrimSpace(htmlBreakRE.ReplaceAllString(s, "; ")), "; ")
	return stripHTML(s)
}
