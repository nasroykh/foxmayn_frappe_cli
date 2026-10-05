package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// toolAction is what a tool does, as far as the MCP policy is concerned.
type toolAction int

const (
	actRead   toolAction = iota // reads documents or metadata
	actWrite                    // creates, changes or deletes documents
	actMethod                   // calls an arbitrary method (call_method)
)

// toolActions lists every MCP tool. A tool missing here is refused at call
// time (fail closed), and TestMCPPolicyCoversEveryTool keeps the list in
// step with registerTools.
var toolActions = map[string]toolAction{
	"ping": actRead, "get_doc": actRead, "list_docs": actRead, "count_docs": actRead,
	"get_schema": actRead, "list_doctypes": actRead, "list_reports": actRead,
	"run_report": actRead, "get_transitions": actRead,

	"create_doc": actWrite, "update_doc": actWrite, "delete_doc": actWrite,
	"bulk_create": actWrite, "bulk_update": actWrite, "bulk_delete": actWrite,
	"submit_doc": actWrite, "cancel_doc": actWrite, "amend_doc": actWrite,
	"copy_doc": actWrite, "rename_doc": actWrite, "apply_workflow": actWrite,

	"call_method": actMethod,
}

// sensitiveDoctypes control users, permissions, credentials or server-side
// code. MCP may read them, but writes are refused unless the site's config
// lists the DocType in mcp.allow_doctypes (D4).
var sensitiveDoctypes = []string{
	"User", "Role", "Has Role", "Role Profile", "Module Profile",
	"DocType", "DocPerm", "Custom DocPerm", "User Permission",
	"System Settings", "OAuth Client", "Social Login Key", "LDAP Settings", "Email Account",
	"Server Script", "Client Script", "Report", "Webhook", "Notification", "Scheduled Job Type",
}

// deniedMethods run code on the server, change installed apps or rotate a
// credential. call_method refuses them unless the site's config lists them
// in mcp.allow_methods.
var deniedMethods = []string{
	"frappe.desk.doctype.system_console.system_console.execute_code",
	"frappe.core.doctype.user.user.generate_keys",
	"frappe.integrations.frappe_providers.*", // Frappe Cloud app install/uninstall
}

// toolScope is what one tool call touches.
type toolScope struct {
	Action   toolAction
	Doctypes []string
	Names    []string
	Method   string
	Report   string // run_report: checked through the report's ref_doctype
}

// scopeOf reads what a tool call touches from its arguments. The tool's own
// parse step has already validated them.
func scopeOf(req mcp.CallToolRequest) (toolScope, error) {
	tool := req.Params.Name
	action, ok := toolActions[tool]
	if !ok {
		return toolScope{}, fmt.Errorf("policy: tool %q has no policy scope", tool)
	}
	sc := toolScope{Action: action}
	args := req.GetArguments()
	str := func(k string) string { s, _ := args[k].(string); return s }
	if dt := str("doctype"); dt != "" {
		sc.Doctypes = append(sc.Doctypes, dt)
	}
	if n := str("name"); n != "" {
		sc.Names = append(sc.Names, n)
	}
	switch tool {
	case "bulk_delete":
		sc.Names, _ = stringsArg(req, "names")
	case "bulk_update":
		var rows []map[string]interface{}
		if _, err := jsonArg(req, "data", &rows); err == nil {
			for _, r := range rows {
				if n, ok := r["name"].(string); ok {
					sc.Names = append(sc.Names, n)
				}
			}
		}
	case "run_report":
		sc.Report = str("report_name")
	case "call_method":
		sc.Method = str("method")
		var a interface{}
		if ok, err := jsonArg(req, "args", &a); ok && err == nil {
			sc.Doctypes = append(sc.Doctypes, methodDoctypes(a)...)
		}
	}
	return sc, nil
}

// methodDoctypes finds the DocTypes a frappe.client-style call names: a
// "doctype" argument, a "doc" or "docs" with a doctype. It is best effort:
// a custom method can touch any DocType without naming it.
func methodDoctypes(args interface{}) []string {
	m, ok := args.(map[string]interface{})
	if !ok {
		return nil
	}
	var out []string
	add := func(v interface{}) {
		if d, ok := v.(map[string]interface{}); ok {
			if dt, ok := d["doctype"].(string); ok && dt != "" {
				out = append(out, dt)
			}
		}
	}
	add(m)
	add(m["doc"])
	if docs, ok := m["docs"].([]interface{}); ok {
		for _, d := range docs {
			add(d)
		}
	}
	return out
}

// mcpPolicy is the policy for one site: the site's config, which may
// loosen the built-in rules, and the `ffc mcp` flags, which only tighten.
type mcpPolicy struct {
	site string
	cfg  config.MCPPolicy
	flag config.MCPPolicy
}

func newMCPPolicy(site *config.SiteConfig, flags config.MCPPolicy) mcpPolicy {
	p := mcpPolicy{site: site.Name, flag: flags}
	if site.MCP != nil {
		p.cfg = *site.MCP
	}
	return p
}

func (p mcpPolicy) readOnly() bool { return p.cfg.ReadOnly || p.flag.ReadOnly }

// key names the config key or flag that controls a rule, for messages.
func (p mcpPolicy) key(name string) string {
	if p.site == "" {
		return "--" + strings.ReplaceAll(name, "_", "-")
	}
	return fmt.Sprintf("sites.%s.mcp.%s", p.site, name)
}

func (p mcpPolicy) flagKey(name string) string { return "--" + strings.ReplaceAll(name, "_", "-") }

// toolAllowed reports whether tool may be registered and called.
func (p mcpPolicy) toolAllowed(tool string) error {
	if len(p.cfg.AllowTools) > 0 && !contains(p.cfg.AllowTools, tool, false) {
		return fmt.Errorf("policy: tool %q is not in %s", tool, p.key("allow_tools"))
	}
	if len(p.flag.AllowTools) > 0 && !contains(p.flag.AllowTools, tool, false) {
		return fmt.Errorf("policy: tool %q is not in %s", tool, p.flagKey("allow_tools"))
	}
	if p.readOnly() && toolActions[tool] != actRead {
		return fmt.Errorf("policy: %s writes and MCP is read-only for this site (%s or --read-only)", tool, p.key("read_only"))
	}
	return nil
}

// check applies the policy to a tool call, before any request is sent.
func (p mcpPolicy) check(tool string, sc toolScope) error {
	if err := p.toolAllowed(tool); err != nil {
		return err
	}
	for _, dt := range sc.Doctypes {
		if err := p.doctypeAllowed(dt, sc.Action != actRead); err != nil {
			return err
		}
	}
	if sc.Action == actMethod {
		return p.methodAllowed(sc.Method)
	}
	return nil
}

func (p mcpPolicy) doctypeAllowed(dt string, write bool) error {
	switch {
	case contains(p.cfg.DenyDoctypes, dt, true):
		return fmt.Errorf("policy: DocType %q is denied by %s", dt, p.key("deny_doctypes"))
	case contains(p.flag.DenyDoctypes, dt, true):
		return fmt.Errorf("policy: DocType %q is denied by %s", dt, p.flagKey("deny_doctypes"))
	case len(p.cfg.AllowDoctypes) > 0 && !contains(p.cfg.AllowDoctypes, dt, true):
		return fmt.Errorf("policy: DocType %q is not in %s", dt, p.key("allow_doctypes"))
	case len(p.flag.AllowDoctypes) > 0 && !contains(p.flag.AllowDoctypes, dt, true):
		return fmt.Errorf("policy: DocType %q is not in %s", dt, p.flagKey("allow_doctypes"))
	case write && contains(sensitiveDoctypes, dt, true) && !contains(p.cfg.AllowDoctypes, dt, true):
		return fmt.Errorf("policy: DocType %q is sensitive; MCP may read it but not write it unless %s lists it", dt, p.key("allow_doctypes"))
	}
	return nil
}

func (p mcpPolicy) methodAllowed(method string) error {
	switch {
	case matchMethod(p.cfg.DenyMethods, method):
		return fmt.Errorf("policy: method %q is denied by %s", method, p.key("deny_methods"))
	case matchMethod(p.flag.DenyMethods, method):
		return fmt.Errorf("policy: method %q is denied by %s", method, p.flagKey("deny_methods"))
	case matchMethod(deniedMethods, method) && !matchMethod(p.cfg.AllowMethods, method):
		return fmt.Errorf("policy: method %q runs code, changes apps or rotates a credential, so MCP may not call it unless %s lists it", method, p.key("allow_methods"))
	case len(p.cfg.AllowMethods) > 0 && !matchMethod(p.cfg.AllowMethods, method):
		return fmt.Errorf("policy: method %q is not in %s", method, p.key("allow_methods"))
	case len(p.flag.AllowMethods) > 0 && !matchMethod(p.flag.AllowMethods, method):
		return fmt.Errorf("policy: method %q is not in %s", method, p.flagKey("allow_methods"))
	case len(p.cfg.AllowMethods) == 0 && len(p.flag.AllowMethods) == 0 &&
		(len(p.cfg.AllowDoctypes) > 0 || len(p.flag.AllowDoctypes) > 0):
		return fmt.Errorf("policy: call_method could reach any DocType, so with allow_doctypes set it may call only the methods in %s", p.key("allow_methods"))
	}
	return nil
}

// checkReport applies the DocType rules to run_report through the report's
// ref_doctype, which only the site knows. It costs a request only when a
// DocType list is set.
func (p mcpPolicy) checkReport(ctx context.Context, c *client.FrappeClient, sc toolScope) error {
	if sc.Report == "" || len(p.cfg.AllowDoctypes)+len(p.cfg.DenyDoctypes)+len(p.flag.AllowDoctypes)+len(p.flag.DenyDoctypes) == 0 {
		return nil
	}
	r, err := c.GetDoc(ctx, "Report", sc.Report)
	if err != nil {
		return fmt.Errorf("policy: reading the DocType of report %q: %w", sc.Report, err)
	}
	dt, _ := r["ref_doctype"].(string)
	if dt == "" {
		return fmt.Errorf("policy: report %q has no ref_doctype, so the DocType rules cannot be checked", sc.Report)
	}
	return p.doctypeAllowed(dt, false)
}

func contains(list []string, s string, fold bool) bool {
	for _, x := range list {
		if x == s || fold && strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// matchMethod reports whether method is in list; an entry ending in "*"
// matches by prefix.
func matchMethod(list []string, method string) bool {
	for _, x := range list {
		if p, ok := strings.CutSuffix(x, "*"); ok && strings.HasPrefix(method, p) || x == method {
			return true
		}
	}
	return false
}
