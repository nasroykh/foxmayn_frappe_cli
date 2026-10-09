package cmd

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// Document lifecycle tools: submit, cancel (one document or many), amend,
// copy, rename and workflow actions. Only get_transitions is read-only.

// docTool declares a tool that acts on one document (doctype + name).
func docTool(name, desc string, readOnly, destructive bool, extra ...mcp.ToolOption) mcp.Tool {
	opts := []mcp.ToolOption{
		mcp.WithDescription(desc),
		mcp.WithReadOnlyHintAnnotation(readOnly),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype", mcp.Required(), mcp.Description("The Frappe DocType")),
		mcp.WithString("name", mcp.Required(), mcp.Description("The name/ID of the document")),
	}
	if !readOnly {
		opts = append(opts, mcp.WithDestructiveHintAnnotation(destructive), mcp.WithIdempotentHintAnnotation(false))
	} else {
		opts = append(opts, mcp.WithIdempotentHintAnnotation(true))
	}
	return mcp.NewTool(name, append(opts, extra...)...)
}

// docHandler parses doctype and name, then the tool's own arguments.
func docHandler(env *mcpEnv, parse func(req mcp.CallToolRequest, doctype, name string) (toolCall, error)) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		name, err := nameArg(req, "name", true)
		if err != nil {
			return nil, err
		}
		return parse(req, doctype, name)
	})
}

func registerLifecycleTools(s *server.MCPServer, env *mcpEnv) {
	s.AddTool(docTool("submit_doc",
		"Submit a draft document (docstatus 0 to 1), which makes it final: a submitted document can only be cancelled, not edited. Refused for DocTypes with an active Workflow (use apply_workflow). Returns the submitted document.",
		false, true),
		docHandler(env, func(_ mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				if err := refuseWorkflow(ctx, c, doctype, name); err != nil {
					return nil, err
				}
				return c.SubmitDoc(ctx, doctype, name)
			}, nil
		}))

	s.AddTool(docTool("cancel_doc",
		"Cancel a submitted document (docstatus 1 to 2). This cannot be undone; amend_doc makes a corrected copy. Submitted documents that link to it block the cancel. Refused for DocTypes with an active Workflow (use apply_workflow). Returns the cancelled document.",
		false, true),
		docHandler(env, func(_ mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				if err := refuseWorkflow(ctx, c, doctype, name); err != nil {
					return nil, err
				}
				return c.CancelDoc(ctx, doctype, name)
			}, nil
		}))

	registerBulkLifecycle(s, env)

	overrides := jsonParam("data", `Optional object of fields to override in the copy, e.g. {"posting_date":"2026-10-01"}`)
	s.AddTool(docTool("amend_doc",
		"Create the amendment of a cancelled document: a new draft linked by amended_from, named <name>-1 (an amendment of <name>-1 is <name>-2). Returns the new draft.",
		false, false, overrides),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			data, err := objectArg(req, "data", false)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return c.AmendDoc(ctx, doctype, name, data)
			}, nil
		}))

	s.AddTool(docTool("copy_doc",
		"Duplicate a document like the desk's Duplicate: identity and \"no copy\" fields are left out, child rows are copied. Returns the new document.",
		false, false, overrides),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			data, err := objectArg(req, "data", false)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return c.DuplicateDoc(ctx, doctype, name, data)
			}, nil
		}))

	s.AddTool(docTool("rename_doc",
		"Rename a document; links to it are updated. With merge=true it is merged into the existing document named new_name and disappears, which cannot be undone. Returns the new name.",
		false, true,
		mcp.WithString("new_name", mcp.Required(), mcp.Description("The new name, or the existing document to merge into")),
		mcp.WithBoolean("merge", mcp.Description("Merge into the existing document new_name (default false)")),
	),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			newName, err := nameArg(req, "new_name", true)
			if err != nil {
				return nil, err
			}
			merge := req.GetBool("merge", false)
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				n, err := c.RenameDoc(ctx, doctype, name, newName, merge)
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{"doctype": doctype, "old_name": name, "name": n, "merged": merge}, nil
			}, nil
		}))

	s.AddTool(docTool("apply_workflow",
		"Apply a workflow action (e.g. Approve) to a document. The action must be one get_transitions lists; it may submit or cancel the document. Returns the document.",
		false, true,
		mcp.WithString("action", mcp.Required(), mcp.Description("The workflow action, as get_transitions lists it")),
	),
		docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			action, err := req.RequireString("action")
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				return c.ApplyWorkflow(ctx, doctype, name, action)
			}, nil
		}))
}

// registerBulkLifecycle adds bulk_submit and bulk_cancel: the CLI's
// per-document step (lifecycleBulk.op: read, check docstatus, act) over a
// list of names, one at a time.
func registerBulkLifecycle(s *server.MCPServer, env *mcpEnv) {
	for _, t := range []struct {
		name, desc string
		l          *lifecycleBulk
	}{
		{"bulk_submit", fmt.Sprintf("Submit multiple draft documents of a submittable DocType in one call (at most %d), docstatus 0 to 1: a submitted document can only be cancelled, not edited. Each document is read first, and one that is not a draft fails on its own. They are submitted one at a time, in the order given. Refused for DocTypes with an active Workflow (use apply_workflow). Returns per-item results. Processing continues on individual failures.", maxMCPBulkItems), bulkSubmit},
		{"bulk_cancel", fmt.Sprintf("Cancel multiple submitted documents in one call (at most %d), docstatus 1 to 2. This cannot be undone; amend_doc makes a corrected copy. Each document is read first, and one that is not submitted fails on its own. They are cancelled one at a time, in the order given, so list submitted documents that link to another one first: they block its cancel. Refused for DocTypes with an active Workflow (use apply_workflow). Returns per-item results. Processing continues on individual failures.", maxMCPBulkItems), bulkCancel},
	} {
		l := t.l
		tool := mcp.NewTool(t.name,
			mcp.WithDescription(t.desc),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("doctype", mcp.Required(), mcp.Description("The Frappe DocType")),
			jsonParam("names", `Array of document names, e.g. ["ACC-SINV-2026-00001","ACC-SINV-2026-00002"]`, mcp.Required()),
		)
		s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
			doctype, err := req.RequireString("doctype")
			if err != nil {
				return nil, err
			}
			raw, err := rawArg(req, "names")
			if err != nil {
				return nil, err
			}
			names, err := parseNames(raw)
			if err != nil {
				return nil, fmt.Errorf("names: %w", err)
			}
			run, err := bulkTool(len(names), 1, l.done, l.op(doctype, names))
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				if err := refuseWorkflow(ctx, c, doctype, ""); err != nil {
					return nil, err
				}
				return run(ctx, c)
			}, nil
		}))
	}
}

func registerGetTransitions(s *server.MCPServer, env *mcpEnv) {
	s.AddTool(docTool("get_transitions",
		"List the workflow actions the current user can apply to a document in its current state (action, next_state, allowed). Empty when none apply.",
		true, false),
		docHandler(env, func(_ mcp.CallToolRequest, doctype, name string) (toolCall, error) {
			return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
				list, err := c.WorkflowTransitions(ctx, doctype, name)
				if list == nil && err == nil {
					list = []interface{}{}
				}
				return list, err
			}, nil
		}))
}
