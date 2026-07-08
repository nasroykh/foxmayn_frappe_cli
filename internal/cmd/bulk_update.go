package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

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
		// Accept numeric names (integer-named DocTypes), not only strings (L27).
		names := make([]string, len(items))
		for i, item := range items {
			n, ok := docName(item["name"])
			if !ok {
				return fmt.Errorf("item %d is missing a \"name\" field", i+1)
			}
			names[i] = n
		}

		c, err := client.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		type itemResult struct {
			name    string
			err     error
			skipped bool
		}
		results := make([]itemResult, len(items))

		interrupted := false
		for i, item := range items {
			name := names[i]
			results[i].name = name

			if interrupted || cmd.Context().Err() != nil {
				results[i].skipped = true
				interrupted = true
				continue
			}

			// Remove "name" from the payload — Frappe expects it only in the URL.
			payload := make(map[string]interface{}, len(item))
			for k, v := range item {
				if k != "name" {
					payload[k] = v
				}
			}

			var apiErr error
			runErr := runSpinner(fmt.Sprintf("Updating %s %s (%d/%d)…", buDoctype, name, i+1, len(items)), func() {
				_, apiErr = c.UpdateDoc(cmd.Context(), buDoctype, name, payload)
			})
			if runErr != nil { // interrupted (M18)
				results[i].skipped = true
				interrupted = true
				continue
			}
			if apiErr != nil {
				results[i].err = apiErr
			}
		}

		succeeded, failed, skipped := 0, 0, 0
		rows := make([]map[string]interface{}, len(results))
		for i, r := range results {
			row := map[string]interface{}{"#": i + 1, "name": r.name}
			switch {
			case r.skipped:
				row["status"] = "skipped"
				row["detail"] = "interrupted"
				skipped++
			case r.err != nil:
				row["status"] = "error"
				row["detail"] = r.err.Error()
				failed++
			default:
				row["status"] = "updated"
				row["detail"] = ""
				succeeded++
			}
			rows[i] = row
		}

		if jsonOutput {
			if err := output.PrintJSON(map[string]interface{}{
				"updated": succeeded,
				"failed":  failed,
				"skipped": skipped,
				"results": rows,
			}); err != nil {
				return err
			}
		} else {
			output.PrintTable(rows, []string{"#", "name", "status", "detail"})
			if failed == 0 && skipped == 0 {
				output.PrintSuccess(fmt.Sprintf("All %d %s documents updated.", succeeded, buDoctype))
			} else {
				output.PrintError(fmt.Sprintf("%d updated, %d failed, %d skipped.", succeeded, failed, skipped))
			}
		}

		if failed > 0 || skipped > 0 {
			return fmt.Errorf("%d of %d items did not succeed (%d failed, %d skipped)", failed+skipped, len(items), failed, skipped)
		}
		return nil
	},
}

func init() {
	bulkUpdateCmd.Flags().StringVarP(&buDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkUpdateCmd.Flags().StringVar(&buData, "data", "", `JSON array of objects; each must include "name" plus fields to update`)
	bulkUpdateCmd.Flags().StringVar(&buFile, "file", "", "Path to a JSON file containing an array of objects")

	_ = bulkUpdateCmd.MarkFlagRequired("doctype")
	bulkUpdateCmd.MarkFlagsMutuallyExclusive("data", "file") // L9

	rootCmd.AddCommand(bulkUpdateCmd)
}
