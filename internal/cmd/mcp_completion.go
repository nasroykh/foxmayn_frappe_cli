package cmd

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// mcpCompleter answers completion/complete for the resource templates
// ({site}, {doctype}) and the prompt arguments (site, doctype, report_name)
// from the local cache that the CLI fills (list-doctypes, list-reports,
// `ffc cache warm`). Like shell completion it sends no request and never
// writes the cache, and it offers no value the site's policy would refuse:
// a site whose policy does not allow the tool, a DocType the DocType rules
// deny, a report whose DocType they deny. Document names are never
// completed (documents are not cached).
type mcpCompleter struct {
	s   *server.MCPServer
	env *mcpEnv
}

// maxCompletionValues is the most a completion result may hold (MCP spec).
const maxCompletionValues = 100

// registerMCPCompletions turns on the completions capability.
func registerMCPCompletions(s *server.MCPServer, env *mcpEnv) {
	c := &mcpCompleter{s: s, env: env}
	server.WithCompletions()(s)
	server.WithResourceCompletionProvider(c)(s)
	server.WithPromptCompletionProvider(c)(s)
}

func completion(values []string) *mcp.Completion {
	out := &mcp.Completion{Values: values, Total: len(values)}
	if out.Values == nil {
		out.Values = []string{}
	}
	if len(values) > maxCompletionValues {
		out.Values, out.HasMore = values[:maxCompletionValues], true
	}
	return out
}

func (m *mcpCompleter) CompleteResourceArgument(ctx context.Context, uri string, arg mcp.CompleteArgument, cc mcp.CompleteContext) (*mcp.Completion, error) {
	var tool string
	switch uri {
	case schemaTemplate:
		tool = "get_schema"
	case docTemplate:
		tool = "get_doc"
	default:
		return completion(nil), nil
	}
	return m.complete(ctx, []string{tool}, arg, cc), nil
}

func (m *mcpCompleter) CompletePromptArgument(ctx context.Context, name string, arg mcp.CompleteArgument, cc mcp.CompleteContext) (*mcp.Completion, error) {
	for _, p := range mcpPrompts {
		if p.name == name {
			return m.complete(ctx, p.needs, arg, cc), nil
		}
	}
	return completion(nil), nil
}

// complete offers values for arg of a template or prompt that needs tools.
func (m *mcpCompleter) complete(ctx context.Context, tools []string, arg mcp.CompleteArgument, cc mcp.CompleteContext) *mcp.Completion {
	registered := m.s.ListTools()
	write := false
	for _, t := range tools {
		if _, ok := registered[t]; !ok {
			return completion(nil)
		}
		write = write || toolActions[t] != actRead
	}
	switch arg.Name {
	case "site":
		var sites []string
		for _, name := range m.env.sites {
			if _, _, ok := m.policy(ctx, name, tools); ok && name != "" {
				sites = append(sites, name)
			}
		}
		return completion(matching(sites, arg.Value))
	case "doctype", "report_name":
		args := map[string]any{}
		if s, ok := cc.Arguments["site"]; ok {
			args["site"] = s
		}
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		name, err := m.env.siteFor(req)
		if err != nil {
			return completion(nil)
		}
		site, p, ok := m.policy(ctx, name, tools)
		if !ok {
			return completion(nil)
		}
		var names []string
		if arg.Name == "doctype" {
			for _, it := range readListCache(site, "DocType", time.Now()) {
				if p.doctypeAllowed(it.Name, write) == nil {
					names = append(names, it.Name)
				}
			}
		} else {
			// As checkReport: with DocType rules, a report is offered only
			// when its DocType is known and allowed.
			rules := len(p.cfg.AllowDoctypes)+len(p.cfg.DenyDoctypes)+len(p.flag.AllowDoctypes)+len(p.flag.DenyDoctypes) > 0
			for _, it := range readListCache(site, "Report", time.Now()) {
				if !rules || (it.RefDoctype != "" && p.doctypeAllowed(it.RefDoctype, false) == nil) {
					names = append(names, it.Name)
				}
			}
		}
		return completion(matching(names, arg.Value))
	}
	return completion(nil)
}

// policy returns a served site's config (read without the network, as for
// a tool call) and its policy, when that policy allows every tool.
func (m *mcpCompleter) policy(ctx context.Context, name string, tools []string) (*config.SiteConfig, mcpPolicy, bool) {
	site, err := m.env.site(ctx, name)
	if err != nil {
		return nil, mcpPolicy{}, false
	}
	p := newMCPPolicy(site, m.env.flags)
	for _, t := range tools {
		if p.toolAllowed(t) != nil {
			return nil, mcpPolicy{}, false
		}
	}
	return site, p, true
}
