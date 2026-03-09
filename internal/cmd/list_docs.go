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

// list-docs flags
var (
	ldDoctype string
	ldFields  string
	ldFilters string
	ldLimit   int
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
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load site config
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		// Parse --fields flag (accepts both comma-separated and JSON array)
		var fields []string
		if ldFields != "" {
			fields, err = parseFields(ldFields)
			if err != nil {
				return fmt.Errorf("--fields: %w", err)
			}
		}

		// Build list options
		opts := client.ListOptions{
			Fields:  fields,
			Filters: ldFilters,
			Limit:   ldLimit,
			OrderBy: ldOrderBy,
		}

		// Call the API with a spinner for feedback.
		var rows []map[string]interface{}
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Fetching %s…", ldDoctype)).
			Action(func() {
				rows, apiErr = c.GetList(ldDoctype, opts)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		// Output
		if jsonOutput {
			output.PrintJSON(rows)
		} else {
			output.PrintTable(rows, fields)
		}

		return nil
	},
}

func init() {
	listDocsCmd.Flags().StringVarP(&ldDoctype, "doctype", "d", "", "Frappe DocType to list (required)")
	listDocsCmd.Flags().StringVarP(&ldFields, "fields", "f", "", `Fields to fetch. JSON array or comma-separated: '["name","modified"]' or name,modified`)
	listDocsCmd.Flags().StringVar(&ldFilters, "filters", "", `Filter expression as JSON: '{"status":"Open"}' or '[["status","=","Open"]]'`)
	listDocsCmd.Flags().IntVarP(&ldLimit, "limit", "l", 20, "Maximum number of records to return")
	listDocsCmd.Flags().StringVarP(&ldOrderBy, "order-by", "o", "", `Order results by field, e.g. "modified desc"`)
	_ = listDocsCmd.MarkFlagRequired("doctype")

	rootCmd.AddCommand(listDocsCmd)
}

// parseFields accepts a JSON array string or comma-separated field names.
func parseFields(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") {
		// JSON array
		var fields []string
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return nil, fmt.Errorf("invalid JSON array: %w", err)
		}
		return fields, nil
	}
	// Comma-separated shorthand: name,modified => ["name","modified"]
	parts := strings.Split(raw, ",")
	var fields []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			fields = append(fields, p)
		}
	}
	return fields, nil
}
