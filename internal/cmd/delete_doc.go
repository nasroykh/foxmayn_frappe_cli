package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh"
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
			err := huh.NewForm(huh.NewGroup(
				huh.NewConfirm().Title(prompt).Value(&confirmed),
			)).WithKeyMap(escQuitKeyMap()).Run() // Escape/Ctrl+C aborts (L8)
			// Distinguish a genuine prompt failure from a user cancel (L4).
			if err != nil && !errors.Is(err, huh.ErrUserAborted) {
				return err
			}
			if err != nil || !confirmed {
				// Neutral cancel message, not a red ✗ error (L7).
				fmt.Fprintln(os.Stderr, "Deletion cancelled.")
				return nil
			}
		}

		var apiErr error
		c, err := client.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		_ = runSpinner(fmt.Sprintf("Deleting %s %s…", ddDoctype, ddName), func() {
			apiErr = c.DeleteDoc(cmd.Context(), ddDoctype, ddName)
		})
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
