package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
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
	"list_sites": actRead, "ping": actRead, "get_doc": actRead, "list_docs": actRead, "count_docs": actRead,
	"get_schema": actRead, "list_doctypes": actRead, "list_reports": actRead,
	"run_report": actRead, "get_transitions": actRead, "search": actRead,

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
	// Users, roles and permissions.
	"User", "Role", "Has Role", "Role Profile", "Module Profile", "User Type", "User Group",
	"DocType", "DocPerm", "Custom DocPerm", "User Permission", "DocShare",
	"Custom Field", "Property Setter", "Customize Form",
	// Settings and credentials.
	"System Settings", "OAuth Client", "OAuth Provider Settings", "OAuth Bearer Token",
	"OAuth Authorization Code", "Connected App", "Token Cache", "Social Login Key",
	"LDAP Settings", "Email Account",
	// Code, templates and automation that run or render on the server or
	// in browsers.
	"Server Script", "Client Script", "Report", "Print Format", "Website Script",
	"Web Page", "Web Form", "Custom HTML Block", "Webhook", "Notification",
	"Auto Email Report", "Assignment Rule", "Energy Point Rule", "Scheduled Job Type", "System Console",
	// Bulk paths into any DocType, and file visibility.
	"Data Import", "File",
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
	Confirm  bool   // destroys or merges documents: ask the user first
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
	if n, ok := docName(args["name"]); ok {
		sc.Names = append(sc.Names, n)
	}
	switch tool {
	case "bulk_delete":
		sc.Names, _ = stringsArg(req, "names")
	case "bulk_update":
		var rows []map[string]interface{}
		if _, err := jsonArg(req, "data", &rows); err == nil {
			for _, r := range rows {
				if n, ok := docName(r["name"]); ok {
					sc.Names = append(sc.Names, n)
				}
			}
		}
	case "run_report":
		sc.Report = str("report_name")
	case "call_method":
		sc.Method = str("method")
		sc.Doctypes = append(sc.Doctypes, methodDoctypes(args["args"])...)
	}
	for i, dt := range sc.Doctypes {
		if text.Sanitize(dt) != dt {
			return sc, fmt.Errorf("policy: DocType name %q contains control or invisible characters", dt)
		}
		sc.Doctypes[i] = strings.TrimSpace(dt)
	}
	sc.Confirm = needsConfirm(req, sc.Method)
	return sc, nil
}

// doctypeKeys are the argument names Frappe methods use for a DocType.
var doctypeKeys = map[string]bool{
	"doctype": true, "dt": true, "ref_doctype": true, "reference_doctype": true,
	"parenttype": true, "document_type": true,
}

// methodDoctypes finds every DocType a method's arguments name, at any
// depth, including inside JSON passed as a string (frappe.client.insert
// and savedocs take the document as a JSON string). It is best effort: a
// custom method can touch any DocType without naming it.
func methodDoctypes(args interface{}) []string {
	var out []string
	var walk func(v interface{}, depth int)
	walk = func(v interface{}, depth int) {
		if depth > 32 {
			return
		}
		switch val := v.(type) {
		case map[string]interface{}:
			for k, x := range val {
				if s, ok := x.(string); ok && doctypeKeys[k] && s != "" {
					out = append(out, s)
				}
				walk(x, depth+1)
			}
		case []interface{}:
			for _, x := range val {
				walk(x, depth+1)
			}
		case string:
			if t := strings.TrimSpace(val); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
				var inner interface{}
				if json.Unmarshal([]byte(t), &inner) == nil {
					walk(inner, depth+1)
				}
			}
		}
	}
	walk(args, 0)
	return out
}

// docMethods change or read documents of the DocType their arguments name.
// A call to one whose DocType ffc cannot find is refused: it might write a
// sensitive or denied DocType.
var docMethods = []string{
	"frappe.client.*", "frappe.desk.form.save.*", "frappe.handler.run_doc_method",
	"frappe.model.workflow.*", "frappe.desk.form.utils.*", "frappe.desk.reportview.*",
}

// docMethodsWithoutDoctype take no DocType argument but are safe: they read
// the site's time zone or edit a comment.
var docMethodsWithoutDoctype = []string{
	"frappe.client.get_time_zone",
	"frappe.desk.form.utils.update_comment", "frappe.desk.form.utils.update_comment_publicity",
}

// methodNames are the names a method call is matched under. Frappe runs an
// undotted name as an API Server Script of that name or, failing that, as
// frappe.handler.<name> (run_doc_method), so both count: a deny entry for
// either refuses the call, an allow entry for either allows it.
func methodNames(method string) []string {
	if strings.Contains(method, ".") {
		return []string{method}
	}
	return []string{method, "frappe.handler." + method}
}

// checkMethodName refuses a method name Frappe would read differently from
// the policy: it cuts the name at the first "/" (so ".../execute_code/x"
// runs execute_code) and strips spaces.
func checkMethodName(method string) error {
	if method == "" || strings.ContainsAny(method, "/ \t\r\n") || text.Sanitize(method) != method {
		return fmt.Errorf("policy: method name %q may only contain letters, digits, dots and underscores", method)
	}
	return nil
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
	action, known := toolActions[tool]
	if !known {
		return fmt.Errorf("policy: tool %q has no policy scope", tool)
	}
	if p.readOnly() && action != actRead {
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
		if err := checkMethodName(sc.Method); err != nil {
			return err
		}
		names := methodNames(sc.Method)
		if anyMatch(docMethods, names) && !anyMatch(docMethodsWithoutDoctype, names) && len(sc.Doctypes) == 0 {
			return fmt.Errorf("policy: %s works on documents but its arguments name no DocType, so the DocType rules cannot be checked; pass doctype", sc.Method)
		}
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
	names := methodNames(method)
	switch {
	case anyMatch(p.cfg.DenyMethods, names):
		return fmt.Errorf("policy: method %q is denied by %s", method, p.key("deny_methods"))
	case anyMatch(p.flag.DenyMethods, names):
		return fmt.Errorf("policy: method %q is denied by %s", method, p.flagKey("deny_methods"))
	case anyMatch(deniedMethods, names) && !anyMatch(p.cfg.AllowMethods, names):
		return fmt.Errorf("policy: method %q runs code, changes apps or rotates a credential, so MCP may not call it unless %s lists it", method, p.key("allow_methods"))
	case len(p.cfg.AllowMethods) > 0 && !anyMatch(p.cfg.AllowMethods, names):
		return fmt.Errorf("policy: method %q is not in %s", method, p.key("allow_methods"))
	case len(p.flag.AllowMethods) > 0 && !anyMatch(p.flag.AllowMethods, names):
		return fmt.Errorf("policy: method %q is not in %s", method, p.flagKey("allow_methods"))
	// A method can reach any DocType, so allow_doctypes needs a method
	// allowlist from the same source or a stricter one: a flag list must not
	// stand in for the config's.
	case len(p.cfg.AllowDoctypes) > 0 && len(p.cfg.AllowMethods) == 0:
		return fmt.Errorf("policy: call_method could reach any DocType, so with allow_doctypes set it may call only the methods in %s", p.key("allow_methods"))
	case len(p.flag.AllowDoctypes) > 0 && len(p.cfg.AllowMethods) == 0 && len(p.flag.AllowMethods) == 0:
		return fmt.Errorf("policy: call_method could reach any DocType, so with --allow-doctypes it may call only the methods in --allow-methods or %s", p.key("allow_methods"))
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

type policyCtxKey struct{}

// withPolicy hands the policy a call was checked against to the call itself,
// for tools whose result spans DocTypes the request does not name.
func withPolicy(ctx context.Context, p mcpPolicy) context.Context {
	return context.WithValue(ctx, policyCtxKey{}, p)
}

// policyFrom returns the policy withPolicy stored. Without one it is the
// zero policy, which allows everything: the call-level check has already run
// by the time a tool uses it.
func policyFrom(ctx context.Context) mcpPolicy {
	p, _ := ctx.Value(policyCtxKey{}).(mcpPolicy)
	return p
}

// filterDoctypeRows keeps the rows whose "doctype" the DocType rules allow
// reading. A global search spans DocTypes, so the request names none for
// check to refuse; its hits are filtered here instead. A row with no DocType
// is dropped: it cannot be checked.
func (p mcpPolicy) filterDoctypeRows(rows []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(rows))
	for _, r := range rows {
		if dt, _ := r["doctype"].(string); dt != "" && p.doctypeAllowed(strings.TrimSpace(dt), false) == nil {
			out = append(out, r)
		}
	}
	return out
}

func contains(list []string, s string, fold bool) bool {
	for _, x := range list {
		if x == s || fold && strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// anyMatch reports whether any of names is in list (see matchMethod).
func anyMatch(list, names []string) bool {
	for _, n := range names {
		if matchMethod(list, n) {
			return true
		}
	}
	return false
}

// matchMethod reports whether method is in list; an entry ending in "*"
// matches by prefix.
func matchMethod(list []string, method string) bool {
	for _, x := range list {
		if p, ok := strings.CutSuffix(x, "*"); (ok && strings.HasPrefix(method, p)) || x == method {
			return true
		}
	}
	return false
}
