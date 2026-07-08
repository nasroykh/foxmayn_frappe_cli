package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// bulk-delete flags
var (
	bdDoctype string
	bdNames   string
	bdFile    string
	bdYes     bool
)

var bulkDeleteCmd = &cobra.Command{
	Use:   "bulk-delete",
	Short: "Delete multiple Frappe documents",
	Long: `Permanently delete multiple documents from a Frappe DocType.

Provide document names as a comma-separated list with --names, or load them
from a JSON file with --file (a JSON array of name strings).

You will be prompted to confirm unless --yes is provided. Processing continues
even when individual deletes fail. The command exits with a non-zero code if
any item failed.

Examples:
  ffc bulk-delete -d "ToDo" --names "TD-0001,TD-0002,TD-0003"
  ffc bulk-delete -d "Note" --file names.json --yes
  ffc bulk-delete -d "Customer" --names "CUST-001,CUST-002" --yes --json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		var names []string
		if bdFile != "" {
			b, err := os.ReadFile(bdFile)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			if err := json.Unmarshal(b, &names); err != nil {
				return fmt.Errorf("file must contain a JSON array of name strings: %w", err)
			}
		} else if bdNames != "" {
			for _, n := range strings.Split(bdNames, ",") {
				if trimmed := strings.TrimSpace(n); trimmed != "" {
					names = append(names, trimmed)
				}
			}
		}

		if len(names) == 0 {
			return fmt.Errorf("provide --names or --file")
		}

		if !bdYes {
			var confirmed bool
			// Show which documents will be deleted, not just a count (L8).
			preview := names
			if len(preview) > 10 {
				preview = append(append([]string{}, names[:10]...), fmt.Sprintf("… and %d more", len(names)-10))
			}
			prompt := fmt.Sprintf("Delete these %d %s document(s)?\n  %s\nThis cannot be undone.",
				len(names), bdDoctype, strings.Join(preview, ", "))
			err := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().Title(prompt).Value(&confirmed),
				),
			).WithKeyMap(escQuitKeyMap()).Run()
			// A user abort (Esc/Ctrl+C) is not a real error (L4).
			if err != nil && !errors.Is(err, huh.ErrUserAborted) {
				return err
			}
			if err != nil || !confirmed {
				fmt.Fprintln(os.Stderr, "Deletion cancelled.") // neutral, not red ✗ (L7)
				return nil
			}
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
		results := make([]itemResult, len(names))

		interrupted := false
		for i, name := range names {
			results[i].name = name
			// Honour Ctrl+C: stop deleting once interrupted (M18).
			if interrupted || cmd.Context().Err() != nil {
				results[i].skipped = true
				interrupted = true
				continue
			}
			var apiErr error
			runErr := runSpinner(fmt.Sprintf("Deleting %s %s (%d/%d)…", bdDoctype, name, i+1, len(names)), func() {
				apiErr = c.DeleteDoc(cmd.Context(), bdDoctype, name)
			})
			if runErr != nil {
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
				row["status"] = "deleted"
				row["detail"] = ""
				succeeded++
			}
			rows[i] = row
		}

		if jsonOutput {
			if err := output.PrintJSON(map[string]interface{}{
				"deleted": succeeded,
				"failed":  failed,
				"skipped": skipped,
				"results": rows,
			}); err != nil {
				return err
			}
		} else {
			output.PrintTable(rows, []string{"#", "name", "status", "detail"})
			if failed == 0 && skipped == 0 {
				output.PrintSuccess(fmt.Sprintf("All %d %s documents deleted.", succeeded, bdDoctype))
			} else {
				output.PrintError(fmt.Sprintf("%d deleted, %d failed, %d skipped.", succeeded, failed, skipped))
			}
		}

		if failed > 0 || skipped > 0 {
			return fmt.Errorf("%d of %d items did not succeed (%d failed, %d skipped)", failed+skipped, len(names), failed, skipped)
		}
		return nil
	},
}

func init() {
	bulkDeleteCmd.Flags().StringVarP(&bdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkDeleteCmd.Flags().StringVar(&bdNames, "names", "", "Comma-separated list of document names to delete")
	bulkDeleteCmd.Flags().StringVar(&bdFile, "file", "", "Path to a JSON file containing an array of name strings")
	bulkDeleteCmd.Flags().BoolVarP(&bdYes, "yes", "y", false, "Skip confirmation prompt")

	_ = bulkDeleteCmd.MarkFlagRequired("doctype")
	bulkDeleteCmd.MarkFlagsMutuallyExclusive("names", "file") // L9

	rootCmd.AddCommand(bulkDeleteCmd)
}
