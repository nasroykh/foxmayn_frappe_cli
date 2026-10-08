package frappetest

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Data Import, as Frappe v15/v16 run it (core/doctype/data_import). A Data
// Import is a document of that DocType; saving import_file parses the file
// (TemplateError fails it, else payload_count is set), the preview answers
// the scripted warnings, form_start_import queues the job, and the
// simulated worker runs during get_import_status: each call counts the
// job down, and when it reaches zero the run either stops on blocking
// warnings (template_warnings set, status Pending) or writes the scripted
// logs and sets the status.

// saveHook checks a create (doc nil, patch the new document) or an update
// (the stored doc and the patch) and may add to patch; the caller holds
// s.mu.
type saveHook func(doc, patch map[string]interface{}) *Error

// DataImportRun scripts every Data Import of the site.
type DataImportRun struct {
	// Rows is the number of data rows of the file (the preview's count);
	// Payloads the number of documents (payload_count). Both default to
	// len(Logs).
	Rows, Payloads int
	// Warnings are the parser's warnings: the preview's, and the
	// template_warnings of a run that they block (any type but "info").
	Warnings []map[string]interface{}
	// RunWarnings are blocking warnings only the worker finds (v16.51
	// checks more than the preview shows).
	RunWarnings []map[string]interface{}
	// TemplateError makes saving import_file fail with this
	// ValidationError, as an unreadable template does.
	TemplateError string
	// SchedulerInactive makes form_start_import refuse and
	// get_scheduler_status answer inactive.
	SchedulerInactive bool
	// Polls is the number of get_import_status calls the job lasts (0 and
	// 1: it is done at the first); < 0: no worker, it stays queued.
	Polls int
	// Logs are the Data Import Logs the run writes (success, docname,
	// messages, exception, row_indexes as Frappe stores them).
	Logs []map[string]interface{}
	// Status is the final status; "" derives it from Logs like importer.py.
	Status string
	// InProgress makes the run look like v16.51: status "In Progress"
	// while it runs and processed_records in get_import_status.
	InProgress bool
}

// DataImports enables Data Import on the site, run as run describes.
// Calling it again changes the script for jobs already queued too: one
// waiting for a worker (Polls < 0) gets the new Polls.
func (s *Site) DataImports(run DataImportRun) {
	s.AddDocType("Data Import", "reference_doctype", "import_type", "status", "import_file", "payload_count", "template_warnings")
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.Rows == 0 {
		run.Rows = len(run.Logs)
	}
	if run.Payloads == 0 {
		run.Payloads = len(run.Logs)
	}
	s.dataImport = &run
	if s.saveHooks == nil {
		s.saveHooks = map[string]saveHook{}
	}
	if s.importJobs == nil {
		s.importJobs = map[string]int{}
		s.importLogs = map[string][]map[string]interface{}{}
	}
	for name, left := range s.importJobs {
		if left < 0 && run.Polls >= 0 {
			s.importJobs[name] = max(run.Polls, 1)
		}
	}
	s.saveHooks["Data Import"] = s.saveDataImport
	const m = "frappe.core.doctype.data_import.data_import."
	s.methods[m+"get_preview_from_template"] = s.importPreview
	s.methods[m+"form_start_import"] = s.startImport
	s.methods[m+"get_import_status"] = s.importStatus
	s.methods[m+"get_import_logs"] = s.importLogsMethod
	s.methods["frappe.utils.scheduler.get_scheduler_status"] = func(*http.Request, map[string]interface{}) (interface{}, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.dataImport.SchedulerInactive {
			return map[string]string{"status": "inactive"}, nil
		}
		return map[string]string{"status": "active"}, nil
	}
}

// DataImportJob reports whether a Data Import's job was started.
func (s *Site) DataImportJob(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.importJobs[name]
	return ok
}

func (s *Site) saveDataImport(doc, patch map[string]interface{}) *Error {
	if doc == nil {
		dt, _ := patch["reference_doctype"].(string)
		if _, ok := s.doctypes[dt]; !ok || dt == "" {
			return Validation(fmt.Sprintf("Could not find Document Type: %s", dt))
		}
		setDefault(patch, "status", "Pending")
		return nil
	}
	if f, ok := patch["import_file"].(string); ok && f != "" {
		if s.dataImport.TemplateError != "" {
			return &Error{http.StatusExpectationFailed, "ValidationError", s.dataImport.TemplateError}
		}
		if _, ok := s.files[f]; !ok {
			return Validation("Invalid template file for import")
		}
		patch["payload_count"] = json.Number(fmt.Sprint(s.dataImport.Payloads))
	}
	return nil
}

// dataImportDoc returns the stored Data Import; the caller holds s.mu.
func (s *Site) dataImportDoc(args map[string]interface{}, key string) (map[string]interface{}, error) {
	name, _ := args[key].(string)
	d := s.doctypes["Data Import"][name]
	if d == nil {
		return nil, NotFound(fmt.Sprintf("Data Import %s not found", name))
	}
	return d, nil
}

func (s *Site) importPreview(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.dataImportDoc(args, "data_import")
	if err != nil {
		return nil, err
	}
	if f, _ := d["import_file"].(string); f == "" {
		return nil, nil
	}
	run := s.dataImport
	data := []interface{}{}
	for i := 0; i < run.Rows && i < 10; i++ {
		data = append(data, []interface{}{i + 2})
	}
	out := map[string]interface{}{"data": data, "columns": []interface{}{}, "warnings": run.Warnings}
	if run.Rows > 10 {
		out["max_rows_exceeded"] = true
		out["total_number_of_rows"] = run.Rows
	}
	return out, nil
}

func (s *Site) startImport(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.dataImportDoc(args, "data_import")
	if err != nil {
		return nil, err
	}
	if s.dataImport.SchedulerInactive {
		return nil, Validation("Scheduler is inactive. Cannot import data.")
	}
	name := d["name"].(string)
	if left, ok := s.importJobs[name]; ok && left != 0 {
		return false, nil // queued or running
	}
	s.importJobs[name] = s.dataImport.Polls
	if s.importJobs[name] == 0 {
		s.importJobs[name] = 1
	}
	return true, nil
}

func (s *Site) importStatus(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.dataImportDoc(args, "data_import_name")
	if err != nil {
		return nil, err
	}
	name := d["name"].(string)
	run := s.dataImport
	if left, ok := s.importJobs[name]; ok && left > 0 {
		s.importJobs[name] = left - 1
		if left == 1 {
			s.finishImport(d)
		} else if run.InProgress {
			d["status"] = "In Progress"
		}
	}
	out := map[string]interface{}{"status": d["status"], "total_records": d["payload_count"]}
	for _, l := range s.importLogs[name] {
		k := "failed"
		if truthy(l["success"]) {
			k = "success"
		}
		n, _ := out[k].(int)
		out[k] = n + 1
	}
	if run.InProgress {
		a, _ := out["success"].(int)
		b, _ := out["failed"].(int)
		out["processed_records"] = a + b
	}
	return out, nil
}

// finishImport is the end of importer.py import_data; the caller holds s.mu.
func (s *Site) finishImport(d map[string]interface{}) {
	run := s.dataImport
	var blocking []map[string]interface{}
	for _, w := range append(append([]map[string]interface{}{}, run.Warnings...), run.RunWarnings...) {
		if w["type"] != "info" {
			blocking = append(blocking, w)
		}
	}
	if len(blocking) > 0 {
		b, _ := json.Marshal(blocking)
		d["template_warnings"] = string(b)
		d["status"] = "Pending"
		return
	}
	name := d["name"].(string)
	s.importLogs[name] = run.Logs
	status := run.Status
	if status == "" {
		ok, failed := 0, 0
		for _, l := range run.Logs {
			if truthy(l["success"]) {
				ok++
			} else {
				failed++
			}
		}
		switch {
		case failed >= run.Payloads && ok == 0:
			status = "Error"
		case failed > 0 && ok > 0:
			status = "Partial Success"
		case ok == run.Payloads:
			status = "Success"
		default:
			status = "Pending"
		}
	}
	d["status"] = status
}

func (s *Site) importLogsMethod(_ *http.Request, args map[string]interface{}) (interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.dataImportDoc(args, "data_import")
	if err != nil {
		return nil, err
	}
	logs := s.importLogs[d["name"].(string)]
	if logs == nil {
		logs = []map[string]interface{}{}
	}
	return logs, nil
}
