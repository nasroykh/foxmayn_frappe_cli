package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// permResult is the answer to a permission check, from `ffc can` and the
// check_permission MCP tool.
type permResult struct {
	DocType string `json:"doctype"`
	Name    string `json:"name,omitempty"`
	Perm    string `json:"perm"`
	Allowed bool   `json:"allowed"`
	// Basis is "document" when the site judged one document (user
	// permissions, sharing and controller rules included) and "doctype" when
	// ffc evaluated the DocType's role rows, because the site can only judge
	// a document.
	Basis string `json:"basis"`
	// OwnerOnly: the role rules allow it only on documents the user created.
	OwnerOnly   bool           `json:"owner_only,omitempty"`
	Permissions map[string]int `json:"permissions,omitempty"` // with --all, for a document
}

// parsePerm returns the lower-cased permission type; ok is false when it is
// not one Frappe checks.
func parsePerm(perm string) (string, bool) {
	p := strings.ToLower(strings.TrimSpace(perm))
	for _, t := range client.PermTypes {
		if t == p {
			return p, true
		}
	}
	return "", false
}

func permTypeHelp(perm string) string {
	return fmt.Sprintf("%q is not a permission type (one of %s)", perm, strings.Join(client.PermTypes, ", "))
}

// validPerm is parsePerm with a usage error for --perm.
func validPerm(perm string) (string, error) {
	p, ok := parsePerm(perm)
	if !ok {
		return "", usageErrorf("--perm: %s", permTypeHelp(perm))
	}
	return p, nil
}

// checkPermission asks the site whether the user may do perm to a document
// of doctype (name set) or to documents of doctype in general (name empty).
// all adds every permission the user holds on the document.
func checkPermission(ctx context.Context, c *client.FrappeClient, doctype, name, perm string, all bool) (*permResult, error) {
	res := &permResult{DocType: doctype, Name: name, Perm: perm}
	if name == "" {
		dp, err := c.DocTypePermission(ctx, doctype, perm)
		if err != nil {
			return nil, err
		}
		res.Allowed, res.OwnerOnly, res.Basis = dp.Allowed, dp.OwnerOnly, "doctype"
		return res, nil
	}
	allowed, err := c.HasPermission(ctx, doctype, name, perm)
	if err != nil {
		return nil, err
	}
	res.Allowed, res.Basis = allowed, "document"
	if all {
		if res.Permissions, err = c.DocPermissions(ctx, doctype, name); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (r *permResult) subject() string {
	if r.Name == "" {
		return r.DocType
	}
	return r.DocType + " " + r.Name
}

var (
	canDoctype string
	canName    string
	canPerm    string
	canAll     bool
)

var canCmd = &cobra.Command{
	Use:   "can",
	Short: "Check whether the user may do something, before doing it",
	Long: `Ask the site whether the configured user holds a permission on a DocType or
on one document, without changing anything.

With --name the site judges that document (frappe.client.has_permission): user
permissions, sharing and controller rules count, as when the action is
really attempted. --all also lists every permission the user holds on it
(frappe.client.get_doc_permissions, the role rules only).

Without --name the site has no way to judge a DocType alone, so ffc applies
the DocType's permission rows to the user's roles. That answers "may I create
a Sales Invoice at all", but not user permissions, sharing or controller
rules; "basis" in the result says which kind of answer it is.

The user Administrator is allowed everything, whether or not the document exists.

Exit code: 0 when allowed, 5 (permission) when denied, 4 when the DocType or
document does not exist. The result is printed either way.

Permission types: select, read, write, create, delete, submit, cancel, amend,
print, email, report, import, export, share.

Examples:
  ffc can -d "Sales Invoice" --perm create
  ffc can -d ToDo -n TD-0001 --perm write
  ffc can -d ToDo -n TD-0001 --all --json
  ffc can -d Customer --perm delete && ffc delete-doc -d Customer -n CUST-0001 --yes
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		perm, err := validPerm(canPerm)
		if err != nil {
			return err
		}
		if canAll && canName == "" {
			return usageErrorf("--all needs --name: the site lists permissions per document")
		}
		res, err := callSite(cmd, "Checking permission…", func(ctx context.Context, c *client.FrappeClient) (*permResult, error) {
			return checkPermission(ctx, c, canDoctype, canName, perm, canAll)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			if err := printResult(res); err != nil {
				return err
			}
		} else if res.Allowed {
			output.PrintSuccess(fmt.Sprintf("allowed: %s on %s", res.Perm, res.subject()))
			if canAll {
				printPermissions(res.Permissions)
			}
		}
		if !res.Allowed {
			msg := fmt.Sprintf("denied: %s on %s", res.Perm, res.subject())
			if res.OwnerOnly {
				msg += " (only for documents you own)"
			}
			return &deniedError{msg}
		}
		return nil
	},
}

func printPermissions(perms map[string]int) {
	row := map[string]interface{}{}
	var keys []string
	for _, p := range client.PermTypes {
		if v, ok := perms[p]; ok {
			row[p] = v == 1
			keys = append(keys, p)
		}
	}
	if len(keys) > 0 {
		output.PrintDocTable(row, keys)
	}
}

func init() {
	canCmd.Flags().StringVarP(&canDoctype, "doctype", "d", "", "Frappe DocType (required)")
	canCmd.Flags().StringVarP(&canName, "name", "n", "", "Name of one document; without it the DocType's role rules are evaluated")
	canCmd.Flags().StringVar(&canPerm, "perm", "read", "Permission type: select, read, write, create, delete, submit, cancel, amend, print, email, report, import, export, share")
	canCmd.Flags().BoolVar(&canAll, "all", false, "With --name: also list every permission the user holds on the document")
	_ = canCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(canCmd)
}
