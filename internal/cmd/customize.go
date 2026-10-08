package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// customType is a DocType that holds customizations (T3.3). Frappe
// (v16.36.1 source, v15 branch checked) names Custom Field "dt-fieldname"
// and Property Setter "doc_type-field-property", Workflow, Workflow State,
// Workflow Action Master and Report from a field, the rest from the name
// sent (Prompt): every name is the same on every site, so a document is
// matched across sites by name.
type customType struct {
	slug     string // folder name, Frappe's scrub of the DocType
	doctype  string
	selector string // the field naming the customized DocType
	module   string // the module field, "" when the DocType has none
	// exclude keeps out what is not a site customization: Custom Fields
	// and Property Setters made by apps and patches (is_system_generated;
	// Customize Form writes 0), and standard Reports, Print Formats and
	// Notifications, which live in app code and change only in developer
	// mode.
	exclude []interface{}
}

// customTypes are the DocTypes customize pulls, by slug. Workflow States
// and Workflow Action Masters come with the Workflows that use them.
var customTypes = []customType{
	{"custom_field", "Custom Field", "dt", "module", []interface{}{"is_system_generated", "=", 0}},
	{"property_setter", "Property Setter", "doc_type", "module", []interface{}{"is_system_generated", "=", 0}},
	{"client_script", "Client Script", "dt", "module", nil},
	{"server_script", "Server Script", "reference_doctype", "module", nil},
	{"print_format", "Print Format", "doc_type", "module", []interface{}{"standard", "!=", "Yes"}},
	{"report", "Report", "ref_doctype", "module", []interface{}{"is_standard", "!=", "Yes"}},
	{"notification", "Notification", "document_type", "module", []interface{}{"is_standard", "=", 0}},
	{"workflow", "Workflow", "document_type", "", nil},
	{"webhook", "Webhook", "webhook_doctype", "", nil},
}

// The DocTypes a pulled Workflow brings along.
var (
	workflowStateType  = customType{slug: "workflow_state", doctype: "Workflow State"}
	workflowActionType = customType{slug: "workflow_action_master", doctype: "Workflow Action Master"}
)

func customTypeSlugs() []string {
	var out []string
	for _, t := range customTypes {
		out = append(out, t.slug)
	}
	return out
}

var customizeCmd = &cobra.Command{
	Use:   "customize",
	Short: "Pull site customizations into files and push them to a site",
	Long: `Copy a site's customizations to files you can review and keep in git
(pull), and apply such a folder to a site (push).

Covers Custom Fields, Property Setters, Client and Server Scripts, Print
Formats, Reports, Notifications, Workflows (with their states and actions)
and Webhooks. Standard Reports, Print Formats and Notifications (shipped in
app code) are left out, and so are Custom Fields and Property Setters that
apps and patches made (pass --include-system to keep them).`,
}

// customize pull flags
var (
	cpDoctypes      string
	cpModule        string
	cpOut           string
	cpTypes         string
	cpIncludeSystem bool
)

var customizePullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Write the customizations of DocTypes or a module to a folder",
	Long: `Write the customizations of some DocTypes (-d) or of a module (--module) to
a folder, one JSON file per document: <out>/<type>/<name>.json.

The files are stable: sorted keys, no timestamps or owners, LF line ends,
so pulling again changes only what changed on the site. Password fields
(a Webhook's secret) are never written.

With --module, a document is included when its own module is the module,
or when the DocType it customizes belongs to it (Custom Fields and Property
Setters rarely have a module of their own; Workflows and Webhooks have
none).

Files of documents no longer on the site are kept and listed.

Examples:
  ffc customize pull -d "Sales Invoice" --out customizations
  ffc customize pull -d "Sales Invoice,Customer" --types custom_field,property_setter --out custom
  ffc customize pull --module Selling --out custom
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		sel, err := customSelection(cpDoctypes, cpModule, cpTypes, cpIncludeSystem)
		if err != nil {
			return err
		}
		if cpOut == "" {
			return usageErrorf("--out is required")
		}
		if fi, err := os.Stat(cpOut); err == nil && !fi.IsDir() {
			return usageErrorf("--out %s is a file, not a folder", cpOut)
		}
		docs, err := callSite(cmd, "Reading customizations…", func(ctx context.Context, c *client.FrappeClient) ([]customDoc, error) {
			return pullCustomizations(ctx, c, &sel)
		})
		if err != nil {
			return err
		}
		res, err := writeCustomizations(cpOut, sel, docs)
		if err != nil {
			return err
		}
		for _, w := range res.Warnings {
			output.PrintWarning(w)
		}
		return render(res, nil, func() error {
			for _, slug := range slices.Sorted(maps.Keys(res.Documents)) {
				fmt.Printf("%-24s %d\n", slug, res.Documents[slug])
			}
			fmt.Printf("\n%d written, %d unchanged in %s\n", res.Written, res.Unchanged, res.Out)
			if len(res.Stale) > 0 {
				fmt.Printf("%d files kept for documents no longer on the site:\n", len(res.Stale))
				for _, f := range res.Stale {
					fmt.Println("  " + f)
				}
			}
			return nil
		})
	},
}

func init() {
	f := customizePullCmd.Flags()
	f.StringVarP(&cpDoctypes, "doctype", "d", "", "DocTypes whose customizations to pull (comma-separated)")
	f.StringVarP(&cpModule, "module", "m", "", "Pull the customizations of a module")
	f.StringVar(&cpOut, "out", "", "Folder to write the files to (created if missing)")
	f.StringVar(&cpTypes, "types", "", "Only these kinds, comma-separated: "+strings.Join(customTypeSlugs(), ", "))
	f.BoolVar(&cpIncludeSystem, "include-system", false, "Also pull Custom Fields and Property Setters made by apps and patches")
	customizeCmd.AddCommand(customizePullCmd)
	rootCmd.AddCommand(customizeCmd)
}

// customSel is what a pull covers.
type customSel struct {
	Doctypes      []string
	Module        string
	Types         []customType
	IncludeSystem bool
	// ModuleDoctypes are the DocTypes of Module, read by the pull.
	ModuleDoctypes []string
}

func customSelection(doctypes, module, types string, includeSystem bool) (customSel, error) {
	sel := customSel{Doctypes: splitCSV(doctypes), Module: strings.TrimSpace(module), IncludeSystem: includeSystem}
	switch {
	case len(sel.Doctypes) > 0 && sel.Module != "":
		return sel, usageErrorf("-d/--doctype and --module cannot be combined")
	case len(sel.Doctypes) == 0 && sel.Module == "":
		return sel, usageErrorf("pass -d/--doctype or --module")
	}
	var err error
	sel.Types, err = customTypeList(types)
	return sel, err
}

// customTypeList parses --types: every kind when empty, else the named
// ones without repeats.
func customTypeList(types string) ([]customType, error) {
	if types == "" {
		return customTypes, nil
	}
	var out []customType
	for _, slug := range splitCSV(types) {
		i := slices.IndexFunc(customTypes, func(t customType) bool { return t.slug == slug })
		if i < 0 {
			return nil, usageErrorf("--types: unknown kind %q (one of %s)", slug, strings.Join(customTypeSlugs(), ", "))
		}
		if !slices.ContainsFunc(out, func(t customType) bool { return t.slug == slug }) {
			out = append(out, customTypes[i])
		}
	}
	return out, nil
}

// customDoc is a pulled document, normalized.
type customDoc struct {
	Type customType
	Name string
	Doc  map[string]interface{}
	// Warnings name values left out of the file (passwords) or written
	// though they look secret (Webhook headers).
	Warnings []string
}

// pullCustomizations reads the selected documents: their names by list
// queries, then each document (child tables included), normalized.
func pullCustomizations(ctx context.Context, c *client.FrappeClient, sel *customSel) ([]customDoc, error) {
	if sel.Module != "" {
		rows, err := c.GetList(ctx, "DocType", client.ListOptions{Fields: []string{"name"}, Filters: mustFilters([][]interface{}{{"module", "=", sel.Module}}), Limit: -1})
		if err != nil {
			return nil, fmt.Errorf("listing the DocTypes of module %s: %w", sel.Module, err)
		}
		for _, r := range rows {
			sel.ModuleDoctypes = append(sel.ModuleDoctypes, fmt.Sprint(r["name"]))
		}
	}
	var out []customDoc
	states, actions := map[string]bool{}, map[string]bool{}
	for _, t := range sel.Types {
		names, err := customNames(ctx, c, t, sel)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			continue
		}
		docs, err := fetchCustomDocs(ctx, c, t, names)
		if err != nil {
			return nil, err
		}
		if t.doctype == "Workflow" {
			for _, d := range docs {
				workflowLinks(d.Doc, states, actions)
			}
		}
		out = append(out, docs...)
	}
	for _, extra := range []struct {
		t     customType
		names map[string]bool
	}{{workflowStateType, states}, {workflowActionType, actions}} {
		if len(extra.names) == 0 {
			continue
		}
		docs, err := fetchCustomDocs(ctx, c, extra.t, slices.Sorted(maps.Keys(extra.names)))
		if err != nil {
			return nil, err
		}
		out = append(out, docs...)
	}
	return out, nil
}

// customNames lists the names of t's documents in the selection, sorted.
func customNames(ctx context.Context, c *client.FrappeClient, t customType, sel *customSel) ([]string, error) {
	var queries [][][]interface{}
	// A list query is a GET: a module's few hundred DocTypes go in chunks,
	// so the URL stays well under a proxy's limit.
	targets := sel.Doctypes
	if len(targets) == 0 {
		targets = sel.ModuleDoctypes
	}
	for chunk := range slices.Chunk(targets, customInChunk) {
		queries = append(queries, [][]interface{}{{t.selector, "in", chunk}})
	}
	if len(sel.Doctypes) == 0 {
		if t.module != "" {
			queries = append(queries, [][]interface{}{{t.module, "=", sel.Module}})
		}
	}
	names := map[string]bool{}
	for _, q := range queries {
		if t.exclude != nil && !(sel.IncludeSystem && t.exclude[0] == "is_system_generated") {
			q = append(q, t.exclude)
		}
		rows, err := c.GetList(ctx, t.doctype, client.ListOptions{Fields: []string{"name"}, Filters: mustFilters(q), Limit: -1})
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", t.doctype, err)
		}
		for _, r := range rows {
			names[fmt.Sprint(r["name"])] = true
		}
	}
	return slices.Sorted(maps.Keys(names)), nil
}

// customInChunk is the most names one "in" filter carries.
const customInChunk = 50

// customMeta is what pull and push need from a kind's meta (child tables
// included).
type customMeta struct {
	tables map[string]string          // table field → child DocType
	secret map[string]map[string]bool // DocType → its Password fields
	fields map[string]map[string]bool // DocType → its fields
}

func readCustomMeta(ctx context.Context, c *client.FrappeClient, doctype string) (*customMeta, error) {
	metas, err := c.FormMetas(ctx, doctype)
	if err != nil {
		return nil, err
	}
	m := &customMeta{tables: map[string]string{}, secret: map[string]map[string]bool{}, fields: map[string]map[string]bool{}}
	if top := metas[doctype]; top != nil {
		for _, f := range top.Fields {
			if f.Options != "" && (f.Fieldtype == "Table" || f.Fieldtype == "Table MultiSelect") {
				m.tables[f.Fieldname] = f.Options
			}
		}
	}
	for dt, meta := range metas {
		m.fields[dt] = map[string]bool{}
		for _, f := range meta.Fields {
			m.fields[dt][f.Fieldname] = true
			if f.Fieldtype == "Password" {
				if m.secret[dt] == nil {
					m.secret[dt] = map[string]bool{}
				}
				m.secret[dt][f.Fieldname] = true
			}
		}
	}
	return m, nil
}

// fetchCustomDocs reads and normalizes t's documents; the meta (child
// tables included) says which fields are passwords.
func fetchCustomDocs(ctx context.Context, c *client.FrappeClient, t customType, names []string) ([]customDoc, error) {
	meta, err := readCustomMeta(ctx, c, t.doctype)
	if err != nil {
		return nil, err
	}
	out := make([]customDoc, 0, len(names))
	for _, name := range names {
		doc, err := c.GetDoc(ctx, t.doctype, name)
		if err != nil {
			return nil, fmt.Errorf("reading %s %s: %w", t.doctype, name, err)
		}
		norm, dropped := normalizeCustomDoc(doc, t.doctype, meta.secret, meta.tables)
		d := customDoc{Type: t, Name: name, Doc: norm}
		if len(dropped) > 0 {
			d.Warnings = append(d.Warnings, fmt.Sprintf("%s %s: %s not written (password); set it on each site", t.doctype, name, strings.Join(dropped, ", ")))
		}
		if keys := credentialHeaders(norm); len(keys) > 0 {
			d.Warnings = append(d.Warnings, fmt.Sprintf("%s %s: header %s looks like a credential and is in the file; keep it out of shared repositories", t.doctype, name, strings.Join(keys, ", ")))
		}
		out = append(out, d)
	}
	return out, nil
}

// Keys never written: who and when, the row identity of child rows, and
// runtime state.
var (
	customDropKeys = map[string]bool{"owner": true, "creation": true, "modified": true, "modified_by": true,
		"docstatus": true, "idx": true, "doctype": true, "datetime_last_run": true}
	customDropRowKeys = map[string]bool{"name": true, "parent": true, "parenttype": true, "parentfield": true,
		"owner": true, "creation": true, "modified": true, "modified_by": true, "docstatus": true, "idx": true, "doctype": true}
)

// normalizeCustomDoc keeps what defines the customization: no nulls, no
// "_" keys (_user_tags, _comments, _assign, _liked_by, _seen, __last_sync_on),
// no password values, and child rows without their identity (a PUT
// replaces child tables, so row names are not kept across sites). It
// returns the password fields left out that had a value. tables maps a
// table field to its child DocType (a row's own "doctype" may be missing).
func normalizeCustomDoc(doc map[string]interface{}, doctype string, secret map[string]map[string]bool, tables map[string]string) (map[string]interface{}, []string) {
	var dropped []string
	out := map[string]interface{}{}
	for k, v := range doc {
		if v == nil || customDropKeys[k] || strings.HasPrefix(k, "_") {
			continue
		}
		if secret[doctype][k] {
			if s, _ := v.(string); s != "" {
				dropped = append(dropped, k)
			}
			continue
		}
		if rows, ok := v.([]interface{}); ok {
			var rowDropped []string
			v, rowDropped = normalizeCustomRows(rows, tables[k], secret)
			for _, f := range rowDropped {
				dropped = append(dropped, k+"."+f)
			}
		}
		out[k] = v
	}
	sort.Strings(dropped)
	dropped = slices.Compact(dropped)
	return out, dropped
}

// normalizeCustomRows strips row identity and password fields; child is
// the table's DocType from the meta, else the row's own.
func normalizeCustomRows(rows []interface{}, child string, secret map[string]map[string]bool) ([]interface{}, []string) {
	var dropped []string
	out := make([]interface{}, 0, len(rows))
	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			out = append(out, r)
			continue
		}
		dt := child
		if dt == "" {
			dt, _ = row["doctype"].(string)
		}
		clean := map[string]interface{}{}
		for k, v := range row {
			if v == nil || customDropRowKeys[k] || strings.HasPrefix(k, "_") {
				continue
			}
			if secret[dt][k] {
				if s, _ := v.(string); s != "" {
					dropped = append(dropped, k)
				}
				continue
			}
			clean[k] = v
		}
		out = append(out, clean)
	}
	return out, dropped
}

// credentialHeaders names the Webhook headers whose key looks like it
// carries a credential (Data fields, so they are written).
func credentialHeaders(doc map[string]interface{}) []string {
	rows, _ := doc["webhook_headers"].([]interface{})
	var out []string
	for _, r := range rows {
		row, _ := r.(map[string]interface{})
		key, _ := row["key"].(string)
		lower := strings.ToLower(key)
		for _, w := range []string{"auth", "token", "key", "secret", "password", "cookie", "signature"} {
			if strings.Contains(lower, w) {
				out = append(out, key)
				break
			}
		}
	}
	return out
}

// workflowLinks collects the Workflow States and Workflow Action Masters a
// Workflow's rows link to.
func workflowLinks(doc map[string]interface{}, states, actions map[string]bool) {
	add := func(set map[string]bool, v interface{}) {
		if s, _ := v.(string); s != "" {
			set[s] = true
		}
	}
	for _, key := range []string{"states", "transitions"} {
		rows, _ := doc[key].([]interface{})
		for _, r := range rows {
			row, _ := r.(map[string]interface{})
			add(states, row["state"])
			add(states, row["next_state"])
			add(actions, row["action"])
		}
	}
}

func mustFilters(f [][]interface{}) string {
	b, err := json.Marshal(f)
	if err != nil {
		panic(err) // strings and ints only
	}
	return string(b)
}

// customPullResult is pull's answer.
type customPullResult struct {
	Out       string         `json:"out"`
	Documents map[string]int `json:"documents"`
	Written   int            `json:"written"`
	Unchanged int            `json:"unchanged"`
	// Stale lists files (relative to Out) of documents in the selection
	// that are no longer on the site; they are kept.
	Stale    []string `json:"stale"`
	Warnings []string `json:"warnings,omitempty"`
}

// writeCustomizations writes one file per document and lists the stale
// ones.
func writeCustomizations(dir string, sel customSel, docs []customDoc) (*customPullResult, error) {
	res := &customPullResult{Out: dir, Documents: map[string]int{}, Stale: []string{}}
	written := map[string]bool{} // relative paths
	type planned struct {
		d        customDoc
		base     string
		json     []byte
		sidecars map[string]string
	}
	// Every file is named before any is written, so a clash stops the pull
	// with nothing changed.
	folded := map[string]string{} // case-folded path → document
	plan := make([]planned, 0, len(docs))
	for _, d := range docs {
		base := customFileBase(d.Name)
		doc, sidecars := splitSidecars(d.Doc, d.Type.doctype, base)
		files := []string{d.Type.slug + "/" + base + ".json"}
		for name := range sidecars {
			files = append(files, d.Type.slug+"/"+name)
		}
		for _, f := range files {
			key := strings.ToLower(f)
			who := d.Type.doctype + " " + strconv.Quote(d.Name)
			if other, ok := folded[key]; ok && other != who {
				return nil, fmt.Errorf("%s and %s would both be written to %s (file names ignore case on Windows and macOS)", other, who, f)
			}
			folded[key] = who
		}
		b, err := marshalCustomDoc(doc)
		if err != nil {
			return nil, fmt.Errorf("encoding %s %s: %w", d.Type.doctype, d.Name, err)
		}
		plan = append(plan, planned{d, base, b, sidecars})
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := writeGitattributes(dir); err != nil {
		return nil, err
	}
	for _, p := range plan {
		d, base := p.d, p.base
		folder := filepath.Join(dir, d.Type.slug)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", folder, err)
		}
		path := filepath.Join(folder, base+".json")
		oldRefs := sidecarRefs(path)
		changed := false
		for name, content := range p.sidecars {
			w, err := writeIfChanged(filepath.Join(folder, name), content)
			if err != nil {
				return nil, err
			}
			changed = changed || w
		}
		w, err := writeIfChanged(path, string(p.json))
		if err != nil {
			return nil, err
		}
		// Sidecars the previous version referenced and this one does not.
		// The old file may have been edited, so only a name this document's
		// sidecars could have is removed.
		for _, old := range oldRefs {
			if _, keep := p.sidecars[old]; !keep && isSidecarOf(old, base) {
				_ = os.Remove(filepath.Join(folder, old))
			}
		}
		if changed || w {
			res.Written++
		} else {
			res.Unchanged++
		}
		written[d.Type.slug+"/"+base+".json"] = true
		res.Documents[d.Type.slug]++
		res.Warnings = append(res.Warnings, d.Warnings...)
	}
	stale, err := staleCustomFiles(dir, sel, written)
	if err != nil {
		return nil, err
	}
	res.Stale = stale
	return res, nil
}

// staleCustomFiles lists the files of the selected kinds, in the selection
// (by their selector or module field), that this pull did not write.
// Workflow States and Actions are shared between Workflows and never listed.
func staleCustomFiles(dir string, sel customSel, written map[string]bool) ([]string, error) {
	in := map[string]bool{}
	for _, dt := range append(slices.Clone(sel.Doctypes), sel.ModuleDoctypes...) {
		in[dt] = true
	}
	stale := []string{}
	for _, t := range sel.Types {
		entries, err := os.ReadDir(filepath.Join(dir, t.slug))
		if err != nil {
			continue // no folder yet
		}
		for _, e := range entries {
			rel := t.slug + "/" + e.Name()
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || written[rel] {
				continue
			}
			var doc map[string]interface{}
			b, err := os.ReadFile(filepath.Join(dir, t.slug, e.Name()))
			if err != nil || json.Unmarshal(b, &doc) != nil {
				continue // not ours to judge
			}
			target, _ := doc[t.selector].(string)
			module, _ := doc[t.module].(string)
			if in[target] || (sel.Module != "" && t.module != "" && module == sel.Module) {
				stale = append(stale, rel)
			}
		}
	}
	sort.Strings(stale)
	return stale, nil
}

// writeIfChanged writes content to path unless the file already holds it,
// and says whether it wrote.
func writeIfChanged(path, content string) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return false, nil
	}
	if _, err := saveAtomic(strings.NewReader(content), path, true, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// customGitattributes keeps the files LF in a git checkout on Windows, so
// a sidecar's script is pushed back as it was pulled.
const customGitattributes = "# Written by ffc customize pull: keep the files as pulled.\n* text eol=lf\n"

// writeGitattributes writes dir/.gitattributes unless there is one.
func writeGitattributes(dir string) error {
	path := filepath.Join(dir, ".gitattributes")
	if _, err := os.Lstat(path); err == nil {
		return nil
	}
	_, err := saveAtomic(strings.NewReader(customGitattributes), path, false, 0o644)
	return err
}

// sidecarFileKey marks a value kept in a file of its own: {"$file": name}.
const sidecarFileKey = "$file"

// splitSidecars moves every multi-line string field of the document into
// a sidecar file, <base>.<field>.<ext>, so a script or template diffs line
// by line; the document keeps {"$file": name} in its place. Child rows
// stay inline.
func splitSidecars(doc map[string]interface{}, doctype, base string) (map[string]interface{}, map[string]string) {
	out := make(map[string]interface{}, len(doc))
	sidecars := map[string]string{}
	for k, v := range doc {
		if s, ok := v.(string); ok && strings.Contains(s, "\n") && client.ValidIdentifier(k) {
			name := base + "." + k + "." + sidecarExt(doctype, k, doc)
			sidecars[name] = s
			out[k] = map[string]interface{}{sidecarFileKey: name}
			continue
		}
		out[k] = v
	}
	return out, sidecars
}

// sidecarExt picks the extension an editor highlights the field with.
func sidecarExt(doctype, field string, doc map[string]interface{}) string {
	switch field {
	case "script":
		if doctype == "Client Script" {
			return "js"
		}
		return "py"
	case "javascript":
		return "js"
	case "report_script", "condition":
		return "py"
	case "query":
		return "sql"
	case "html":
		return "html"
	case "css":
		return "css"
	case "webhook_json":
		return "jinja" // a template; never .json, which names documents
	case "message":
		switch doc["message_type"] {
		case "HTML":
			return "html"
		case "Markdown":
			return "md"
		}
	}
	return "txt"
}

// sidecarExts are the extensions sidecarExt gives.
var sidecarExts = map[string]bool{"js": true, "py": true, "sql": true, "html": true, "css": true, "jinja": true, "md": true, "txt": true}

// isSidecarOf says whether name is one base's sidecars could have:
// <base>.<field>.<ext>. Another document's file never matches: its JSON
// ends in .json, and its sidecars have one more dot-separated part.
func isSidecarOf(name, base string) bool {
	rest, ok := strings.CutPrefix(name, base+".")
	if !ok {
		return false
	}
	field, ext, ok := strings.Cut(rest, ".")
	return ok && client.ValidIdentifier(field) && sidecarExts[ext]
}

// sidecarRefs lists the sidecar names a document file references.
func sidecarRefs(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]interface{}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	var out []string
	for _, v := range doc {
		if m, ok := v.(map[string]interface{}); ok && len(m) == 1 {
			if name, ok := m[sidecarFileKey].(string); ok {
				out = append(out, name)
			}
		}
	}
	return out
}

// marshalCustomDoc is the file format: sorted keys (encoding/json sorts
// map keys), two-space indent, LF, a final newline, and no HTML escaping
// so templates and scripts read as written. Numbers keep the site's
// literal (json.Number).
func marshalCustomDoc(doc map[string]interface{}) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// customFileBase is a document name as a file name, without extension,
// that every OS accepts:
// characters Windows forbids, control characters and % are
// percent-encoded, and so are a leading dot, a trailing dot or space, and
// the first letter of a name Windows reserves (CON, NUL, COM1, ...). The
// real name is inside the file.
func customFileBase(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|%`, r) {
			fmt.Fprintf(&b, "%%%02X", r)
			continue
		}
		b.WriteRune(r)
	}
	s := b.String()
	if strings.HasPrefix(s, ".") {
		s = "%2E" + s[1:]
	}
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
		s = s[:len(s)-1] + fmt.Sprintf("%%%02X", s[len(s)-1])
	}
	base := strings.ToUpper(s)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if windowsReserved(strings.TrimRight(base, " ")) {
		s = fmt.Sprintf("%%%02X", s[0]) + s[1:]
	}
	return s
}

func windowsReserved(base string) bool {
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '0' && base[3] <= '9'
	}
	return false
}
