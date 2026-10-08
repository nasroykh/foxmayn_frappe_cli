package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// import --server (T3.1): the file goes to Frappe's own Data Import, which
// parses, converts and writes it in a background job on the site. ffc
// creates the Data Import, uploads the file to it, checks the preview's
// warnings, starts the job, waits for it and prints the per-document logs.
// Nothing is parsed on the client: CSV and Excel are read by Frappe.

// import --server flags
var (
	imServer     bool
	imPreview    bool
	imMuteEmails bool
	imWait       time.Duration
	imResume     string
)

// importArgs: FILE, except with --resume (the Data Import has the file).
func importArgs(cmd *cobra.Command, args []string) error {
	if imResume != "" {
		if len(args) > 0 {
			return usageErrorf("--resume continues an existing Data Import, which has its file: give no FILE")
		}
		return nil
	}
	return cobra.ExactArgs(1)(cmd, args)
}

// checkImportServerFlags refuses flags that do not belong to the engine in
// use, before anything is read or sent.
func checkImportServerFlags(cmd *cobra.Command) error {
	changed := cmd.Flags().Changed
	if !imServer && imResume == "" {
		for _, f := range []string{"preview", "wait", "mute-emails"} {
			if changed(f) {
				return usageErrorf("--%s needs --server", f)
			}
		}
		return nil
	}
	if dryRunOn(cmd) {
		return usageErrorf("--dry-run cannot be used with --server: creating the Data Import on the site is already a write. " +
			"Use --preview to have Frappe check the file without importing it, or drop --server for a client-side dry run")
	}
	for _, f := range []string{"concurrency", "fail-fast"} {
		if changed(f) {
			return usageErrorf("--%s applies only without --server (Frappe's Data Import writes one document at a time)", f)
		}
	}
	if imWait < time.Second {
		return usageErrorf("--wait must be at least 1s, got %s", imWait)
	}
	if imResume != "" {
		for _, f := range []string{"mode", "submit", "mute-emails", "preview", "format"} {
			if changed(f) {
				return usageErrorf("--%s cannot be used with --resume: the Data Import keeps the settings it was created with", f)
			}
		}
	}
	return nil
}

// importServerFormat picks csv or xlsx from --format or the extension.
// Frappe's Data Import reads CSV and Excel only.
func importServerFormat(file, flag string) (string, error) {
	f := strings.ToLower(strings.TrimSpace(flag))
	if f == "" {
		switch strings.ToLower(filepath.Ext(file)) {
		case ".xlsx":
			f = "xlsx"
		case ".xls", ".ods":
			f = "xls"
		case ".json":
			f = "json"
		case ".ndjson", ".jsonl":
			f = "ndjson"
		case ".tsv", ".tab":
			f = "tsv"
		default:
			f = "csv"
		}
	}
	switch f {
	case "csv", "xlsx":
		return f, nil
	case "excel":
		return "xlsx", nil
	case "json", "ndjson":
		return "", usageErrorf("--server reads CSV and Excel (.xlsx) files only (Frappe's Data Import): import %s without --server", strings.ToUpper(f))
	case "tsv":
		return "", usageErrorf("--server reads CSV and Excel (.xlsx) files only: export with --output csv (the default), or import without --server")
	case "xls":
		return "", usageErrorf("--server reads CSV and Excel (.xlsx) files only: save the sheet as .xlsx or CSV UTF-8")
	}
	return "", usageErrorf("--format must be csv or xlsx with --server, not %q", flag)
}

// uploadName is the file name sent to the site: Frappe picks the reader
// by its extension.
func uploadName(file, format string) string {
	name := filepath.Base(file)
	if file == "-" {
		name = "import"
	}
	if strings.ToLower(filepath.Ext(name)) != "."+format {
		name += "." + format
	}
	return name
}

// resumeHint is the command that picks a Data Import up again.
func resumeHint(name string) string {
	return "ffc import --server --resume " + shellQuote(name)
}

// dataImportType maps --mode to Data Import's import_type.
func dataImportType(mode string) (string, error) {
	switch strings.ToLower(mode) {
	case "insert":
		return client.DataImportInsert, nil
	case "update":
		return client.DataImportUpdate, nil
	case "":
		return "", usageErrorf(`required flag(s) "mode" not set`)
	}
	return "", usageErrorf("--mode must be insert or update")
}

func runImportServer(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if imResume != "" {
		c, err := newClient(ctx)
		if err != nil {
			return err
		}
		defer c.CloseQuietly()
		return resumeDataImport(cmd, c, imResume)
	}
	file := args[0]
	if file == "" {
		return usageErrorf("FILE is empty: give a path, or - for stdin")
	}
	importType, err := dataImportType(imMode)
	if err != nil {
		return err
	}
	format, err := importServerFormat(file, imFormat)
	if err != nil {
		return err
	}

	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	u := client.FileUpload{Filename: uploadName(file, format), Field: "import_file", Private: true}
	var limit int64
	if spinErr := runSpinner("Reading the site's upload limit…", func() { limit = c.MaxFileSize(ctx) }); spinErr != nil {
		return errAborted
	}
	closeFile, err := openUpload(&u, file, limit)
	defer closeFile()
	if err != nil {
		return err
	}

	// Create the Data Import, upload the file to it, save it (Frappe
	// parses the file) and read the preview's warnings.
	var name string
	var payloads int
	var preview *client.DataImportPreview
	var stepErr error
	spinErr := runSpinner(fmt.Sprintf("Uploading %s to a new Data Import for %s…", u.Filename, imDoctype), func() {
		di, err := c.NewDataImport(ctx, imDoctype, importType, imSubmit, imMuteEmails)
		if err != nil {
			stepErr = err
			return
		}
		name = fmt.Sprint(di["name"])
		u.Doctype, u.Docname = "Data Import", name
		f, err := c.UploadFile(ctx, u)
		if err != nil {
			stepErr = err
			return
		}
		saved, err := c.SetDataImportFile(ctx, name, fmt.Sprint(f["file_url"]))
		if err != nil {
			stepErr = err
			return
		}
		payloads, _ = strconv.Atoi(fmt.Sprint(saved["payload_count"]))
		preview, stepErr = c.DataImportPreview(ctx, name)
	})
	if stepErr == nil && spinErr != nil {
		stepErr = errAborted
	}
	if stepErr != nil {
		if name != "" {
			return discardDataImport(c, name, stepErr)
		}
		return stepErr
	}

	var blocking []client.DataImportWarning
	for _, w := range preview.Warnings {
		if w.Blocking() {
			blocking = append(blocking, w)
		} else if !machineOutput() {
			output.PrintWarning("note: " + warningText(w))
		}
	}
	if len(blocking) > 0 {
		if err := printDataImportWarnings(name, imDoctype, blocking); err != nil {
			return err
		}
		n := len(blocking)
		if imPreview {
			return &client.StateError{Message: fmt.Sprintf("%d %s in %s: Frappe would import nothing; Data Import %s was kept for --preview", n, plural(n, "warning"), file, name)}
		}
		return discardDataImport(c, name, &client.StateError{Message: fmt.Sprintf("%d %s in %s: Frappe imports nothing while they remain; nothing was imported", n, plural(n, "warning"), file)})
	}
	if imPreview {
		return printDataImportPreview(name, imDoctype, preview, payloads)
	}
	return runDataImport(cmd, c, name, imDoctype, importType, true)
}

// discardDataImport deletes a Data Import that was never started (Frappe
// deletes its file with it) and returns cause, naming the document when
// it could not be deleted.
func discardDataImport(c *client.FrappeClient, name string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.DeleteDoc(ctx, "Data Import", name); err != nil {
		output.PrintWarning(fmt.Sprintf("warning: could not delete Data Import %s: %v", name, err))
	}
	return cause
}

// resumeDataImport picks up an existing Data Import: one still Pending is
// started (Frappe does not queue a job that is already queued or
// running), then the run is waited for; a finished one is reported.
func resumeDataImport(cmd *cobra.Command, c *client.FrappeClient, name string) error {
	ctx := cmd.Context()
	var doc map[string]interface{}
	var err error
	if spinErr := runSpinner(fmt.Sprintf("Reading Data Import %s…", name), func() { doc, err = c.GetDataImport(ctx, name) }); spinErr != nil && err == nil {
		return errAborted
	}
	if err != nil {
		return err
	}
	doctype := fmt.Sprint(doc["reference_doctype"])
	if imDoctype != "" && !strings.EqualFold(imDoctype, doctype) {
		return &client.StateError{Message: fmt.Sprintf("Data Import %s imports into %s, not %s", name, doctype, imDoctype)}
	}
	importType, _ := doc["import_type"].(string)
	status, _ := doc["status"].(string)
	start := status == "" || status == "Pending"
	if start {
		if tw, _ := doc["template_warnings"].(string); strings.TrimSpace(tw) != "" {
			ws := client.DataImportWarnings(tw)
			if err := printDataImportWarnings(name, doctype, ws); err != nil {
				return err
			}
			return &client.StateError{Message: fmt.Sprintf("Frappe stopped Data Import %s on %d %s in its file; nothing was imported. Fix the file and import it again", name, len(ws), plural(len(ws), "warning"))}
		}
		if f, _ := doc["import_file"].(string); f == "" {
			return &client.StateError{Message: fmt.Sprintf("Data Import %s has no file to import", name)}
		}
	}
	return runDataImport(cmd, c, name, doctype, importType, start)
}

// runDataImport starts the job (when start is set), waits for it and
// reports the run.
func runDataImport(cmd *cobra.Command, c *client.FrappeClient, name, doctype, importType string, start bool) error {
	ctx := cmd.Context()
	if start {
		var started bool
		var err error
		if spinErr := runSpinner(fmt.Sprintf("Starting Data Import %s…", name), func() { started, err = c.StartDataImport(ctx, name) }); spinErr != nil && err == nil {
			return errAborted
		}
		if client.IsSchedulerInactive(err) {
			return &client.StateError{Message: fmt.Sprintf("%v. Enable it (System Settings > Enable Scheduler, or bench --site <site> enable-scheduler), then start the import with: %s",
				err, resumeHint(name))}
		}
		if err != nil {
			return fmt.Errorf("%w (Data Import %s was created; retry with: %s)", err, name, resumeHint(name))
		}
		if !started && !machineOutput() {
			fmt.Fprintf(os.Stderr, "Data Import %s is already queued or running.\n", name)
		}
	}

	var st client.DataImportStatus
	var err error
	progress := func(client.DataImportStatus) {}
	if !spinnerEnabled() && !quiet {
		last := ""
		progress = func(s client.DataImportStatus) {
			line := fmt.Sprintf("Data Import %s: %s", name, s.Status)
			if s.Total > 0 {
				line += fmt.Sprintf(", %d of %d documents done", s.Success+s.Failed, s.Total)
			}
			if line != last {
				fmt.Fprintln(os.Stderr, text.Sanitize(line))
				last = line
			}
		}
	}
	title := fmt.Sprintf("Importing into %s (Data Import %s; waiting up to %s)…", doctype, name, imWait)
	if spinErr := runSpinner(title, func() { st, err = c.WaitDataImport(ctx, name, imWait, progress) }); spinErr != nil && err == nil {
		return errAborted
	}
	var wait *client.DataImportWaitError
	if errors.As(err, &wait) {
		return &codeError{exitNetwork, fmt.Sprintf("%s. Is a worker running on the site's \"default\" queue? It is still running; pick it up with: %s --wait 30m",
			wait.Error(), resumeHint(name))}
	}
	if err != nil {
		return err
	}
	return reportDataImport(ctx, c, name, doctype, importType, st)
}

// dataImportRow is one document of the run, from its Data Import Log.
type dataImportRow struct {
	Rows    []int  `json:"rows"` // file rows (1 is the header)
	Name    string `json:"name,omitempty"`
	Status  string `json:"status"` // created | updated | imported | failed
	Message string `json:"message,omitempty"`
}

// dataImportResult is the machine output of a finished run.
type dataImportResult struct {
	DataImport string          `json:"data_import"`
	Doctype    string          `json:"doctype"`
	Status     string          `json:"status"`
	Success    int             `json:"success"`
	Failed     int             `json:"failed"`
	Total      int             `json:"total"`
	Rows       []dataImportRow `json:"rows"`
}

// reportDataImport prints the run's logs. Failed documents exit 8; a run
// that did not log every document and ended in Error or Timed Out (the
// job itself failed) or was stopped by warnings exits 6.
func reportDataImport(ctx context.Context, c *client.FrappeClient, name, doctype, importType string, st client.DataImportStatus) error {
	if st.Blocked {
		if err := printDataImportWarnings(name, doctype, st.Warnings); err != nil {
			return err
		}
		n := len(st.Warnings)
		return &client.StateError{Message: fmt.Sprintf("Frappe stopped Data Import %s on %d %s in the file; nothing was imported", name, n, plural(n, "warning"))}
	}
	var logs []client.DataImportLog
	var err error
	if spinErr := runSpinner("Reading the import log…", func() { logs, err = c.DataImportLogs(ctx, name) }); spinErr != nil && err == nil {
		return errAborted
	}
	if err != nil {
		return err
	}
	done := "imported"
	switch importType {
	case client.DataImportInsert:
		done = "created"
	case client.DataImportUpdate:
		done = "updated"
	}
	res := dataImportResult{DataImport: name, Doctype: doctype, Status: st.Final(), Total: st.Total, Rows: make([]dataImportRow, 0, len(logs))}
	for _, l := range logs {
		r := dataImportRow{Rows: l.Rows, Name: l.Docname, Status: done}
		if l.Success {
			res.Success++
		} else {
			res.Failed++
			r.Status = "failed"
			r.Message = strings.Join(l.Messages, "; ")
			if r.Message == "" {
				r.Message = l.Exception
			}
		}
		res.Rows = append(res.Rows, r)
	}
	// The logs are capped (1000 or 5000); the counts are not.
	if st.Success+st.Failed > len(logs) {
		output.PrintWarning(fmt.Sprintf("warning: the site returned %d of %d logs; the counts are the site's", len(logs), st.Success+st.Failed))
		res.Success, res.Failed = st.Success, st.Failed
	}

	var exit error
	switch {
	case (res.Status == "Error" || res.Status == "Timed Out") && res.Success+res.Failed < res.Total:
		exit = &client.StateError{Message: fmt.Sprintf("Data Import %s ended %q after %d of %d documents: the import job failed (see 'ffc errors' for \"Data import failed\")",
			name, res.Status, res.Success+res.Failed, res.Total)}
	case res.Failed > 0:
		exit = &partialError{fmt.Sprintf("%d of %d documents failed (Data Import %s)", res.Failed, res.Success+res.Failed, name)}
	}
	if machineOutput() {
		if err := printResult(res); err != nil {
			return err
		}
		return exit
	}
	if len(res.Rows) > 0 {
		rows := make([]map[string]interface{}, len(res.Rows))
		for i, r := range res.Rows {
			rows[i] = map[string]interface{}{"row": rowNumbers(r.Rows), "name": r.Name, "status": r.Status, "message": r.Message}
		}
		output.PrintTable(rows, []string{"row", "name", "status", "message"})
	}
	summary := fmt.Sprintf("Data Import %s: %s, %d of %d documents %s", name, res.Status, res.Success, res.Total, done)
	if exit == nil {
		output.PrintSuccess(summary + ".")
	} else {
		output.PrintError(fmt.Sprintf("%s, %d failed.", summary, res.Failed))
	}
	return exit
}

// rowNumbers prints a document's file rows: "4", or "4-6" when they follow
// each other.
func rowNumbers(rows []int) string {
	switch {
	case len(rows) == 0:
		return ""
	case len(rows) > 1 && rows[len(rows)-1]-rows[0] == len(rows)-1:
		return fmt.Sprintf("%d-%d", rows[0], rows[len(rows)-1])
	}
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = strconv.Itoa(r)
	}
	return strings.Join(parts, ",")
}

// warningText is one warning on one line: where, then the message.
func warningText(w client.DataImportWarning) string {
	var where []string
	switch {
	case w.Row > 0:
		where = append(where, fmt.Sprintf("row %d", w.Row))
	case len(w.Rows) > 0:
		where = append(where, "rows "+rowNumbers(w.Rows))
	}
	if w.Col > 0 {
		where = append(where, fmt.Sprintf("column %d", w.Col))
	}
	if len(where) == 0 {
		return w.Message
	}
	return strings.Join(where, ", ") + ": " + w.Message
}

// printDataImportWarnings lists the warnings that stop an import: stdout
// data in machine output, stderr lines otherwise.
func printDataImportWarnings(name, doctype string, ws []client.DataImportWarning) error {
	if machineOutput() {
		return printResult(map[string]interface{}{"data_import": name, "doctype": doctype, "status": "Blocked", "warnings": ws})
	}
	for _, w := range ws {
		output.PrintWarning(warningText(w))
	}
	return nil
}

// printDataImportPreview reports a Data Import that --preview left ready.
func printDataImportPreview(name, doctype string, p *client.DataImportPreview, payloads int) error {
	resume := resumeHint(name)
	if machineOutput() {
		ws := p.Warnings
		if ws == nil {
			ws = []client.DataImportWarning{}
		}
		return printResult(map[string]interface{}{
			"data_import": name, "doctype": doctype, "status": "Pending", "preview": true,
			"rows": p.Rows, "documents": payloads, "warnings": ws, "resume": resume,
		})
	}
	output.PrintSuccess(fmt.Sprintf("Data Import %s is ready: %d %s, %d %s for %s. Nothing was imported.",
		name, p.Rows, plural(p.Rows, "row"), payloads, plural(payloads, "document"), doctype))
	fmt.Fprintf(os.Stderr, "Start it with: %s\n", resume)
	return nil
}

func init() {
	importCmd.Flags().BoolVar(&imServer, "server", false, "Import with Frappe's Data Import on the site (CSV or .xlsx; runs as a background job)")
	importCmd.Flags().BoolVar(&imPreview, "preview", false, "With --server: upload the file and show Frappe's warnings, but do not start the import")
	importCmd.Flags().BoolVar(&imMuteEmails, "mute-emails", false, "With --server: send no emails while importing")
	importCmd.Flags().DurationVar(&imWait, "wait", 10*time.Minute, "With --server: how long to wait for the import job")
	importCmd.Flags().StringVar(&imResume, "resume", "", "Start (if not yet started) and wait for this Data Import, made by an earlier --server run")
}
