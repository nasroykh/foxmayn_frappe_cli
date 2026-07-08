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

// bulk-create flags
var (
	bcDoctype string
	bcData    string
	bcFile    string
)

var bulkCreateCmd = &cobra.Command{
	Use:   "bulk-create",
	Short: "Create multiple Frappe documents from a JSON array",
	Long: `Create multiple documents in a Frappe DocType in one command.

Provide records inline with --data or load them from a JSON file with --file.
Each element of the array is a JSON object of field values.

Processing continues even when individual items fail. A per-item summary is
printed at the end. The command exits with a non-zero code if any item failed.

Examples:
  ffc bulk-create -d "ToDo" --data '[{"description":"Task 1"},{"description":"Task 2"}]'
  ffc bulk-create -d "Note" --file notes.json
  ffc bulk-create -d "Customer" --file customers.json --json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		raw := bcData
		if bcFile != "" {
			b, err := os.ReadFile(bcFile)
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
			return fmt.Errorf("no items to create")
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
			// Stop launching new work once the user interrupts (Ctrl+C) — do not
			// keep creating documents after an abort (M18).
			if interrupted || cmd.Context().Err() != nil {
				results[i].skipped = true
				interrupted = true
				continue
			}
			var doc map[string]interface{}
			var apiErr error
			runErr := runSpinner(fmt.Sprintf("Creating %s (%d/%d)…", bcDoctype, i+1, len(items)), func() {
				doc, apiErr = c.CreateDoc(cmd.Context(), bcDoctype, item)
			})
			if runErr != nil { // spinner interrupted
				results[i].skipped = true
				interrupted = true
				continue
			}
			if apiErr != nil {
				results[i].err = apiErr
			} else if n, ok := docName(doc["name"]); ok { // accept integer names (L27)
				results[i].name = n
			}
		}

		succeeded, failed, skipped := 0, 0, 0
		rows := make([]map[string]interface{}, len(results))
		for i, r := range results {
			row := map[string]interface{}{"#": i + 1}
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
				row["status"] = "created"
				row["detail"] = r.name
				succeeded++
			}
			rows[i] = row
		}

		if jsonOutput {
			if err := output.PrintJSON(map[string]interface{}{
				"created": succeeded,
				"failed":  failed,
				"skipped": skipped,
				"results": rows,
			}); err != nil {
				return err
			}
		} else {
			output.PrintTable(rows, []string{"#", "status", "detail"})
			if failed == 0 && skipped == 0 {
				output.PrintSuccess(fmt.Sprintf("All %d %s documents created.", succeeded, bcDoctype))
			} else {
				output.PrintError(fmt.Sprintf("%d created, %d failed, %d skipped.", succeeded, failed, skipped))
			}
		}

		if failed > 0 || skipped > 0 {
			return fmt.Errorf("%d of %d items did not succeed (%d failed, %d skipped)", failed+skipped, len(items), failed, skipped)
		}
		return nil
	},
}

func init() {
	bulkCreateCmd.Flags().StringVarP(&bcDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkCreateCmd.Flags().StringVar(&bcData, "data", "", "JSON array of field-value objects to create")
	bulkCreateCmd.Flags().StringVar(&bcFile, "file", "", "Path to a JSON file containing an array of objects")

	_ = bulkCreateCmd.MarkFlagRequired("doctype")
	// --data and --file are alternatives, not both (L9).
	bulkCreateCmd.MarkFlagsMutuallyExclusive("data", "file")

	rootCmd.AddCommand(bulkCreateCmd)
}
