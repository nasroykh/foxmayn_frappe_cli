package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// Import (T3.1) file reading: the header, the documents, the values. It
// mirrors Frappe's Data Import (frappe/core/doctype/data_import/importer.py,
// v16) where ffc can; import.go writes.

// importField is what a column (or a JSON key) sets.
type importField struct {
	table  string // "" for the DocType, else the table fieldname
	field  string // the fieldname; "name" for the ID
	f      client.FormField
	ignore bool // a standard column ffc does not set (owner, modified, ...)
}

func (f importField) key() string {
	if f.table == "" {
		return f.field
	}
	return f.table + "." + f.field
}

// importTable is a table of the DocType.
type importTable struct {
	field   client.FormField
	doctype string
	fields  map[string]client.FormField
}

// importMeta is what import needs to know of the DocType.
type importMeta struct {
	doctype     string
	autoname    string
	submittable bool
	order       []client.FormField // the DocType's data fields, form order
	fields      map[string]client.FormField
	tableOrder  []string
	tables      map[string]importTable
	childOrder  map[string][]client.FormField
}

func newImportMeta(doctype string, metas map[string]*client.FormMeta) (*importMeta, error) {
	meta := metas[doctype]
	if meta == nil {
		return nil, fmt.Errorf("no meta for %s", doctype)
	}
	m := &importMeta{doctype: doctype, autoname: meta.Autoname, submittable: meta.IsSubmittable,
		fields: map[string]client.FormField{}, tables: map[string]importTable{}, childOrder: map[string][]client.FormField{}}
	for _, f := range meta.Fields {
		switch {
		case isTableType(f.Fieldtype):
			child := metas[f.Options]
			if child == nil {
				continue // the site sent no meta for it: its columns are unknown
			}
			t := importTable{field: f, doctype: child.Name, fields: map[string]client.FormField{}}
			for _, cf := range child.Fields {
				if !noDataTypes[cf.Fieldtype] {
					t.fields[cf.Fieldname] = cf
					m.childOrder[f.Fieldname] = append(m.childOrder[f.Fieldname], cf)
				}
			}
			m.tables[f.Fieldname] = t
			m.tableOrder = append(m.tableOrder, f.Fieldname)
		case !noDataTypes[f.Fieldtype]:
			m.fields[f.Fieldname] = f
			m.order = append(m.order, f)
		}
	}
	return m, nil
}

// autonameField is the field a "field:x" naming rule names documents by.
func (m *importMeta) autonameField() string {
	if f, ok := strings.CutPrefix(m.autoname, "field:"); ok {
		return strings.TrimSpace(f)
	}
	return ""
}

// keepsName reports whether a name sent with an insert is kept: the REST
// API's set_new_name (frappe/model/naming.py:158) drops it unless the
// naming rule is prompt or UUID; Data Import keeps it only because it runs
// with frappe.flags.in_import.
func (m *importMeta) keepsName() bool {
	a := strings.ToLower(m.autoname)
	return a == "prompt" || a == "uuid"
}

// importStandard are standard columns ffc accepts and leaves out: Frappe's
// importer matches owner and docstatus (get_standard_fields), export
// --fields can write the rest.
var (
	importStandard      = []string{"owner", "docstatus", "creation", "modified", "modified_by", "idx"}
	importStandardLabel = map[string]string{"owner": "Owner", "docstatus": "Document Status"}
	importChildStandard = []string{"parent", "parenttype", "parentfield", "idx"}
	importChildLabel    = map[string]string{"parent": "Parent", "parenttype": "Parent Type", "parentfield": "Parent Field", "idx": "Row Index"}
)

// headerMap is the column headers Frappe's importer matches
// (build_fields_dict_for_column_matching, importer.py:2106), untranslated,
// built in the same order so the same header wins: "name" and "ID"; a
// field's label (the first field with it), fieldname and "Label
// (fieldname)"; for a table "items.name", "ID (Items)", "items.qty" and
// "Qty (Items)" (the table's label, else its fieldname). "ID (Label)" of
// a "field:x" naming rule is that field. Unlike Frappe, "name" and "ID"
// always mean the document's name (see import.go).
func (m *importMeta) headerMap() map[string]importField {
	out := map[string]importField{}
	set := func(h string, f importField, overwrite bool) {
		if h == "" {
			return
		}
		if _, ok := out[h]; ok && !overwrite {
			return
		}
		out[h] = f
	}
	name := importField{field: "name"}
	set("name", name, true)
	set("ID", name, true)
	for _, s := range importStandard {
		f := importField{field: s, ignore: true}
		set(importStandardLabel[s], f, false)
		set(s, f, true)
	}
	for _, f := range m.order {
		label := strings.TrimSpace(f.Label)
		col := importField{field: f.Fieldname, f: f}
		set(label, col, false)
		set(f.Fieldname, col, true)
		if label != "" {
			set(label+" ("+f.Fieldname+")", col, true)
		}
	}
	for _, t := range m.tableOrder {
		tbl := m.tables[t]
		ref := strings.TrimSpace(tbl.field.Label)
		if ref == "" {
			ref = t
		}
		rowName := importField{table: t, field: "name"}
		set(t+".name", rowName, true)
		set("ID ("+ref+")", rowName, true)
		for _, s := range importChildStandard {
			f := importField{table: t, field: s, ignore: true}
			set(t+"."+s, f, true)
			set(importChildLabel[s]+" ("+ref+")", f, true)
		}
		for _, f := range m.childOrder[t] {
			col := importField{table: t, field: f.Fieldname, f: f}
			set(t+"."+f.Fieldname, col, true)
			if label := strings.TrimSpace(f.Label); label != "" {
				set(label+" ("+ref+")", col, true)
			}
		}
	}
	if af := m.autonameField(); af != "" {
		if f, ok := m.fields[af]; ok && strings.TrimSpace(f.Label) != "" {
			set("ID ("+strings.TrimSpace(f.Label)+")", importField{field: af, f: f}, true)
		}
	}
	return out
}

// importValue is one value of the file: raw while parsing (a string, or a
// JSON value), converted by convert.
type importValue struct {
	v   interface{}
	col string // the header (CSV) or the key path (JSON), for problems
}

// importRow is a document's own values or a table row: fieldname → value,
// blank values left out. "name" is the document's or the row's name.
type importRow struct {
	line   int
	values map[string]*importValue
}

func (r importRow) name() string {
	if v := r.values["name"]; v != nil {
		return strings.TrimSpace(cellText(v.v))
	}
	return ""
}

// importDoc is one document of the file.
type importDoc struct {
	line   int // where it starts: the CSV line, the JSON item or NDJSON line
	parent importRow
	tables map[string][]importRow // only the tables it has rows for
}

// importProblem is a value (or document) that cannot be imported.
type importProblem struct {
	Row    int    `json:"row"`
	Column string `json:"column,omitempty"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason"`
}

// importFile is a parsed file.
type importFile struct {
	docs     []*importDoc
	unit     string // what a row number counts: "line" or "item"
	hasName  bool   // the file has a name column (CSV) or key (JSON)
	ignored  []string
	untitled int // columns without a header that hold values
	problems []importProblem
}

// cellText is a raw value as text: strings as they are, JSON numbers as
// written.
func cellText(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case nil:
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// unescapeFormula is csvutils.unescape_formula_injection (csvutils.py:27):
// a leading ' before a formula trigger is removed, so an export comes back
// as it was. A value that really starts with such a quote loses it, as in
// Frappe.
func unescapeFormula(s string) string {
	if len(s) > 1 && s[0] == '\'' && strings.IndexByte(formulaTriggers, s[1]) >= 0 {
		return s[1:]
	}
	return s
}

// importHeaderHint explains the header forms.
const importHeaderHint = `a column is a fieldname (customer), a label (Customer), "Label (fieldname)", name or ID; a table column is "items.qty", "Qty (Items)", "items.name" or "ID (Items)"`

// parseImportCSV reads a CSV file in Data Import's layout
// (parse_data_from_template, importer.py:823; parse_next_row_for_import,
// :971): blank rows are skipped, the first row is the header; a row with a
// value in any of the DocType's columns starts a document, a row without
// one adds table rows to the document above. Cells are trimmed, unescaped
// (read_csv_content, csvutils.py:73-131) and empty means not set.
func parseImportCSV(data []byte, m *importMeta) (*importFile, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return nil, usageErrorf("the file is not UTF-8 text: save it as CSV UTF-8")
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true // like Python's csv module, a stray quote is text
	out := &importFile{unit: "line"}
	var cols []importField
	var headers []string
	untitledUsed := map[int]bool{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, usageErrorf("reading the CSV: %v", err)
		}
		line, _ := r.FieldPos(0)
		blank := true
		for i, c := range rec {
			rec[i] = unescapeFormula(strings.TrimSpace(c))
			if rec[i] != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		if cols == nil {
			headers = rec
			if cols, err = matchImportHeader(rec, m, out); err != nil {
				return nil, err
			}
			continue
		}
		var parent importRow
		rows := map[string]*importRow{}
		for j, cell := range rec {
			if cell == "" {
				continue
			}
			if j >= len(cols) {
				out.problems = append(out.problems, importProblem{Row: line, Column: fmt.Sprintf("column %d", j+1), Value: cell,
					Reason: "the row has more cells than the header"})
				continue
			}
			c := cols[j]
			if c.field == "" {
				untitledUsed[j] = true
				continue
			}
			if c.ignore {
				continue
			}
			target := &parent
			if c.table != "" {
				if rows[c.table] == nil {
					rows[c.table] = &importRow{line: line, values: map[string]*importValue{}}
				}
				target = rows[c.table]
			}
			if target.values == nil {
				target.values = map[string]*importValue{}
			}
			target.values[c.field] = &importValue{v: cell, col: headers[j]}
		}
		parent.line = line
		switch {
		case len(parent.values) > 0:
			out.docs = append(out.docs, &importDoc{line: line, parent: parent, tables: map[string][]importRow{}})
		case len(rows) == 0:
			continue // only ignored or untitled columns hold values
		case len(out.docs) == 0:
			out.problems = append(out.problems, importProblem{Row: line,
				Reason: "the first row has no value in the document's columns: table rows continue the document above them"})
			continue
		}
		d := out.docs[len(out.docs)-1]
		for _, t := range m.tableOrder {
			if row := rows[t]; row != nil {
				d.tables[t] = append(d.tables[t], *row)
			}
		}
	}
	if cols == nil {
		return nil, usageErrorf("the file is empty: a header row and at least one document are needed")
	}
	out.untitled = len(untitledUsed)
	return out, nil
}

// matchImportHeader maps each header to a field. Unknown headers,
// two columns for one field and a table named without a field are usage
// errors, all reported at once. A blank header is skipped, as Frappe does.
func matchImportHeader(header []string, m *importMeta, out *importFile) ([]importField, error) {
	known := m.headerMap()
	cols := make([]importField, len(header))
	seen := map[string]int{}
	var bad []string
	for j, h := range header {
		if h == "" {
			continue
		}
		c, ok := known[h]
		if !ok {
			if _, isTable := m.tables[h]; isTable {
				bad = append(bad, fmt.Sprintf("column %d %q is a table: name its fields (%s.<field>)", j+1, h, h))
			} else {
				bad = append(bad, fmt.Sprintf("column %d %q is not a field of %s", j+1, h, m.doctype))
			}
			continue
		}
		if prev, dup := seen[c.key()]; dup {
			bad = append(bad, fmt.Sprintf("columns %d %q and %d %q both set %s", prev+1, header[prev], j+1, h, c.key()))
			continue
		}
		seen[c.key()] = j
		cols[j] = c
		if c.ignore {
			out.ignored = append(out.ignored, h)
		}
		if c.field == "name" && c.table == "" {
			out.hasName = true
		}
	}
	if len(bad) > 0 {
		return nil, usageErrorf("the header does not match %s:\n  %s\n(%s)", m.doctype, strings.Join(bad, "\n  "), importHeaderHint)
	}
	return cols, nil
}

// parseImportJSON reads a JSON array of documents, or NDJSON (one per
// line), as ffc export writes them: fieldnames as keys, each table an array
// of rows. null and "" mean not set.
func parseImportJSON(data []byte, ndjson bool, m *importMeta) (*importFile, error) {
	out := &importFile{unit: "item"}
	type item struct {
		row int
		raw json.RawMessage
	}
	var items []item
	if ndjson {
		out.unit = "line"
		for i, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				items = append(items, item{i + 1, json.RawMessage(line)})
			}
		}
	} else {
		var elems []json.RawMessage
		if err := json.Unmarshal(data, &elems); err != nil {
			return nil, usageErrorf("expected a JSON array of documents: %v", err)
		}
		for i, e := range elems {
			items = append(items, item{i + 1, e})
		}
	}
	if len(items) == 0 {
		return nil, usageErrorf("the file has no documents")
	}
	decode := func(raw []byte, v interface{}) error {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(v); err != nil {
			return err
		}
		if dec.More() {
			return errors.New("unexpected data after the object")
		}
		return nil
	}
	ignored := map[string]bool{}
	unknown := map[string]bool{}
	var bad []string
	blank := func(v interface{}) bool {
		s, ok := v.(string)
		return v == nil || ok && strings.TrimSpace(s) == ""
	}
	for _, it := range items {
		var obj map[string]interface{}
		if err := decode(it.raw, &obj); err != nil || obj == nil {
			bad = append(bad, fmt.Sprintf("%s %d is not a JSON object", out.unit, it.row))
			continue
		}
		d := &importDoc{line: it.row, parent: importRow{line: it.row, values: map[string]*importValue{}}, tables: map[string][]importRow{}}
		for _, k := range objectKeys(obj) {
			v := obj[k]
			if t, isTable := m.tables[k]; isTable {
				if v == nil {
					continue
				}
				list, ok := v.([]interface{})
				if !ok {
					bad = append(bad, fmt.Sprintf("%s %d: %s is a table: give an array of rows", out.unit, it.row, k))
					continue
				}
				for i, r := range list {
					row, ok := r.(map[string]interface{})
					if !ok {
						bad = append(bad, fmt.Sprintf("%s %d: %s[%d] is not an object", out.unit, it.row, k, i+1))
						continue
					}
					ir := importRow{line: it.row, values: map[string]*importValue{}}
					for _, rk := range objectKeys(row) {
						rv := row[rk]
						_, isField := t.fields[rk]
						switch {
						case rk == "name" || isField:
							if !blank(rv) {
								ir.values[rk] = &importValue{v: rv, col: fmt.Sprintf("%s[%d].%s", k, i+1, rk)}
							}
						case contains(importChildStandard, rk, false) || rk == "doctype" || strings.HasPrefix(rk, "_"):
							ignored[k+"."+rk] = true
						default:
							unknown[k+"."+rk] = true
						}
					}
					if len(ir.values) > 0 {
						d.tables[k] = append(d.tables[k], ir)
					}
				}
				continue
			}
			if _, isField := m.fields[k]; isField || k == "name" {
				if k == "name" {
					out.hasName = true
				}
				if !blank(v) {
					d.parent.values[k] = &importValue{v: v, col: k}
				}
				continue
			}
			if contains(importStandard, k, false) || k == "doctype" || strings.HasPrefix(k, "_") {
				ignored[k] = true
				continue
			}
			unknown[k] = true
		}
		if len(d.parent.values) == 0 && len(d.tables) == 0 {
			out.problems = append(out.problems, importProblem{Row: it.row, Reason: "the document has no values"})
			continue
		}
		out.docs = append(out.docs, d)
	}
	for k := range unknown {
		bad = append(bad, fmt.Sprintf("%q is not a field of %s", k, m.doctype))
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, usageErrorf("the documents do not match %s:\n  %s", m.doctype, strings.Join(bad, "\n  "))
	}
	for k := range ignored {
		out.ignored = append(out.ignored, k)
	}
	sort.Strings(out.ignored)
	return out, nil
}

func objectKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ─── Values ──────────────────────────────────────────────────────────────────

// fieldOf is the meta field of a fieldname of the DocType or a table.
func (m *importMeta) fieldOf(table, field string) client.FormField {
	if table == "" {
		return m.fields[field]
	}
	return m.tables[table].fields[field]
}

// eachValue calls fn on every value of the file but the names, document by
// document, row by row.
func (f *importFile) eachValue(m *importMeta, fn func(d *importDoc, line int, table, field string, v *importValue)) {
	for _, d := range f.docs {
		for _, k := range sortedValueKeys(d.parent.values) {
			if k != "name" {
				fn(d, d.parent.line, "", k, d.parent.values[k])
			}
		}
		for _, t := range m.tableOrder {
			for _, r := range d.tables[t] {
				for _, k := range sortedValueKeys(r.values) {
					if k != "name" {
						fn(d, r.line, t, k, r.values[k])
					}
				}
			}
		}
	}
}

func sortedValueKeys(m map[string]*importValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// convert turns every value into what Frappe stores, as parse_value
// (importer.py:1693) and validate_value (:1614) do, and reports the ones
// that cannot be: Check words, numbers, dates in the column's format,
// Durations, Select options. A failed value is left out of its document.
func (f *importFile) convert(m *importMeta) {
	// One date format per column, the most common one its values match
	// (guess_date_format_for_column, importer.py:1927).
	type colKey struct{ table, field string }
	dateValues := map[colKey][]string{}
	f.eachValue(m, func(_ *importDoc, _ int, table, field string, v *importValue) {
		ft := m.fieldOf(table, field).Fieldtype
		if s, ok := v.v.(string); ok && (ft == "Date" || ft == "Datetime") {
			k := colKey{table, field}
			dateValues[k] = append(dateValues[k], strings.TrimSpace(s))
		}
	})
	formats := map[colKey]string{}
	for k, vals := range dateValues {
		formats[k] = columnDateFormat(vals)
	}
	f.eachValue(m, func(d *importDoc, line int, table, field string, v *importValue) {
		conv, err := convertImportValue(m.fieldOf(table, field), v.v, formats[colKey{table, field}])
		if err != nil {
			f.problems = append(f.problems, importProblem{Row: line, Column: v.col, Value: cellText(v.v), Reason: err.Error()})
			v.v = nil
			return
		}
		v.v = conv
	})
	// Drop the values that failed, so nothing half-converted is sent.
	for _, d := range f.docs {
		dropNil(d.parent.values)
		for _, rows := range d.tables {
			for _, r := range rows {
				dropNil(r.values)
			}
		}
	}
}

func dropNil(m map[string]*importValue) {
	for k, v := range m {
		if v.v == nil {
			delete(m, k)
		}
	}
}

var (
	plainNumberRe = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)
	jsonNumberRe  = regexp.MustCompile(`^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$`)
	// durationRe is DURATION_PATTERN (importer.py:32) with the parts
	// captured; ffc also takes a leading "-", which its export writes for
	// a negative Duration.
	durationRe = regexp.MustCompile(`^(?:(\d+)d)?(?:(?:^|\s)(\d+)h)?(?:(?:^|\s)(\d+)m)?(?:(?:^|\s)(\d+)s)?$`)
)

// convertImportValue converts one value for its field. Strings come from
// CSV cells (trimmed) or JSON; JSON numbers and booleans are taken where
// they fit.
func convertImportValue(f client.FormField, v interface{}, dateFormat string) (interface{}, error) {
	switch v.(type) {
	case map[string]interface{}, []interface{}:
		return nil, errors.New("expected a single value, not an object or array")
	}
	s := strings.TrimSpace(cellText(v))
	switch f.Fieldtype {
	case "Check":
		if b, ok := v.(bool); ok {
			if b {
				return json.Number("1"), nil
			}
			return json.Number("0"), nil
		}
		switch strings.ToLower(s) {
		case "t", "true", "y", "yes":
			return json.Number("1"), nil
		case "f", "false", "n", "no":
			return json.Number("0"), nil
		}
		if n, ok := wholeNumber(s); ok {
			return n, nil
		}
		return nil, errors.New("not a Check value: use 1 or 0, yes or no, true or false")
	case "Int", "Long Int":
		if n, ok := wholeNumber(s); ok {
			return n, nil
		}
		return nil, errors.New("not a whole number")
	case "Float", "Currency", "Percent":
		if n, ok := plainNumber(s); ok {
			return n, nil
		}
		return nil, errors.New("not a number: write it plain, like 1234.56")
	case "Date", "Datetime":
		if _, ok := v.(string); !ok {
			return nil, fmt.Errorf("expected a %s as text", strings.ToLower(f.Fieldtype))
		}
		format := dateFormat
		if format == "" {
			format = "%Y-%m-%d" // importer.py:1993
		}
		t, ok := strptime(s, format)
		if !ok {
			// Seconds with and without a fraction are one format to ffc:
			// the site sends a fraction only when it is not zero, so an
			// export mixes both in one column.
			if strings.Contains(format, "%S.%f") {
				t, ok = strptime(s, strings.Replace(format, "%S.%f", "%S", 1))
			} else if strings.Contains(format, "%S") {
				t, ok = strptime(s, strings.Replace(format, "%S", "%S.%f", 1))
			}
		}
		if !ok {
			return nil, fmt.Errorf("not a valid %s: use %s, the format of the column", strings.ToLower(f.Fieldtype), userDateFormat(format))
		}
		if f.Fieldtype == "Date" {
			return t.Format("2006-01-02"), nil
		}
		out := t.Format("2006-01-02 15:04:05")
		if t.Nanosecond() != 0 {
			out += fmt.Sprintf(".%06d", t.Nanosecond()/1000)
		}
		return out, nil
	case "Duration":
		if n, ok := v.(json.Number); ok {
			if p, ok := plainNumber(n.String()); ok {
				return p, nil // JSON: seconds, as the site sends them
			}
		}
		if secs, ok := durationSeconds(s); ok {
			return json.Number(strconv.FormatInt(secs, 10)), nil
		}
		return nil, errors.New(`not a duration: use "1d 2h 3m 4s" (any of the parts, in that order)`)
	case "Select":
		var options []string
		for _, o := range strings.Split(f.Options, "\n") {
			if o != "" {
				options = append(options, o)
			}
		}
		if len(options) > 0 && !contains(options, s, false) {
			return nil, fmt.Errorf("not one of the options: %s", strings.Join(options, ", "))
		}
		return s, nil
	case "Link", "Dynamic Link":
		return s, nil
	}
	if _, ok := v.(bool); ok {
		return nil, errors.New("expected text, not true or false")
	}
	return v, nil
}

// wholeNumber is s as an integer literal. Frappe's cint truncates 2.5 to
// 2; ffc refuses it instead of changing the value.
func wholeNumber(s string) (json.Number, bool) {
	if !plainNumberRe.MatchString(s) {
		return "", false
	}
	if n, err := strconv.ParseInt(strings.TrimPrefix(s, "+"), 10, 64); err == nil {
		return json.Number(strconv.FormatInt(n, 10)), true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) >= 1<<63 {
		return "", false
	}
	return json.Number(strconv.FormatInt(int64(f), 10)), true
}

// plainNumber is s as a JSON number: the literal when it is one, else its
// value. Grouped numbers (1,234.56) are refused.
func plainNumber(s string) (json.Number, bool) {
	if !plainNumberRe.MatchString(s) {
		return "", false
	}
	if jsonNumberRe.MatchString(s) {
		return json.Number(s), true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return "", false
	}
	return json.Number(strconv.FormatFloat(f, 'f', -1, 64)), true
}

// durationSeconds is frappe.utils.duration_to_seconds (utils/data.py:836)
// of a value DURATION_PATTERN accepts, with an optional leading "-".
func durationSeconds(s string) (int64, bool) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	g := durationRe.FindStringSubmatch(s)
	if s == "" || g == nil {
		return 0, false
	}
	var total int64
	for i, unit := range []int64{86400, 3600, 60, 1} {
		if g[i+1] == "" {
			continue
		}
		n, err := strconv.ParseInt(g[i+1], 10, 64)
		if err != nil {
			return 0, false
		}
		total += n * unit
	}
	if neg {
		total = -total
	}
	return total, true
}

// ─── Dates: frappe.utils.guess_date_format and Python's strptime ──────────

// dateFormats and timeFormats are guess_date_format's lists
// (utils/data.py:2652-2688), in its order: day first wins over month first.
var (
	dateFormats = []string{
		"%d/%b/%y", "%d/%b/%Y", "%d %b %Y", "%d %B %Y", "%d-%b-%Y", "%d-%b-%y",
		"%d-%m-%Y", "%m-%d-%Y", "%Y-%m-%d", "%d-%m-%y", "%m-%d-%y", "%y-%m-%d", "%y-%b-%d",
		"%d/%m/%Y", "%m/%d/%Y", "%Y/%m/%d", "%d/%m/%y", "%m/%d/%y", "%y/%m/%d",
		"%d.%m.%Y", "%m.%d.%Y", "%Y.%m.%d", "%d.%m.%y", "%m.%d.%y", "%y.%m.%d",
	}
	timeFormats = []string{"%H:%M:%S.%f", "%H:%M:%S", "%H:%M", "%I:%M:%S.%f %p", "%I:%M:%S %p", "%I:%M %p"}
)

// guessDateFormat is frappe.utils.guess_date_format: the first date
// format, else time format, s matches; else a date and a time format for
// the parts around the first space; else "".
func guessDateFormat(s string) string {
	s = strings.TrimSpace(s)
	first := func(formats []string, v string) string {
		for _, f := range formats {
			if _, ok := strptime(v, f); ok {
				return f
			}
		}
		return ""
	}
	if f := first(dateFormats, s); f != "" {
		return f
	}
	if f := first(timeFormats, s); f != "" {
		return f
	}
	if d, t, ok := strings.Cut(s, " "); ok {
		df, tf := first(dateFormats, d), first(timeFormats, t)
		if df != "" && tf != "" {
			return df + " " + tf
		}
	}
	return ""
}

// columnDateFormat is the format most of a column's values match; a tie
// goes to the format seen first (Frappe's pick among equals is arbitrary).
func columnDateFormat(values []string) string {
	byValue := map[string]string{}
	count := map[string]int{}
	var order []string
	for _, v := range values {
		f, ok := byValue[v]
		if !ok {
			f = guessDateFormat(v)
			byValue[v] = f
		}
		if f == "" {
			continue
		}
		if count[f] == 0 {
			order = append(order, f)
		}
		count[f]++
	}
	best := ""
	for _, f := range order {
		if count[f] > count[best] {
			best = f
		}
	}
	return best
}

// userDateFormat is importer.py get_user_format: %Y → yyyy and so on.
func userDateFormat(f string) string {
	return strings.NewReplacer("%Y", "yyyy", "%y", "yy", "%m", "mm", "%d", "dd").Replace(f)
}

// strptimeDirectives are the patterns of Python's _strptime for the
// directives guess_date_format uses (C locale).
var strptimeDirectives = map[byte]string{
	'd': `(?P<d>3[01]|[12]\d|0[1-9]|[1-9]| [1-9])`,
	'm': `(?P<m>1[0-2]|0[1-9]|[1-9])`,
	'Y': `(?P<Y>\d\d\d\d)`,
	'y': `(?P<y>\d\d)`,
	'b': `(?P<b>jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)`,
	'B': `(?P<B>january|february|march|april|may|june|july|august|september|october|november|december)`,
	'H': `(?P<H>2[0-3]|[0-1]\d|\d)`,
	'I': `(?P<I>1[0-2]|0[1-9]|[1-9])`,
	'M': `(?P<M>[0-5]\d|\d)`,
	'S': `(?P<S>6[0-1]|[0-5]\d|\d)`,
	'f': `(?P<f>[0-9]{1,6})`,
	'p': `(?P<p>am|pm)`,
}

var (
	strptimeMu    sync.Mutex
	strptimeCache = map[string]*regexp.Regexp{}
)

func strptimeRegexp(format string) *regexp.Regexp {
	strptimeMu.Lock()
	defer strptimeMu.Unlock()
	if re, ok := strptimeCache[format]; ok {
		return re
	}
	var b strings.Builder
	b.WriteString(`(?i)^`)
	for i := 0; i < len(format); i++ {
		c := format[i]
		switch {
		case c == '%' && i+1 < len(format):
			i++
			if p, ok := strptimeDirectives[format[i]]; ok {
				b.WriteString(p)
			} else {
				b.WriteString(regexp.QuoteMeta(format[i-1 : i+1]))
			}
		case c == ' ':
			b.WriteString(`\s+`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`$`)
	re := regexp.MustCompile(b.String())
	strptimeCache[format] = re
	return re
}

var monthNames = []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}

// strptime is Python's datetime.strptime for those directives: the whole
// string must match and the date must exist. Missing parts default to
// 1900-01-01 00:00:00.
func strptime(s, format string) (time.Time, bool) {
	re := strptimeRegexp(format)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	get := map[string]string{}
	for i, n := range re.SubexpNames() {
		if n != "" {
			get[n] = strings.TrimSpace(m[i])
		}
	}
	num := func(k string, def int) int {
		if v := get[k]; v != "" {
			n, _ := strconv.Atoi(v)
			return n
		}
		return def
	}
	year, month, day := num("Y", 1900), num("m", 1), num("d", 1)
	if y := get["y"]; y != "" {
		n, _ := strconv.Atoi(y)
		if n < 69 {
			year = 2000 + n
		} else {
			year = 1900 + n
		}
	}
	for _, k := range []string{"b", "B"} {
		if v := strings.ToLower(get[k]); v != "" {
			for i, name := range monthNames {
				if name == v || name[:3] == v {
					month = i + 1
				}
			}
		}
	}
	hour := num("H", 0)
	if get["I"] != "" {
		hour = num("I", 0) % 12
		if strings.EqualFold(get["p"], "pm") {
			hour += 12
		}
	}
	minute, sec := num("M", 0), num("S", 0)
	if sec > 59 {
		return time.Time{}, false
	}
	micro := 0
	if f := get["f"]; f != "" {
		micro, _ = strconv.Atoi(f + strings.Repeat("0", 6-len(f)))
	}
	t := time.Date(year, time.Month(month), day, hour, minute, sec, micro*1000, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return time.Time{}, false // 31-02-2026
	}
	return t, true
}
