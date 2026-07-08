package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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

Examples:
  ffc get-schema -d "Sales Invoice"
  ffc get-schema -d "Sales Invoice" --json
  ffc get-schema -d "Sales Invoice" --json --full
  ffc get-schema -d "Sales Invoice" --json --keys fields
  ffc get-schema -d "Sales Invoice" --json --keys name,module,fields
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		var doc map[string]interface{}
		var apiErr, cfWarn, psWarn error
		c, err := client.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		_ = runSpinner(fmt.Sprintf("Fetching schema for %s…", gsDoctype), func() {
			doc, apiErr = c.GetDoc(cmd.Context(), "DocType", gsDoctype)
			if apiErr != nil {
				return
			}
			// Custom Field / Property Setter reads are best-effort: a 403 or
			// error on them should degrade to the base schema, not abort (L12).
			cfWarn = mergeCustomFields(cmd.Context(), c, gsDoctype, doc)
			psWarn = applyPropertySetterOverrides(cmd.Context(), c, gsDoctype, doc)
		})
		if apiErr != nil {
			return apiErr
		}
		if cfWarn != nil {
			fmt.Fprintf(os.Stderr, "warning: could not merge custom fields (%v) — showing base schema only\n", cfWarn)
		}
		if psWarn != nil {
			fmt.Fprintf(os.Stderr, "warning: could not apply Property Setter overrides (%v)\n", psWarn)
		}

		if jsonOutput {
			result := map[string]interface{}(doc)
			if !gsFull {
				result = compactSchema(doc)
			}
			if gsKeys != "" {
				result = filterSchemaKeys(result, strings.Split(gsKeys, ","))
			}
			return output.PrintJSON(result)
		}

		// Table output: extract fields and render a schema-specific table.
		rawFields, ok := doc["fields"].([]interface{})
		if !ok || len(rawFields) == 0 {
			fmt.Fprintln(os.Stderr, "No fields found in schema.")
			return nil
		}

		rows := make([]map[string]interface{}, 0, len(rawFields))
		for _, rf := range rawFields {
			f, ok := rf.(map[string]interface{})
			if !ok {
				continue
			}
			reqd := ""
			if r, ok := f["reqd"]; ok {
				switch v := r.(type) {
				case float64:
					if v == 1 {
						reqd = "✓"
					}
				case bool:
					if v {
						reqd = "✓"
					}
				}
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

		output.PrintTable(rows, []string{"fieldname", "label", "fieldtype", "required", "options", "default"})
		return nil
	},
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
// DocField level — kept when non-empty string: options, default, description,
//
//	fetch_from, depends_on, mandatory_depends_on, read_only_depends_on
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

	// Include string values only when non-empty.
	for _, k := range []string{
		"options", "default", "description", "fetch_from",
		"depends_on", "mandatory_depends_on", "read_only_depends_on",
	} {
		if v, ok := f[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				out[k] = v
			}
		}
	}

	// Include numeric values only when > 0.
	for _, k := range []string{"length", "permlevel"} {
		if v, ok := f[k]; ok {
			if n, ok := v.(float64); ok && n > 0 {
				out[k] = v
			}
		}
	}

	return out
}

// filterSchemaKeys returns a new map containing only the specified top-level keys.
func filterSchemaKeys(doc map[string]interface{}, keys []string) map[string]interface{} {
	out := make(map[string]interface{}, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if v, ok := doc[k]; ok {
			out[k] = v
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

// applyPropertySetterOverrides fetches all Property Setter records for the given
// doctype where property="options" and patches the matching fields in doc in-place.
// This corrects Select field options that have been customised via Frappe's
// Customize Form / Property Setter, which are invisible in the raw DocType schema.
func applyPropertySetterOverrides(ctx context.Context, fc *client.FrappeClient, doctype string, doc map[string]interface{}) error {
	filters, _ := json.Marshal(map[string]interface{}{
		"doc_type": doctype,
		"property": "options",
	})
	rows, err := fc.GetList(ctx, "Property Setter", client.ListOptions{
		Fields:  []string{"field_name", "value"},
		Filters: string(filters),
		Limit:   -1,
	})
	if err != nil || len(rows) == 0 {
		return err
	}

	overrides := make(map[string]string, len(rows))
	for _, row := range rows {
		fn, _ := row["field_name"].(string)
		val, _ := row["value"].(string)
		if fn != "" {
			overrides[fn] = val
		}
	}

	rawFields, ok := doc["fields"].([]interface{})
	if !ok {
		return nil
	}
	for _, rf := range rawFields {
		f, ok := rf.(map[string]interface{})
		if !ok {
			continue
		}
		fn, _ := f["fieldname"].(string)
		if val, found := overrides[fn]; found {
			f["options"] = val
		}
	}
	return nil
}

// isTruthy reports whether v represents a non-zero numeric or boolean true.
func isTruthy(v interface{}) bool {
	switch val := v.(type) {
	case float64:
		return val != 0
	case bool:
		return val
	case int:
		return val != 0
	}
	return false
}

func init() {
	getSchemaCmd.Flags().StringVarP(&gsDoctype, "doctype", "d", "", "DocType to inspect (required)")
	getSchemaCmd.Flags().BoolVar(&gsFull, "full", false, "Return the complete unfiltered Frappe response (JSON mode only)")
	getSchemaCmd.Flags().StringVar(&gsKeys, "keys", "", "Comma-separated top-level keys to include, e.g. name,fields (JSON mode only)")
	_ = getSchemaCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(getSchemaCmd)
}
