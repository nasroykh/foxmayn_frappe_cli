package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// get-doc flags
var (
	gdDoctype string
	gdName    string
	gdFields  string
	gdKeys    string
)

var getDocCmd = &cobra.Command{
	Use:   "get-doc",
	Short: "Get a single document by name",
	Long: `Retrieve a single document from a Frappe DocType by its name.
The output is displayed as a Field/Value table by default.

For Single DocTypes (e.g. "System Settings", "HR Settings"), --name can be
omitted — the DocType name is used as the document name automatically.

Examples:
  ffc get-doc --doctype "Company" --name "My Company"
  ffc get-doc -d "User" -n "jane@example.com" --fields '["name","email","enabled"]'
  ffc get-doc -d "ToDo" -n "TDP-2024-001" --json
  ffc get-doc -d "Sales Invoice" -n "SINV-0001" --json --keys name,status,grand_total
  ffc get-doc -d "System Settings" --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var fields []string
		if gdFields != "" {
			var err error
			if fields, err = parseFields(gdFields); err != nil {
				return usageErrorf("--fields: %w", err)
			}
		}
		name := docNameOrSingle(gdName, gdDoctype)

		// --keys outside --fields needs the whole document.
		some := fields
		if gdKeys != "" && !subset(splitCSV(gdKeys), fields) {
			some = nil
		}
		doc, err := callSite(cmd, fmt.Sprintf("Fetching %s %s…", gdDoctype, name), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			if len(some) > 0 {
				return fetchDocFields(ctx, c, gdDoctype, name, some)
			}
			return c.GetDoc(ctx, gdDoctype, name)
		})
		if err != nil {
			return err
		}

		if machineOutput() {
			// --keys takes priority; otherwise --fields also narrows JSON output.
			switch {
			case gdKeys != "":
				doc = selectKeys(doc, gdKeys)
			case len(fields) > 0:
				doc, _ = filterKeys(doc, fields)
			}
			return printResult(doc, fields...)
		}
		output.PrintDocTable(doc, fields)
		return nil
	},
}

func init() {
	getDocCmd.Flags().StringVarP(&gdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	getDocCmd.Flags().StringVarP(&gdName, "name", "n", "", "Name of the document (defaults to the DocType name for Single DocTypes)")
	getDocCmd.Flags().StringVarP(&gdFields, "fields", "f", "", `Fields to display / include in output (JSON array or CSV)`)
	getDocCmd.Flags().StringVar(&gdKeys, "keys", "", "Comma-separated keys to include in JSON output, e.g. name,status,grand_total")

	_ = getDocCmd.MarkFlagRequired("doctype")

	rootCmd.AddCommand(getDocCmd)
}

// hookedDoctypes are the DocTypes Frappe itself guards with has_permission
// hooks (frappe/hooks.py, v16.36.1). Those hooks decide per document, and
// frappe.client.get_value does not run them, so fetchDocFields reads these
// with GetDoc. Hooks from other apps cannot be known from here: for them
// get_value shows what list-docs shows.
var hookedDoctypes = map[string]bool{
	"Event": true, "ToDo": true, "Note": true, "User": true, "Dashboard Chart": true, "Number Card": true,
	"Kanban Board": true, "Contact": true, "Address": true, "Communication": true, "Workflow Action": true,
	"File": true, "Prepared Report": true, "Notification Settings": true, "Dashboard Settings": true,
	"Notification Log": true, "User Invitation": true, "Document Follow": true,
}

// fetchDocFields returns the given top-level fields of a document, keyed as
// filterKeys would key them from GetDoc. Plain fields of a regular DocType
// come from frappe.client.get_value, which sends only those columns; a
// Single (name == doctype), a hooked DocType, a "*" or a name that is not a
// plain identifier (link.field, child.field) goes to GetDoc. So does any
// get_value answer that is an error, empty or missing a field (unknown or
// child-table field, a Table field, a document the list query does not
// show, a field above the user's permission level on v15), so errors and
// exit codes stay those of GetDoc. CLI get-doc and MCP get_doc share it.
func fetchDocFields(ctx context.Context, c *client.FrappeClient, doctype, name string, fields []string) (map[string]interface{}, error) {
	if valueFields(doctype, name, fields) {
		doc, err := c.GetValue(ctx, doctype, name, fields)
		var api *client.APIError
		switch {
		case err == nil:
			if out, missing := filterKeys(doc, fields); len(missing) == 0 {
				return out, nil
			}
		case !errors.As(err, &api):
			return nil, err
		}
	}
	doc, err := c.GetDoc(ctx, doctype, name)
	if err != nil {
		return nil, err
	}
	out, _ := filterKeys(doc, fields)
	return out, nil
}

// valueFields reports whether fetchDocFields may ask get_value.
func valueFields(doctype, name string, fields []string) bool {
	if name == doctype || hookedDoctypes[doctype] || len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		if !client.ValidIdentifier(f) {
			return false
		}
	}
	return true
}

// subset reports whether every key is in fields.
func subset(keys, fields []string) bool {
	for _, k := range keys {
		if !slices.Contains(fields, k) {
			return false
		}
	}
	return true
}
