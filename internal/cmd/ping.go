package cmd

import (
	"fmt"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Check connectivity to the Frappe site",
	Long: `Send a ping to the Frappe site and display the response time.

frappe.ping answers without credentials, so a pong alone proves nothing about
your login. ping therefore also asks who the credentials belong to
(frappe.auth.get_logged_user) and reports the user; credentials the site
rejects fail the command (exit 3).

Useful for verifying that your config is correct and the site is reachable.
For a full check of the setup see ffc doctor.

Examples:
  ffc ping
  ffc ping --site dev
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadSite(cmd.Context())
		if err != nil {
			return err
		}
		start := time.Now()
		c, err := client.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}
		defer c.CloseQuietly()
		resp, err := c.Ping(cmd.Context())
		if err != nil {
			return err
		}
		// For session-auth sites the latency includes the login round-trip.
		elapsed := time.Since(start)

		user, err := c.LoggedUser(cmd.Context())
		if err != nil {
			return err
		}
		if user == "Guest" {
			return &client.AuthError{Message: "the site answered but sees these credentials as Guest: check your credentials or run 'ffc init' to reconfigure"}
		}

		if machineOutput() {
			return printResult(map[string]interface{}{
				"response": resp,
				"url":      cfg.URL,
				"latency":  elapsed.String(),
				"user":     user,
			})
		}
		output.PrintSuccess(fmt.Sprintf("pong — %s as %s (%s)", cfg.URL, user, elapsed.Round(time.Millisecond)))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pingCmd)
}
