package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// serverView is the site's apps in a whoami result.
type serverView struct {
	Frappe    string                       `json:"frappe"`
	Major     int                          `json:"major"`
	Apps      map[string]client.AppVersion `json:"apps"`
	Cached    bool                         `json:"cached"`
	FetchedAt time.Time                    `json:"fetched_at"`
}

// whoamiResult is what `ffc whoami` and the whoami MCP tool return.
type whoamiResult struct {
	User     string   `json:"user"`
	FullName string   `json:"full_name,omitempty"`
	Roles    []string `json:"roles"`
	// RolesSource is "has_role" when Roles was read from the user's Has Role
	// rows, "user_doc" when from the User document, "unavailable" when the
	// site refused both (Roles is then empty, and Notes says why).
	RolesSource string      `json:"roles_source"`
	Site        string      `json:"site,omitempty"`
	URL         string      `json:"url"`
	Auth        string      `json:"auth"`
	Server      *serverView `json:"server,omitempty"`
	Notes       []string    `json:"notes,omitempty"`
}

// authMethod names how cfg authenticates, for display. It carries no secret.
func authMethod(cfg *config.SiteConfig) string {
	switch {
	case cfg.AccessToken != "":
		return "oauth"
	case cfg.APIKey != "" && cfg.APISecret != "":
		return "api_key"
	case cfg.IsSessionAuth():
		return "password"
	}
	return "none"
}

// buildWhoami asks the site who the credentials belong to, what roles they
// have and which apps run there. The user is required (its failure is the
// error); everything else degrades into a note.
//
// A user who is not a System Manager can read their own User document but
// not its "roles" table (permission level 1), so the roles come from the
// Has Role rows (frappe.client.get_list with the parent DocType), which that
// user may list; the User document's table is the fallback.
func buildWhoami(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, refresh bool) (*whoamiResult, error) {
	user, err := c.LoggedUser(ctx)
	if err != nil {
		if serverChanged(err) {
			invalidateServerCache(cfg)
		}
		return nil, err
	}
	res := &whoamiResult{User: user, Roles: []string{}, RolesSource: "unavailable", Site: cfg.Name, URL: redactedURL(cfg.URL), Auth: authMethod(cfg)}
	if user == "Guest" {
		res.Notes = append(res.Notes, "the site sees these credentials as Guest: they are not authenticated")
		return res, nil
	}

	doc, docErr := c.GetDoc(ctx, "User", user)
	if docErr == nil {
		res.FullName, _ = doc["full_name"].(string)
	}
	roles, rolesErr := c.UserRoles(ctx, user)
	switch {
	case rolesErr == nil:
		res.Roles, res.RolesSource = roles, "has_role"
	case docErr == nil:
		var rows []interface{}
		rows, _ = doc["roles"].([]interface{})
		for _, r := range rows {
			if m, ok := r.(map[string]interface{}); ok {
				if role, _ := m["role"].(string); role != "" {
					res.Roles = append(res.Roles, role)
				}
			}
		}
		if len(res.Roles) > 0 {
			res.RolesSource = "user_doc"
		} else {
			res.Notes = append(res.Notes, fmt.Sprintf("roles unavailable: listing them failed (%v) and the User document has no readable roles table", rolesErr))
		}
	default:
		res.Notes = append(res.Notes, fmt.Sprintf("roles unavailable: %v", rolesErr))
	}
	if docErr != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("the User document of %s is not readable: %v", user, docErr))
	}

	info, cached, err := serverInfo(ctx, c, cfg, refresh)
	if err != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("server versions unavailable: %v", err))
		return res, nil
	}
	res.Server = &serverView{Frappe: info.FrappeVersion(), Major: info.FrappeMajor(), Apps: info.Apps, Cached: cached, FetchedAt: info.FetchedAt}
	return res, nil
}

// table is the human view: one Field/Value row each.
func (r *whoamiResult) table() map[string]interface{} {
	out := map[string]interface{}{"user": r.User, "auth": r.Auth, "url": r.URL}
	if r.FullName != "" {
		out["full_name"] = r.FullName
	}
	if r.Site != "" {
		out["site"] = r.Site
	}
	if len(r.Roles) > 0 {
		out["roles"] = strings.Join(r.Roles, ", ")
	} else {
		out["roles"] = "(" + r.RolesSource + ")"
	}
	if r.Server != nil {
		out["frappe"] = r.Server.Frappe
		var apps []string
		for _, n := range sortedKeys(r.Server.Apps) {
			apps = append(apps, n+" "+r.Server.Apps[n].Version)
		}
		out["apps"] = strings.Join(apps, ", ")
	}
	if len(r.Notes) > 0 {
		out["notes"] = strings.Join(r.Notes, "; ")
	}
	return out
}

func sortedKeys(m map[string]client.AppVersion) []string {
	return (&client.ServerInfo{Apps: m}).AppNames()
}

var whoamiRefresh bool

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the logged-in user, their roles and the site's versions",
	Long: `Show who the configured credentials belong to on the site: the user, their
roles, and the installed apps with their versions (Frappe, ERPNext, ...).

The roles are read from the user's Has Role rows: a user who is not a System
Manager can read their own User document but not its roles table. If the
site refuses that too, the roles are reported as unavailable, with a note.
The automatic roles (All, Guest, Desk User) are not Has Role rows and are not
listed.

When the site sees the credentials as Guest (not authenticated) the result is
printed and the command exits 3, like ping.

The versions are cached for 24 hours per site under the user cache directory
(ffc/<site>/server.json); --refresh reads them again.

Examples:
  ffc whoami
  ffc whoami --site prod --json
  ffc whoami --refresh --jq .server.frappe
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := callSiteCfg(cmd, "Asking the site…", func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (*whoamiResult, error) {
			return buildWhoami(ctx, c, cfg, whoamiRefresh)
		})
		if err != nil {
			return err
		}
		if err := render(res, nil, func() error {
			view := res.table()
			var keys []string
			for _, k := range []string{"user", "full_name", "roles", "auth", "site", "url", "frappe", "apps", "notes"} {
				if _, ok := view[k]; ok {
					keys = append(keys, k)
				}
			}
			output.PrintDocTable(view, keys)
			return nil
		}); err != nil {
			return err
		}
		// Like ping: the site answered, but these credentials are not
		// accepted. The result above says so; the exit code does too.
		if res.User == "Guest" {
			return &client.AuthError{Message: "the site sees these credentials as Guest: check your credentials or run 'ffc init' to reconfigure"}
		}
		return nil
	},
}

func init() {
	whoamiCmd.Flags().BoolVar(&whoamiRefresh, "refresh", false, "Read the site's app versions again instead of using the cache")
	rootCmd.AddCommand(whoamiCmd)
}
