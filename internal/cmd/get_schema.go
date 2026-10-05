package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// get-schema flags
var (
	gsDoctype string
	gsFull    bool
	gsKeys    string
	gsRefresh bool
)

var getSchemaCmd = &cobra.Command{
	Use:   "get-schema",
	Short: "Show the field schema of a DocType",
	Long: `Display all field definitions for a Frappe DocType.

By default, --json returns a compact view: only meaningful DocType properties
and field attributes. Zero-value noise, internal metadata, and parent/owner
fields are stripped. The table output (no --json) is unchanged.

Flags (JSON mode only):
  --full    Return the complete unfiltered Frappe response.
  --keys    Comma-separated top-level keys to include in the output.
            Use --keys fields to get just the field definitions array.

The schema is cached locally for 1 hour per site (the compact view): a repeat
call prints the same output without a request. --refresh fetches it again
(after a Customize Form change); --full always fetches.

Examples:
  ffc get-schema -d "Sales Invoice"
  ffc get-schema -d "Sales Invoice" --json
  ffc get-schema -d "Sales Invoice" --json --full
  ffc get-schema -d "Sales Invoice" --json --keys fields
  ffc get-schema -d "Sales Invoice" --json --keys name,module,fields
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := loadSchema(cmd, gsDoctype, gsFull, gsRefresh)
		if err != nil {
			return err
		}

		if machineOutput() {
			result := res.compact
			if gsFull {
				result = res.full
			}
			result = selectKeys(result, gsKeys)
			if len(res.warnings) > 0 {
				// In-band too, so a script can tell the schema is partial.
				result["_warnings"] = res.warnings
			}
			for _, w := range res.warnings {
				fmt.Fprintln(os.Stderr, "warning: "+w)
			}
			return printResult(result)
		}
		for _, w := range res.warnings {
			fmt.Fprintln(os.Stderr, "warning: "+w)
		}
		if len(res.rows) == 0 {
			fmt.Fprintln(os.Stderr, "No fields found in schema.")
			return nil
		}
		output.PrintTable(res.rows, []string{"fieldname", "label", "fieldtype", "required", "options", "default"})
		return nil
	},
}

// schemaResult is what get-schema prints: the compact view (--json), the
// full definition (--full; nil when it came from the cache), the table rows
// and the warnings of a partial fetch.
type schemaResult struct {
	compact, full map[string]interface{}
	rows          []map[string]interface{}
	warnings      []string
}

// loadSchema answers from the local cache while it is fresh, unless full
// (the cache keeps only the compact view) or refresh is set; otherwise it
// fetches the schema and caches it. A cache hit sends no request and does
// not sign in. --debug says which it was.
func loadSchema(cmd *cobra.Command, doctype string, full, refresh bool) (schemaResult, error) {
	if !full && !refresh {
		if cfg, err := loadSiteConfig(); err == nil {
			if sc := readSchemaCache(cfg, doctype, time.Now()); sc != nil {
				client.DebugNote(fmt.Sprintf("schema of %q from the local cache, fetched %s ago (--refresh fetches it)",
					doctype, time.Since(sc.FetchedAt).Round(time.Second)))
				return schemaResult{compact: sc.Schema, rows: sc.Rows, warnings: sc.Warnings}, nil
			}
		}
	}
	return callSiteCfg(cmd, fmt.Sprintf("Fetching schema for %s…", doctype), func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (schemaResult, error) {
		doc, warnings, err := fetchSchema(ctx, c, doctype)
		if err != nil {
			return schemaResult{}, err
		}
		res := schemaResult{compact: compactSchema(doc), full: doc, rows: schemaTableRows(doc), warnings: warnings}
		_ = writeSchemaCache(cfg, doctype, res.compact, res.rows, warnings) // a cache that cannot be written costs the next call a request
		return res, nil
	})
}

// schemaTableRows are the rows of get-schema's table: one per field.
func schemaTableRows(doc map[string]interface{}) []map[string]interface{} {
	rawFields, _ := doc["fields"].([]interface{})
	rows := make([]map[string]interface{}, 0, len(rawFields))
	for _, rf := range rawFields {
		f, ok := rf.(map[string]interface{})
		if !ok {
			continue
		}
		reqd := ""
		if isTruthy(f["reqd"]) {
			reqd = "✓"
		}
		rows = append(rows, map[string]interface{}{
			"fieldname": f["fieldname"],
			"label":     f["label"],
			"fieldtype": f["fieldtype"],
			"required":  reqd,
			"options":   f["options"],
			"default":   f["default"],
		})
	}
	return rows
}

// fetchSchema returns a DocType definition as the desk sees it: the base
// DocType plus Custom Fields and every Property Setter override. Custom Field
// and Property Setter reads are best-effort — a user without read access to
// them still gets the base schema, with a warning explaining what is missing.
func fetchSchema(ctx context.Context, c *client.FrappeClient, doctype string) (map[string]interface{}, []string, error) {
	doc, err := c.GetDoc(ctx, "DocType", doctype)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	if err := mergeCustomFields(ctx, c, doctype, doc); err != nil {
		warnings = append(warnings, "custom fields could not be merged (showing base schema only): "+err.Error())
	}
	if err := applyPropertySetters(ctx, c, doctype, doc); err != nil {
		warnings = append(warnings, "Property Setter overrides could not be applied: "+err.Error())
	}
	return doc, warnings, nil
}

// compactSchema returns a filtered view of a raw DocType document.
// It retains only keys meaningful for understanding the DocType's structure,
// discarding operational noise, zero-value flags, and internal metadata.
//
// DocType level — always kept:
//
//	name, module, autoname, naming_rule, is_submittable, issingle, istable,
//	is_tree, is_virtual, read_only, custom
//
// DocType level — kept only when truthy: allow_rename, track_changes
// DocType level — kept only when non-empty: actions, links, states
//
// DocField level — always kept: fieldname, label, fieldtype
// DocField level — kept when truthy: reqd, read_only, hidden, unique, is_virtual,
//
//	non_negative, allow_on_submit, in_list_view, in_standard_filter,
//	set_only_once, translatable, ignore_user_permissions
//
// DocType level — kept when non-empty: title_field, search_fields, sort_field,
// sort_order, image_field, description, permissions (role/permlevel/rights)
//
// DocField level — kept when non-empty: options, default, description,
//
//	fetch_from, depends_on, mandatory_depends_on,
//	read_only_depends_on, precision, link_filters, insert_after
//
// DocField level — kept when truthy: fetch_if_empty, no_copy, search_index,
// bold, collapsible, print_hide, report_hide
//
// DocField level — kept when > 0: length, permlevel
func compactSchema(doc map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}

	// Always include these top-level keys.
	for _, k := range []string{
		"name", "module", "autoname", "naming_rule",
		"is_submittable", "issingle", "istable", "is_tree", "is_virtual",
		"read_only", "custom",
	} {
		if v, ok := doc[k]; ok {
			out[k] = v
		}
	}

	// Include only when non-empty: these name the fields Frappe uses for
	// titles, search, sorting and images.
	for _, k := range []string{
		"title_field", "search_fields", "sort_field", "sort_order", "image_field",
		"description",
	} {
		if v, ok := doc[k]; ok && !isEmpty(v) {
			out[k] = v
		}
	}

	if perms := compactPermissions(doc["permissions"]); len(perms) > 0 {
		out["permissions"] = perms
	}
	// Include only when truthy (non-zero / true).
	for _, k := range []string{"allow_rename", "track_changes"} {
		if v, ok := doc[k]; ok && isTruthy(v) {
			out[k] = v
		}
	}

	// Include array keys only when non-empty.
	for _, k := range []string{"actions", "links", "states"} {
		if v, ok := doc[k]; ok {
			if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
				out[k] = v
			}
		}
	}

	// Compact each field in the fields array.
	if rawFields, ok := doc["fields"].([]interface{}); ok {
		fields := make([]map[string]interface{}, 0, len(rawFields))
		for _, rf := range rawFields {
			if f, ok := rf.(map[string]interface{}); ok {
				fields = append(fields, compactField(f))
			}
		}
		out["fields"] = fields
	}

	return out
}

// compactField reduces a DocField map to its meaningful properties only.
func compactField(f map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}

	// Always include core identity.
	for _, k := range []string{"fieldname", "label", "fieldtype"} {
		if v, ok := f[k]; ok {
			out[k] = v
		}
	}

	// Include boolean/numeric flags only when truthy.
	for _, k := range []string{
		"reqd", "read_only", "hidden", "unique", "is_virtual", "non_negative",
		"allow_on_submit", "in_list_view", "in_standard_filter", "set_only_once",
		"translatable", "ignore_user_permissions",
	} {
		if v, ok := f[k]; ok && isTruthy(v) {
			out[k] = v
		}
	}

	// Include values only when non-empty. default may be numeric.
	for _, k := range []string{
		"options", "default", "description", "fetch_from",
		"depends_on", "mandatory_depends_on", "read_only_depends_on",
		"precision", "link_filters", "insert_after",
	} {
		if v, ok := f[k]; ok && !isEmpty(v) {
			out[k] = v
		}
	}

	// Include flags that change data handling only when truthy.
	for _, k := range []string{"fetch_if_empty", "no_copy", "search_index", "bold", "collapsible", "print_hide", "report_hide"} {
		if v, ok := f[k]; ok && isTruthy(v) {
			out[k] = v
		}
	}

	// Include numeric values only when > 0.
	for _, k := range []string{"length", "permlevel"} {
		if v, ok := f[k]; ok {
			if n, ok := numeric(v); ok && n > 0 {
				out[k] = v
			}
		}
	}

	return out
}

// mergeCustomFields fetches all Custom Field records for the given doctype and
// inserts them into doc["fields"] at the positions indicated by their insert_after
// field. Custom fields whose insert_after target doesn't exist in the base schema
// are appended at the end. This is necessary because GetDoc("DocType", ...) only
// returns the standard fields defined in the DocType itself — custom fields added
// via Customize Form are stored separately in the Custom Field doctype.
func mergeCustomFields(ctx context.Context, fc *client.FrappeClient, doctype string, doc map[string]interface{}) error {
	filters, _ := json.Marshal(map[string]interface{}{"dt": doctype})
	rows, err := fc.GetList(ctx, "Custom Field", client.ListOptions{
		Fields:  []string{"*"},
		Filters: string(filters),
		OrderBy: "idx asc",
		Limit:   -1, // no limit — a DocType can have many custom fields (L12)
	})
	if err != nil || len(rows) == 0 {
		return err
	}

	rawFields, _ := doc["fields"].([]interface{})

	// Group custom fields by their immediate insert_after target, preserving idx
	// order for siblings.
	afterMap := make(map[string][]map[string]interface{})
	for _, row := range rows {
		insertAfter, _ := row["insert_after"].(string)
		afterMap[insertAfter] = append(afterMap[insertAfter], row)
	}

	// Emit base fields, recursively splicing each custom field — and any custom
	// field chained after it — immediately after its target. This resolves the
	// common case of a custom field inserted after another custom field, which
	// the previous single-pass approach dropped to the end (M11). `visited`
	// guards against cycles and marks which custom fields were placed.
	visited := make(map[string]bool, len(rows))
	out := make([]interface{}, 0, len(rawFields)+len(rows))
	var emit func(fieldname string, node interface{})
	emit = func(fieldname string, node interface{}) {
		out = append(out, node)
		if fieldname == "" {
			return
		}
		for _, child := range afterMap[fieldname] {
			cfn, _ := child["fieldname"].(string)
			if cfn == "" || visited[cfn] {
				continue
			}
			visited[cfn] = true
			emit(cfn, child)
		}
	}
	for _, rf := range rawFields {
		fn := ""
		if f, ok := rf.(map[string]interface{}); ok {
			fn, _ = f["fieldname"].(string)
		}
		emit(fn, rf)
	}

	// Append custom fields whose insert_after target never appeared.
	for _, row := range rows {
		cfn, _ := row["fieldname"].(string)
		if cfn == "" || !visited[cfn] {
			out = append(out, row)
			if cfn != "" {
				visited[cfn] = true
			}
		}
	}

	doc["fields"] = out
	return nil
}

// applyPropertySetters applies the doctype's Property Setters — what
// Customize Form saves for every changed property (reqd, hidden, label,
// options, default, in_list_view, precision, ...), not only Select options —
// to the DocType or the matching field, cast by property_type. A DocType-level
// field_order setter (saved when fields are reordered) reorders the fields.
func applyPropertySetters(ctx context.Context, fc *client.FrappeClient, doctype string, doc map[string]interface{}) error {
	filters, _ := json.Marshal(map[string]interface{}{"doc_type": doctype})
	rows, err := fc.GetList(ctx, "Property Setter", client.ListOptions{
		Fields:  []string{"doctype_or_field", "field_name", "property", "property_type", "value"},
		Filters: string(filters),
		OrderBy: "creation asc",
		Limit:   -1,
	})
	if err != nil || len(rows) == 0 {
		return err
	}

	fieldsByName := map[string]map[string]interface{}{}
	rawFields, _ := doc["fields"].([]interface{})
	for _, rf := range rawFields {
		if f, ok := rf.(map[string]interface{}); ok {
			if fn, _ := f["fieldname"].(string); fn != "" {
				fieldsByName[fn] = f
			}
		}
	}

	for _, row := range rows {
		prop, _ := row["property"].(string)
		if prop == "" {
			continue
		}
		val := castProperty(row["value"], row["property_type"])
		switch row["doctype_or_field"] {
		case "DocField":
			fn, _ := row["field_name"].(string)
			if f, ok := fieldsByName[fn]; ok {
				f[prop] = val
			}
		case "DocType":
			if prop == "field_order" {
				reorderFields(doc, val)
				continue
			}
			doc[prop] = val
		}
	}
	return nil
}

// castProperty converts a Property Setter's string value to the JSON type the
// DocType itself uses for that property.
func castProperty(v, propertyType interface{}) interface{} {
	s, ok := v.(string)
	if !ok {
		return v
	}
	// A json.Number, like every number the site sends (UseNumber): the table
	// prints it the same whether it was just fetched or read from the cache.
	// A float is written as encoding/json writes a float64, so the JSON
	// output is what it was; NaN and Inf stay strings.
	switch propertyType {
	case "Check", "Int":
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return json.Number(strconv.Itoa(n))
		}
	case "Float", "Currency", "Percent":
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			if b, err := json.Marshal(f); err == nil {
				return json.Number(b)
			}
		}
	}
	return s
}

// reorderFields sorts doc["fields"] by a field_order value (a JSON-encoded
// list of fieldnames). Fields it does not mention keep their relative order
// at the end.
func reorderFields(doc map[string]interface{}, order interface{}) {
	s, _ := order.(string)
	var names []string
	if json.Unmarshal([]byte(s), &names) != nil || len(names) == 0 {
		return
	}
	rawFields, _ := doc["fields"].([]interface{})
	pos := make(map[string]int, len(names))
	for i, n := range names {
		pos[n] = i
	}
	rank := func(f interface{}) int {
		m, _ := f.(map[string]interface{})
		fn, _ := m["fieldname"].(string)
		if p, ok := pos[fn]; ok {
			return p
		}
		return len(names)
	}
	sort.SliceStable(rawFields, func(i, j int) bool { return rank(rawFields[i]) < rank(rawFields[j]) })
	doc["fields"] = rawFields
}

// compactPermissions keeps role, permlevel and the granted rights of each
// DocPerm row.
func compactPermissions(v interface{}) []map[string]interface{} {
	rows, _ := v.([]interface{})
	out := make([]map[string]interface{}, 0, len(rows))
	for _, r := range rows {
		p, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		c := map[string]interface{}{"role": p["role"]}
		if isTruthy(p["permlevel"]) {
			c["permlevel"] = p["permlevel"]
		}
		var rights []string
		for _, k := range []string{"select", "read", "write", "create", "delete", "submit", "cancel", "amend", "report", "export", "import", "print", "email", "share", "if_owner"} {
			if isTruthy(p[k]) {
				rights = append(rights, k)
			}
		}
		c["rights"] = rights
		out = append(out, c)
	}
	return out
}

// isEmpty reports nil, "" and empty arrays/objects.
func isEmpty(v interface{}) bool {
	switch val := v.(type) {
	case nil:
		return true
	case string:
		return val == ""
	case []interface{}:
		return len(val) == 0
	case map[string]interface{}:
		return len(val) == 0
	}
	return false
}

// isTruthy reports whether v represents a non-zero numeric or boolean true.
func isTruthy(v interface{}) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	n, ok := numeric(v)
	return ok && n != 0
}

// numeric returns v as a float64 for the number types a decoded response can
// hold (json.Number from the client, float64 from castProperty or tests).
func numeric(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case json.Number:
		f, err := val.Float64()
		return f, err == nil
	case float64:
		return val, true
	case int:
		return float64(val), true
	}
	return 0, false
}

func init() {
	getSchemaCmd.Flags().StringVarP(&gsDoctype, "doctype", "d", "", "DocType to inspect (required)")
	getSchemaCmd.Flags().BoolVar(&gsFull, "full", false, "Return the complete unfiltered Frappe response (JSON mode only)")
	getSchemaCmd.Flags().StringVar(&gsKeys, "keys", "", "Comma-separated top-level keys to include, e.g. name,fields (JSON mode only)")
	getSchemaCmd.Flags().BoolVar(&gsRefresh, "refresh", false, "Fetch the schema from the site instead of the local cache (kept 1 h)")
	_ = getSchemaCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(getSchemaCmd)
}
