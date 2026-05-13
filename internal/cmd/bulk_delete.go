package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
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
			prompt := fmt.Sprintf("Delete %d %s document(s)? This cannot be undone.", len(names), bdDoctype)
			if err := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().Title(prompt).Value(&confirmed),
				),
			).WithKeyMap(escQuitKeyMap()).Run(); err != nil {
				return err
			}
			if !confirmed {
				output.PrintError("Deletion cancelled.")
				return nil
			}
		}

		c := client.New(cfg)
		type itemResult struct {
			name string
			err  error
		}
		results := make([]itemResult, len(names))

		for i, name := range names {
			results[i].name = name
			var apiErr error
			_ = spinner.New().
				Title(fmt.Sprintf("Deleting %s %s (%d/%d)…", bdDoctype, name, i+1, len(names))).
				Action(func() {
					apiErr = c.DeleteDoc(bdDoctype, name)
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
				row["status"] = "deleted"
				row["detail"] = ""
				succeeded++
			}
			rows[i] = row
		}

		if jsonOutput {
			output.PrintJSON(map[string]interface{}{
				"deleted": succeeded,
				"failed":  failed,
				"results": rows,
			})
		} else {
			output.PrintTable(rows, []string{"#", "name", "status", "detail"})
			if failed == 0 {
				output.PrintSuccess(fmt.Sprintf("All %d %s documents deleted.", succeeded, bdDoctype))
			} else {
				output.PrintError(fmt.Sprintf("%d deleted, %d failed.", succeeded, failed))
			}
		}

		if failed > 0 {
			return fmt.Errorf("%d of %d items failed", failed, len(names))
		}
		return nil
	},
}

func init() {
	bulkDeleteCmd.Flags().StringVarP(&bdDoctype, "doctype", "d", "", "Frappe DocType (required)")
	bulkDeleteCmd.Flags().StringVar(&bdNames, "names", "", "Comma-separated list of document names to delete")
	bulkDeleteCmd.Flags().StringVarP(&bdFile, "file", "f", "", "Path to a JSON file containing an array of name strings")
	bulkDeleteCmd.Flags().BoolVarP(&bdYes, "yes", "y", false, "Skip confirmation prompt")

	_ = bulkDeleteCmd.MarkFlagRequired("doctype")

	rootCmd.AddCommand(bulkDeleteCmd)
}
