package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
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
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Fetching schema for %s…", gsDoctype)).
			Action(func() {
				doc, apiErr = c.GetDoc("DocType", gsDoctype)
				if apiErr != nil {
					return
				}
				apiErr = mergeCustomFields(c, gsDoctype, doc)
				if apiErr != nil {
					return
				}
				apiErr = applyPropertySetterOverrides(c, gsDoctype, doc)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			result := map[string]interface{}(doc)
			if !gsFull {
				result = compactSchema(doc)
			}
			if gsKeys != "" {
				result = filterSchemaKeys(result, strings.Split(gsKeys, ","))
			}
			output.PrintJSON(result)
			return nil
		}

		// Table output: extract fields and render a schema-specific table.
		rawFields, ok := doc["fields"].([]interface{})
		if !ok || len(rawFields) == 0 {
			output.PrintError("No fields found in schema.")
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
func mergeCustomFields(fc *client.FrappeClient, doctype string, doc map[string]interface{}) error {
	filters, _ := json.Marshal(map[string]interface{}{"dt": doctype})
	rows, err := fc.GetList("Custom Field", client.ListOptions{
		Fields:  []string{"*"},
		Filters: string(filters),
		OrderBy: "idx asc",
		Limit:   500,
	})
	if err != nil || len(rows) == 0 {
		return err
	}

	rawFields, _ := doc["fields"].([]interface{})

	// Index existing fieldnames so we know where insert_after targets land.
	existingNames := make(map[string]bool, len(rawFields))
	for _, rf := range rawFields {
		if f, ok := rf.(map[string]interface{}); ok {
			if fn, ok := f["fieldname"].(string); ok {
				existingNames[fn] = true
			}
		}
	}

	// Group custom fields by their insert_after target.
	afterMap := make(map[string][]interface{})
	var orphans []interface{}
	for _, row := range rows {
		insertAfter, _ := row["insert_after"].(string)
		if existingNames[insertAfter] {
			afterMap[insertAfter] = append(afterMap[insertAfter], row)
		} else {
			orphans = append(orphans, row)
		}
	}

	// Rebuild the fields slice: standard fields in order, custom fields spliced in.
	merged := make([]interface{}, 0, len(rawFields)+len(rows))
	for _, rf := range rawFields {
		merged = append(merged, rf)
		if f, ok := rf.(map[string]interface{}); ok {
			if fn, ok := f["fieldname"].(string); ok {
				merged = append(merged, afterMap[fn]...)
			}
		}
	}
	merged = append(merged, orphans...)

	doc["fields"] = merged
	return nil
}

// applyPropertySetterOverrides fetches all Property Setter records for the given
// doctype where property="options" and patches the matching fields in doc in-place.
// This corrects Select field options that have been customised via Frappe's
// Customize Form / Property Setter, which are invisible in the raw DocType schema.
func applyPropertySetterOverrides(fc *client.FrappeClient, doctype string, doc map[string]interface{}) error {
	filters, _ := json.Marshal(map[string]interface{}{
		"doc_type": doctype,
		"property": "options",
	})
	rows, err := fc.GetList("Property Setter", client.ListOptions{
		Fields:  []string{"field_name", "value"},
		Filters: string(filters),
		Limit:   500,
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
