package cmd

import (
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/spf13/cobra"
)

// delete-doc flags
var (
	ddDoctype string
	ddName    string
	ddYes     bool
)

var deleteDocCmd = &cobra.Command{
	Use:   "delete-doc",
	Short: "Delete a Frappe document",
	Long: `Permanently delete a document from a Frappe DocType.

You will be prompted to confirm deletion unless --yes is provided.

Examples:
  ffc delete-doc --doctype "ToDo" --name "TD-0001"
  ffc delete-doc -d "Note" -n "Old Note" --yes
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		// Confirmation prompt unless --yes is passed.
		if !ddYes {
			var confirmed bool
			prompt := fmt.Sprintf("Delete %s %q? This cannot be undone.", ddDoctype, ddName)
			if err := huh.NewConfirm().
				Title(prompt).
				Value(&confirmed).
				Run(); err != nil {
				return err
			}
			if !confirmed {
				output.PrintError("Deletion cancelled.")
				return nil
			}
		}

		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Deleting %s %s…", ddDoctype, ddName)).
			Action(func() {
				apiErr = c.DeleteDoc(ddDoctype, ddName)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		output.PrintSuccess(fmt.Sprintf("Deleted %s %s", ddDoctype, ddName))
		return nil
	},
}

func init() {
	deleteDocCmd.Flags().StringVarP(&ddDoctype, "doctype", "d", "", "Frappe DocType (required)")
	deleteDocCmd.Flags().StringVarP(&ddName, "name", "n", "", "Name of the document (required)")
	deleteDocCmd.Flags().BoolVarP(&ddYes, "yes", "y", false, "Skip confirmation prompt")

	_ = deleteDocCmd.MarkFlagRequired("doctype")
	_ = deleteDocCmd.MarkFlagRequired("name")

	rootCmd.AddCommand(deleteDocCmd)
}
