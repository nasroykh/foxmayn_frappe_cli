package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// The collaboration commands (T2.5); the logic is in collab.go.

// collab flags. Each command has its own variables: the harness resets
// flags, not variables shared between commands.
var (
	cmDoctype, cmName string
	cmHTML            bool

	asDoctype, asName, asTo, asDescription, asDate, asPriority string
	uaDoctype, uaName, uaTo                                    string

	tgDoctype, tgName string
	utDoctype, utName string

	shDoctype, shName, shUser                      string
	shRead, shWrite, shSubmit, shShare, shEveryone bool
	shNotify, shYes                                bool
	usDoctype, usName, usUser                      string
	usEveryone                                     bool
)

// docFlags adds the required --doctype/-d and --name/-n flags.
func docFlags(cmd *cobra.Command, doctype, name *string) {
	cmd.Flags().StringVarP(doctype, "doctype", "d", "", "Frappe DocType (required)")
	cmd.Flags().StringVarP(name, "name", "n", "", "Name of the document (required)")
	_ = cmd.MarkFlagRequired("doctype")
	_ = cmd.MarkFlagRequired("name")
}

// joinOrNone lists values for a message.
func joinOrNone(v []string) string {
	if len(v) == 0 {
		return "none"
	}
	return text.Sanitize(strings.Join(v, ", "))
}

// strs reads a []string a collab result holds.
func strs(m map[string]interface{}, k string) []string {
	v, _ := m[k].([]string)
	return v
}

var commentCmd = &cobra.Command{
	Use:   "comment TEXT",
	Short: "Add a comment to a document",
	Long: `Add a comment to a document's timeline (frappe.desk.form.utils.add_comment),
as the user ffc signs in as. It needs read permission on the document.

TEXT is plain text: it is escaped, so it shows as typed, and line breaks are
kept. Several arguments are joined with spaces; "-" reads the text from stdin.
With --html the text is sent as HTML; Frappe removes scripts, forms and
unsafe attributes either way. @mentions in plain text do not notify anyone.

Examples:
  ffc comment -d ToDo -n TD-0001 "Called the customer, waiting for the PO"
  git log -1 --format=%B | ffc comment -d Task -n TASK-0042 -
  ffc comment -d Issue -n ISS-0007 --html "<b>Fixed</b> in v2.3"
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text, err := readCommentText(args)
		if err != nil {
			return err
		}
		res, err := callSite(cmd, fmt.Sprintf("Commenting on %s %s…", cmDoctype, cmName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return addComment(ctx, c, cmDoctype, cmName, text, cmHTML)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(res)
		}
		output.PrintSuccess(fmt.Sprintf("Commented on %s %s (Comment %v)", cmDoctype, cmName, res["comment"]))
		return nil
	},
}

var assignCmd = &cobra.Command{
	Use:   "assign",
	Short: "Assign a document to users",
	Long: `Assign a document to one or more users (frappe.desk.form.assign_to.add): each
gets an open ToDo for it. It needs read permission on the document.

A user who already has an open ToDo for the document is reported, not
assigned twice. Frappe notifies each new assignee, makes them follow the
document if their settings say so, and, when an assignee cannot read the
document, shares it with them read-only (or fails when document sharing is
disabled in System Settings).

Examples:
  ffc assign -d Task -n TASK-0042 --to jane@example.com
  ffc assign -d Issue -n ISS-0007 --to jane@example.com,bob@example.com --priority High --date 2026-11-01
  ffc assign -d Lead -n CRM-LEAD-0001 --to jane@example.com --description "Call back before Friday"
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		users, err := cleanList("user in --to", splitCSV(asTo), true)
		if err != nil {
			return err
		}
		opts, err := assignOptions(asDescription, asDate, asPriority)
		if err != nil {
			return err
		}
		res, err := callSite(cmd, fmt.Sprintf("Assigning %s %s…", asDoctype, asName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return assignUsers(ctx, c, asDoctype, asName, users, opts)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(res)
		}
		if a := strs(res, "assigned"); len(a) > 0 {
			output.PrintSuccess(fmt.Sprintf("Assigned %s %s to %s", asDoctype, asName, strings.Join(a, ", ")))
		}
		if a := strs(res, "already_assigned"); len(a) > 0 {
			output.PrintWarning("Already assigned: " + strings.Join(a, ", "))
		}
		fmt.Println("Assignees: " + joinOrNone(strs(res, "assignees")))
		return nil
	},
}

var unassignCmd = &cobra.Command{
	Use:   "unassign",
	Short: "Remove users' assignments of a document",
	Long: `Cancel users' open assignments of a document (frappe.desk.form.assign_to.remove):
their ToDos become Cancelled and Frappe notifies them. It needs read
permission on the document. A user who is not assigned is reported, not sent.

Examples:
  ffc unassign -d Task -n TASK-0042 --to jane@example.com
  ffc unassign -d Issue -n ISS-0007 --to jane@example.com,bob@example.com --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		users, err := cleanList("user in --to", splitCSV(uaTo), true)
		if err != nil {
			return err
		}
		res, err := callSite(cmd, fmt.Sprintf("Unassigning %s %s…", uaDoctype, uaName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return unassignUsers(ctx, c, uaDoctype, uaName, users)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(res)
		}
		if a := strs(res, "unassigned"); len(a) > 0 {
			output.PrintSuccess(fmt.Sprintf("Unassigned %s from %s %s", strings.Join(a, ", "), uaDoctype, uaName))
		}
		if a := strs(res, "not_assigned"); len(a) > 0 {
			output.PrintWarning("Not assigned: " + strings.Join(a, ", "))
		}
		fmt.Println("Assignees: " + joinOrNone(strs(res, "assignees")))
		return nil
	},
}

var tagCmd = &cobra.Command{
	Use:   "tag TAG...",
	Short: "Add tags to a document",
	Long: `Add tags to a document (frappe.desk.doctype.tag.tag.add_tag), creating each Tag
that does not exist yet. It needs write permission on the document. A tag
the document already has (same case) is reported, not added again. A tag
cannot contain a comma.

Examples:
  ffc tag -d Customer -n "Acme Ltd" vip export
  ffc tag -d Issue -n ISS-0007 "needs review" --json
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tags, err := cleanTags(args)
		if err != nil {
			return err
		}
		res, err := callSite(cmd, fmt.Sprintf("Tagging %s %s…", tgDoctype, tgName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return tagDoc(ctx, c, tgDoctype, tgName, tags)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(res)
		}
		if a := strs(res, "added"); len(a) > 0 {
			output.PrintSuccess(fmt.Sprintf("Tagged %s %s: %s", tgDoctype, tgName, strings.Join(a, ", ")))
		}
		if a := strs(res, "already_tagged"); len(a) > 0 {
			output.PrintWarning("Already tagged: " + strings.Join(a, ", "))
		}
		fmt.Println("Tags: " + joinOrNone(strs(res, "tags")))
		return nil
	},
}

var untagCmd = &cobra.Command{
	Use:   "untag TAG...",
	Short: "Remove tags from a document",
	Long: `Remove tags from a document (frappe.desk.doctype.tag.tag.remove_tag); the match
ignores case, as Frappe's does. It needs write permission on the document.
The Tag records themselves stay. A tag the document does not have is
reported, not sent.

Examples:
  ffc untag -d Customer -n "Acme Ltd" vip
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tags, err := cleanTags(args)
		if err != nil {
			return err
		}
		res, err := callSite(cmd, fmt.Sprintf("Untagging %s %s…", utDoctype, utName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return untagDoc(ctx, c, utDoctype, utName, tags)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(res)
		}
		if a := strs(res, "removed"); len(a) > 0 {
			output.PrintSuccess(fmt.Sprintf("Removed from %s %s: %s", utDoctype, utName, strings.Join(a, ", ")))
		}
		if a := strs(res, "not_tagged"); len(a) > 0 {
			output.PrintWarning("Not tagged: " + strings.Join(a, ", "))
		}
		fmt.Println("Tags: " + joinOrNone(strs(res, "tags")))
		return nil
	},
}

var shareCmd = &cobra.Command{
	Use:   "share",
	Short: "Share a document with a user or everyone",
	Long: `Give a user, or every user, access to one document (frappe.share.add), on top
of what their roles allow. Read is always granted; --write, --submit (only
on a submittable DocType) and --share (may share it further) add rights. You
need share permission on the document and every right you grant.

Sharing again with the same user replaces that share's rights with the ones
given: "ffc share --user U" after "--write" takes write away. Use ffc unshare
to remove a share.

It widens access, so you are asked to confirm unless --yes is given.

Examples:
  ffc share -d Project -n PROJ-0001 --user jane@example.com --write --yes
  ffc share -d Note -n "Release plan" --everyone --yes
  ffc share -d Task -n TASK-0042 --user bob@example.com --notify
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		user := strings.TrimSpace(shUser)
		if err := checkShareTarget(user, shEveryone); err != nil {
			return err
		}
		if !shRead {
			return usageErrorf("--read=false: a share always grants read; use ffc unshare to remove one")
		}
		opts := client.ShareOptions{User: user, Everyone: shEveryone, Write: shWrite, Submit: shSubmit, Share: shShare, Notify: shNotify}
		if !shYes && !dryRunOn(cmd) {
			if err := confirm(fmt.Sprintf("Share %s %q with %s (%s)?", shDoctype, shName, shareTarget(user, shEveryone), shareRights(opts))); err != nil {
				return err
			}
		}
		res, err := callSite(cmd, fmt.Sprintf("Sharing %s %s…", shDoctype, shName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return shareDoc(ctx, c, shDoctype, shName, opts)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(res)
		}
		output.PrintSuccess(fmt.Sprintf("Shared %s %s with %s (%s)", shDoctype, shName, shareTarget(user, shEveryone), shareRights(opts)))
		return nil
	},
}

var unshareCmd = &cobra.Command{
	Use:   "unshare",
	Short: "Remove a user's or everyone's share of a document",
	Long: `Remove the share of a document with a user, or with everyone (what the desk's
share dialog does: frappe.share.set_permission clears the read right, which
removes the share). Access the user's roles give is not affected. You need
share permission on the document. A share that does not exist is reported,
not sent.

Examples:
  ffc unshare -d Project -n PROJ-0001 --user jane@example.com
  ffc unshare -d Note -n "Release plan" --everyone
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		user := strings.TrimSpace(usUser)
		if err := checkShareTarget(user, usEveryone); err != nil {
			return err
		}
		res, err := callSite(cmd, fmt.Sprintf("Unsharing %s %s…", usDoctype, usName), func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			return unshareDoc(ctx, c, usDoctype, usName, user, usEveryone)
		})
		if err != nil {
			return err
		}
		if machineOutput() {
			return printResult(res)
		}
		if res["removed"] == true {
			output.PrintSuccess(fmt.Sprintf("Unshared %s %s from %s", usDoctype, usName, shareTarget(user, usEveryone)))
		} else {
			output.PrintWarning(fmt.Sprintf("%s %s is not shared with %s", usDoctype, usName, shareTarget(user, usEveryone)))
		}
		return nil
	},
}

func init() {
	docFlags(commentCmd, &cmDoctype, &cmName)
	commentCmd.Flags().BoolVar(&cmHTML, "html", false, "Send TEXT as HTML instead of escaping it")

	docFlags(assignCmd, &asDoctype, &asName)
	assignCmd.Flags().StringVar(&asTo, "to", "", "Users to assign, comma-separated (User IDs, usually emails; required)")
	assignCmd.Flags().StringVar(&asDescription, "description", "", "ToDo description (default: Frappe's \"Assignment for <DocType> <name>\")")
	assignCmd.Flags().StringVar(&asDate, "date", "", "Due date, YYYY-MM-DD")
	assignCmd.Flags().StringVar(&asPriority, "priority", "", "Low, Medium or High (Frappe's default: Medium)")
	_ = assignCmd.MarkFlagRequired("to")

	docFlags(unassignCmd, &uaDoctype, &uaName)
	unassignCmd.Flags().StringVar(&uaTo, "to", "", "Users to unassign, comma-separated (required)")
	_ = unassignCmd.MarkFlagRequired("to")

	docFlags(tagCmd, &tgDoctype, &tgName)
	docFlags(untagCmd, &utDoctype, &utName)

	docFlags(shareCmd, &shDoctype, &shName)
	shareCmd.Flags().StringVar(&shUser, "user", "", "User to share with (User ID, usually an email)")
	shareCmd.Flags().BoolVar(&shEveryone, "everyone", false, "Share with every user instead of one")
	shareCmd.Flags().BoolVar(&shRead, "read", true, "Grant read (always granted; accepted for clarity)")
	shareCmd.Flags().BoolVar(&shWrite, "write", false, "Also grant write")
	shareCmd.Flags().BoolVar(&shSubmit, "submit", false, "Also grant submit (submittable DocTypes only)")
	shareCmd.Flags().BoolVar(&shShare, "share", false, "Also let the user share the document further")
	shareCmd.Flags().BoolVar(&shNotify, "notify", false, "Send the user a notification")
	shareCmd.Flags().BoolVarP(&shYes, "yes", "y", false, "Skip the confirmation prompt")

	docFlags(unshareCmd, &usDoctype, &usName)
	unshareCmd.Flags().StringVar(&usUser, "user", "", "User whose share to remove")
	unshareCmd.Flags().BoolVar(&usEveryone, "everyone", false, "Remove the share with everyone")

	for _, c := range []*cobra.Command{commentCmd, assignCmd, unassignCmd, tagCmd, untagCmd, shareCmd, unshareCmd} {
		addDryRun(c, false)
		rootCmd.AddCommand(c)
	}
}
