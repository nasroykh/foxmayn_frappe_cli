package cmd

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// registerIdentityTools adds the tools that tell a model what it is allowed
// to do: whoami and check_permission. Both only read.
func registerIdentityTools(s *server.MCPServer, env *mcpEnv) {
	registerWhoami(s, env)
	registerCheckPermission(s, env)
}

func registerWhoami(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("whoami",
		mcp.WithDescription("Show which user the configured credentials belong to on the Frappe site, the user's roles, and the installed apps with their versions (Frappe major version included). Unlike ping this proves the login works. Versions are cached for 24 hours."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithBoolean("refresh", mcp.Description("Read the site's app versions again instead of using the cache.")),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		refresh := req.GetBool("refresh", false)
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			site, ok := siteFrom(ctx)
			if !ok {
				return nil, fmt.Errorf("whoami: no site in the call context")
			}
			return buildWhoami(ctx, c, site, refresh)
		}, nil
	}))
}

func registerCheckPermission(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("check_permission",
		mcp.WithDescription("Check whether the configured user holds a permission on a DocType or on one document, without changing anything. Use it before a write to avoid a refused call. With name, the site judges that document (user permissions, sharing and controller rules count): basis is 'document'. Without name, the DocType's role permission rows are applied to the user's roles (if_owner rows narrow a right to the user's own documents, select is implied by read, submit/cancel/amend need a submittable DocType, import an importable one): basis is 'doctype', and user permissions, sharing, controller rules and the disable_document_sharing setting are not considered. owner_only means the right holds only for documents the user created (read and select stay allowed, other rights are denied). A child table is refused: check its parent DocType. The automatic role Desk User counts only for a user known to be a System User; note says when it would have changed the answer. A denial is a normal result (allowed: false), not an error."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype", mcp.Required(), mcp.Description("The Frappe DocType, e.g. 'Sales Invoice'")),
		mcp.WithString("name", mcp.Description("Name of one document. Omit to check the DocType as a whole (needed to ask about 'create').")),
		mcp.WithString("perm_type", mcp.Description("Permission type: select, read (default), write, create, delete, submit, cancel, amend, print, email, report, import, export, share. With name, any lower-case permission type the site defines.")),
		mcp.WithBoolean("all", mcp.Description("With name: also return every permission the user holds on the document.")),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		name, err := nameArg(req, "name", false)
		if err != nil {
			return nil, err
		}
		perm := req.GetString("perm_type", "read")
		if perm == "" {
			perm = "read"
		}
		perm, ok := parsePerm(perm, name != "")
		if !ok {
			return nil, fmt.Errorf("perm_type: %s", permTypeHelp(req.GetString("perm_type", ""), name != ""))
		}
		all := req.GetBool("all", false)
		if all && name == "" {
			return nil, fmt.Errorf("all: needs name, the site lists permissions per document")
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return checkPermission(ctx, c, doctype, name, perm, all)
		}, nil
	}))
}
