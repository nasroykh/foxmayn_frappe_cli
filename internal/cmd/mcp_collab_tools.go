package cmd

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// Collaboration tools (T2.5): comments, assignments and tags in the collab
// tool set, sharing in the admin tool set. Neither set is exposed unless
// --toolsets names it. The logic is shared with the CLI (collab.go).

// collabDoctypes are the DocTypes each tool writes besides the document it
// names, so the DocType rules apply to them too: DocShare is sensitive, so
// share_doc and unshare_doc are refused unless the site's
// mcp.allow_doctypes lists it.
var collabDoctypes = map[string][]string{
	"add_comment":       {"Comment"},
	"assign_to":         {"ToDo"},
	"remove_assignment": {"ToDo"},
	"add_tag":           {"Tag Link", "Tag"},
	"remove_tag":        {"Tag Link"},
	"share_doc":         {"DocShare"},
	"unshare_doc":       {"DocShare"},
}

// listParam declares a list of strings, given as an array or a
// comma-separated string.
func listParam(name, desc string) mcp.ToolOption {
	return jsonParam(name, desc, mcp.Required())
}

// listArg reads a listParam.
func listArg(req mcp.CallToolRequest, key, what string, fold bool) ([]string, error) {
	v, err := keysArg(req, key)
	if err != nil {
		return nil, err
	}
	return cleanList(what, v, fold)
}

func registerCollabTools(s *server.MCPServer, env *mcpEnv) {
	idem := mcp.WithIdempotentHintAnnotation(true)

	s.AddTool(docTool("add_comment",
		"Add a comment to a document's timeline as the signed-in user. text is plain text (escaped, line breaks kept) unless html is true; Frappe sanitises HTML either way. Needs read permission on the document. Returns the Comment's name and stored content.",
		false, false,
		mcp.WithString("text", mcp.Required(), mcp.Description("The comment")),
		mcp.WithBoolean("html", mcp.Description("text is HTML (default false: plain text)")),
	),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			text, err := req.RequireString("text")
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(text) == "" {
				return nil, usageErrorf("text is empty")
			}
			asHTML := req.GetBool("html", false)
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return addComment(ctx, c, doctype, name, text, asHTML)
			}, nil
		}))

	s.AddTool(docTool("assign_to",
		"Assign a document to users: each gets an open ToDo and a notification. Users already assigned are reported in already_assigned, not assigned twice. An assignee who cannot read the document gets it shared read-only. Needs read permission on the document. Returns assigned, already_assigned and assignees (everyone assigned now).",
		false, false, idem,
		listParam("users", `User IDs (usually emails) as an array, e.g. ["jane@example.com"], or comma-separated`),
		mcp.WithString("description", mcp.Description(`ToDo description (default "Assignment for <DocType> <name>")`)),
		mcp.WithString("date", mcp.Description("Due date, YYYY-MM-DD")),
		mcp.WithString("priority", mcp.Description("Low, Medium (default) or High")),
	),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			users, err := listArg(req, "users", "user", true)
			if err != nil {
				return nil, err
			}
			var a [3]string
			for i, k := range []string{"description", "date", "priority"} {
				if a[i], err = stringArg(req, k); err != nil {
					return nil, err
				}
			}
			opts, err := assignOptions(a[0], a[1], a[2])
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return assignUsers(ctx, c, doctype, name, users, opts)
			}, nil
		}))

	s.AddTool(docTool("remove_assignment",
		"Remove users' assignments of a document: their open ToDos are cancelled and they are notified. Users not assigned are reported in not_assigned. Returns unassigned, not_assigned and assignees.",
		false, true, idem,
		listParam("users", `User IDs as an array or comma-separated`),
	),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			users, err := listArg(req, "users", "user", true)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return unassignUsers(ctx, c, doctype, name, users)
			}, nil
		}))

	s.AddTool(docTool("add_tag",
		"Add tags to a document, creating each Tag that does not exist. Needs write permission on the document. A tag cannot contain a comma. Returns added, already_tagged and tags (all the document's tags).",
		false, false, idem,
		listParam("tags", `Tags as an array, e.g. ["vip","export"], or comma-separated`),
	),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			raw, err := keysArg(req, "tags")
			if err != nil {
				return nil, err
			}
			tags, err := cleanTags(raw)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return tagDoc(ctx, c, doctype, name, tags)
			}, nil
		}))

	s.AddTool(docTool("remove_tag",
		"Remove tags from a document (the match ignores case). Needs write permission on the document. Returns removed, not_tagged and tags.",
		false, true, idem,
		listParam("tags", `Tags as an array or comma-separated`),
	),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			raw, err := keysArg(req, "tags")
			if err != nil {
				return nil, err
			}
			tags, err := cleanTags(raw)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return untagDoc(ctx, c, doctype, name, tags)
			}, nil
		}))

	shareTarget := []mcp.ToolOption{
		mcp.WithString("user", mcp.Description("User ID (usually an email); omit with everyone")),
		mcp.WithBoolean("everyone", mcp.Description("Every user instead of one (default false)")),
	}
	s.AddTool(docTool("share_doc",
		"Give a user, or every user, access to one document beyond what their roles allow. Read is always granted; write, submit (submittable DocTypes only) and share (may share further) add rights. Sharing again with the same user replaces that share's rights. The user may be asked to confirm. Returns the share's rights.",
		false, true, append(shareTarget, idem,
			mcp.WithBoolean("write", mcp.Description("Also grant write")),
			mcp.WithBoolean("submit", mcp.Description("Also grant submit")),
			mcp.WithBoolean("share", mcp.Description("Also let the user share the document")),
			mcp.WithBoolean("notify", mcp.Description("Send the user a notification")),
		)...),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			user, everyone, err := shareTargetArgs(req)
			if err != nil {
				return nil, err
			}
			opts := client.ShareOptions{User: user, Everyone: everyone,
				Write: req.GetBool("write", false), Submit: req.GetBool("submit", false),
				Share: req.GetBool("share", false), Notify: req.GetBool("notify", false)}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return shareDoc(ctx, c, doctype, name, opts)
			}, nil
		}))

	s.AddTool(docTool("unshare_doc",
		"Remove a user's (or everyone's) share of a document. Access their roles give is not affected. Returns removed: false when there was no such share.",
		false, true, append(shareTarget, idem)...),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			user, everyone, err := shareTargetArgs(req)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return unshareDoc(ctx, c, doctype, name, user, everyone)
			}, nil
		}))
}

// shareTargetArgs reads user and everyone (exactly one of them).
func shareTargetArgs(req mcp.CallToolRequest) (string, bool, error) {
	user, err := stringArg(req, "user")
	if err != nil {
		return "", false, err
	}
	user = strings.TrimSpace(user)
	everyone := req.GetBool("everyone", false)
	return user, everyone, checkShareTarget(user, everyone)
}
