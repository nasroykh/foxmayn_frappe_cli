package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// One MCP server can serve several sites (T1.6): `ffc mcp --sites a,b` or
// --all-sites. Each call then names its site in a `site` argument, which is
// required so a call never lands on a site by default; each site's own
// `mcp:` policy applies to calls on it, and the flags narrow every site.
// The set of sites is fixed at start.

// siteless tools work on the server, not on a site: they take no `site`
// argument and build no client.
var siteless = map[string]bool{"list_sites": true}

// mcpSites resolves the sites `ffc mcp` serves, the default one first.
// Without --sites or --all-sites it is the selected site alone, as before.
func mcpSites(o mcpOptions) ([]string, error) {
	if !o.allSites && len(o.sites) == 0 {
		site, err := config.LoadSite(o.site, o.configPath)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		return []string{site.Name}, nil
	}
	// FFC_API_KEY/FFC_API_SECRET replace the stored credentials of the site
	// they are loaded for; with several sites they would be sent to all.
	if os.Getenv("FFC_API_KEY") != "" && os.Getenv("FFC_API_SECRET") != "" {
		return nil, o.fail("FFC_API_KEY and FFC_API_SECRET apply to one site; unset them to serve several sites")
	}
	path, err := o.cfgPath()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Read(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	want := o.sites
	if o.allSites {
		want = nil
		for name := range cfg.Sites {
			want = append(want, name)
		}
		sort.Strings(want)
	}
	var sites []string
	seen := map[string]bool{}
	for _, w := range want {
		site, err := config.LoadSite(w, path) // exact, then a unique case-insensitive match
		if err != nil {
			return nil, fmt.Errorf("%s: %w", o.optName("--sites", "Sites"), err)
		}
		if !seen[site.Name] {
			seen[site.Name] = true
			sites = append(sites, site.Name)
		}
	}
	if len(sites) == 0 {
		return nil, fmt.Errorf("config: no sites in %s", path)
	}
	// The default site goes first: --site, else default_site when served.
	first := cfg.DefaultSite
	if o.site != "" {
		site, err := config.LoadSite(o.site, path)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		if !seen[site.Name] {
			return nil, o.fail("%s %s is not one of the served sites (%s)", o.optName("--site", "Site"), site.Name, strings.Join(sites, ", "))
		}
		first = site.Name
	}
	for i, s := range sites {
		if s == first {
			sites[0], sites[i] = sites[i], sites[0]
			break
		}
	}
	return sites, nil
}

// siteFor is the site a call is for: its `site` argument, which a
// multi-site server requires, or the only site.
func (env *mcpEnv) siteFor(req mcp.CallToolRequest) (string, error) {
	v, given := req.GetArguments()["site"]
	if !given || v == nil || v == "" {
		if len(env.sites) > 1 {
			return "", fmt.Errorf("site is required: this MCP server serves %s (list_sites describes them)", env.siteNames())
		}
		return env.sites[0], nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("site: expected a string")
	}
	s = strings.TrimSpace(s)
	var folded []string
	for _, name := range env.sites {
		if name == s {
			return name, nil
		}
		if strings.EqualFold(name, s) {
			folded = append(folded, name)
		}
	}
	if len(folded) == 1 {
		return folded[0], nil
	}
	return "", fmt.Errorf("site %s is not served by this MCP server; it serves %s", quoted(s, 100), env.siteNames())
}

func (env *mcpEnv) siteNames() string {
	q := make([]string, len(env.sites))
	for i, s := range env.sites {
		q[i] = strconv.Quote(s)
	}
	return strings.Join(q, ", ")
}

// policies returns the policy of every served site.
func (env *mcpEnv) policies(ctx context.Context) ([]mcpPolicy, error) {
	out := make([]mcpPolicy, 0, len(env.sites))
	for _, name := range env.sites {
		site, err := env.site(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, newMCPPolicy(site, env.flags))
	}
	return out, nil
}

// runSiteless runs a tool that works on the server: allowed when some
// served site's policy allows it.
func (env *mcpEnv) runSiteless(ctx context.Context, req mcp.CallToolRequest, parse func(req mcp.CallToolRequest) (toolCall, error), rec *auditRecord) *mcp.CallToolResult {
	fail := func(status string, err error) *mcp.CallToolResult {
		rec.Status, rec.Error, rec.cause = status, err.Error(), err
		return mcp.NewToolResultError(err.Error())
	}
	call, err := parse(req)
	if err != nil {
		return fail(auditInvalid, err)
	}
	policies, err := env.policies(ctx)
	if err != nil {
		return fail(auditError, err)
	}
	if err := anyAllows(policies, req.Params.Name); err != nil {
		return fail(auditDenied, err)
	}
	out, err := call(ctx, nil)
	if err != nil {
		return fail(auditError, err)
	}
	res := toolResult(out)
	rec.Status = auditOK
	if res.IsError {
		rec.Status = auditError // the result was too large to return
	}
	return res
}

// anyAllows returns nil when some policy allows tool, else the default
// site's reason. No policy allows nothing.
func anyAllows(policies []mcpPolicy, tool string) error {
	if len(policies) == 0 {
		return fmt.Errorf("policy: no site's policy could be read")
	}
	for _, p := range policies {
		if p.toolAllowed(tool) == nil {
			return nil
		}
	}
	return policies[0].toolAllowed(tool)
}

// authKind names how ffc signs in to a site, never with the secret.
func authKind(s *config.SiteConfig) string {
	switch {
	case s.IsOAuth():
		return "oauth"
	case s.APIKey != "" && s.APISecret != "":
		return "api_key"
	case s.IsSessionAuth():
		return "password"
	}
	return "none"
}

// listSitesSchema is list_sites' output schema (its structuredContent).
var listSitesSchema = json.RawMessage(`{"type":"object","description":"The served sites. The text content is the bare array, as before structured output; structuredContent wraps the same array as {sites}.",` +
	`"properties":{"sites":{"type":"array","items":{"type":"object",` +
	`"properties":{"name":{"type":"string"},"url":{"type":"string"},"auth":{"type":"string","enum":["oauth","api_key","password","none"]},` +
	`"read_only":{"type":"boolean"},"default":{"type":"boolean"}},"required":["name","url","auth","read_only"]}}},"required":["sites"]}`)

func registerListSites(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("list_sites",
		mcp.WithDescription("List the Frappe sites this MCP server serves: name, URL, how ffc signs in, and whether MCP may only read it. When the server serves several sites, every other tool requires one of these names in its site argument; there is no default."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithRawOutputSchema(listSitesSchema),
	)
	s.AddTool(tool, toolHandler(env, func(mcp.CallToolRequest) (toolCall, error) {
		return func(ctx context.Context, _ *client.FrappeClient) (interface{}, error) {
			out := make([]map[string]interface{}, 0, len(env.sites))
			for i, name := range env.sites {
				site, err := env.site(ctx, name)
				if err != nil {
					return nil, err
				}
				row := map[string]interface{}{
					"name": site.Name, "url": site.URL, "auth": authKind(site),
					"read_only": newMCPPolicy(site, env.flags).readOnly(),
				}
				if len(env.sites) == 1 {
					row["default"] = i == 0 // with several, calls must name the site
				}
				out = append(out, row)
			}
			// The text stays the bare list it always was.
			return structuredOut{Text: out, Structured: map[string]interface{}{"sites": out}}, nil
		}, nil
	}))
}

// addSiteParam gives every site tool a `site` argument naming the served
// sites. A single-site server keeps its tools as they were.
func addSiteParam(s *server.MCPServer, env *mcpEnv) {
	if len(env.sites) < 2 {
		return
	}
	desc := fmt.Sprintf("Required. The site to use: one of %s (see list_sites).", env.siteNames())
	var tools []server.ServerTool
	for name, t := range s.ListTools() {
		if siteless[name] {
			continue
		}
		tool := t.Tool
		props := make(map[string]any, len(tool.InputSchema.Properties)+1)
		for k, v := range tool.InputSchema.Properties {
			props[k] = v
		}
		props["site"] = map[string]any{"type": "string", "enum": env.sites, "description": desc}
		tool.InputSchema.Properties = props
		tool.InputSchema.Required = append(append([]string(nil), tool.InputSchema.Required...), "site")
		tools = append(tools, server.ServerTool{Tool: tool, Handler: t.Handler})
	}
	s.AddTools(tools...)
}
