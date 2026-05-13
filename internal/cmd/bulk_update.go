package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
	"github.com/spf13/cobra"
)

// bulk-update flags
var (
	buDoctype string
	buData    string
	buFile    string
)

var bulkUpdateCmd = &cobra.Command{
	Use:   "bulk-update",
	Short: "Update multiple Frappe documents from a JSON array",
	Long: `Update multiple documents in a Frappe DocType in one command.

Provide records inline with --data or load them from a JSON file with --file.
Each element of the array must include a "name" field identifying the document,
plus any fields you want to change.

Processing continues even when individual items fail. A per-item summary is
printed at the end. The command exits with a non-zero code if any item failed.

Examples:
  ffc bulk-update -d "ToDo" --data '[{"name":"TD-0001","status":"Closed"},{"name":"TD-0002","priority":"High"}]'
  ffc bulk-update -d "Customer" --file updates.json
  ffc bulk-update -d "Item" --file items.json --json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		raw := buData
		if buFile != "" {
			b, err := os.ReadFile(buFile)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			raw = string(b)
		}
		if raw == "" {
			return fmt.Errorf("provide --data or --file")
		}

		var items []map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return fmt.Errorf("invalid JSON array: %w", err)
		}
		if len(items) == 0 {
			return fmt.Errorf("no items to update")
		}

		// Validate all items have a name before starting any API calls.
		for i, item := range items {
			if _, ok := item["name"].(string); !ok || item["name"] == "" {
				return fmt.Errorf("item %d is missing a \"name\" field", i+1)
			}
		}

		c := client.New(cfg)
		type itemResult struct {
			name string
			err  error
		}
		results := make([]itemResult, len(items))

		for i, item := range items {
			name := item["name"].(string)
			results[i].name = name

			// Remove "name" from the payload — Frappe expects it only in the URL.
			payload := make(map[string]interface{}, len(item)-1)
			for k, v := range item {
				if k != "name" {
					payload[k] = v
				}
			}

			var apiErr error
			_ = spinner.New().
				Title(fmt.Sprintf("Updating %s %s (%d/%d)…", buDoctype, name, i+1, len(items))).
				Action(func() {
					_, apiErr = c.UpdateDoc(buDoctype, name, payload)
				}).
				Run()
			if apiErr != nil {
				results[i].err = apiErr
			}
		}

		succeeded, failed := 0, 0
		rows := make([]map[string]interface{}, len(results))
		for i, r := range results {
			row := map[string]interface{}{"#": i + 1, "name": r.name}
			if r.err != nil {
				row["status"] = "error"
				row["detail"] = r.err.Error()
				failed++
			} else {
				row["status"] = "updated"
				row["detail"] = ""
				succeeded++
			}
			rows[i] = row
		}

		if jsonOutput {
			output.PrintJSON(map[string]interface{}{
				"updated": succeeded,
				"failed":  failed,
				"results": rows,
			})
		} else {
			output.PrintTable(rows, []string{"#", "name", "status", "detail"})
			if failed == 0 {
				output.PrintSuccess(fmt.Sprintf("All %d %s documents updated.", succeeded, buDoctype))
			} else {
				output.PrintError(fmt.Sprintf("%d updated, %d failed.", succeeded, failed))
			}
		}

		if failed > 0 {
			return fmt.Errorf("%d of %d items failed", failed, len(items))
		}
		return nil
	},
}

func init() {
	bulkUpdateCmd.Flags().StringVarP(&buDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkUpdateCmd.Flags().StringVar(&buData, "data", "", `JSON array of objects; each must include "name" plus fields to update`)
	bulkUpdateCmd.Flags().StringVarP(&buFile, "file", "f", "", "Path to a JSON file containing an array of objects")

	_ = bulkUpdateCmd.MarkFlagRequired("doctype")

	rootCmd.AddCommand(bulkUpdateCmd)
}
