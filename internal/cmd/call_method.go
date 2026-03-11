package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/huh/spinner"
	"github.com/spf13/cobra"
)

// call-method flags
var (
	cmMethod string
	cmArgs   string
)

var callMethodCmd = &cobra.Command{
	Use:   "call-method",
	Short: "Call a whitelisted Frappe server method",
	Long: `Execute a whitelisted Frappe method via POST /api/method/<method>.

The --args flag accepts a JSON object of method parameters.
The response "message" field is printed to stdout.

Examples:
  ffc call-method --method "frappe.ping"
  ffc call-method --method "frappe.client.get_count" --args '{"doctype":"ToDo","filters":{"status":"Open"}}'
  ffc call-method --method "erpnext.setup.doctype.company.company.get_default_currency" --json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		var methodArgs map[string]interface{}
		if cmArgs != "" {
			if err := json.Unmarshal([]byte(cmArgs), &methodArgs); err != nil {
				return fmt.Errorf("--args: invalid JSON object: %w", err)
			}
		}

		var result interface{}
		var apiErr error
		c := client.New(cfg)
		_ = spinner.New().
			Title(fmt.Sprintf("Calling %s…", cmMethod)).
			Action(func() {
				result, apiErr = c.CallMethod(cmMethod, methodArgs)
			}).
			Run()
		if apiErr != nil {
			return apiErr
		}

		output.PrintJSON(result)
		return nil
	},
}

func init() {
	callMethodCmd.Flags().StringVar(&cmMethod, "method", "", "Frappe method path, e.g. frappe.ping (required)")
	callMethodCmd.Flags().StringVar(&cmArgs, "args", "", `JSON object of method arguments, e.g. '{"doctype":"ToDo"}'`)
	_ = callMethodCmd.MarkFlagRequired("method")
	rootCmd.AddCommand(callMethodCmd)
}
