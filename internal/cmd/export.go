package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// Export (T3.1): documents with their child rows in Frappe's Data Import
// layout. import-template (import_template.go) shares the column choice.

// export flags
var (
	exDoctype  string
	exFilters  string
	exOrderBy  string
	exLimit    int
	exPageSize int
	exOut      string
	exXLSX     bool
	exForce    bool
	exSelect   exportSelect
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export documents with their child tables, ready to import",
	Long: `Export the documents of a DocType with the rows of its child tables, in
the layout of Frappe's Data Import: re-importing the file (Data Import, or
a later ffc import) updates or recreates the same documents.

The format is --output: csv (the default), tsv, json or ndjson.
CSV and TSV are flat, as Frappe writes them: a header of fieldnames (name,
the DocType's fields, then items.name, items.<field> for each table), and
per document one row with its values and the first row of each table,
followed by one row for each further table row with the document's own
columns blank. JSON and NDJSON write each document with its tables nested
as arrays (rows keep name; their order is their idx).

Cells follow Frappe's exporter: Duration as "1d 2h 3m 4s", a text starting
with = + - @, a tab or a carriage return gets a leading ' so a spreadsheet
does not run it as a formula (the importer strips it again), null is empty.
Numbers are written as the site sends them.

Columns: name and every field you may read that holds data (no layout
fields, tables, Password or virtual fields), in form order, and every
table with its readable fields. --fields picks columns instead: plain names
for the DocType's fields, "items.qty" for a table's field, "items" for a
whole table; only the tables it names are exported (all tables when it
names none). --tables picks tables, --no-tables leaves them out.

Documents are read page by page (--page-size) in creation order unless
--order-by says otherwise, and their rows per page with one request per
table. The output is written as it arrives; with --output-file it goes to
a temporary file renamed into place when complete, and an existing file is
replaced only with --force.

--xlsx asks Frappe for an Excel workbook instead (Data Import's
download_template). Frappe builds the whole file in memory, orders by your
list view settings, needs the Export permission and, on v16, turns Text
Editor HTML into plain text. A workbook is never printed to a terminal.

Examples:
  ffc export -d "Sales Invoice" --filters '{"docstatus":1}' -o invoices.csv
  ffc export -d "Sales Invoice" --fields customer,posting_date,items.item_code,items.qty
  ffc export -d Customer --no-tables --output ndjson | jq .customer_name
  ffc export -d Item --xlsx -o items.xlsx
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		filters, err := filtersFlag(exFilters)
		if err != nil {
			return err
		}
		if exLimit < 0 {
			return usageErrorf("--limit must be >= 0 (0 means no limit)")
		}
		if exPageSize < 1 {
			return usageErrorf("--page-size must be at least 1")
		}
		var format output.Format
		if exXLSX {
			if err := refuseFormat("--xlsx"); err != nil {
				return err
			}
		} else if format, err = exportFormat(); err != nil {
			return err
		}
		out, err := exportTarget(exOut, exForce, exXLSX)
		if err != nil {
			return err
		}

		ctx := cmd.Context()
		c, err := newClient(ctx)
		if err != nil {
			return err
		}
		defer c.CloseQuietly()
		layout, err := loadExportLayout(ctx, c, exDoctype, exSelect)
		if err != nil {
			return err
		}
		if exXLSX {
			if filters == "" {
				filters = "{}"
			}
			return saveTemplate(ctx, c, layout, client.TemplateOptions{Records: "by_filter", Filters: filters, FileType: "Excel"}, out, exForce)
		}

		q := exportQuery{filters: filters, orderBy: exOrderBy, limit: exLimit, pageSize: exPageSize}
		if q.orderBy == "" {
			q.orderBy = "creation asc, name asc" // stable pages while documents change
		} else if !sortsByName(q.orderBy) {
			// Offset paging over equal sort keys can repeat or skip
			// documents: name breaks the ties.
			q.orderBy += ", name asc"
		}
		var n exportCount
		write := func(w io.Writer, clean bool) error {
			var werr error
			n, werr = runExport(ctx, c, layout, q, w, format, clean)
			return werr
		}
		if out == "-" {
			return write(os.Stdout, stdoutIsTerminal())
		}
		if err := writeAtomic(out, exForce, 0o600, func(w io.Writer) error { return write(w, false) }); err != nil {
			return err
		}
		if !quiet {
			output.PrintSuccess(fmt.Sprintf("Exported %d %s (%d %s) to %s", n.docs, plural(n.docs, "document"), n.rows, plural(n.rows, "row"), out))
		}
		return nil
	},
}

// plural is word, with an s unless n is 1.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// sortsByName reports whether an order_by already sorts by name (bare,
// backticked or qualified), so the pages have no ties.
func sortsByName(orderBy string) bool {
	for _, part := range strings.Split(orderBy, ",") {
		f := strings.Fields(strings.TrimSpace(part))
		if len(f) == 0 {
			continue
		}
		col := strings.ReplaceAll(f[0], "`", "")
		if col == "name" || strings.HasSuffix(col, ".name") {
			return true
		}
	}
	return false
}

// exportFormat is the format export writes: an explicit --output (or
// --json), csv otherwise. FFC_OUTPUT is a default for other commands and
// is ignored, as for --xlsx. --jq and the table-shaped formats do not
// apply to a stream of documents.
func exportFormat() (output.Format, error) {
	if jqCode != nil {
		return "", usageErrorf("--jq does not apply to export: pipe --output ndjson to jq instead")
	}
	flags := rootCmd.PersistentFlags()
	if !flags.Changed("output") && !flags.Changed("json") {
		return output.FormatCSV, nil
	}
	switch outFormat {
	case output.FormatTable:
		return output.FormatCSV, nil
	case output.FormatCSV, output.FormatTSV, output.FormatJSON, output.FormatNDJSON:
		return outFormat, nil
	}
	return "", usageErrorf("export writes csv, tsv, json or ndjson, not %s", outFormat)
}

// refuseFormat refuses --output, --json and --jq next to flag, whose file
// format is fixed. FFC_OUTPUT is ignored: it is a default, not a request.
func refuseFormat(flag string) error {
	flags := rootCmd.PersistentFlags()
	if jqCode != nil || flags.Changed("output") || flags.Changed("json") {
		return usageErrorf("%s writes its own file format: drop --output, --json and --jq", flag)
	}
	return nil
}

// exportTarget is where the output goes: "-" for stdout (also the
// default), else a file that must not exist without force. A workbook is
// binary, so it is refused on a terminal before anything is sent; writeBody
// applies the same rule to the answer.
func exportTarget(out string, force, binary bool) (string, error) {
	if out == "" {
		out = "-"
	}
	if out == "-" && binary && stdoutIsTerminal() {
		return "", usageErrorf("an Excel workbook is binary and is not printed to a terminal: pass --output-file or redirect stdout")
	}
	return out, checkTarget(out, force)
}

// exportSelect holds the column flags of export and import-template.
type exportSelect struct {
	fields   string
	tables   string
	noTables bool
}

func (s *exportSelect) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&s.fields, "fields", "f", "", `Columns, comma-separated: fields of the DocType, "table.field", or a table name for all its fields`)
	cmd.Flags().StringVar(&s.tables, "tables", "", "Child tables to include, comma-separated table fieldnames (default all)")
	cmd.Flags().BoolVar(&s.noTables, "no-tables", false, "Leave out every child table")
	cmd.MarkFlagsMutuallyExclusive("tables", "no-tables")
}

// exportColumn is one column of an export: a field of the DocType or of a
// table's child DocType.
type exportColumn struct {
	field     string
	fieldtype string
	hideDays  bool // Duration: days counted as hours
}

// exportTable is a child table of the export.
type exportTable struct {
	field   string // the table fieldname on the parent
	doctype string // the child DocType
	columns []exportColumn
}

// exportLayout is the columns of an export: the DocType's, then each
// table's, every list starting with name.
type exportLayout struct {
	doctype string
	columns []exportColumn
	tables  []exportTable
}

// header is the CSV header: fieldnames, "table.field" for table columns,
// which is what Frappe's importer matches (importer.py
// build_fields_dict_for_column_matching).
func (l exportLayout) header() []string {
	h := l.fieldnames()
	for _, t := range l.tables {
		for _, c := range t.columns {
			h = append(h, t.field+"."+c.field)
		}
	}
	return h
}

// fieldnames are the DocType's columns.
func (l exportLayout) fieldnames() []string {
	out := make([]string, len(l.columns))
	for i, c := range l.columns {
		out[i] = c.field
	}
	return out
}

// templateFields is the export_fields argument of download_template: the
// DocType's and each table's fieldnames, "name" included (Frappe adds the
// ID column only when it is listed).
func (l exportLayout) templateFields() map[string][]string {
	m := map[string][]string{l.doctype: l.fieldnames()}
	for _, t := range l.tables {
		names := make([]string, len(t.columns))
		for i, c := range t.columns {
			names[i] = c.field
		}
		m[t.field] = names
	}
	return m
}

// noDataTypes are the field types without a value: Frappe's
// display_fieldtypes and the tables (frappe/model/__init__.py).
var noDataTypes = map[string]bool{
	"Section Break": true, "Column Break": true, "Tab Break": true, "Attachment Gallery": true, "HTML": true,
	"Button": true, "Image": true, "Fold": true, "Heading": true, "Table": true, "Table MultiSelect": true,
}

func isTableType(fieldtype string) bool {
	return fieldtype == "Table" || fieldtype == "Table MultiSelect"
}

// exportStandard are the standard columns of a document --fields may name
// (no DocFields); the DocType's own fields come from the meta.
var exportStandard = map[string]bool{"owner": true, "creation": true, "modified": true, "modified_by": true, "docstatus": true, "idx": true}

// unexportable says why a field cannot be a column, or "".
func unexportable(f client.FormField) string {
	switch {
	case noDataTypes[f.Fieldtype]:
		return fmt.Sprintf("a %s field holds no data", f.Fieldtype)
	case f.Fieldtype == "Password":
		return "Password fields are never exported"
	case f.IsVirtual:
		return "a virtual field is not stored"
	}
	return ""
}

// loadExportLayout reads the meta of the DocType and its tables and the
// fields the user may read, and picks the columns. When the user's roles
// cannot be read, only level-0 fields are offered (with a warning), as in
// edit-doc.
func loadExportLayout(ctx context.Context, c *client.FrappeClient, doctype string, sel exportSelect) (exportLayout, error) {
	var metas map[string]*client.FormMeta
	var access *client.FieldAccess
	var apiErr, accessErr error
	spinErr := runSpinner(fmt.Sprintf("Reading the %s meta…", doctype), func() {
		if metas, apiErr = c.FormMetas(ctx, doctype); apiErr == nil {
			access, accessErr = c.ReadableFields(ctx, doctype)
		}
	})
	if apiErr != nil {
		return exportLayout{}, apiErr
	}
	if spinErr != nil {
		return exportLayout{}, errAborted
	}
	if accessErr != nil {
		if ctx.Err() != nil {
			return exportLayout{}, accessErr
		}
		access = nil
		output.PrintWarning(fmt.Sprintf("warning: your permission levels are unknown (%v): only level-0 fields are exported", accessErr))
	}
	return buildExportLayout(doctype, metas, access, sel)
}

// buildExportLayout picks the columns (see the export help). An unknown
// field or table is a usage error, one the user may not read a permission
// error (exit 5), like aggregate's.
func buildExportLayout(doctype string, metas map[string]*client.FormMeta, access *client.FieldAccess, sel exportSelect) (exportLayout, error) {
	meta := metas[doctype]
	if meta == nil {
		return exportLayout{}, fmt.Errorf("no meta for %s", doctype)
	}
	readable := func(table string, f client.FormField) bool {
		switch {
		case access == nil:
			return f.Permlevel == 0
		case table == "":
			return access.Readable(f.Fieldname)
		}
		return access.ReadableRow(table, f.Fieldname)
	}
	fieldOf := func(m *client.FormMeta, name string) (client.FormField, bool) {
		for _, f := range m.Fields {
			if f.Fieldname == name {
				return f, true
			}
		}
		return client.FormField{}, false
	}
	// pick resolves one named column of m (table "" for the DocType).
	pick := func(m *client.FormMeta, table, name string) (exportColumn, error) {
		where := doctype
		if table != "" {
			where = doctype + "." + table
		}
		f, ok := fieldOf(m, name)
		if !ok {
			if table == "" && exportStandard[name] {
				return exportColumn{field: name}, nil
			}
			return exportColumn{}, usageErrorf("--fields: %s has no field %q", where, name)
		}
		if why := unexportable(f); why != "" {
			return exportColumn{}, usageErrorf("--fields: %s.%s: %s", where, name, why)
		}
		if !readable(table, f) {
			return exportColumn{}, unreadableField(where + "." + name)
		}
		return exportColumn{field: name, fieldtype: f.Fieldtype, hideDays: f.HideDays}, nil
	}
	// all lists m's readable data fields, in form order.
	all := func(m *client.FormMeta, table string) []exportColumn {
		var cols []exportColumn
		for _, f := range m.Fields {
			if unexportable(f) == "" && f.Fieldname != "lft" && f.Fieldname != "rgt" && readable(table, f) {
				cols = append(cols, exportColumn{field: f.Fieldname, fieldtype: f.Fieldtype, hideDays: f.HideDays})
			}
		}
		return cols
	}
	nameCol := exportColumn{field: "name", fieldtype: "Data"}
	add := func(cols []exportColumn, c exportColumn) []exportColumn {
		for _, have := range cols {
			if have.field == c.field {
				return cols
			}
		}
		return append(cols, c)
	}

	// The DocType's tables, in form order.
	type tableField struct {
		field client.FormField
		meta  *client.FormMeta
	}
	var tables []tableField
	tableIndex := map[string]int{}
	for _, f := range meta.Fields {
		if isTableType(f.Fieldtype) {
			tableIndex[f.Fieldname] = len(tables)
			tables = append(tables, tableField{f, metas[f.Options]})
		}
	}
	// --fields, split into the DocType's columns and per-table picks; a
	// table named alone (or by "table.name" only) takes all its fields.
	var parentNames []string
	picked := map[string][]string{}
	wholeTable := map[string]bool{}
	for _, raw := range splitCSV(sel.fields) {
		table, field, dotted := strings.Cut(raw, ".")
		if !dotted {
			if _, isTable := tableIndex[raw]; isTable {
				wholeTable[raw] = true
			} else {
				parentNames = append(parentNames, raw)
			}
			continue
		}
		if _, ok := tableIndex[table]; !ok {
			return exportLayout{}, usageErrorf("--fields: %s has no table %q", doctype, table)
		}
		if field == "" || strings.Contains(field, ".") {
			return exportLayout{}, usageErrorf("--fields: %q is not a table field (use table.field)", raw)
		}
		picked[table] = append(picked[table], field)
	}
	namesTables := len(picked) > 0 || len(wholeTable) > 0
	if sel.noTables && namesTables {
		return exportLayout{}, usageErrorf("--no-tables conflicts with the table columns in --fields")
	}
	var only map[string]bool // nil: every table
	switch {
	case sel.noTables:
		only = map[string]bool{}
	case sel.tables != "":
		only = map[string]bool{}
		for _, t := range splitCSV(sel.tables) {
			if _, ok := tableIndex[t]; !ok {
				return exportLayout{}, usageErrorf("--tables: %s has no table %q", doctype, t)
			}
			only[t] = true
		}
		for t := range picked {
			if !only[t] {
				return exportLayout{}, usageErrorf("--fields names %s.%s, but --tables leaves out %s", t, picked[t][0], t)
			}
		}
		for t := range wholeTable {
			if !only[t] {
				return exportLayout{}, usageErrorf("--fields names %s, but --tables leaves out %s", t, t)
			}
		}
	case namesTables:
		only = map[string]bool{}
		for t := range picked {
			only[t] = true
		}
		for t := range wholeTable {
			only[t] = true
		}
	}

	layout := exportLayout{doctype: doctype, columns: []exportColumn{nameCol}}
	if sel.fields == "" {
		layout.columns = append(layout.columns, all(meta, "")...)
	} else {
		for _, name := range parentNames {
			if name == "name" {
				continue
			}
			col, err := pick(meta, "", name)
			if err != nil {
				return exportLayout{}, err
			}
			layout.columns = add(layout.columns, col)
		}
	}
	for _, t := range tables {
		name := t.field.Fieldname
		explicit := only != nil && only[name]
		if only != nil && !explicit {
			continue
		}
		if !readable("", t.field) {
			if explicit {
				return exportLayout{}, unreadableField(doctype + "." + name)
			}
			continue
		}
		if t.meta == nil {
			if explicit {
				return exportLayout{}, fmt.Errorf("the site sent no meta for %s (table %s)", t.field.Options, name)
			}
			continue
		}
		tbl := exportTable{field: name, doctype: t.meta.Name, columns: []exportColumn{nameCol}}
		fields, restricted := picked[name]
		if !restricted || wholeTable[name] {
			tbl.columns = append(tbl.columns, all(t.meta, name)...)
		} else {
			for _, f := range fields {
				if f == "name" {
					continue
				}
				col, err := pick(t.meta, name, f)
				if err != nil {
					return exportLayout{}, err
				}
				tbl.columns = add(tbl.columns, col)
			}
		}
		layout.tables = append(layout.tables, tbl)
	}
	return layout, nil
}

// unreadableField is the permission error for a column the user may not
// read (permission level), as aggregate reports it.
func unreadableField(what string) error {
	return &client.APIError{Status: http.StatusForbidden, ExcType: "PermissionError",
		Message: fmt.Sprintf("your user may not read %s (permission level)", what)}
}

// exportQuery selects the documents to export.
type exportQuery struct {
	filters  string
	orderBy  string
	limit    int // 0: every document
	pageSize int
}

// exportCount is what an export wrote: documents and output rows (CSV
// rows, not counting the header; one per document for JSON).
type exportCount struct{ docs, rows int }

// runExport writes the documents page by page: a page of parents, then the
// rows of each table for that page, so at most one page is in memory.
// clean strips control characters from CSV/TSV cells, for a terminal. When
// a page fails the rows so far stay written, as with list-docs --all.
func runExport(ctx context.Context, c *client.FrappeClient, l exportLayout, q exportQuery, w io.Writer, f output.Format, clean bool) (exportCount, error) {
	buf := bufio.NewWriter(w)
	sink := &exportSink{layout: l, f: f, stream: output.NewListStream(buf, f, nil, clean)}
	var prevFirst string
	for start := 0; ; {
		size := q.pageSize
		if q.limit > 0 {
			size = min(size, q.limit-sink.n.docs)
		}
		var parents []map[string]interface{}
		var children []map[string][]map[string]interface{}
		var fetchErr error
		if err := runSpinner(fmt.Sprintf("Exporting %s (%d so far)…", l.doctype, start), func() {
			parents, fetchErr = c.GetList(ctx, l.doctype, client.ListOptions{
				Fields: l.fieldnames(), Filters: q.filters, OrderBy: q.orderBy, Start: start, Limit: size,
			})
			if fetchErr == nil {
				children, fetchErr = exportChildren(ctx, c, l, parents)
			}
		}); err != nil {
			_ = buf.Flush()
			return sink.n, errAborted
		}
		if fetchErr != nil {
			_ = buf.Flush()
			return sink.n, fetchErr
		}
		// A server that ignores the offset returns the same page forever.
		if len(parents) > 0 {
			first, _ := json.Marshal(parents[0])
			if string(first) == prevFirst {
				_ = buf.Flush()
				return sink.n, fmt.Errorf("the page at offset %d repeats the previous page: the server ignores the offset", start)
			}
			prevFirst = string(first)
		}
		if err := sink.page(parents, children); err != nil {
			return sink.n, writeErr(err)
		}
		if err := buf.Flush(); err != nil {
			return sink.n, writeErr(err)
		}
		start += len(parents)
		if len(parents) < size || (q.limit > 0 && sink.n.docs >= q.limit) {
			break
		}
		if err := ctx.Err(); err != nil {
			return sink.n, err
		}
	}
	if err := sink.close(); err != nil {
		return sink.n, writeErr(err)
	}
	return sink.n, writeErr(buf.Flush())
}

// exportChildren reads each table's rows of the parents and groups them by
// parent name, per table (in layout order), keeping their idx order.
func exportChildren(ctx context.Context, c *client.FrappeClient, l exportLayout, parents []map[string]interface{}) ([]map[string][]map[string]interface{}, error) {
	if len(parents) == 0 || len(l.tables) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(parents))
	for _, p := range parents {
		n, ok := docName(p["name"])
		if !ok {
			// Its row would have a blank name and read as a continuation.
			return nil, fmt.Errorf("the site listed a %s without a name", l.doctype)
		}
		names = append(names, n)
	}
	out := make([]map[string][]map[string]interface{}, len(l.tables))
	for i, t := range l.tables {
		fields := make([]string, len(t.columns))
		for j, col := range t.columns {
			fields[j] = col.field
		}
		rows, err := c.ChildRows(ctx, l.doctype, t.field, t.doctype, names, fields)
		if err != nil {
			return nil, err
		}
		byParent := map[string][]map[string]interface{}{}
		for _, r := range rows {
			if p, ok := docName(r["parent"]); ok {
				byParent[p] = append(byParent[p], r)
			}
		}
		out[i] = byParent
	}
	return out, nil
}

// exportSink writes documents in one format.
type exportSink struct {
	layout exportLayout
	f      output.Format
	stream *output.ListStream
	header bool // the CSV/TSV header is written
	n      exportCount
}

func (s *exportSink) flat() bool { return s.f == output.FormatCSV || s.f == output.FormatTSV }

func (s *exportSink) writeHeader() error {
	if s.header || !s.flat() {
		return nil
	}
	s.header = true
	return s.stream.Record(s.layout.header())
}

// page writes one page of parents; children[i][name] are the rows of table
// i of the parent called name.
func (s *exportSink) page(parents []map[string]interface{}, children []map[string][]map[string]interface{}) error {
	if err := s.writeHeader(); err != nil {
		return err
	}
	rowsOf := func(parent map[string]interface{}) [][]map[string]interface{} {
		name, _ := docName(parent["name"])
		out := make([][]map[string]interface{}, len(s.layout.tables))
		for i := range s.layout.tables {
			if i < len(children) {
				out[i] = children[i][name]
			}
		}
		return out
	}
	if !s.flat() {
		docs := make([]map[string]interface{}, len(parents))
		for i, p := range parents {
			docs[i] = s.layout.nested(p, rowsOf(p))
		}
		s.n.docs += len(docs)
		s.n.rows += len(docs)
		return s.stream.Rows(docs)
	}
	for _, p := range parents {
		for _, rec := range s.layout.flatten(p, rowsOf(p)) {
			if err := s.stream.Record(rec); err != nil {
				return err
			}
			s.n.rows++
		}
		s.n.docs++
	}
	return nil
}

func (s *exportSink) close() error {
	if err := s.writeHeader(); err != nil {
		return err
	}
	return s.stream.Close()
}

// nested is a document for JSON: its columns, and each table as an array of
// rows with their columns (no parent, parentfield or idx: the array's order
// is the idx).
func (l exportLayout) nested(parent map[string]interface{}, tables [][]map[string]interface{}) map[string]interface{} {
	doc := make(map[string]interface{}, len(l.columns)+len(l.tables))
	for _, c := range l.columns {
		doc[c.field] = parent[c.field]
	}
	for i, t := range l.tables {
		rows := make([]interface{}, 0, len(tables[i]))
		for _, r := range tables[i] {
			row := make(map[string]interface{}, len(t.columns))
			for _, c := range t.columns {
				row[c.field] = r[c.field]
			}
			rows = append(rows, row)
		}
		doc[t.field] = rows
	}
	return doc
}

// flatten lays one document out as Frappe's exporter does
// (exporter.py add_data_row): the first row has the document's values and
// the first row of each table; row i has the i-th row of each table that
// has one and blank document columns, which tells the importer it belongs
// to the same document. Every row has every column.
func (l exportLayout) flatten(parent map[string]interface{}, tables [][]map[string]interface{}) [][]string {
	n := 1
	for _, rows := range tables {
		n = max(n, len(rows))
	}
	width := len(l.columns)
	for _, t := range l.tables {
		width += len(t.columns)
	}
	out := make([][]string, n)
	for i := range out {
		out[i] = make([]string, width)
	}
	for j, c := range l.columns {
		out[0][j] = exportCell(c, parent[c.field])
	}
	at := len(l.columns)
	for ti, t := range l.tables {
		for i, r := range tables[ti] {
			for j, c := range t.columns {
				out[i][at+j] = exportCell(c, r[c.field])
			}
		}
		at += len(t.columns)
	}
	return out
}

// exportCell is a value as Frappe's CSV exporter writes it: Duration
// through format_duration, then every text through
// escape_formula_injection (numbers are not text there, so not escaped).
func exportCell(c exportColumn, v interface{}) string {
	if c.fieldtype == "Duration" {
		v = formatDuration(v, c.hideDays)
	}
	if s, ok := v.(string); ok {
		return escapeFormula(s)
	}
	return output.Cell(v)
}

// formulaTriggers are the first characters a spreadsheet reads as a
// formula (frappe/utils/csvutils.py FORMULA_TRIGGER_CHARS).
const formulaTriggers = "=+-@\t\r"

// escapeFormula is Frappe's escape_formula_injection: a leading ' before a
// formula trigger. Frappe's importer removes it again
// (unescape_formula_injection), v15 and v16 alike.
func escapeFormula(s string) string {
	if s != "" && strings.IndexByte(formulaTriggers, s[0]) >= 0 {
		return "'" + s
	}
	return s
}

// formatDuration is Frappe's format_duration(flt(value), hide_days)
// (frappe/utils/data.py): whole seconds (truncated) as "1d 2h 3m 4s",
// leaving out zero parts, days folded into hours with hideDays, "" for 0.
func formatDuration(v interface{}, hideDays bool) string {
	f := 0.0
	if v != nil {
		if p, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(v)), 64); err == nil && !math.IsNaN(p) && !math.IsInf(p, 0) {
			f = p
		}
	}
	secs := int64(f) // cint: toward zero
	negative := secs < 0
	if negative {
		secs = -secs
	}
	var days, hours int64
	if hideDays {
		hours = secs / 3600
	} else {
		days, hours = secs/86400, secs%86400/3600
	}
	minutes, seconds := secs%3600/60, secs%60
	var parts []string
	for _, p := range []struct {
		n    int64
		unit string
	}{{days, "d"}, {hours, "h"}, {minutes, "m"}, {seconds, "s"}} {
		if p.n != 0 {
			parts = append(parts, strconv.FormatInt(p.n, 10)+p.unit)
		}
	}
	out := strings.Join(parts, " ")
	if negative && out != "" {
		out = "-" + out
	}
	return out
}

// saveTemplate downloads Frappe's own template or export
// (download_template) with the layout's columns and saves it to out ("-":
// stdout, where writeBody refuses a binary answer on a terminal).
func saveTemplate(ctx context.Context, c *client.FrappeClient, l exportLayout, o client.TemplateOptions, out string, force bool) error {
	o.Fields = l.templateFields()
	resp, err := fetchStream(fmt.Sprintf("Building the %s file on the site…", l.doctype), func() (*client.RawResponse, error) {
		return c.DownloadTemplate(ctx, l.doctype, o)
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.Status >= 400 {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if err != nil {
			return fmt.Errorf("reading the error response: %w", err)
		}
		return client.ResponseError(resp.Status, body)
	}
	if o.FileType == "Excel" {
		if err := checkXLSX(resp); err != nil {
			return err
		}
	}
	return saveDownload(resp, out, force, 0o600)
}

// checkXLSX refuses an answer that does not start like an .xlsx file (a
// zip archive, "PK").
func checkXLSX(resp *client.RawResponse) error {
	br := bufio.NewReader(resp.Body)
	head, err := br.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading the workbook: %w", err)
	}
	if string(head) != "PK" {
		return fmt.Errorf("the site's answer (%s) is not an Excel workbook; nothing was saved", resp.Header.Get("Content-Type"))
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{br, resp.Body}
	return nil
}

func init() {
	exportCmd.Flags().StringVarP(&exDoctype, "doctype", "d", "", "DocType to export (required)")
	exportCmd.Flags().StringVar(&exFilters, "filters", "", `Filter expression as JSON: '{"status":"Open"}' or '[["status","=","Open"]]'`)
	exportCmd.Flags().StringVar(&exOrderBy, "order-by", "", `Order of the documents (default "creation asc, name asc")`)
	exportCmd.Flags().IntVarP(&exLimit, "limit", "l", 0, "Maximum documents to export (0 = all)")
	exportCmd.Flags().IntVar(&exPageSize, "page-size", 500, "Documents per request")
	exportCmd.Flags().StringVarP(&exOut, "output-file", "o", "", `File to write ("-" or none: stdout)`)
	exportCmd.Flags().BoolVar(&exXLSX, "xlsx", false, "Ask the site for an Excel workbook (Frappe's Data Import export)")
	exportCmd.Flags().BoolVar(&exForce, "force", false, "Replace an existing output file")
	exSelect.register(exportCmd)
	exportCmd.MarkFlagsMutuallyExclusive("xlsx", "limit")
	exportCmd.MarkFlagsMutuallyExclusive("xlsx", "order-by")
	exportCmd.MarkFlagsMutuallyExclusive("xlsx", "page-size")
	_ = exportCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(exportCmd)
}
