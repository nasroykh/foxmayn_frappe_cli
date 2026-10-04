package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// list-docs flags
var (
	ldDoctype string
	ldFields  string
	ldFilters string
	ldLimit   int
	ldStart   int
	ldOrderBy string
)

var listDocsCmd = &cobra.Command{
	Use:   "list-docs",
	Short: "List documents of a given DocType",
	Long: `Retrieve a list of documents from a Frappe DocType via the REST API.

Examples:
  ffc list-docs --doctype "Company"
  ffc list-docs --doctype "User" --fields '["name","email","enabled"]' --limit 10
  ffc list-docs --doctype "ToDo" --filters '{"status":"Open"}' --order-by "modified desc"
  ffc list-docs --doctype "Sales Invoice" --limit 5 --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var fields []string
		if ldFields != "" {
			var err error
			if fields, err = parseFields(ldFields); err != nil {
				return usageErrorf("--fields: %w", err)
			}
		}
		if err := validateFiltersJSON(ldFilters); err != nil {
			return err
		}
		limit, err := listLimit("--limit", ldLimit)
		if err != nil {
			return err
		}
		if ldStart < 0 {
			return usageErrorf("--start must be >= 0")
		}

		opts := client.ListOptions{
			Fields:  fields,
			Filters: ldFilters,
			Limit:   limit,
			Start:   ldStart,
			OrderBy: ldOrderBy,
		}
		rows, err := callSite(cmd, fmt.Sprintf("Fetching %s…", ldDoctype), func(ctx context.Context, c *client.FrappeClient) ([]map[string]interface{}, error) {
			return c.GetList(ctx, ldDoctype, opts)
		})
		if err != nil {
			return err
		}

		if jsonOutput {
			return output.PrintJSON(rows)
		}
		output.PrintTable(rows, fields)
		return nil
	},
}

func init() {
	listDocsCmd.Flags().StringVarP(&ldDoctype, "doctype", "d", "", "Frappe DocType to list (required)")
	listDocsCmd.Flags().StringVarP(&ldFields, "fields", "f", "", `Fields to fetch. JSON array or comma-separated: '["name","modified"]' or name,modified`)
	listDocsCmd.Flags().StringVar(&ldFilters, "filters", "", `Filter expression as JSON: '{"status":"Open"}' or '[["status","=","Open"]]'`)
	listDocsCmd.Flags().IntVarP(&ldLimit, "limit", "l", 20, "Maximum records to return (0 = no limit)")
	listDocsCmd.Flags().IntVar(&ldStart, "start", 0, "Offset into the result set (for pagination)")
	listDocsCmd.Flags().StringVarP(&ldOrderBy, "order-by", "o", "", `Order results by field, e.g. "modified desc"`)
	_ = listDocsCmd.MarkFlagRequired("doctype")

	rootCmd.AddCommand(listDocsCmd)
}

// parseFields accepts a JSON array string or comma-separated field names.
// Field expressions may contain commas ("count(name) as n" is fine, but
// "ifnull(a, b)" is not), so use the JSON form for those.
func parseFields(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") {
		var fields []string
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return nil, usageErrorf("invalid JSON array: %w", err)
		}
		return fields, nil
	}
	if strings.HasPrefix(raw, "{") {
		return nil, usageErrorf("expected a JSON array or comma-separated names, not an object")
	}
	return splitCSV(raw), nil
}
