package cmd

import (
	"fmt"
	"net/http"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"

	"github.com/spf13/cobra"
)

var erpCmd = &cobra.Command{
	Use:   "erp",
	Short: "ERPNext helpers: map documents between DocTypes",
	Long: `Helpers for sites that run ERPNext. They call the whitelisted methods ERPNext
itself uses (the desk's "Create" buttons), so its validations, permissions and
hooks apply.

They need ERPNext 15 or 16. Anything else goes through 'ffc api' or
'ffc call-method'.`,
}

// erpNextMajor returns the ERPNext major of a site, or the error that ends an
// 'ffc erp' command: exit 4 when ERPNext is not installed, exit 1 when its
// major is one ffc has not checked.
func erpNextMajor(info *client.ServerInfo, cfg *config.SiteConfig) (int, error) {
	version := info.Version(client.ERPNextApp)
	if version == "" {
		site := cfg.Name
		if site == "" {
			site = redactedURL(cfg.URL)
		}
		return 0, &client.APIError{Status: http.StatusNotFound, Message: fmt.Sprintf("ERPNext is not installed on %s", site)}
	}
	major := info.Major(client.ERPNextApp)
	if !client.SupportedERPNext(major) {
		return 0, fmt.Errorf("ffc erp supports ERPNext 15 and 16, this site runs %s: call its methods with 'ffc api' or 'ffc call-method'", version)
	}
	return major, nil
}

func init() {
	erpCmd.AddCommand(erpMapCmd)
	rootCmd.AddCommand(erpCmd)
}
