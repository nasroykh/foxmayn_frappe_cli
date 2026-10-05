package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
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
	OwnerOnly bool `json:"owner_only,omitempty"`
	// Note says what a DocType-level answer assumed or could not know (a
	// role that was not counted).
	Note        string         `json:"note,omitempty"`
	Permissions map[string]int `json:"permissions,omitempty"` // with --all, for a document
}

// permIdent matches the name of a permission type a site can define (v16
// Permission Type): a lower-case identifier.
var permIdent = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// parsePerm returns the lower-cased permission type; ok is false when it is
// not one Frappe checks. With custom (a check of one document, which the site
// judges itself) any lower-case identifier is accepted, since a site can add
// permission types; without it only the types Frappe ships are, because the
// role rows ffc evaluates are read by those names.
func parsePerm(perm string, custom bool) (string, bool) {
	p := strings.ToLower(strings.TrimSpace(perm))
	for _, t := range client.PermTypes {
		if t == p {
			return p, true
		}
	}
	if custom && permIdent.MatchString(p) {
		return p, true
	}
	return "", false
}

func permTypeHelp(perm string, custom bool) string {
	if custom {
		return fmt.Sprintf("%q is not a permission type (one of %s, or a custom type: a lower-case identifier)", perm, strings.Join(client.PermTypes, ", "))
	}
	return fmt.Sprintf("%q is not a permission type for a DocType check (one of %s; custom types need --name)", perm, strings.Join(client.PermTypes, ", "))
}

// validPerm is parsePerm with a usage error for --perm.
func validPerm(perm string, custom bool) (string, error) {
	p, ok := parsePerm(perm, custom)
	if !ok {
		return "", usageErrorf("--perm: %s", permTypeHelp(perm, custom))
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
		var child *client.ChildTableError
		if errors.As(err, &child) {
			return nil, usageErrorf("%v", err)
		}
		if err != nil {
			return nil, err
		}
		res.Allowed, res.OwnerOnly, res.Note, res.Basis = dp.Allowed, dp.OwnerOnly, dp.Note, "doctype"
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
(frappe.client.get_doc_permissions, the role rules only; every key the site
returns is printed, custom permission types included). With --name, --perm
may be any lower-case permission type the site defines.

Without --name the site has no way to judge a DocType alone, so ffc applies
the DocType's permission rows to the user's roles, as Frappe's role
permission system does: if_owner rows narrow a right to the user's own
documents only when no other row grants it (never create), select is implied
by read, submit/cancel/amend need a submittable DocType and import an
importable one. That answers "may I create a Sales Invoice at all". It does
not evaluate sharing, user permissions or controller rules, and it does not
check the System Settings option disable_document_sharing. "basis" in the
result says which kind of answer it is. A child table is refused: check its
parent DocType.

An owner-only right shows as "owner_only": for read and select the answer is
allowed (the user lists documents, narrowed to their own), for the other
rights it is denied because it holds only per document.

The automatic role Desk User is counted only when the User document shows a
System User. Users who cannot read user_type (permission level 1, so everyone
but a user manager) are evaluated without it, and "note" says so when a Desk
User row would have changed the answer.

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
		perm, err := validPerm(canPerm, canName != "")
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
		} else {
			if res.Allowed {
				output.PrintSuccess(fmt.Sprintf("allowed: %s on %s", res.Perm, res.subject()))
			}
			if res.Note != "" {
				output.PrintWarning("note: " + res.Note)
			}
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

// printPermissions prints every right the site returned: the types Frappe
// ships first, then the others (custom types) sorted.
func printPermissions(perms map[string]int) {
	row := map[string]interface{}{}
	var keys []string
	for _, p := range client.PermTypes {
		if v, ok := perms[p]; ok {
			row[p] = v == 1
			keys = append(keys, p)
		}
	}
	var extra []string
	for p := range perms {
		if _, ok := row[p]; !ok {
			extra = append(extra, p)
		}
	}
	sort.Strings(extra)
	for _, p := range extra {
		row[p] = perms[p] == 1
	}
	keys = append(keys, extra...)
	if len(keys) > 0 {
		output.PrintDocTable(row, keys)
	}
}

func init() {
	canCmd.Flags().StringVarP(&canDoctype, "doctype", "d", "", "Frappe DocType (required)")
	canCmd.Flags().StringVarP(&canName, "name", "n", "", "Name of one document; without it the DocType's role rules are evaluated")
	canCmd.Flags().StringVar(&canPerm, "perm", "read", "Permission type: select, read, write, create, delete, submit, cancel, amend, print, email, report, import, export, share (with --name, any type the site defines)")
	canCmd.Flags().BoolVar(&canAll, "all", false, "With --name: also list every permission the user holds on the document")
	_ = canCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(canCmd)
}
