package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// Import (T3.1): a file in Data Import's layout (as ffc export writes it)
// read, checked and converted on the client, then written document by
// document through the REST API. import_parse.go reads the file.

// import flags
var (
	imDoctype string
	imMode    string
	imFormat  string
	imSubmit  bool
	imBulk    bulkFlags
)

var importCmd = &cobra.Command{
	Use:   "import FILE",
	Short: "Create or update documents from a CSV or JSON file",
	Long: `Import documents with their child tables from a file in the layout of
Frappe's Data Import, as 'ffc export' writes it. FILE "-" reads stdin.

--mode insert creates a document for each one in the file; --mode update
changes existing documents, found by the name column. In update mode a
blank cell leaves the field as it is (it never clears it), and a table
that has rows in the file replaces the document's rows: a row whose
table.name is one of the document's rows keeps it (with the file's values
on top), other rows are new, and rows the file leaves out are removed. A
document with nothing to change is "unchanged" and not written.

Formats: CSV (the default), or a JSON array / NDJSON of documents with
their tables nested, as ffc export --output json/ndjson writes them; the
extension (.json, .ndjson, .jsonl) or --format chooses. TSV and Excel are
not read: export again as CSV.

CSV columns are matched like Frappe's importer: fieldnames, labels,
"Label (fieldname)", name or ID; for a table "items.qty", "Qty (Items)",
"items.name" or "ID (Items)". A document is its first row plus the rows
below whose document columns are all blank. Values are converted like
Frappe's: Check takes 1/0, yes/no, true/false; numbers are plain (1234.56);
each date column takes one format, guessed from its values (2026-01-31,
31-01-2026, 01/31/2026, 31 Jan 2026, ...); Duration takes "1d 2h 3m 4s".
Select values must be options, Link values must exist on the site.

The whole file is read and checked before anything is written: on any
problem, every one is listed (row, column, value, reason) and nothing is
written (exit 6; a header that does not match is exit 2). Documents are
then written one request each (--concurrency in flight), and a result is
printed per document; any failure exits 8. --dry-run checks the file, reads
the documents an update would change, and shows the writes it would send.

--submit submits each document after writing it (a draft only); the
DocType must be submittable and have no active workflow.

--server hands the file (CSV or .xlsx) to Frappe's own Data Import instead:
ffc creates a Data Import, uploads the file, stops on Frappe's warnings,
starts the background job (the site's scheduler must be enabled and a
worker running) and waits up to --wait, then prints Frappe's log for each
document. --preview stops before the job starts; --resume NAME starts a
Data Import that was not started yet, or waits again for one still
running. Data Import needs the System Manager role.

Examples:
  ffc export -d Customer -o customers.csv && ffc import -d Customer customers.csv --mode update
  ffc import -d "Sales Invoice" invoices.csv --mode insert --submit
  ffc import -d Item items.json --mode update --dry-run
  cat todo.ndjson | ffc import -d ToDo - --format ndjson --mode insert --json
  ffc import -d Item items.xlsx --mode insert --server
  ffc import --server --resume "Item Import on 2026-10-08 10:15:02.123456" --wait 30m
`,
	Args: importArgs,
	RunE: runImport,
}

// importFormat picks the file format: --format, else the extension, else
// CSV. TSV and Excel are refused.
func importFormat(file, flag string) (string, error) {
	f := strings.ToLower(strings.TrimSpace(flag))
	if f == "" {
		switch strings.ToLower(filepath.Ext(file)) {
		case ".json":
			f = "json"
		case ".ndjson", ".jsonl":
			f = "ndjson"
		case ".tsv", ".tab":
			f = "tsv"
		case ".xlsx", ".xls", ".ods":
			f = "xlsx"
		default:
			f = "csv"
		}
	}
	switch f {
	case "csv", "json", "ndjson":
		return f, nil
	case "tsv":
		return "", usageErrorf("TSV is not imported: ffc export escapes backslashes, tabs and line breaks in TSV cells, so the values would not come back as they were; export with --output csv (the default) or json")
	case "xlsx", "xls", "excel":
		return "", usageErrorf("Excel files are read only by Frappe's Data Import: add --server, or save the sheet as CSV UTF-8")
	}
	return "", usageErrorf("--format must be csv, json or ndjson, not %q", flag)
}

func runImport(cmd *cobra.Command, args []string) error {
	if err := checkImportServerFlags(cmd); err != nil {
		return err
	}
	if imDoctype == "" && imResume == "" {
		return usageErrorf(`required flag(s) "doctype" not set`)
	}
	if imServer || imResume != "" {
		return runImportServer(cmd, args)
	}
	file := args[0]
	if file == "" {
		return usageErrorf("FILE is empty: give a path, or - for stdin")
	}
	mode := strings.ToLower(imMode)
	if mode == "" {
		return usageErrorf(`required flag(s) "mode" not set`)
	}
	if mode != "insert" && mode != "update" {
		return usageErrorf("--mode must be insert or update")
	}
	format, err := importFormat(file, imFormat)
	if err != nil {
		return err
	}
	if err := imBulk.check(); err != nil {
		return err
	}
	data, err := readInput("", file)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	var metas map[string]*client.FormMeta
	var metaErr error
	if spinErr := runSpinner(fmt.Sprintf("Reading the %s meta…", imDoctype), func() {
		metas, metaErr = c.FormMetas(ctx, imDoctype)
	}); spinErr != nil && metaErr == nil {
		return errAborted
	}
	if metaErr != nil {
		return metaErr
	}
	m, err := newImportMeta(imDoctype, metas)
	if err != nil {
		return err
	}
	if imSubmit && !m.submittable {
		return usageErrorf("--submit: %s is not submittable", imDoctype)
	}

	var f *importFile
	if format == "csv" {
		f, err = parseImportCSV(data, m)
	} else {
		f, err = parseImportJSON(data, format == "ndjson", m)
	}
	if err != nil {
		return err
	}
	if len(f.docs) == 0 && len(f.problems) == 0 {
		return usageErrorf("%s has no documents: a header row and at least one document row are needed", file)
	}
	if mode == "update" && !f.hasName {
		return usageErrorf("--mode update finds documents by name: the file needs a name (or ID) column")
	}
	job := &importJob{c: c, m: m, docs: f.docs, update: mode == "update", submit: imSubmit}
	warnings := job.prepare(f)
	warnings = append(warnings, f.convert(m)...)

	var linkWarnings []string
	var sameFile bool
	var linkErr error
	if spinErr := runSpinner("Checking Link values…", func() {
		linkWarnings, sameFile, linkErr = job.checkLinks(ctx, f)
		if linkErr == nil && job.update {
			linkErr = job.checkUnnamedRows(ctx, f)
		}
		if linkErr == nil && imSubmit {
			linkErr = refuseImportWorkflow(ctx, c, imDoctype)
		}
	}); spinErr != nil && linkErr == nil {
		return errAborted
	}
	if linkErr != nil {
		return linkErr
	}
	workers := imBulk.concurrency
	if sameFile && workers > 1 {
		workers = 1
		linkWarnings = append(linkWarnings, "documents link to documents created earlier in the file: writing one at a time, in file order (--concurrency ignored)")
	}
	for _, w := range append(warnings, linkWarnings...) {
		output.PrintWarning("warning: " + w)
	}
	if len(f.problems) > 0 {
		return reportImportProblems(f, file)
	}

	if dryRunOn(cmd) {
		return printImportReport(job.dryRun(ctx, f.unit), true)
	}
	results := make([]importResult, len(job.docs))
	var rep bulkReport
	_ = runSpinner(fmt.Sprintf("Importing %d %s documents…", len(job.docs), imDoctype), func() {
		rep = runBulk(ctx, len(job.docs), workers, imBulk.failFast, "done", func(ctx context.Context, i int) (string, error) {
			r, err := job.one(ctx, i)
			results[i] = r
			return r.Name, err
		})
	})
	for i, br := range rep.Results {
		r := &results[i]
		r.Row = job.docs[i].line
		if r.Name == "" {
			r.Name = br.Name
		}
		if r.Name == "" && job.update {
			r.Name = job.docs[i].parent.name()
		}
		switch br.Status {
		case "done":
		case "error":
			r.Status, r.Error, r.Submitted = "failed", br.Error, false
		default: // interrupted, skipped
			r.Status, r.Error, r.Submitted = br.Status, br.Error, false
		}
	}
	return printImportReport(importReport{results: results, unit: f.unit}, false)
}

// refuseImportWorkflow refuses --submit on a DocType with an active
// Workflow, as submit-doc does: its documents move through workflow
// actions. When the user may not read Workflows the site decides.
func refuseImportWorkflow(ctx context.Context, c *client.FrappeClient, doctype string) error {
	wf, _, err := c.ActiveWorkflow(ctx, doctype)
	if err != nil {
		return err
	}
	if wf != "" {
		return &client.StateError{Message: fmt.Sprintf("--submit: %s uses the workflow %q: import without --submit and apply its actions with 'ffc workflow apply'", doctype, wf)}
	}
	return nil
}

// importJob writes the documents of a checked file.
type importJob struct {
	c      *client.FrappeClient
	m      *importMeta
	docs   []*importDoc
	update bool
	submit bool
	// insert: send the name (prompt or UUID naming), or put it in the
	// naming field ("field:x" without an x column)
	sendName  bool
	nameField string
}

// prepare checks the names before anything is converted (each document
// of an update needs one, a document or row appears once) and decides
// what an insert does with them. It returns warnings.
func (j *importJob) prepare(f *importFile) []string {
	var warnings []string
	if len(f.ignored) > 0 {
		warnings = append(warnings, fmt.Sprintf("ignored columns (ffc import does not set them): %s", strings.Join(f.ignored, ", ")))
	}
	if f.untitled > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s without a header hold values: they are ignored", f.untitled, plural(f.untitled, "column")))
	}
	if !j.update {
		hasNames, rowNames := false, false
		for _, d := range j.docs {
			hasNames = hasNames || d.parent.name() != ""
			for _, rows := range d.tables {
				for _, r := range rows {
					rowNames = rowNames || r.name() != ""
				}
			}
		}
		switch af := j.m.autonameField(); {
		case !hasNames:
		case j.m.keepsName():
			j.sendName = true
		case af != "" && !j.anyValue(af):
			j.nameField = af
		case af != "":
			warnings = append(warnings, fmt.Sprintf("the name column is ignored on insert: %s names documents by %s, which the file also has", j.m.doctype, af))
		default:
			rule := j.m.autoname
			if rule == "" {
				rule = "hash"
			}
			warnings = append(warnings, fmt.Sprintf("the name column is ignored on insert: %s names its documents itself (naming rule %q); through the REST API only prompt and UUID naming keep a given name", j.m.doctype, rule))
		}
		if rowNames {
			warnings = append(warnings, "table row names (table.name) are ignored on insert: new rows get new names")
		}
	}
	seen := map[string]int{}
	for _, d := range j.docs {
		name := d.parent.name()
		switch {
		case name == "" && j.update:
			f.problems = append(f.problems, importProblem{Row: d.line, Column: "name", Reason: "no name: an update needs the name of each document"})
		case name != "" && (j.update || j.sendName):
			key := strings.ToLower(name) // names compare case-insensitively on MariaDB
			if prev, dup := seen[key]; dup {
				f.problems = append(f.problems, importProblem{Row: d.line, Column: "name", Value: name,
					Reason: fmt.Sprintf("the same document as %s %d", f.unit, prev)})
			} else {
				seen[key] = d.line
			}
		}
		if !j.update {
			continue
		}
		for _, t := range j.m.tableOrder {
			rows := map[string]bool{}
			for _, r := range d.tables[t] {
				n := r.name()
				if n == "" {
					continue
				}
				if rows[n] {
					f.problems = append(f.problems, importProblem{Row: r.line, Column: t + ".name", Value: n, Reason: "the same row twice in one document"})
				}
				rows[n] = true
			}
		}
	}
	return warnings
}

func (j *importJob) anyValue(field string) bool {
	for _, d := range j.docs {
		if d.parent.values[field] != nil {
			return true
		}
	}
	return false
}

// linkURLBudget caps the escaped filters of one existence check: they
// travel in a GET URL, and servers and proxies refuse long ones (4 KiB is
// a common limit).
const linkURLBudget = 3500

// linkChunks splits names into lists whose filters stay within
// linkURLBudget once escaped; a name longer than that goes alone.
func linkChunks(values []string) ([][]string, error) {
	base := len(url.QueryEscape(`[["name","in",[]]]`))
	var out [][]string
	var cur []string
	size := base
	for _, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		n := len(url.QueryEscape(string(b))) + len("%2C")
		if len(cur) > 0 && size+n > linkURLBudget {
			out = append(out, cur)
			cur, size = nil, base
		}
		cur = append(cur, v)
		size += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out, nil
}

// fileNames are the names an insert keeps (prompt or UUID naming, the
// "field:x" naming field), lower-case, with the line of their document: a
// Link to the imported DocType may name one of them. An update creates
// nothing, and a name the site makes up cannot be linked to.
func (j *importJob) fileNames() map[string]int {
	out := map[string]int{}
	if j.update {
		return out
	}
	af := j.m.autonameField()
	for _, d := range j.docs {
		var n string
		switch {
		case j.sendName || j.nameField != "":
			n = d.parent.name()
		case af != "":
			if v := d.parent.values[af]; v != nil {
				n = strings.TrimSpace(cellText(v.v))
			}
		}
		if k := strings.ToLower(n); k != "" {
			if _, dup := out[k]; !dup {
				out[k] = d.line
			}
		}
	}
	return out
}

// checkLinks reports Link values that name no document, as Data Import's
// value check does (value_mapping.py get_invalid_link_select_items: one
// query per target DocType, names compared case-insensitively like
// MariaDB). It lists the names with a GET list, which applies the user's
// User Permissions where Frappe's get_all does not: a restricted user may
// be told a document does not exist. A Link to the imported DocType may
// name a document an insert creates earlier in the file (sameFile is then
// true: the writes must keep the file's order). Dynamic Links are not
// checked (Frappe does not either). When the user may not read a target
// DocType, the site checks on save (warning).
func (j *importJob) checkLinks(ctx context.Context, f *importFile) (warnings []string, sameFile bool, err error) {
	m := j.m
	type ref struct {
		line, docLine int
		col, value    string
	}
	refs := map[string][]ref{}
	f.eachValue(m, func(d *importDoc, line int, table, field string, v *importValue) {
		fld := m.fieldOf(table, field)
		if fld.Fieldtype == "Link" && fld.Options != "" {
			refs[fld.Options] = append(refs[fld.Options], ref{line, d.line, v.col, cellText(v.v)})
		}
	})
	inFile := j.fileNames()
	targets := make([]string, 0, len(refs))
	for t := range refs {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, target := range targets {
		var values []string
		seen := map[string]bool{}
		for _, r := range refs[target] {
			if k := strings.ToLower(r.value); !seen[k] {
				seen[k] = true
				values = append(values, r.value)
			}
		}
		found := map[string]bool{}
		chunks, err := linkChunks(values)
		if err != nil {
			return nil, false, err
		}
		var readErr error
		for _, chunk := range chunks {
			if readErr != nil {
				break
			}
			filters, err := json.Marshal([]interface{}{[]interface{}{"name", "in", chunk}})
			if err != nil {
				return nil, false, err
			}
			var rows []map[string]interface{}
			rows, readErr = j.c.GetList(ctx, target, client.ListOptions{Fields: []string{"name"}, Filters: string(filters), Limit: -1})
			for _, r := range rows {
				if n, ok := docName(r["name"]); ok {
					found[strings.ToLower(n)] = true
				}
			}
		}
		var api *client.APIError
		if errors.As(readErr, &api) && api.Status == http.StatusForbidden {
			warnings = append(warnings, fmt.Sprintf("your user may not read %s: its Link values are checked by the site when each document is saved", target))
			continue
		}
		if readErr != nil {
			return nil, false, fmt.Errorf("checking the %s Link values: %w", target, readErr)
		}
		for _, r := range refs[target] {
			k := strings.ToLower(r.value)
			if found[k] {
				continue
			}
			reason := fmt.Sprintf("no %s named %q (or your User Permissions hide it from you)", target, r.value)
			if at, ok := inFile[k]; ok && target == m.doctype {
				switch {
				case at < r.docLine:
					sameFile = true
					continue
				case at == r.docLine:
					reason = "the document links to itself: it does not exist before it is created"
				default:
					reason = fmt.Sprintf("%s %q is created by %s %d, after this document: put it above", target, r.value, f.unit, at)
				}
			}
			f.problems = append(f.problems, importProblem{Row: r.line, Column: r.col, Value: r.value, Reason: reason})
		}
	}
	return warnings, sameFile, nil
}

// checkUnnamedRows reports, in an update, a table whose rows in the file
// carry no row names while the document has rows on the site: the file's
// rows would replace them, and every column the file does not have would
// go back to its default. ffc export writes table.name, so an exported
// file keeps the rows. It reads those documents (GET, so a dry run too);
// one that is not found is left to the write, which reports it.
func (j *importJob) checkUnnamedRows(ctx context.Context, f *importFile) error {
	for _, d := range j.docs {
		var tables []string
		for _, t := range j.m.tableOrder {
			rows := d.tables[t]
			named := false
			for _, r := range rows {
				named = named || r.name() != ""
			}
			if len(rows) > 0 && !named {
				tables = append(tables, t)
			}
		}
		name := d.parent.name()
		if len(tables) == 0 || name == "" {
			continue
		}
		cur, err := j.c.GetDoc(ctx, j.m.doctype, name)
		var api *client.APIError
		if errors.As(err, &api) && api.Status == http.StatusNotFound {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s %s: %w", j.m.doctype, name, err)
		}
		for _, t := range tables {
			if n := len(rowsOf(cur[t])); n > 0 {
				f.problems = append(f.problems, importProblem{Row: d.line, Column: t + ".name",
					Reason: fmt.Sprintf("%s has %d %s %s on the site and the file names none of them: they would be replaced by the file's rows, losing every column the file does not have. Export the document with ffc export (it writes %s.name) and edit that file",
						name, n, t, plural(n, "row"), t)})
			}
		}
	}
	return nil
}

// reportImportProblems lists every problem (stdout data in machine
// output, stderr lines otherwise) and returns the validation error.
func reportImportProblems(f *importFile, file string) error {
	sort.SliceStable(f.problems, func(a, b int) bool { return f.problems[a].Row < f.problems[b].Row })
	n := len(f.problems)
	if machineOutput() {
		if err := printResult(map[string]interface{}{"problems": f.problems}); err != nil {
			return err
		}
	} else {
		for _, p := range f.problems {
			where := fmt.Sprintf("%s %d", f.unit, p.Row)
			if p.Column != "" {
				where += ", " + text.Sanitize(p.Column)
			}
			if p.Value != "" {
				where += fmt.Sprintf(" %q", text.Sanitize(p.Value))
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", where, text.Sanitize(p.Reason))
		}
	}
	if file == "-" {
		file = "the input"
	}
	return &client.StateError{Message: fmt.Sprintf("%d %s in %s; nothing was written", n, plural(n, "problem"), file)}
}

// ─── Writing ─────────────────────────────────────────────────────────────────

// importResult is the outcome of one document.
type importResult struct {
	Row       int    `json:"row"` // where the document starts in the file
	Name      string `json:"name,omitempty"`
	Status    string `json:"status"` // created | updated | unchanged | failed | interrupted | skipped
	Submitted bool   `json:"submitted,omitempty"`
	Warning   string `json:"warning,omitempty"`
	Error     string `json:"error,omitempty"`
}

// one writes document i: insert, or read + compare + update; then the
// submit. The result's status is what was done (or, under a dry run, held
// back); an error means it failed after that, or before.
func (j *importJob) one(ctx context.Context, i int) (importResult, error) {
	d := j.docs[i]
	res := importResult{Row: d.line}
	var doc map[string]interface{}
	if j.update {
		res.Name = d.parent.name()
		cur, err := j.c.GetDoc(ctx, j.m.doctype, res.Name)
		if err != nil {
			return res, err
		}
		payload, changes := j.updatePayload(cur, d)
		doc = cur
		if len(payload) == 0 {
			res.Status = "unchanged"
		} else {
			res.Status = "updated"
			modified := cur["modified"]
			payload["modified"] = modified
			saved, err := j.c.UpdateDoc(ctx, j.m.doctype, res.Name, payload)
			var plan *client.DryRunError
			if errors.As(err, &plan) && len(plan.Requests) == 1 {
				plan.Requests[0].Changes = changes
				return res, err
			}
			if err != nil {
				return res, conflictError(err, j.m.doctype, res.Name, fmt.Sprint(modified))
			}
			doc = saved
			res.Warning = j.notKept(d, payload, saved)
		}
	} else {
		res.Status = "created"
		payload := j.insertPayload(d)
		saved, err := j.c.CreateDoc(ctx, j.m.doctype, payload)
		if err != nil {
			return res, err
		}
		res.Name, _ = docName(saved["name"])
		doc = saved
		res.Warning = j.notKept(d, payload, saved)
	}
	if !j.submit {
		return res, nil
	}
	switch fmt.Sprint(doc["docstatus"]) {
	case "0":
	case "1":
		return res, nil // already submitted
	default:
		return res, fmt.Errorf("%s %s is cancelled and cannot be submitted", j.m.doctype, res.Name)
	}
	if _, err := j.c.SubmitDoc(ctx, j.m.doctype, res.Name); err != nil {
		var plan *client.DryRunError
		if errors.As(err, &plan) || res.Status == "unchanged" {
			return res, err
		}
		return res, fmt.Errorf("%s as %s, but the submit failed: %w", res.Status, res.Name, err)
	}
	res.Submitted = true
	return res, nil
}

// sentRows are the file's rows of a table as a write sends them, in
// order: an insert leaves out rows that hold only a row name.
func (j *importJob) sentRows(d *importDoc, t string) []importRow {
	if j.update {
		return d.tables[t]
	}
	var out []importRow
	for _, r := range d.tables[t] {
		for k := range r.values {
			if k != "name" {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// insertPayload is a new document: the file's values and table rows
// (without row names: Frappe names new rows; a row with nothing else is
// left out). The name is sent only when the naming rule keeps it, or goes
// into the "field:x" naming field.
func (j *importJob) insertPayload(d *importDoc) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range d.parent.values {
		if k != "name" {
			out[k] = v.v
		}
	}
	if name := d.parent.name(); name != "" {
		switch {
		case j.sendName:
			out["name"] = name
		case j.nameField != "":
			out[j.nameField] = name
		}
	}
	for t := range d.tables {
		rows := j.sentRows(d, t)
		if len(rows) == 0 {
			continue
		}
		list := make([]interface{}, 0, len(rows))
		for _, r := range rows {
			row := map[string]interface{}{}
			for k, v := range r.values {
				if k != "name" {
					row[k] = v.v
				}
			}
			list = append(list, row)
		}
		out[t] = list
	}
	return out
}

// updatePayload compares the file's document with the site's: the fields
// whose value differs, and each table of the file that differs in any way,
// sent whole (a PUT replaces a table): rows the document has keep their
// values with the file's on top, other rows are new, idx is the file's
// order. changes describe it for a dry run. Nothing differs: nil.
func (j *importJob) updatePayload(cur map[string]interface{}, d *importDoc) (map[string]interface{}, map[string]interface{}) {
	out := map[string]interface{}{}
	changes := map[string]interface{}{}
	for k, v := range d.parent.values {
		if k == "name" {
			continue
		}
		if !sameImportValue(j.m.fields[k], cur[k], v.v) {
			out[k] = v.v
			changes[k] = map[string]interface{}{"from": cur[k], "to": v.v}
		}
	}
	for _, t := range j.m.tableOrder {
		rows, ok := d.tables[t]
		if !ok {
			continue
		}
		site := rowsOf(cur[t])
		byName := map[string]map[string]interface{}{}
		for _, r := range site {
			if n, ok := docName(r["name"]); ok {
				byName[n] = r
			}
		}
		changed := len(rows) != len(site)
		list := make([]interface{}, 0, len(rows))
		for i, r := range rows {
			row := map[string]interface{}{}
			if old, kept := byName[r.name()]; kept && r.name() != "" {
				for k, v := range old {
					row[k] = v
				}
				if n, _ := docName(site[min(i, len(site)-1)]["name"]); i >= len(site) || n != r.name() {
					changed = true // moved
				}
			} else {
				changed = true
			}
			for k, v := range r.values {
				if k == "name" {
					continue
				}
				if !sameImportValue(j.m.tables[t].fields[k], row[k], v.v) {
					changed = true
				}
				row[k] = v.v
			}
			row["idx"] = json.Number(strconv.Itoa(i + 1))
			list = append(list, row)
		}
		if changed {
			out[t] = list
			changes[t] = map[string]interface{}{"from": fmt.Sprintf("%d rows", len(site)), "to": fmt.Sprintf("%d rows", len(rows))}
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, changes
}

// sameImportValue compares a site value with a file value: numbers by
// value, Link names case-insensitively (MariaDB matches them so, and the
// site corrects the case on save), times as HH:MM:SS[.ffffff] (the site
// answers "9:05:00").
func sameImportValue(f client.FormField, site, file interface{}) bool {
	a, aok := site.(string)
	b, bok := file.(string)
	switch {
	case !aok || !bok:
	case f.Fieldtype == "Link" || f.Fieldtype == "Dynamic Link":
		return strings.EqualFold(a, b)
	case f.Fieldtype == "Time":
		ta, aok := normalTime(a)
		tb, bok := normalTime(b)
		if aok && bok {
			return ta == tb
		}
	}
	return sameValue(site, file)
}

// notKept names the fields and table cells the site saved with another
// value than the one sent (fields above the user's permission level are
// dropped, a controller may rewrite a value), like edit-doc's warning.
// Table rows are compared by position, on the file's columns only.
func (j *importJob) notKept(d *importDoc, sent, saved map[string]interface{}) string {
	var out []string
	for _, k := range objectKeys(sent) {
		if k == "modified" || k == "name" {
			continue
		}
		if _, isTable := j.m.tables[k]; isTable {
			continue
		}
		if !sameImportValue(j.m.fields[k], saved[k], sent[k]) {
			out = append(out, fmt.Sprintf("%s: sent %s, saved %s", k, diffValue(sent[k]), diffValue(saved[k])))
		}
	}
	for _, t := range j.m.tableOrder {
		if _, ok := sent[t]; !ok {
			continue
		}
		sentList, savedList := rowsOf(sent[t]), rowsOf(saved[t])
		if len(savedList) != len(sentList) {
			out = append(out, fmt.Sprintf("%s: sent %d rows, saved %d", t, len(sentList), len(savedList)))
			continue
		}
		for i, r := range j.sentRows(d, t) {
			if i >= len(sentList) {
				break
			}
			for _, k := range sortedValueKeys(r.values) {
				if k == "name" {
					continue
				}
				if !sameImportValue(j.m.tables[t].fields[k], savedList[i][k], sentList[i][k]) {
					out = append(out, fmt.Sprintf("%s row %d %s: sent %s, saved %s", t, i+1, k, diffValue(sentList[i][k]), diffValue(savedList[i][k])))
				}
			}
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "not saved as sent: " + strings.Join(out, "; ")
}

// dryRun plans every document in turn under the dry run: the reads run,
// the writes are held back. A document stops at its first write, so the
// submit of a created or updated document is not shown.
func (j *importJob) dryRun(ctx context.Context, unit string) importReport {
	rep := importReport{results: make([]importResult, len(j.docs)), dryRun: true, unit: unit}
	for i := range j.docs {
		if ctx.Err() != nil {
			rep.results[i] = importResult{Row: j.docs[i].line, Name: j.docs[i].parent.name(), Status: "skipped"}
			continue
		}
		r, err := j.one(ctx, i)
		var plan *client.DryRunError
		switch {
		case err == nil:
		case errors.As(err, &plan):
			rep.requests = append(rep.requests, plan.Requests...)
		default:
			r.Status, r.Error = "failed", err.Error()
		}
		rep.results[i] = r
	}
	return rep
}

// importReport is the result of a run.
type importReport struct {
	results  []importResult
	dryRun   bool
	requests []client.PlannedRequest
	unit     string
}

func (r importReport) counts() map[string]int {
	n := map[string]int{"created": 0, "updated": 0, "unchanged": 0, "submitted": 0, "failed": 0, "skipped": 0}
	for _, res := range r.results {
		switch res.Status {
		case "interrupted":
			n["failed"]++
		default:
			n[res.Status]++
		}
		if res.Submitted {
			n["submitted"]++
		}
	}
	return n
}

// printImportReport prints the per-document results and returns exit 8
// when any document failed or was skipped.
func printImportReport(r importReport, dryRun bool) error {
	n := r.counts()
	var exit error
	if bad := n["failed"] + n["skipped"]; bad > 0 {
		exit = &partialError{fmt.Sprintf("%d of %d documents did not succeed (%d failed or interrupted, %d skipped)", bad, len(r.results), n["failed"], n["skipped"])}
	}
	if machineOutput() {
		out := map[string]interface{}{"results": r.results}
		for k, v := range n {
			out[k] = v
		}
		if dryRun {
			out["dry_run"] = true
			requests := r.requests
			if requests == nil {
				requests = []client.PlannedRequest{}
			}
			out["requests"] = requests
		}
		if err := printResult(out); err != nil {
			return err
		}
		return exit
	}
	rows := make([]map[string]interface{}, len(r.results))
	submit := imSubmit
	for i, res := range r.results {
		msg := res.Error
		if msg == "" {
			msg = res.Warning
		}
		rows[i] = map[string]interface{}{"row": res.Row, "name": res.Name, "status": res.Status, "submitted": res.Submitted, "message": msg}
	}
	cols := []string{"row", "name", "status", "message"}
	if submit {
		cols = []string{"row", "name", "status", "submitted", "message"}
	}
	output.PrintTable(rows, cols)
	summary := fmt.Sprintf("%d created, %d updated, %d unchanged", n["created"], n["updated"], n["unchanged"])
	if submit {
		summary += fmt.Sprintf(", %d submitted", n["submitted"])
	}
	if dryRun {
		if len(r.requests) > 0 {
			fmt.Println()
			if err := printPlan(&client.DryRunError{Requests: r.requests}); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(os.Stderr, "Dry run: nothing was sent.")
		}
		summary = "Dry run: " + summary
	}
	if exit == nil {
		output.PrintSuccess(summary + ".")
	} else {
		output.PrintError(fmt.Sprintf("%s, %d failed or interrupted, %d skipped.", summary, n["failed"], n["skipped"]))
	}
	return exit
}

func init() {
	importCmd.Flags().StringVarP(&imDoctype, "doctype", "d", "", "DocType to import into (required, except with --resume)")
	importCmd.Flags().StringVar(&imMode, "mode", "", "insert (create every document) or update (change documents found by name) (required, except with --resume)")
	importCmd.Flags().StringVar(&imFormat, "format", "", "File format: csv, json or ndjson; with --server csv or xlsx (default: from the extension, else csv)")
	importCmd.Flags().BoolVar(&imSubmit, "submit", false, "Submit each document after writing it (submittable DocTypes)")
	imBulk.register(importCmd)
	addDryRun(importCmd, false)
	rootCmd.AddCommand(importCmd)
}
