package services

import (
	"github.com/nasroykh/foxmayn_frappe_cli/internal/mcpinstall"
)

// planRemove plans the removal of an entry. mcpinstall.PlanRemove (the
// CLI's "ffc mcp uninstall") is not on main yet; until it is, disconnecting
// is reported as unavailable.
func planRemove(string, string, mcpinstall.Env) (*mcpinstall.Change, error) {
	return nil, &Error{Code: CodeUnavailable, Message: "Disconnecting is not available in this version yet. Remove the \"frappe\" entry from the assistant's settings by hand."}
}
