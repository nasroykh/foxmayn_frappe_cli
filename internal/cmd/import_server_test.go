package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// importServerTSite is a site with a Ticket DocType and Data Import
// scripted by run; status checks are not paused.
func importServerTSite(t *testing.T, run frappetest.DataImportRun) *frappetest.Site {
	t.Helper()
	old := client.DataImportPollStart
	client.DataImportPollStart = time.Millisecond
	t.Cleanup(func() { client.DataImportPollStart = old })
	s := frappetest.New(t)
	s.AddDocType("Ticket", "subject")
	s.DataImports(run)
	return s
}

// importLog is a Data Import Log as Frappe stores it (create_import_log):
// row_indexes and messages are JSON strings, a success has json.dumps("[]").
func importLog(ok bool, docname string, rows string, msg string) map[string]interface{} {
	l := map[string]interface{}{"success": 0, "docname": nil, "row_indexes": rows, "messages": `"[]"`, "exception": nil}
	if ok {
		l["success"], l["docname"] = 1, docname
		return l
	}
	m, _ := json.Marshal([]map[string]interface{}{{"message": msg, "title": "Message", "indicator": "red", "raise_exception": 1}})
	l["messages"] = string(m)
	l["exception"] = "Traceback (most recent call last):\n  File \"x.py\"\nfrappe.exceptions.ValidationError: " + msg
	return l
}

var dataImportNameRE = regexp.MustCompile(`Data-Import-\d+`)

func decodeImportServer(t *testing.T, r cliResult) dataImportResult {
	t.Helper()
	var out dataImportResult
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("exit %d: %v\nstdout: %s\nstderr: %s", r.Code, err, r.Stdout, r.Stderr)
	}
	return out
}

func TestImportServerSuccess(t *testing.T) {
	s := importServerTSite(t, frappetest.DataImportRun{Polls: 3, Logs: []map[string]interface{}{
		importLog(true, "T-1", "[2, 3]", ""), importLog(true, "T-2", "[4]", ""),
	}})
	csv := "subject,lines.item\nA,x\n,y\nB,z\n"
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "t.csv", csv), "--mode", "insert", "--server", "--submit", "--mute-emails")
	if r.Code != exitOK {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	out := decodeImportServer(t, r)
	if out.Status != "Success" || out.Success != 2 || out.Failed != 0 || out.Total != 2 || out.Doctype != "Ticket" || len(out.Rows) != 2 {
		t.Fatalf("result %+v", out)
	}
	if got := out.Rows[0]; got.Name != "T-1" || got.Status != "created" || fmt.Sprint(got.Rows) != "[2 3]" {
		t.Errorf("row %+v", got)
	}
	di, ok := s.Doc("Data Import", out.DataImport)
	if !ok {
		t.Fatalf("Data Import %q not kept", out.DataImport)
	}
	if di["import_type"] != "Insert New Records" || fmt.Sprint(di["submit_after_import"]) != "1" || fmt.Sprint(di["mute_emails"]) != "1" || di["reference_doctype"] != "Ticket" {
		t.Errorf("Data Import %v", di)
	}
	content, ok := s.File(fmt.Sprint(di["import_file"]))
	if !ok || string(content) != csv || !strings.HasPrefix(fmt.Sprint(di["import_file"]), "/private/files/t") {
		t.Errorf("import_file %v: %q", di["import_file"], content)
	}
	if !s.DataImportJob(out.DataImport) {
		t.Error("job not started")
	}
	// The upload is attached to the Data Import's import_file field.
	up := s.RequestsTo(http.MethodPost, "/api/method/upload_file")
	if len(up) != 1 || !strings.Contains(up[0].Body, "import_file") || !strings.Contains(up[0].Body, out.DataImport) {
		t.Errorf("upload %v", up)
	}

	// The table: rows, names, status; the summary on stderr.
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.csv", csv), "--mode", "update", "--server")
	if r.Code != exitOK || !strings.Contains(r.Stdout, "2-3") || !strings.Contains(r.Stdout, "updated") || !strings.Contains(r.Stderr, "2 of 2 documents updated") {
		t.Errorf("exit %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
}

func TestImportServerPartialFailure(t *testing.T) {
	s := importServerTSite(t, frappetest.DataImportRun{Polls: 2, Logs: []map[string]interface{}{
		importLog(true, "T-1", "[2]", ""), importLog(false, "", "[3]", "<b>Status</b> cannot be &quot;Bogus&quot;"),
	}})
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "t.csv", "subject\nA\nB\n"), "--mode", "insert", "--server")
	if r.Code != exitPartial {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	out := decodeImportServer(t, r)
	if out.Status != "Partial Success" || out.Success != 1 || out.Failed != 1 {
		t.Fatalf("result %+v", out)
	}
	if got := out.Rows[1]; got.Status != "failed" || got.Message != `Status cannot be "Bogus"` || got.Name != "" {
		t.Errorf("failed row %+v", got)
	}

	// v16.51: "In Progress" while running, "Partial Success" is final even
	// when not every document has a log (skipped rows).
	s = importServerTSite(t, frappetest.DataImportRun{Polls: 3, InProgress: true, Payloads: 3, Status: "Partial Success", Logs: []map[string]interface{}{
		importLog(true, "T-1", "[2]", ""), importLog(false, "", "[3]", "bad"),
	}})
	r = cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.csv", "subject\nA\nB\nC\n"), "--mode", "insert", "--server")
	if r.Code != exitPartial || !strings.Contains(r.Stdout, "bad") || !strings.Contains(r.Stderr, "1 failed") {
		t.Errorf("exit %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
}

func TestImportServerJobFailed(t *testing.T) {
	// The job itself failed (status Error, no logs): exit 6.
	s := importServerTSite(t, frappetest.DataImportRun{Polls: 1, Payloads: 2, Status: "Error"})
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.csv", "subject\nA\nB\n"), "--mode", "insert", "--server")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, "the import job failed") {
		t.Errorf("exit %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	// Every document failed: per-document failures, exit 8.
	s = importServerTSite(t, frappetest.DataImportRun{Polls: 1, Logs: []map[string]interface{}{importLog(false, "", "[2]", "nope")}})
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "t.csv", "subject\nA\n"), "--mode", "insert", "--server")
	if out := decodeImportServer(t, r); r.Code != exitPartial || out.Status != "Error" || out.Failed != 1 {
		t.Errorf("exit %d: %+v", r.Code, out)
	}
}

func TestImportServerWarnings(t *testing.T) {
	warnings := []map[string]interface{}{
		{"col": 3, "message": "Skipping column <b>Notes</b>", "type": "info"},
		{"row": 3, "message": "Value must be one of Open, Closed", "field": "status"},
	}
	s := importServerTSite(t, frappetest.DataImportRun{Warnings: warnings, Logs: []map[string]interface{}{importLog(true, "T-1", "[2]", "")}})
	file := writeImportFile(t, "t.csv", "subject,status\nA,Bogus\n")
	r := cmdTRun(t, s, "import", "-d", "Ticket", file, "--mode", "insert", "--server")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, "row 3: Value must be one of Open, Closed") ||
		!strings.Contains(r.Stderr, "note: column 3: Skipping column Notes") || !strings.Contains(r.Stderr, "nothing was imported") {
		t.Errorf("exit %d\n%s", r.Code, r.Stderr)
	}
	if n := s.Count("Data Import"); n != 0 {
		t.Errorf("%d Data Imports left behind", n)
	}
	if len(s.RequestsTo(http.MethodPost, "/api/method/frappe.core.doctype.data_import.data_import.form_start_import")) != 0 {
		t.Error("an import with warnings was started")
	}

	// --preview keeps the Data Import; machine output lists the warnings.
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", file, "--mode", "insert", "--server", "--preview")
	var out struct {
		DataImport string                     `json:"data_import"`
		Status     string                     `json:"status"`
		Warnings   []client.DataImportWarning `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || r.Code != exitValidation || out.Status != "Blocked" || len(out.Warnings) != 1 || out.Warnings[0].Row != 3 {
		t.Fatalf("exit %d %v: %s\n%s", r.Code, err, r.Stdout, r.Stderr)
	}
	if _, ok := s.Doc("Data Import", out.DataImport); !ok {
		t.Error("--preview deleted the Data Import")
	}

	// Warnings only the worker finds stop the run (status stays Pending).
	s = importServerTSite(t, frappetest.DataImportRun{Polls: 2, RunWarnings: warnings[1:], Logs: []map[string]interface{}{importLog(true, "T-1", "[2]", "")}})
	r = cmdTRun(t, s, "--json", "import", "-d", "Ticket", file, "--mode", "insert", "--server")
	if r.Code != exitValidation || !strings.Contains(r.Stdout, `"Blocked"`) || !strings.Contains(r.Stderr, "nothing was imported") {
		t.Errorf("exit %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	name := dataImportNameRE.FindString(r.Stderr)
	// Resuming it does not start it again.
	r = cmdTRun(t, s, "import", "--resume", name)
	if r.Code != exitValidation || !strings.Contains(r.Stderr, "Fix the file") {
		t.Errorf("resume: exit %d\n%s", r.Code, r.Stderr)
	}
}

func TestImportServerTemplateError(t *testing.T) {
	s := importServerTSite(t, frappetest.DataImportRun{TemplateError: "Import template should contain a Header and atleast one row."})
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.csv", "subject\n"), "--mode", "insert", "--server")
	if r.Code != exitValidation || !strings.Contains(r.Stderr, "atleast one row") {
		t.Errorf("exit %d\n%s", r.Code, r.Stderr)
	}
	if n := s.Count("Data Import"); n != 0 {
		t.Errorf("%d Data Imports left behind", n)
	}
}

func TestImportServerPreviewResume(t *testing.T) {
	s := importServerTSite(t, frappetest.DataImportRun{Polls: 2, Rows: 12, Logs: []map[string]interface{}{
		importLog(true, "T-1", "[2]", ""), importLog(true, "T-2", "[3]", ""),
	}})
	r := cmdTRun(t, s, "--json", "import", "-d", "Ticket", writeImportFile(t, "t.xlsx", "PK\x03\x04"), "--mode", "insert", "--server", "--preview")
	var pv map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &pv); err != nil || r.Code != exitOK {
		t.Fatalf("exit %d %v\n%s\n%s", r.Code, err, r.Stdout, r.Stderr)
	}
	name := fmt.Sprint(pv["data_import"])
	if pv["preview"] != true || fmt.Sprint(pv["rows"]) != "12" || fmt.Sprint(pv["documents"]) != "2" || pv["resume"] != "ffc import --server --resume "+name {
		t.Errorf("preview %v", pv)
	}
	if s.DataImportJob(name) {
		t.Error("--preview started the import")
	}
	di, _ := s.Doc("Data Import", name)
	if !strings.HasSuffix(fmt.Sprint(di["import_file"]), "/t.xlsx") {
		t.Errorf("import_file %v", di["import_file"])
	}

	// -d must match; then --resume starts it and waits.
	if r := cmdTRun(t, s, "import", "-d", "ToDo", "--resume", name); r.Code != exitValidation {
		t.Errorf("-d mismatch: exit %d %s", r.Code, r.Stderr)
	}
	r = cmdTRun(t, s, "--json", "import", "--server", "--resume", name)
	if out := decodeImportServer(t, r); r.Code != exitOK || out.Success != 2 || out.Status != "Success" || !s.DataImportJob(name) {
		t.Errorf("exit %d: %+v\n%s", r.Code, out, r.Stderr)
	}
	// A finished import is only reported: nothing is started.
	starts := len(s.RequestsTo(http.MethodPost, "/api/method/frappe.core.doctype.data_import.data_import.form_start_import"))
	r = cmdTRun(t, s, "--json", "import", "--resume", name)
	if out := decodeImportServer(t, r); r.Code != exitOK || out.Success != 2 {
		t.Errorf("exit %d: %+v", r.Code, out)
	}
	if n := len(s.RequestsTo(http.MethodPost, "/api/method/frappe.core.doctype.data_import.data_import.form_start_import")); n != starts {
		t.Errorf("a finished import was started again (%d starts)", n)
	}
}

func TestImportServerWaitTimeout(t *testing.T) {
	// No worker: the wait runs out (exit 7) with the resume command.
	s := importServerTSite(t, frappetest.DataImportRun{Polls: -1, Logs: []map[string]interface{}{importLog(true, "T-1", "[2]", "")}})
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.csv", "subject\nA\n"), "--mode", "insert", "--server", "--wait", "1s")
	name := dataImportNameRE.FindString(r.Stderr)
	if r.Code != exitNetwork || name == "" || !strings.Contains(r.Stderr, "ffc import --server --resume "+name) || !strings.Contains(r.Stderr, `"default" queue`) {
		t.Fatalf("exit %d\n%s", r.Code, r.Stderr)
	}
	// A worker comes up: --resume does not queue it twice, and waits.
	s.DataImports(frappetest.DataImportRun{Polls: 2, Logs: []map[string]interface{}{importLog(true, "T-1", "[2]", "")}})
	r = cmdTRun(t, s, "--json", "import", "--server", "--resume", name)
	if out := decodeImportServer(t, r); r.Code != exitOK || out.Success != 1 {
		t.Errorf("exit %d: %+v\n%s", r.Code, out, r.Stderr)
	}
}

func TestImportServerSchedulerInactive(t *testing.T) {
	s := importServerTSite(t, frappetest.DataImportRun{SchedulerInactive: true, Logs: []map[string]interface{}{importLog(true, "T-1", "[2]", "")}})
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.csv", "subject\nA\n"), "--mode", "insert", "--server")
	name := dataImportNameRE.FindString(r.Stderr)
	if r.Code != exitValidation || !strings.Contains(r.Stderr, "scheduler is inactive") || !strings.Contains(r.Stderr, "--resume "+name) || name == "" {
		t.Fatalf("exit %d\n%s", r.Code, r.Stderr)
	}
	if _, ok := s.Doc("Data Import", name); !ok {
		t.Error("the Data Import ready to start was deleted")
	}
}

func TestImportServerPermission(t *testing.T) {
	s := importServerTSite(t, frappetest.DataImportRun{})
	s.Handle("POST /api/resource/Data Import", frappetest.ErrorHandler(frappetest.Permission("No permission for Data Import")))
	r := cmdTRun(t, s, "import", "-d", "Ticket", writeImportFile(t, "t.csv", "subject\nA\n"), "--mode", "insert", "--server")
	if r.Code != exitPermission || !strings.Contains(r.Stderr, "System Manager") {
		t.Errorf("exit %d\n%s", r.Code, r.Stderr)
	}
	if len(s.RequestsTo(http.MethodPost, "/api/method/upload_file")) != 0 {
		t.Error("uploaded without a Data Import")
	}
}

func TestImportServerUsage(t *testing.T) {
	s := importServerTSite(t, frappetest.DataImportRun{})
	csv := writeImportFile(t, "t.csv", "subject\nA\n")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-d", "Ticket", csv, "--mode", "insert", "--server", "--dry-run"}, "--preview"},
		{[]string{"-d", "Ticket", writeImportFile(t, "t.json", "[]"), "--mode", "insert", "--server"}, "without --server"},
		{[]string{"-d", "Ticket", writeImportFile(t, "t.tsv", "subject\n"), "--mode", "insert", "--server"}, "CSV"},
		{[]string{"-d", "Ticket", writeImportFile(t, "t.xls", "x"), "--mode", "insert", "--server"}, ".xlsx"},
		{[]string{"-d", "Ticket", csv, "--mode", "insert", "--preview"}, "--preview needs --server"},
		{[]string{"-d", "Ticket", csv, "--mode", "insert", "--server", "--concurrency", "2"}, "--concurrency"},
		{[]string{"-d", "Ticket", csv, "--mode", "insert", "--server", "--wait", "10ms"}, "--wait"},
		{[]string{"-d", "Ticket", csv, "--resume", "DI-1"}, "give no FILE"},
		{[]string{"--resume", "DI-1", "--mode", "insert"}, "--mode cannot be used with --resume"},
		{[]string{"-d", "Ticket", csv, "--server"}, `"mode"`},
		{[]string{csv, "--mode", "insert", "--server"}, `"doctype"`},
		{[]string{"-d", "Ticket", writeImportFile(t, "t.xlsx", "PK"), "--mode", "insert"}, "add --server"},
	} {
		r := cmdTRun(t, s, append([]string{"import"}, c.args...)...)
		if r.Code != exitUsage || !strings.Contains(r.Stderr, c.want) {
			t.Errorf("%v: exit %d, want 2 and %q\n%s", c.args, r.Code, c.want, r.Stderr)
		}
	}
	if n := s.Count("Data Import"); n != 0 {
		t.Errorf("a usage error created %d Data Imports", n)
	}
}
