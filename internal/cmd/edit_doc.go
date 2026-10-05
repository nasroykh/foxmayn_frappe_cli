package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// edit-doc flags
var (
	edDoctype string
	edName    string
	edKeys    string
	edYes     bool
)

// Test hooks: whether prompts are off, and the editor run on the file.
var (
	editInputDisabled = inputDisabled
	runEditor         = editorRun
)

var editDocCmd = &cobra.Command{
	Use:   "edit-doc",
	Short: "Edit a document in your editor",
	Long: `Open a document's editable fields as YAML in $VISUAL or $EDITOR (vi, or
notepad on Windows), then save what you changed, like kubectl edit.

Read-only, hidden, computed and system fields are left out, and so are
Password fields; on a submitted document only the fields allowed on submit
are shown. Child tables are lists of rows keyed by the row "name": delete a
row to remove it, add one without "name" to add it. A field you delete from
the file is not changed; set it to null to clear it.

ffc shows the changes and asks before saving (--yes skips the question). Only
the changed fields are sent, with the document's "modified" timestamp: if
someone saved the document after you opened it, nothing is saved (exit 6).
A changed child table is sent whole, because Frappe replaces the table with
the rows it gets. A file with a YAML error opens again with the error on
top; save it unchanged to give up. An unchanged or empty file cancels.

It needs a terminal: with --no-input or without one it fails.

Examples:
  ffc edit-doc -d ToDo -n TD-0001
  EDITOR="code --wait" ffc edit-doc -d "Sales Order" -n SO-0001
  ffc edit-doc -d "System Settings"
  ffc edit-doc -d ToDo -n TD-0001 --dry-run
`,
	Args: cobra.NoArgs,
	RunE: runEditDoc,
}

func runEditDoc(cmd *cobra.Command, _ []string) error {
	if editInputDisabled() {
		return &usageError{fmt.Errorf("edit-doc opens an editor: %w", errNoInput)}
	}
	ctx := cmd.Context()
	doctype, name := edDoctype, docNameOrSingle(edName, edDoctype)
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	defer c.CloseQuietly()

	var doc map[string]interface{}
	var metas map[string]*client.FormMeta
	var access *client.FieldAccess
	var apiErr, accessErr error
	spinErr := runSpinner(fmt.Sprintf("Reading %s %s…", doctype, name), func() {
		if doc, apiErr = c.GetDoc(ctx, doctype, name); apiErr == nil {
			if metas, apiErr = c.FormMetas(ctx, doctype); apiErr == nil {
				access, accessErr = c.ReadableFields(ctx, doctype)
			}
		}
	})
	if apiErr != nil {
		return apiErr
	}
	if spinErr != nil {
		return spinErr
	}
	if accessErr != nil {
		if ctx.Err() != nil {
			return accessErr
		}
		// Without the roles, only level-0 fields that are not masked are
		// certainly kept.
		access = nil
		output.PrintWarning(fmt.Sprintf("warning: your permission levels are unknown (%v): only level-0 fields are shown", accessErr))
	}

	form, err := newEditForm(doctype, name, doc, metas, access)
	if err != nil {
		return err
	}
	edited, why, err := form.edit(ctx)
	if err != nil {
		return err
	}
	var d *editDiff
	if edited != nil {
		if d = form.diff(edited); d.empty() {
			why = "no changes made"
		}
	}
	if why != "" {
		fmt.Fprintf(os.Stderr, "Edit cancelled, %s.\n", why)
		if machineOutput() {
			return printResult(map[string]interface{}{"cancelled": true, "reason": why})
		}
		return nil
	}
	form.printDiff(d)
	if !dryRunOn(cmd) && !edYes {
		q := fmt.Sprintf("Save these changes to %s %s?", doctype, name)
		if n := d.removedRows(); n > 0 {
			q = fmt.Sprintf("Save these changes to %s %s? %d child row(s) will be deleted.", doctype, name, n)
		}
		if err := confirm(q); err != nil {
			return err
		}
	}

	var saved map[string]interface{}
	spinErr = runSpinner(fmt.Sprintf("Saving %s %s…", doctype, name), func() {
		saved, apiErr = c.UpdateDoc(ctx, doctype, name, form.body(edited, d))
	})
	if apiErr != nil {
		var plan *client.DryRunError
		if errors.As(apiErr, &plan) {
			plan.Requests[0].Changes = form.planChanges(d)
			return apiErr
		}
		return conflictError(apiErr, doctype, name, "you opened it")
	}
	if spinErr != nil {
		return spinErr
	}
	lost := form.notKept(edited, d, saved)
	for _, l := range lost {
		output.PrintWarning("warning: " + l)
	}
	if machineOutput() {
		return printResult(selectKeys(saved, edKeys))
	}
	if len(lost) > 0 {
		output.PrintWarning(fmt.Sprintf("Updated %s %s, but the site did not keep %d change(s) as sent", doctype, name, len(lost)))
		return nil
	}
	output.PrintSuccess(fmt.Sprintf("Updated %s %s", doctype, name))
	return nil
}

// notKept compares the saved document with the changes sent and describes
// each one the site did not keep: Frappe drops changes to fields the user
// may not write instead of refusing them, and a controller may rewrite a
// value on save.
func (f *editForm) notKept(ed *editValues, d *editDiff, saved map[string]interface{}) []string {
	var out []string
	for _, c := range d.fields {
		if got := saved[c.field]; !sameValue(got, c.to) {
			out = append(out, fmt.Sprintf("%s: sent %s, saved %s", text.Sanitize(c.field), diffValue(c.to), diffValue(got)))
		}
	}
	for _, td := range d.tables {
		sent, got := ed.tables[td.field], rowsOf(saved[td.field])
		if len(got) != len(sent) {
			out = append(out, fmt.Sprintf("%s: sent %d rows, saved %d", text.Sanitize(td.field), len(sent), len(got)))
			continue
		}
		changed := map[string][]fieldChange{}
		for _, rc := range td.changed {
			changed[rc.name] = rc.changes
		}
		for i, r := range sent {
			check := changed[r.name]
			if r.name == "" {
				for _, k := range f.tables[td.field].order {
					if v, ok := r.values[k]; ok {
						check = append(check, fieldChange{field: k, to: v})
					}
				}
			}
			for _, c := range check {
				if v := got[i][c.field]; !sameValue(v, c.to) {
					out = append(out, fmt.Sprintf("%s row %d %s: sent %s, saved %s", text.Sanitize(td.field), i+1,
						text.Sanitize(c.field), diffValue(c.to), diffValue(v)))
				}
			}
		}
	}
	return out
}

// conflictError explains Frappe's TimestampMismatchError: the document was
// saved by someone else after the "modified" timestamp the update sent. The
// API error stays wrapped (exit 6, exc_type in --json).
func conflictError(err error, doctype, name, since string) error {
	var api *client.APIError
	if errors.As(err, &api) && api.ExcType == "TimestampMismatchError" {
		return fmt.Errorf("%s %s was changed on the server since %s; nothing was saved: %w", doctype, name, since, err)
	}
	return err
}

// ─── The editable view of a document ────────────────────────────────────────

// Fieldtypes that hold no value, or one a user cannot set.
var editSkipTypes = map[string]bool{
	"Section Break": true, "Column Break": true, "Tab Break": true, "HTML": true, "Button": true,
	"Heading": true, "Fold": true, "Image": true, "Read Only": true, "Password": true,
}

// Fields never shown: identity, timestamps, tree bounds (a node edited by
// hand corrupts the nested set).
var editSkipFields = map[string]bool{
	"name": true, "owner": true, "creation": true, "modified": true, "modified_by": true, "docstatus": true,
	"idx": true, "doctype": true, "parent": true, "parentfield": true, "parenttype": true, "lft": true, "rgt": true,
}

var editNumericTypes = map[string]bool{
	"Int": true, "Float": true, "Currency": true, "Percent": true, "Check": true, "Rating": true, "Duration": true,
}

var editTableTypes = map[string]bool{"Table": true, "Table MultiSelect": true}

// editTable is an editable child table.
type editTable struct {
	child  string
	fields map[string]client.FormField // editable row fields
	order  []string
	fixed  bool // submitted, and the table is not "allow on submit": rows cannot be added, removed or moved
	rows   map[string]map[string]interface{}
	names  []string // row names, in order
}

// editForm is the editable part of a document.
type editForm struct {
	doctype, name string
	doc           map[string]interface{}
	submitted     bool
	order         []string                    // top-level keys, in form order
	fields        map[string]client.FormField // editable top-level fields (not tables)
	tables        map[string]*editTable
	text          []byte      // the file as first written
	base          *editValues // text parsed back: what "unchanged" means
}

// editValues is a parsed file: the fields and tables it lists.
type editValues struct {
	fields map[string]interface{}
	tables map[string][]editRow
}

type editRow struct {
	name   string // "" for a new row
	values map[string]interface{}
}

// editableField reports whether the user may change f: writable reports
// whether the site keeps a change to it (permission level and mask).
func editableField(f client.FormField, submitted bool, value interface{}, writable bool) bool {
	switch {
	case !writable:
		return false
	case f.Fieldname == "", editSkipFields[f.Fieldname], editSkipTypes[f.Fieldtype], f.ReadOnly, f.Hidden, f.IsVirtual:
		return false
	case f.FetchFrom != "" && !f.FetchIfEmpty: // fetched again on every save
		return false
	case f.SetOnlyOnce && !emptyValue(value):
		return false
	case submitted && !f.AllowOnSubmit:
		return false
	}
	return true
}

func emptyValue(v interface{}) bool {
	return v == nil || v == ""
}

// newEditForm picks the fields the user may edit. access says which fields
// the site keeps a change to; nil (unknown) keeps only level-0 fields that
// are not masked.
func newEditForm(doctype, name string, doc map[string]interface{}, metas map[string]*client.FormMeta, access *client.FieldAccess) (*editForm, error) {
	ds := fmt.Sprint(doc["docstatus"])
	if ds == "2" {
		return nil, &client.StateError{Message: fmt.Sprintf("%s %s is cancelled and cannot be edited; amend it with 'ffc amend-doc'", doctype, name)}
	}
	f := &editForm{doctype: doctype, name: name, doc: doc, submitted: ds == "1",
		fields: map[string]client.FormField{}, tables: map[string]*editTable{}}
	writable := func(fd client.FormField) bool {
		if access == nil {
			return fd.Permlevel == 0 && !fd.Mask
		}
		return access.Writable(fd.Fieldname)
	}
	writableRow := func(table string, fd client.FormField) bool {
		if access == nil {
			return fd.Permlevel == 0 && !fd.Mask
		}
		return access.WritableRow(table, fd.Fieldname)
	}
	for _, fd := range metas[doctype].Fields {
		if !editTableTypes[fd.Fieldtype] {
			if editableField(fd, f.submitted, doc[fd.Fieldname], writable(fd)) {
				f.fields[fd.Fieldname] = fd
				f.order = append(f.order, fd.Fieldname)
			}
			continue
		}
		cm := metas[fd.Options]
		if cm == nil || editSkipFields[fd.Fieldname] || fd.ReadOnly || fd.Hidden || fd.IsVirtual || !writable(fd) {
			continue
		}
		t := &editTable{child: fd.Options, fields: map[string]client.FormField{}, rows: map[string]map[string]interface{}{},
			fixed: f.submitted && !fd.AllowOnSubmit}
		for _, rf := range cm.Fields {
			if !editTableTypes[rf.Fieldtype] && !rf.SetOnlyOnce && editableField(rf, f.submitted, nil, writableRow(fd.Fieldname, rf)) {
				t.fields[rf.Fieldname] = rf
				t.order = append(t.order, rf.Fieldname)
			}
		}
		if f.submitted && len(t.fields) == 0 && t.fixed {
			continue
		}
		for _, r := range rowsOf(doc[fd.Fieldname]) {
			if n, ok := docName(r["name"]); ok {
				t.rows[n] = r
				t.names = append(t.names, n)
			}
		}
		f.tables[fd.Fieldname] = t
		f.order = append(f.order, fd.Fieldname)
	}
	if len(f.order) == 0 {
		what := "has no editable fields"
		if f.submitted {
			what = "is submitted and has no fields that may change after submit"
		}
		return nil, &client.StateError{Message: fmt.Sprintf("%s %s %s", doctype, name, what)}
	}
	text, err := f.render()
	if err != nil {
		return nil, err
	}
	f.text = text
	if f.base, err = f.parse(text); err != nil {
		return nil, fmt.Errorf("reading back the generated file: %w", err)
	}
	return f, nil
}

func rowsOf(v interface{}) []map[string]interface{} {
	list, _ := v.([]interface{})
	out := make([]map[string]interface{}, 0, len(list))
	for _, r := range list {
		if row, ok := r.(map[string]interface{}); ok {
			out = append(out, row)
		}
	}
	return out
}

// ─── The file ────────────────────────────────────────────────────────────────

const editErrorPrefix = "# ERROR: "

func (f *editForm) header() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Editing %s %s. Save and close the editor to apply the changes.\n", text.Sanitize(f.doctype), text.Sanitize(f.name))
	b.WriteString("# Lines starting with # are ignored. An empty or unchanged file cancels the edit.\n")
	b.WriteString("# A field you delete is not changed; set it to null to clear it.\n")
	if len(f.tables) > 0 {
		b.WriteString("# Child tables: delete a row to remove it, add a row without \"name\" to add one.\n")
	}
	if f.submitted {
		b.WriteString("# The document is submitted: only the fields allowed on submit are shown.\n")
	}
	b.WriteString("# Read-only, hidden, computed and system fields, and fields you may not write, are not shown.\n")
	return b.String()
}

func (f *editForm) render() ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range f.order {
		var v *yaml.Node
		if t := f.tables[k]; t != nil {
			v = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, row := range rowsOf(f.doc[k]) {
				n, _ := docName(row["name"])
				m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				m.Content = append(m.Content, strNode("name"), editValueNode(n))
				for _, rk := range t.order {
					m.Content = append(m.Content, strNode(rk), editValueNode(row[rk]))
				}
				v.Content = append(v.Content, m)
			}
		} else {
			v = editValueNode(f.doc[k])
		}
		root.Content = append(root.Content, strNode(k), v)
	}
	var buf bytes.Buffer
	buf.WriteString(f.header())
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("writing the document as YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// editValueNode is a value as YAML: numbers keep their literal, strings are
// quoted when they would read back as something else.
func editValueNode(v interface{}) *yaml.Node {
	switch val := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(val.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: val.String()}
	case map[string]interface{}:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range mapKeys(val) {
			n.Content = append(n.Content, strNode(k), editValueNode(val[k]))
		}
		return n
	case []interface{}:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, x := range val {
			n.Content = append(n.Content, editValueNode(x))
		}
		return n
	}
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return strNode(fmt.Sprint(v))
	}
	return &n
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parse reads an edited file. Every error names the line it is on.
func (f *editForm) parse(b []byte) (*editValues, error) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(b))
	out := &editValues{fields: map[string]interface{}{}, tables: map[string][]editRow{}}
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		return nil, err
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, errors.New("the file has more than one YAML document")
	}
	if len(doc.Content) == 0 {
		return out, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: expected the fields as \"field: value\" lines", root.Line)
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		kn, vn := root.Content[i], root.Content[i+1]
		k := kn.Value
		if seen[k] {
			return nil, fmt.Errorf("line %d: %s appears twice", kn.Line, k)
		}
		seen[k] = true
		if t := f.tables[k]; t != nil {
			rows, err := f.parseTable(k, t, vn)
			if err != nil {
				return nil, err
			}
			out.tables[k] = rows
			continue
		}
		fd, ok := f.fields[k]
		if !ok {
			return nil, fmt.Errorf("line %d: %q is not an editable field of %s", kn.Line, k, f.doctype)
		}
		v, err := editNodeValue(vn, fd.Fieldtype)
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", vn.Line, k, err)
		}
		out.fields[k] = v
	}
	return out, nil
}

func (f *editForm) parseTable(field string, t *editTable, n *yaml.Node) ([]editRow, error) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	var items []*yaml.Node
	switch {
	case n.ShortTag() == "!!null": // no rows
	case n.Kind == yaml.SequenceNode:
		items = n.Content
	default:
		return nil, fmt.Errorf("line %d: %s: expected a list of rows", n.Line, field)
	}
	rows := []editRow{}
	var lines []int
	seen := map[string]bool{}
	for _, rn := range items {
		if rn.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("line %d: %s: expected a row of \"field: value\" lines", rn.Line, field)
		}
		row := editRow{values: map[string]interface{}{}}
		keys := map[string]bool{}
		for i := 0; i+1 < len(rn.Content); i += 2 {
			kn, vn := rn.Content[i], rn.Content[i+1]
			k := kn.Value
			if keys[k] {
				return nil, fmt.Errorf("line %d: %s appears twice in the row", kn.Line, k)
			}
			keys[k] = true
			if k == "name" {
				if vn.ShortTag() != "!!null" {
					row.name = vn.Value
				}
				continue
			}
			fd, ok := t.fields[k]
			if !ok {
				return nil, fmt.Errorf("line %d: %q is not an editable field of %s rows", kn.Line, k, field)
			}
			v, err := editNodeValue(vn, fd.Fieldtype)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", vn.Line, k, err)
			}
			row.values[k] = v
		}
		if row.name != "" {
			if t.rows[row.name] == nil {
				return nil, fmt.Errorf("line %d: %s has no row %q (leave \"name\" out to add a row)", rn.Line, field, row.name)
			}
			if seen[row.name] {
				return nil, fmt.Errorf("line %d: row %q of %s appears twice", rn.Line, row.name, field)
			}
			seen[row.name] = true
		} else if t.fixed {
			return nil, fmt.Errorf("line %d: rows cannot be added to %s after submit", rn.Line, field)
		}
		rows = append(rows, row)
		lines = append(lines, rn.Line)
	}
	if t.fixed {
		if len(seen) != len(t.rows) {
			return nil, fmt.Errorf("line %d: rows cannot be removed from %s after submit", n.Line, field)
		}
		for i, r := range rows {
			if r.name != t.names[i] {
				return nil, fmt.Errorf("line %d: rows of %s cannot be moved after submit", lines[i], field)
			}
		}
	}
	return rows, f.checkReplaced(field, t, rows, lines, seen)
}

// checkReplaced refuses a new row that is a removed row with its "name"
// line deleted: saved, it would replace the row and lose its columns that
// are not in the file. A removed row and a different new row are a real
// edit, shown in the diff.
func (f *editForm) checkReplaced(field string, t *editTable, rows []editRow, lines []int, kept map[string]bool) error {
	if f.base == nil { // the generated file itself
		return nil
	}
	before := map[string]map[string]interface{}{}
	for _, r := range f.base.tables[field] {
		before[r.name] = r.values
	}
	for i, r := range rows {
		if r.name != "" {
			continue
		}
		for _, name := range t.names {
			if kept[name] {
				continue
			}
			same := true
			for _, k := range t.order {
				if !sameValue(before[name][k], r.values[k]) {
					same = false
					break
				}
			}
			if same {
				return fmt.Errorf("line %d: this row is row %q of %s without its \"name\" line; saved like this it would replace the row and lose its columns not shown here. Put back \"name: %s\" to keep the row, or change it to add a new one",
					lines[i], name, field, name)
			}
		}
	}
	return nil
}

// editNodeValue converts a YAML value for a field of fieldtype: text fields
// take the text as written ("0123" stays a string), number fields need a
// number (a Check also takes true/false), and other values keep their YAML
// type.
func editNodeValue(n *yaml.Node, fieldtype string) (interface{}, error) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	switch n.Kind {
	case yaml.MappingNode:
		m := map[string]interface{}{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := editNodeValue(n.Content[i+1], "")
			if err != nil {
				return nil, err
			}
			m[n.Content[i].Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		list := []interface{}{}
		for _, c := range n.Content {
			v, err := editNodeValue(c, "")
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
		return list, nil
	}
	tag := n.ShortTag()
	if tag == "!!null" {
		return nil, nil
	}
	switch {
	case editNumericTypes[fieldtype]:
		if tag == "!!bool" && fieldtype == "Check" {
			if n.Value == "true" || n.Value == "True" || n.Value == "TRUE" {
				return json.Number("1"), nil
			}
			return json.Number("0"), nil
		}
		if num, ok := yamlNumber(n); ok {
			return num, nil
		}
		return nil, fmt.Errorf("expected a number, got %q", n.Value)
	case fieldtype == "":
		switch tag {
		case "!!int", "!!float":
			if num, ok := yamlNumber(n); ok {
				return num, nil
			}
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err == nil {
				return b, nil
			}
		}
	}
	return n.Value, nil
}

// yamlNumber is a scalar as a JSON number: its literal when that is valid
// JSON, else the value YAML reads (0x1F, 1_000).
func yamlNumber(n *yaml.Node) (json.Number, bool) {
	s := strings.TrimSpace(n.Value)
	if s != "" && (s[0] == '-' || (s[0] >= '0' && s[0] <= '9')) && json.Valid([]byte(s)) {
		return json.Number(s), true
	}
	switch n.ShortTag() {
	case "!!int":
		var i int64
		if n.Decode(&i) == nil {
			return json.Number(strconv.FormatInt(i, 10)), true
		}
	case "!!float":
		var x float64
		if n.Decode(&x) == nil && !math.IsInf(x, 0) && !math.IsNaN(x) {
			return json.Number(strconv.FormatFloat(x, 'g', -1, 64)), true
		}
	}
	return "", false
}

// ─── Editing ─────────────────────────────────────────────────────────────────

// edit writes the file to a private temporary directory, opens the editor
// and reads the result, until it parses. It returns the values, or why the
// edit was cancelled. The directory is removed on every return; an
// interrupt cancels the context, so the defers run.
func (f *editForm) edit(ctx context.Context) (*editValues, string, error) {
	dir, err := os.MkdirTemp("", "ffc-edit-")
	if err != nil {
		return nil, "", fmt.Errorf("creating the temporary file: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "document.yaml")
	content := f.text
	var lastErr error
	for {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return nil, "", fmt.Errorf("writing the temporary file: %w", err)
		}
		runErr := runEditor(path)
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if runErr != nil {
			return nil, "", fmt.Errorf("%w; nothing was saved", runErr)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("reading the edited file: %w", err)
		}
		if bytes.Equal(b, content) {
			if lastErr != nil {
				return nil, "", usageErrorf("the file still has an error, nothing was saved: %v", lastErr)
			}
			return nil, "no changes made", nil
		}
		body := stripEditErrors(b)
		if blankYAML(body) {
			return nil, "the file is empty", nil
		}
		// Parsed below the error header the file reopens with (always
		// editErrorLines lines), so the line numbers match what the user sees.
		vals, err := f.parse(append([]byte(strings.Repeat("#\n", editErrorLines)), body...))
		if err == nil {
			return vals, "", nil
		}
		lastErr = err
		content = append(editErrorHeader(err), body...)
	}
}

// editErrorLines is the number of lines editErrorHeader writes.
const editErrorLines = 2

// editErrorHeader is the comment put on top of a file that did not parse.
func editErrorHeader(err error) []byte {
	msg := strings.ReplaceAll(text.Sanitize(strings.ReplaceAll(err.Error(), "\n", " ")), "\n", " ")
	return []byte(editErrorPrefix + msg + "\n" +
		editErrorPrefix + "fix the file and save it, or save it unchanged to give up.\n")
}

// stripEditErrors removes the error lines editErrorHeader added.
func stripEditErrors(b []byte) []byte {
	for bytes.HasPrefix(b, []byte(editErrorPrefix)) {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return nil
		}
		b = b[i+1:]
	}
	return b
}

// blankYAML reports whether b has nothing but comments and blank lines.
func blankYAML(b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "#") {
			return false
		}
	}
	return true
}

// editorRun opens path in the user's editor, attached to the terminal.
func editorRun(path string) error {
	argv, err := editorCommand()
	if err != nil {
		return err
	}
	c := exec.Command(argv[0], append(argv[1:], path)...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	if !isTerminal(os.Stdout) {
		// Keep the editor's screen out of a redirected stdout (--json > file).
		c.Stdout = os.Stderr
	}
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("editor %s: %w", argv[0], err)
	}
	return nil
}

// editorCommand is $VISUAL, else $EDITOR, else vi or notepad. A value that
// names a file is the editor itself (a path with spaces); otherwise it is
// split into words like a shell would, without running one: quotes group
// ('/opt/my editor/ed' --wait), and outside Windows a backslash escapes.
func editorCommand() ([]string, error) {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		v := strings.TrimSpace(os.Getenv(env))
		if v == "" {
			continue
		}
		if st, err := os.Stat(v); err == nil && !st.IsDir() {
			return []string{v}, nil
		}
		argv, err := splitCommand(v, runtime.GOOS != "windows")
		if err != nil {
			return nil, usageErrorf("$%s: %v", env, err)
		}
		if len(argv) > 0 {
			return argv, nil
		}
	}
	if runtime.GOOS == "windows" {
		return []string{"notepad"}, nil
	}
	return []string{"vi"}, nil
}

// splitCommand splits a command line into words: blanks separate them,
// single quotes keep everything literal, double quotes keep blanks, and
// with escapes a backslash takes the next character literally (inside
// double quotes only before " or \).
func splitCommand(s string, escapes bool) ([]string, error) {
	var out []string
	var word strings.Builder
	inWord := false
	var quote rune
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case escapes && r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
				i++
				word.WriteRune(rs[i])
			default:
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case escapes && r == '\\':
			if i+1 == len(rs) {
				return nil, errors.New("ends with a backslash")
			}
			i++
			word.WriteRune(rs[i])
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				out = append(out, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inWord {
		out = append(out, word.String())
	}
	return out, nil
}

// ─── The changes ─────────────────────────────────────────────────────────────

type fieldChange struct {
	field    string
	from, to interface{}
}

type rowChange struct {
	name    string
	changes []fieldChange
}

type tableDiff struct {
	field     string
	changed   []rowChange
	added     []editRow
	removed   []string // row names
	reordered bool
}

type editDiff struct {
	fields []fieldChange
	tables []tableDiff
}

func (d *editDiff) empty() bool { return len(d.fields) == 0 && len(d.tables) == 0 }

func (d *editDiff) removedRows() int {
	n := 0
	for _, t := range d.tables {
		n += len(t.removed)
	}
	return n
}

// diff compares edited values with the file as first written, in form
// order; "from" values are the document's.
func (f *editForm) diff(ed *editValues) *editDiff {
	d := &editDiff{}
	for _, k := range f.order {
		if v, ok := ed.fields[k]; ok && !sameValue(f.base.fields[k], v) {
			d.fields = append(d.fields, fieldChange{k, f.doc[k], v})
		}
		rows, ok := ed.tables[k]
		if !ok {
			continue
		}
		t := f.tables[k]
		baseRows := map[string]map[string]interface{}{}
		var baseOrder []string
		for _, r := range f.base.tables[k] {
			baseRows[r.name] = r.values
			baseOrder = append(baseOrder, r.name)
		}
		td := tableDiff{field: k}
		kept := map[string]bool{}
		var keptOrder []string
		for _, r := range rows {
			if r.name == "" {
				td.added = append(td.added, r)
				continue
			}
			kept[r.name] = true
			keptOrder = append(keptOrder, r.name)
			rc := rowChange{name: r.name}
			for _, rk := range t.order {
				if v, ok := r.values[rk]; ok && !sameValue(baseRows[r.name][rk], v) {
					rc.changes = append(rc.changes, fieldChange{rk, t.rows[r.name][rk], v})
				}
			}
			if len(rc.changes) > 0 {
				td.changed = append(td.changed, rc)
			}
		}
		var stayed []string
		for _, n := range baseOrder {
			if kept[n] {
				stayed = append(stayed, n)
			} else {
				td.removed = append(td.removed, n)
			}
		}
		td.reordered = strings.Join(stayed, "\x00") != strings.Join(keptOrder, "\x00")
		if len(td.changed)+len(td.added)+len(td.removed) > 0 || td.reordered {
			d.tables = append(d.tables, td)
		}
	}
	return d
}

// body is the update: the changed fields, each changed table whole (kept
// rows as read with the edits applied, so no column is lost; new rows; idx
// from the order in the file), and "modified" for Frappe's check that
// nobody saved the document in between.
func (f *editForm) body(ed *editValues, d *editDiff) map[string]interface{} {
	body := map[string]interface{}{"modified": f.doc["modified"]}
	for _, c := range d.fields {
		body[c.field] = c.to
	}
	for _, td := range d.tables {
		changed := map[string]rowChange{}
		for _, rc := range td.changed {
			changed[rc.name] = rc
		}
		rows := []interface{}{}
		for i, r := range ed.tables[td.field] {
			row := map[string]interface{}{}
			if r.name != "" {
				for k, v := range f.tables[td.field].rows[r.name] {
					if !strings.HasPrefix(k, "__") {
						row[k] = v
					}
				}
				for _, c := range changed[r.name].changes {
					row[c.field] = c.to
				}
			} else {
				for k, v := range r.values {
					row[k] = v
				}
			}
			row["idx"] = json.Number(strconv.Itoa(i + 1))
			rows = append(rows, row)
		}
		body[td.field] = rows
	}
	return body
}

// planChanges is the dry-run summary: field → {from, to}, a table as row
// counts.
func (f *editForm) planChanges(d *editDiff) map[string]interface{} {
	out := map[string]interface{}{}
	for _, c := range d.fields {
		out[c.field] = map[string]interface{}{"from": c.from, "to": c.to}
	}
	for _, td := range d.tables {
		before := len(f.tables[td.field].rows)
		to := fmt.Sprintf("%d rows: %d changed, %d added, %d removed", before-len(td.removed)+len(td.added),
			len(td.changed), len(td.added), len(td.removed))
		if len(td.removed) > 0 {
			to += " (" + strings.Join(td.removed, ", ") + ")"
		}
		if td.reordered {
			to += ", reordered"
		}
		out[td.field] = map[string]interface{}{"from": fmt.Sprintf("%d rows", before), "to": to}
	}
	return out
}

// printDiff shows the changes on stderr; removed rows stand out.
func (f *editForm) printDiff(d *editDiff) {
	w := os.Stderr
	fmt.Fprintf(w, "Changes to %s %s:\n", text.Sanitize(f.doctype), text.Sanitize(f.name))
	for _, c := range d.fields {
		fmt.Fprintf(w, "  %s: %s → %s\n", text.Sanitize(c.field), diffValue(c.from), diffValue(c.to))
	}
	for _, td := range d.tables {
		fmt.Fprintf(w, "  %s (the whole table is saved):\n", text.Sanitize(td.field))
		for _, rc := range td.changed {
			fmt.Fprintf(w, "    ~ row %s:", text.Sanitize(rc.name))
			for _, c := range rc.changes {
				fmt.Fprintf(w, " %s: %s → %s;", text.Sanitize(c.field), diffValue(c.from), diffValue(c.to))
			}
			fmt.Fprintln(w)
		}
		for _, r := range td.added {
			fmt.Fprintf(w, "    + new row: %s\n", rowSummary(f.tables[td.field].order, r.values))
		}
		for _, n := range td.removed {
			output.PrintWarning(fmt.Sprintf("    - REMOVED row %s: %s", n, rowSummary(f.tables[td.field].order, f.tables[td.field].rows[n])))
		}
		if td.reordered {
			fmt.Fprintln(w, "    rows reordered")
		}
		if len(td.removed) > 0 && len(td.added) > 0 {
			output.PrintWarning("    a new row has only the values shown; nothing is carried over from a removed row")
		}
	}
}

func rowSummary(order []string, values map[string]interface{}) string {
	var parts []string
	for _, k := range order {
		if v, ok := values[k]; ok && !emptyValue(v) {
			parts = append(parts, text.Sanitize(k)+": "+diffValue(v))
		}
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, ", ")
}

// diffValue is a value as JSON, clipped, safe for the terminal.
func diffValue(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return text.Sanitize(fmt.Sprint(v))
	}
	s := text.Sanitize(string(b))
	if r := []rune(s); len(r) > 80 {
		s = string(r[:77]) + "..."
	}
	return s
}

func init() {
	editDocCmd.Flags().StringVarP(&edDoctype, "doctype", "d", "", "Frappe DocType (required)")
	editDocCmd.Flags().StringVarP(&edName, "name", "n", "", "Name of the document. Defaults to DocType name for Single DocTypes.")
	editDocCmd.Flags().StringVar(&edKeys, "keys", "", "Comma-separated keys to include in data output, e.g. name,modified")
	editDocCmd.Flags().BoolVarP(&edYes, "yes", "y", false, "Save without asking after the editor closes")
	_ = editDocCmd.MarkFlagRequired("doctype")
	addDryRun(editDocCmd, false)
	rootCmd.AddCommand(editDocCmd)
}
