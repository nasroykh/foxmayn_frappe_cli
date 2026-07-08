package cmd

import (
	"fmt"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Check connectivity to the Frappe site",
	Long: `Send a ping to the Frappe site and display the response time.

Useful for verifying that your config is correct and the site is reachable.

Examples:
  ffc ping
  ffc ping --site dev
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(siteName, configPath)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}

		c, err := client.New(cmd.Context(), cfg)
		if err != nil {
			return err
		}

		start := time.Now()
		resp, apiErr := c.Ping(cmd.Context())
		elapsed := time.Since(start)

		if apiErr != nil {
			return apiErr
		}

		if jsonOutput {
			return output.PrintJSON(map[string]interface{}{
				"response": resp,
				"url":      cfg.URL,
				"latency":  elapsed.String(),
			})
		}
		output.PrintSuccess(fmt.Sprintf("pong — %s (%s)", cfg.URL, elapsed.Round(time.Millisecond)))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pingCmd)
}
