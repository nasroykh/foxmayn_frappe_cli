package cmd

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// customize push flags
var (
	cuDoctypes string
	cuTypes    string
	cuYes      bool
	cuFailFast bool
)

var customizePushCmd = &cobra.Command{
	Use:   "push DIR",
	Short: "Apply a folder of customizations to a site",
	Long: `Apply the files that customize pull wrote to a site.

Each document is compared with the site's copy first. The plan lists what
would be created and updated, with the changed fields, and push asks before
it writes (--yes skips the question, --dry-run only prints the plan).

Only the fields in the file are compared and sent: a field that was cleared
on the source site (and so is missing from the file) keeps its value on the
target. Push never deletes. With -d, the documents on the site that the
folder lacks are listed, and left alone.

Each update carries the modification time read for the plan, so a document
changed on the site in between is refused, not overwritten. Password fields
are never sent.

Examples:
  ffc customize push customizations --dry-run
  ffc --site prod customize push customizations -d "Sales Invoice"
  ffc --site prod customize push customizations --types custom_field,property_setter --yes
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		types, err := customTypeList(cuTypes)
		if err != nil {
			return err
		}
		doctypes := splitCSV(cuDoctypes)
		docs, warnings, err := readCustomDir(args[0], doctypes, types)
		if err != nil {
			return err
		}
		c, cfg, err := newClientCfg(cmd.Context())
		if err != nil {
			return err
		}
		defer c.CloseQuietly()
		var plan *customPushPlan
		var planErr error
		_ = runSpinner("Comparing with the site…", func() {
			plan, planErr = planCustomPush(cmd.Context(), c, docs, doctypes, types)
		})
		if planErr != nil {
			return planErr
		}
		plan.Warnings = append(warnings, plan.Warnings...)
		for _, w := range plan.Warnings {
			output.PrintWarning(w)
		}
		changes := plan.changes()
		if dryRunOn(cmd) || len(changes) == 0 {
			if machineOutput() {
				return printResult(plan.JSON(nil))
			}
			plan.print(os.Stdout)
			return nil
		}
		if !machineOutput() {
			plan.print(os.Stdout)
		} else if !cuYes {
			plan.print(os.Stderr) // the question needs the plan in sight
		}
		if !cuYes {
			site := cfg.Name
			if site == "" {
				site = redactedURL(cfg.URL)
			}
			if err := confirm(fmt.Sprintf("Apply %d changes to %s?", len(changes), site)); err != nil {
				return err
			}
		}
		var rep bulkReport
		_ = runSpinner("Applying customizations…", func() {
			rep = runBulk(cmd.Context(), len(changes), 1, cuFailFast, "applied", func(ctx context.Context, i int) (string, error) {
				return changes[i].apply(ctx, c)
			})
		})
		if machineOutput() {
			if err := printResult(plan.JSON(&rep)); err != nil {
				return err
			}
			return rep.err()
		}
		fmt.Println()
		return printBulkReport(rep, "customization")
	},
}

func init() {
	f := customizePushCmd.Flags()
	f.StringVarP(&cuDoctypes, "doctype", "d", "", "Only the customizations of these DocTypes (comma-separated)")
	f.StringVar(&cuTypes, "types", "", "Only these kinds, comma-separated: "+strings.Join(customTypeSlugs(), ", "))
	f.BoolVarP(&cuYes, "yes", "y", false, "Apply without asking")
	f.BoolVar(&cuFailFast, "fail-fast", false, "Stop after the first document that fails")
	addDryRun(customizePushCmd, false)
	customizeCmd.AddCommand(customizePushCmd)
}

// customPushOrder is the order push writes in: what a document links to
// goes first (Workflow States and Actions before Workflows, Custom Fields
// before the Property Setters that may change them, Print Formats before
// Notifications).
var customPushOrder = []string{"workflow_state", "workflow_action_master", "custom_field", "property_setter",
	"client_script", "server_script", "print_format", "report", "workflow", "notification", "webhook"}

// customKind returns the kind stored in folder slug.
func customKind(slug string) (customType, bool) {
	for _, t := range append(slices.Clone(customTypes), workflowStateType, workflowActionType) {
		if t.slug == slug {
			return t, true
		}
	}
	return customType{}, false
}

// readCustomDir reads the documents of the selected kinds from a folder
// that pull wrote, with their sidecar files put back in place. With
// doctypes, only the documents customizing those DocTypes are kept, plus
// the Workflow States and Actions their Workflows use. A file that cannot
// be read stops the push before any request.
func readCustomDir(dir string, doctypes []string, types []customType) ([]customDoc, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, usageErrorf("reading %s: %w", dir, err)
	}
	var warnings []string
	for _, e := range entries {
		if _, known := customKind(e.Name()); e.IsDir() && !known && !strings.HasPrefix(e.Name(), ".") {
			warnings = append(warnings, fmt.Sprintf("folder %s skipped: not a kind of customization", e.Name()))
		}
	}
	selected := map[string]bool{}
	for _, t := range types {
		selected[t.slug] = true
	}
	if selected["workflow"] {
		selected[workflowStateType.slug], selected[workflowActionType.slug] = true, true
	}
	in := map[string]bool{}
	for _, dt := range doctypes {
		in[dt] = true
	}
	var docs []customDoc
	var problems []string
	states, actions := map[string]bool{}, map[string]bool{}
	for _, slug := range customPushOrder {
		t, _ := customKind(slug)
		if !selected[slug] {
			continue
		}
		folder := filepath.Join(dir, slug)
		fi, err := os.Lstat(folder)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil && !fi.IsDir() {
			err = errors.New("not a folder (push does not follow links)")
		}
		var files []os.DirEntry
		if err == nil {
			files, err = os.ReadDir(folder)
		}
		if err != nil {
			return nil, nil, usageErrorf("reading %s: %w", folder, err)
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			doc, err := readCustomFile(folder, f.Name())
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s/%s: %v", slug, f.Name(), err))
				continue
			}
			if want := customDerivedName(slug, doc); want != "" && want != doc["name"] {
				problems = append(problems, fmt.Sprintf("%s/%s: Frappe names this document %q from its fields, not %q", slug, f.Name(), want, doc["name"]))
				continue
			}
			if len(in) > 0 && t.selector != "" {
				if target, _ := doc[t.selector].(string); !in[target] {
					continue
				}
			}
			if slug == "workflow" {
				workflowLinks(doc, states, actions)
			}
			docs = append(docs, customDoc{Type: t, Name: doc["name"].(string), Doc: doc})
		}
	}
	if len(problems) > 0 {
		return nil, nil, usageErrorf("%s has files push cannot read, nothing was sent:\n  %s", dir, strings.Join(problems, "\n  "))
	}
	if len(in) > 0 {
		// Workflow States and Actions name no DocType: keep those the
		// selected Workflows use.
		docs = slices.DeleteFunc(docs, func(d customDoc) bool {
			switch d.Type.slug {
			case workflowStateType.slug:
				return !states[d.Name]
			case workflowActionType.slug:
				return !actions[d.Name]
			}
			return false
		})
	}
	return docs, warnings, nil
}

// customDerivedName is the name Frappe gives a document of a kind named
// from its fields (custom_field.py autoname, property_setter.py autoname,
// "field:" autonames), "" for the kinds that keep the name sent. Insert
// recomputes it, so a file whose name differs would be created under
// another name and created again on every push.
func customDerivedName(slug string, doc map[string]interface{}) string {
	str := func(k string) string { v, _ := doc[k].(string); return v }
	switch slug {
	case "custom_field":
		if str("dt") != "" && str("fieldname") != "" {
			return str("dt") + "-" + str("fieldname")
		}
	case "property_setter":
		if str("doc_type") != "" && str("property") != "" {
			return str("doc_type") + "-" + cmp.Or(str("field_name"), str("row_name"), "main") + "-" + str("property")
		}
	case "workflow":
		return str("workflow_name")
	case "workflow_state":
		return str("workflow_state_name")
	case "workflow_action_master":
		return str("workflow_action_name")
	case "report":
		return str("report_name")
	}
	return ""
}

// customMaxFile is the largest file push reads.
const customMaxFile = 16 << 20

// readCustomRegular reads a regular file: never a link (a cloned
// repository may hold one pointing at a secret, which push would send as a
// field), a device or a pipe.
func readCustomRegular(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (push does not follow links)", filepath.Base(path))
	}
	if fi.Size() > customMaxFile {
		return nil, fmt.Errorf("%s is larger than %d MiB", filepath.Base(path), customMaxFile>>20)
	}
	return os.ReadFile(path)
}

// readCustomFile reads one document file and its sidecars. The file name
// must be the one pull gives the document's name, and a sidecar must be one
// this document's name could have, in the same folder: an edited file
// cannot make push read another file.
func readCustomFile(folder, file string) (map[string]interface{}, error) {
	b, err := readCustomRegular(filepath.Join(folder, file))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var doc map[string]interface{}
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, fmt.Errorf("not a JSON object: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("data after the JSON object")
	}
	name, _ := doc["name"].(string)
	if name == "" {
		return nil, errors.New(`no "name"`)
	}
	base := customFileBase(name)
	if base+".json" != file {
		return nil, fmt.Errorf("the name %q belongs in %s.json", name, base)
	}
	for k, v := range doc {
		ref, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		side, isRef := ref[sidecarFileKey].(string)
		if !isRef || len(ref) != 1 {
			continue
		}
		if !isSidecarOf(side, base) {
			return nil, fmt.Errorf("%s: %q is not a sidecar of this document (%s.<field>.<ext>)", k, side, base)
		}
		content, err := readCustomRegular(filepath.Join(folder, side))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		doc[k] = string(content)
	}
	return doc, nil
}

// customPushItem is one document of the plan.
type customPushItem struct {
	Type   customType
	Name   string
	Action string                 // create, update or unchanged
	Fields []string               // the fields an update changes
	local  map[string]interface{} // the document as push sends it
	data   map[string]interface{} // the request body
}

func (it *customPushItem) label() string { return it.Type.slug + "/" + it.Name }

// apply sends the item: a create with the whole document, an update with
// the changed fields and the modification time read for the plan.
func (it *customPushItem) apply(ctx context.Context, c *client.FrappeClient) (string, error) {
	var err error
	if it.Action == "create" {
		_, err = c.CreateDoc(ctx, it.Type.doctype, it.data)
	} else {
		_, err = c.UpdateDoc(ctx, it.Type.doctype, it.Name, it.data)
		err = conflictError(err, it.Type.doctype, it.Name, "the plan was made")
	}
	return it.label(), err
}

// customPushPlan is what push would do.
type customPushPlan struct {
	Items []*customPushItem
	// Extras are documents on the site, for the -d DocTypes, that the
	// folder lacks. Push never deletes them.
	Extras   []string
	Warnings []string
}

func (p *customPushPlan) changes() []*customPushItem {
	var out []*customPushItem
	for _, it := range p.Items {
		if it.Action != "unchanged" {
			out = append(out, it)
		}
	}
	return out
}

func (p *customPushPlan) count(action string) int {
	n := 0
	for _, it := range p.Items {
		if it.Action == action {
			n++
		}
	}
	return n
}

// JSON is the machine-readable plan, with the run's report when it ran.
func (p *customPushPlan) JSON(rep *bulkReport) map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(p.Items))
	for _, it := range p.Items {
		m := map[string]interface{}{"type": it.Type.slug, "name": it.Name, "action": it.Action}
		if it.Action == "update" {
			m["fields"] = it.Fields
		}
		items = append(items, m)
	}
	out := map[string]interface{}{
		"plan":      items,
		"create":    p.count("create"),
		"update":    p.count("update"),
		"unchanged": p.count("unchanged"),
		"extras":    p.Extras,
	}
	if len(p.Warnings) > 0 {
		out["warnings"] = p.Warnings
	}
	if rep != nil {
		maps.Copy(out, rep.JSON())
	}
	return out
}

func (p *customPushPlan) print(w io.Writer) {
	for _, it := range p.changes() {
		line := fmt.Sprintf("%-7s %s", it.Action, it.label())
		if it.Action == "update" {
			line += ": " + strings.Join(it.Fields, ", ")
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "\n%d to create, %d to update, %d unchanged\n", p.count("create"), p.count("update"), p.count("unchanged"))
	if len(p.Extras) > 0 {
		fmt.Fprintf(w, "%d on the site but not in the folder (push does not delete):\n", len(p.Extras))
		for _, e := range p.Extras {
			fmt.Fprintln(w, "  "+e)
		}
	}
}

// planCustomPush compares each document with the site's copy. Fields the
// site's meta does not know and Password fields are left out (Frappe would
// ignore the first, and an empty password deletes the stored secret).
func planCustomPush(ctx context.Context, c *client.FrappeClient, docs []customDoc, doctypes []string, types []customType) (*customPushPlan, error) {
	plan := &customPushPlan{Extras: []string{}}
	metas := map[string]*customMeta{}
	left := &customLeftOut{unknown: map[string]bool{}, secret: map[string]bool{}}
	var replaced []string // tables whose rows lose their passwords
	scripts := false
	for _, d := range docs {
		meta := metas[d.Type.doctype]
		if meta == nil {
			var err error
			if meta, err = readCustomMeta(ctx, c, d.Type.doctype); err != nil {
				return nil, err
			}
			metas[d.Type.doctype] = meta
		}
		it := &customPushItem{Type: d.Type, Name: d.Name}
		current, err := c.GetDoc(ctx, d.Type.doctype, d.Name)
		var api *client.APIError
		switch {
		case errors.As(err, &api) && api.Status == http.StatusNotFound && !api.MissingDocType:
			current = nil
		case err != nil:
			return nil, fmt.Errorf("reading %s %s: %w", d.Type.doctype, d.Name, err)
		}
		var target map[string]interface{}
		if current != nil {
			target, _ = normalizeCustomDoc(current, d.Type.doctype, meta.secret, meta.tables)
		}
		local := cleanPushDoc(d, meta, target, left)
		it.local = local
		if current == nil {
			it.Action, it.data = "create", local
			if d.Type.doctype == "Webhook" && sameValue(local["enable_security"], 1) {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("Webhook %s: security is on, but the secret is never pushed; set it on the site", d.Name))
			}
		} else {
			it.data = map[string]interface{}{"modified": current["modified"]}
			for _, k := range slices.Sorted(maps.Keys(local)) {
				if k != "name" && !sameCustomValue(local[k], target[k]) {
					it.Fields = append(it.Fields, k)
					it.data[k] = local[k]
					if child := meta.tables[k]; len(meta.secret[child]) > 0 {
						replaced = append(replaced, fmt.Sprintf("%s %s: the %s rows are replaced, so their password fields (%s) must be set again on the site",
							d.Type.doctype, d.Name, k, strings.Join(slices.Sorted(maps.Keys(meta.secret[child])), ", ")))
					}
					if cols := siteOnlyColumns(local[k], target[k]); len(cols) > 0 {
						replaced = append(replaced, fmt.Sprintf("%s %s: the %s rows are replaced, and the columns the file lacks (%s) go back to their defaults",
							d.Type.doctype, d.Name, k, strings.Join(cols, ", ")))
					}
				}
			}
			it.Action = "update"
			if len(it.Fields) == 0 {
				it.Action, it.data = "unchanged", nil
			}
		}
		scripts = scripts || (d.Type.slug == "server_script" && it.Action != "unchanged")
		if d.Type.slug == "workflow" && it.Action != "unchanged" {
			w, err := workflowDeactivates(ctx, c, d.Name, local, target)
			if err != nil {
				return nil, err
			}
			if w != "" {
				plan.Warnings = append(plan.Warnings, w)
			}
		}
		plan.Items = append(plan.Items, it)
	}
	if len(left.unknown) > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("not fields on this site, left out: %s", strings.Join(slices.Sorted(maps.Keys(left.unknown)), ", ")))
	}
	if len(left.secret) > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("password fields are never sent, left out: %s", strings.Join(slices.Sorted(maps.Keys(left.secret)), ", ")))
	}
	plan.Warnings = append(plan.Warnings, replaced...)
	if scripts {
		// Saving a Server Script needs no setting; running one needs
		// server_script_enabled. null: the user may not read Server Scripts.
		on, err := c.CallMethod(ctx, "frappe.core.doctype.server_script.server_script.enabled", nil, true)
		if err == nil && on != nil && !sameValue(on, true) {
			plan.Warnings = append(plan.Warnings, "Server Scripts are disabled on this site (server_script_enabled): pushed scripts will not run")
		}
	}
	orderCustomFields(plan.Items)
	if len(doctypes) > 0 {
		extras, err := customExtras(ctx, c, docs, doctypes, types)
		if err != nil {
			return nil, err
		}
		plan.Extras = extras
	}
	return plan, nil
}

// workflowDeactivates warns when saving an active Workflow turns off the
// site's other active Workflows of the same DocType: Workflow.validate
// calls set_active, which sets is_active 0 on every Workflow of the
// DocType (workflow.py:35, 116), on create and on every update.
func workflowDeactivates(ctx context.Context, c *client.FrappeClient, name string, local, target map[string]interface{}) (string, error) {
	active, ok := local["is_active"]
	if !ok {
		active = target["is_active"]
	}
	dt, _ := local["document_type"].(string)
	if !sameValue(active, 1) || dt == "" {
		return "", nil
	}
	rows, err := c.GetList(ctx, "Workflow", client.ListOptions{Fields: []string{"name"}, Limit: -1,
		Filters: mustFilters([][]interface{}{{"document_type", "=", dt}, {"is_active", "=", 1}, {"name", "!=", name}})})
	if err != nil {
		return "", fmt.Errorf("listing the active Workflows of %s: %w", dt, err)
	}
	var others []string
	for _, r := range rows {
		others = append(others, fmt.Sprint(r["name"]))
	}
	if len(others) == 0 {
		return "", nil
	}
	return fmt.Sprintf("Workflow %s is active: saving it deactivates %s on %s", name, strings.Join(others, ", "), dt), nil
}

// siteOnlyColumns names the row fields the site's table has a value for
// and no row of the file has: a replaced table loses them.
func siteOnlyColumns(local, site interface{}) []string {
	inFile := map[string]bool{}
	lrows, _ := local.([]interface{})
	for _, r := range lrows {
		row, _ := r.(map[string]interface{})
		for k := range row {
			inFile[k] = true
		}
	}
	cols := map[string]bool{}
	srows, _ := site.([]interface{})
	for _, r := range srows {
		row, _ := r.(map[string]interface{})
		for k, v := range row {
			if !inFile[k] && !sameValue(v, "") && !sameValue(v, 0) {
				cols[k] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(cols))
}

// customLeftOut collects the fields push leaves out, as "DocType.field".
type customLeftOut struct {
	unknown map[string]bool // not in the site's meta
	secret  map[string]bool // Password fields
}

// cleanPushDoc is the document as push sends it: no Password fields, no
// fields the site's meta lacks (in rows too). A top-level unknown field
// whose value the site's copy already has is dropped silently: it is a
// column the site keeps without a field, and so is the same everywhere.
func cleanPushDoc(d customDoc, meta *customMeta, target map[string]interface{}, left *customLeftOut) map[string]interface{} {
	dt := d.Type.doctype
	out := map[string]interface{}{}
	for k, v := range d.Doc {
		switch {
		case k == "name":
		case meta.secret[dt][k]:
			left.secret[dt+"."+k] = true
			continue
		case !meta.fields[dt][k]:
			if target == nil || !sameCustomValue(v, target[k]) {
				left.unknown[dt+"."+k] = true
			}
			continue
		}
		if rows, ok := v.([]interface{}); ok && meta.tables[k] != "" {
			v = cleanPushRows(rows, meta.tables[k], meta, left)
		}
		out[k] = v
	}
	return out
}

func cleanPushRows(rows []interface{}, child string, meta *customMeta, left *customLeftOut) []interface{} {
	fields := meta.fields[child]
	out := make([]interface{}, 0, len(rows))
	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			out = append(out, r)
			continue
		}
		clean := map[string]interface{}{}
		for k, v := range row {
			switch {
			case meta.secret[child][k]:
				left.secret[child+"."+k] = true
			case fields != nil && !fields[k]:
				left.unknown[child+"."+k] = true
			default:
				clean[k] = v
			}
		}
		out = append(out, clean)
	}
	return out
}

// sameCustomValue compares a file's value with the site's. A table is the
// same when it has as many rows and each row's fields in the file have the
// site's values: a newer Frappe adds columns to rows, which must not show
// as a change on every push.
func sameCustomValue(local, site interface{}) bool {
	lrows, ok := local.([]interface{})
	if !ok {
		return sameValue(local, site)
	}
	srows, ok := site.([]interface{})
	if !ok || len(lrows) != len(srows) {
		return len(lrows) == 0 && site == nil
	}
	for i := range lrows {
		lrow, lok := lrows[i].(map[string]interface{})
		srow, sok := srows[i].(map[string]interface{})
		if !lok || !sok {
			if !sameValue(lrows[i], srows[i]) {
				return false
			}
			continue
		}
		for k, v := range lrow {
			if !sameValue(v, srow[k]) {
				return false
			}
		}
	}
	return true
}

// orderCustomFields puts a Custom Field after the pushed Custom Field of
// the same DocType it is inserted after, so the field it names exists when
// it is created. The other items keep their place.
func orderCustomFields(items []*customPushItem) {
	var idx []int
	for i, it := range items {
		if it.Type.slug == "custom_field" {
			idx = append(idx, i)
		}
	}
	fields := make([]*customPushItem, len(idx))
	byField := map[string]*customPushItem{}
	key := func(it *customPushItem, field string) string { return fmt.Sprint(it.local["dt"]) + "\x00" + field }
	for j, i := range idx {
		fields[j] = items[i]
		if f, _ := items[i].local["fieldname"].(string); f != "" {
			byField[key(items[i], f)] = items[i]
		}
	}
	var sorted []*customPushItem
	done := map[*customPushItem]bool{}
	var visit func(it *customPushItem)
	visit = func(it *customPushItem) {
		if done[it] {
			return
		}
		done[it] = true // set first: a cycle stops here
		if after, _ := it.local["insert_after"].(string); after != "" {
			if dep := byField[key(it, after)]; dep != nil {
				visit(dep)
			}
		}
		sorted = append(sorted, it)
	}
	for _, it := range fields {
		visit(it)
	}
	for j, i := range idx {
		items[i] = sorted[j]
	}
}

// customExtras lists the documents of the -d DocTypes on the site that the
// folder lacks, as pull would select them.
func customExtras(ctx context.Context, c *client.FrappeClient, docs []customDoc, doctypes []string, types []customType) ([]string, error) {
	have := map[string]bool{}
	for _, d := range docs {
		have[d.Type.slug+"/"+d.Name] = true
	}
	out := []string{}
	sel := &customSel{Doctypes: doctypes}
	for _, t := range types {
		names, err := customNames(ctx, c, t, sel)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if !have[t.slug+"/"+n] {
				out = append(out, t.slug+"/"+n)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
