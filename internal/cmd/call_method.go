package cmd

import (
	"context"
	"fmt"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// call-method flags
var (
	cmMethod string
	cmArgs   string
	cmGet    bool
	cmRaw    bool
)

var callMethodCmd = &cobra.Command{
	Use:   "call-method",
	Short: "Call a whitelisted Frappe server method",
	Long: `Execute a whitelisted Frappe method via POST /api/method/<method>.

The --args flag accepts a JSON object of method parameters.
The response "message" field is printed to stdout; --raw prints the whole
response object, including what desk methods return next to "message"
(docs, docinfo, _server_messages). For binary responses (PDFs, files) use
'ffc api ... --output-file'.

Examples:
  ffc call-method --method "frappe.ping"
  ffc call-method --method "frappe.client.get_count" --args '{"doctype":"ToDo","filters":{"status":"Open"}}'
  ffc call-method --method "erpnext.setup.doctype.company.company.get_default_currency" --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var methodArgs map[string]interface{}
		if cmArgs != "" {
			var err error
			if methodArgs, err = parseObject("--args", cmArgs); err != nil {
				return err
			}
		}
		result, err := callSite(cmd, fmt.Sprintf("Calling %s…", cmMethod), func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			if cmRaw {
				return c.CallMethodFull(ctx, cmMethod, methodArgs, cmGet)
			}
			return c.CallMethod(ctx, cmMethod, methodArgs, cmGet)
		})
		if err != nil {
			return err
		}
		// The method's "message" can be any JSON value, so it is always printed
		// as JSON (with or without --json).
		return output.PrintJSON(result)
	},
}

func init() {
	callMethodCmd.Flags().StringVar(&cmMethod, "method", "", "Frappe method path, e.g. frappe.ping (required)")
	callMethodCmd.Flags().StringVar(&cmArgs, "args", "", `JSON object of method arguments, e.g. '{"doctype":"ToDo"}'`)
	callMethodCmd.Flags().BoolVar(&cmGet, "get", false, "Send as a GET request (for methods whitelisted GET-only)")
	callMethodCmd.Flags().BoolVar(&cmRaw, "raw", false, `Print the whole response object, not only "message"`)
	_ = callMethodCmd.MarkFlagRequired("method")
	rootCmd.AddCommand(callMethodCmd)
}
