package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// mcpTPolicy starts tools on a fake site under the site policy cfg and the
// flag policy flags, with an audit log in a temporary directory.
func mcpTPolicy(t *testing.T, cfg *config.MCPPolicy, flags config.MCPPolicy) (*server.MCPServer, *frappetest.Site, *config.SiteConfig, string) {
	t.Helper()
	site := frappetest.New(t)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1", "description": "a"})
	site.Add("User", map[string]interface{}{"name": "u@example.com"})
	site.Add("Report",
		map[string]interface{}{"name": "Users", "ref_doctype": "User"},
		map[string]interface{}{"name": "Todos", "ref_doctype": "ToDo"})
	c, err := client.New(context.Background(), &config.SiteConfig{URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	sc := &config.SiteConfig{Name: "prod", MCP: cfg}
	audit := &auditLog{path: filepath.Join(t.TempDir(), auditFileName)}
	env := &mcpEnv{
		sites:    []string{sc.Name},
		site:     func(context.Context, string) (*config.SiteConfig, error) { return sc, nil },
		client:   func(context.Context, *config.SiteConfig) (*client.FrappeClient, error) { return c, nil },
		flags:    flags,
		audit:    audit,
		toolsets: knownToolsets,
	}
	s := server.NewMCPServer("test", "0")
	registerTools(s, env, []mcpPolicy{newMCPPolicy(sc, flags)})
	return s, site, sc, audit.path
}

func TestMCPPolicyCoversEveryTool(t *testing.T) {
	s := server.NewMCPServer("t", "0")
	registerAllTools(s, &mcpEnv{})
	tools := s.ListTools()
	for name := range tools {
		if _, ok := toolActions[name]; !ok {
			t.Errorf("tool %s has no entry in toolActions", name)
		}
	}
	for name := range toolActions {
		if _, ok := tools[name]; !ok {
			t.Errorf("toolActions lists %s, which is not registered", name)
		}
	}
	// A read tool must not be annotated as writing, and the reverse.
	for name, tool := range tools {
		ro := tool.Tool.Annotations.ReadOnlyHint != nil && *tool.Tool.Annotations.ReadOnlyHint
		if ro != (toolActions[name] == actRead) {
			t.Errorf("%s: readOnlyHint %v but action %v", name, ro, toolActions[name])
		}
	}
}

func TestMCPPolicyRules(t *testing.T) {
	todo := map[string]interface{}{"doctype": "ToDo", "data": map[string]interface{}{"description": "x"}}
	user := map[string]interface{}{"doctype": "User", "name": "u@example.com", "data": map[string]interface{}{"enabled": 0}}
	method := func(m string, args map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"method": m, "args": args}
	}
	cases := []struct {
		name  string
		cfg   *config.MCPPolicy
		flags config.MCPPolicy
		tool  string
		args  map[string]interface{}
		want  string // "" = allowed by the policy
	}{
		{"defaults allow ordinary writes", nil, config.MCPPolicy{}, "create_doc", todo, ""},
		{"sensitive DocTypes can be read", nil, config.MCPPolicy{}, "get_doc", map[string]interface{}{"doctype": "User", "name": "u@example.com"}, ""},
		{"sensitive DocTypes are not written", nil, config.MCPPolicy{}, "update_doc", user,
			`DocType "User" is sensitive; MCP may read it but not write it unless sites.prod.mcp.allow_doctypes lists it`},
		{"the config can allow a sensitive DocType", &config.MCPPolicy{AllowDoctypes: []string{"user"}}, config.MCPPolicy{}, "update_doc", user, ""},
		{"a flag cannot", nil, config.MCPPolicy{AllowDoctypes: []string{"User"}}, "update_doc", user, "is sensitive"},
		{"deny_doctypes", &config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "get_doc",
			map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}, `DocType "ToDo" is denied by sites.prod.mcp.deny_doctypes`},
		{"check_permission obeys deny_doctypes", &config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "check_permission",
			map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}, `DocType "ToDo" is denied by sites.prod.mcp.deny_doctypes`},
		{"check_permission obeys allow_doctypes", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "check_permission",
			map[string]interface{}{"doctype": "User"}, `DocType "User" is not in sites.prod.mcp.allow_doctypes`},
		{"check_permission of an allowed DocType", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "check_permission",
			map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}, ""},
		{"whoami obeys deny_doctypes for User", &config.MCPPolicy{DenyDoctypes: []string{"User"}}, config.MCPPolicy{}, "whoami",
			map[string]interface{}{}, `DocType "User" is denied by sites.prod.mcp.deny_doctypes`},
		{"whoami obeys --deny-doctypes for User", nil, config.MCPPolicy{DenyDoctypes: []string{"User"}}, "whoami",
			map[string]interface{}{}, `DocType "User" is denied by --deny-doctypes`},
		{"whoami needs User in allow_doctypes", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "whoami",
			map[string]interface{}{}, `DocType "User" is not in sites.prod.mcp.allow_doctypes`},
		{"whoami in read_only", &config.MCPPolicy{ReadOnly: true}, config.MCPPolicy{}, "whoami", map[string]interface{}{}, ""},
		{"--deny-doctypes", nil, config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, "create_doc", todo, "denied by --deny-doctypes"},
		{"allow_doctypes", &config.MCPPolicy{AllowDoctypes: []string{"User"}}, config.MCPPolicy{}, "create_doc", todo,
			`DocType "ToDo" is not in sites.prod.mcp.allow_doctypes`},
		{"--allow-doctypes narrows the config", &config.MCPPolicy{AllowDoctypes: []string{"ToDo", "Note"}}, config.MCPPolicy{AllowDoctypes: []string{"Note"}},
			"create_doc", todo, "not in --allow-doctypes"},
		{"search names its DocType", &config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "search",
			map[string]interface{}{"text": "a", "doctype": "ToDo"}, `DocType "ToDo" is denied by sites.prod.mcp.deny_doctypes`},
		{"search outside allow_doctypes", &config.MCPPolicy{AllowDoctypes: []string{"User"}}, config.MCPPolicy{}, "search",
			map[string]interface{}{"text": "a", "doctype": "ToDo"}, `DocType "ToDo" is not in sites.prod.mcp.allow_doctypes`},
		{"search of an allowed DocType", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "search",
			map[string]interface{}{"text": "a", "doctype": "ToDo"}, ""},
		{"search is read-only safe", &config.MCPPolicy{ReadOnly: true}, config.MCPPolicy{}, "search",
			map[string]interface{}{"text": "a"}, ""},
		{"read_only", &config.MCPPolicy{ReadOnly: true}, config.MCPPolicy{}, "list_docs", map[string]interface{}{"doctype": "ToDo"}, ""},
		{"run_report through its ref_doctype", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "run_report",
			map[string]interface{}{"report_name": "Users"}, `DocType "User" is not in sites.prod.mcp.allow_doctypes`},
		{"run_report of an allowed DocType", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "run_report",
			map[string]interface{}{"report_name": "Todos"}, ""},
		{"built-in method deny list", nil, config.MCPPolicy{}, "call_method",
			method("frappe.desk.doctype.system_console.system_console.execute_code", nil), "runs code, changes apps or rotates a credential"},
		{"deny list prefix", nil, config.MCPPolicy{}, "call_method",
			method("frappe.integrations.frappe_providers.cloud_settings.install_app", nil), "sites.prod.mcp.allow_methods lists it"},
		{"the config can allow a denied method", &config.MCPPolicy{AllowMethods: []string{"frappe.core.doctype.user.user.generate_keys"}}, config.MCPPolicy{},
			"call_method", method("frappe.core.doctype.user.user.generate_keys", nil), ""},
		{"a flag cannot", nil, config.MCPPolicy{AllowMethods: []string{"frappe.core.doctype.user.user.*"}},
			"call_method", method("frappe.core.doctype.user.user.generate_keys", nil), "rotates a credential"},
		{"call_method on a sensitive DocType", nil, config.MCPPolicy{}, "call_method",
			method("frappe.client.delete", map[string]interface{}{"doctype": "User", "name": "x"}), `DocType "User" is sensitive`},
		{"call_method with a doc", nil, config.MCPPolicy{}, "call_method",
			method("frappe.client.insert", map[string]interface{}{"doc": map[string]interface{}{"doctype": "Role"}}), `DocType "Role" is sensitive`},
		{"call_method under allow_doctypes", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "call_method",
			method("frappe.client.get_count", map[string]interface{}{"doctype": "ToDo"}), "with allow_doctypes set it may call only the methods in sites.prod.mcp.allow_methods"},
		{"allow_methods", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}, AllowMethods: []string{"frappe.client.get_count"}}, config.MCPPolicy{},
			"call_method", method("frappe.client.get_count", map[string]interface{}{"doctype": "ToDo"}), ""},
		// Review findings: a document as a JSON string, method aliases, flags.
		{"a doc passed as a JSON string", nil, config.MCPPolicy{}, "call_method",
			method("frappe.client.insert", map[string]interface{}{"doc": `{"doctype":"Server Script","script":"x"}`}), `DocType "Server Script" is sensitive`},
		{"args passed as a JSON string", nil, config.MCPPolicy{}, "call_method",
			map[string]interface{}{"method": "frappe.client.insert", "args": `{"doc":"{\"doctype\":\"User\"}"}`}, `DocType "User" is sensitive`},
		{"a document method naming no DocType", nil, config.MCPPolicy{}, "call_method",
			method("frappe.client.insert", map[string]interface{}{"doc": "not json"}), "name no DocType"},
		{"run_doc_method resolves to frappe.handler", &config.MCPPolicy{DenyDoctypes: []string{"Note"}}, config.MCPPolicy{}, "call_method",
			method("run_doc_method", map[string]interface{}{"dt": "Note", "dn": "x", "method": "y"}), `DocType "Note" is denied`},
		{"a flag allowlist cannot stand in for the config's", &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, config.MCPPolicy{AllowMethods: []string{"*"}},
			"call_method", method("frappe.client.get_count", map[string]interface{}{"doctype": "ToDo"}), "sites.prod.mcp.allow_methods"},
		{"--allow-doctypes needs a method allowlist", nil, config.MCPPolicy{AllowDoctypes: []string{"ToDo"}},
			"call_method", method("frappe.client.get_count", map[string]interface{}{"doctype": "ToDo"}), "--allow-methods"},
		{"--allow-doctypes with --allow-methods", nil, config.MCPPolicy{AllowDoctypes: []string{"ToDo"}, AllowMethods: []string{"frappe.client.get_count"}},
			"call_method", method("frappe.client.get_count", map[string]interface{}{"doctype": "ToDo"}), ""},
		{"DocType names are trimmed", &config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{}, "get_doc",
			map[string]interface{}{"doctype": "ToDo ", "name": "TD-1"}, `DocType "ToDo" is denied`},
		{"invisible characters in a DocType", nil, config.MCPPolicy{}, "get_doc",
			map[string]interface{}{"doctype": "Us\u200ber", "name": "x"}, "invisible characters"},
		// Second review: Frappe cuts a method at "/" and runs undotted names
		// as Server Script APIs first.
		{"a method cut at a slash", nil, config.MCPPolicy{}, "call_method",
			method("frappe.desk.doctype.system_console.system_console.execute_code/x", nil), "may only contain"},
		{"an undotted name on a deny list", &config.MCPPolicy{DenyMethods: []string{"wipe_all"}}, config.MCPPolicy{}, "call_method",
			method("wipe_all", nil), "denied by sites.prod.mcp.deny_methods"},
		{"an undotted name on an allow list", &config.MCPPolicy{AllowMethods: []string{"my_api"}}, config.MCPPolicy{}, "call_method",
			method("my_api", nil), ""},
		{"a frappe.client method without a DocType that is safe", nil, config.MCPPolicy{}, "call_method",
			method("frappe.client.get_time_zone", nil), ""},
		{"deny_methods", &config.MCPPolicy{DenyMethods: []string{"frappe.client.*"}}, config.MCPPolicy{}, "call_method",
			method("frappe.client.get_count", map[string]interface{}{"doctype": "ToDo"}), "denied by sites.prod.mcp.deny_methods"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, site, _, _ := mcpTPolicy(t, c.cfg, c.flags)
			before := len(site.Requests())
			res := callTool(t, s, c.tool, c.args)
			msg := resultText(t, res)
			if c.want == "" {
				if strings.Contains(msg, "policy:") {
					t.Fatalf("refused: %s", msg)
				}
				return
			}
			if !res.IsError || !strings.Contains(msg, "policy: ") || !strings.Contains(msg, c.want) {
				t.Fatalf("result %q, want a policy error with %q", msg, c.want)
			}
			// A refusal sends no request, except run_report's ref_doctype read.
			for _, r := range site.Requests()[before:] {
				if r.Path != "/api/resource/Report/Users" {
					t.Errorf("refused call sent %s %s", r.Method, r.Path)
				}
			}
		})
	}
}

func TestMCPPolicyToolList(t *testing.T) {
	names := func(cfg *config.MCPPolicy, flags config.MCPPolicy) []string {
		s, _, _, _ := mcpTPolicy(t, cfg, flags)
		var out []string
		for n := range s.ListTools() {
			out = append(out, n)
		}
		return out
	}
	for _, n := range names(&config.MCPPolicy{ReadOnly: true}, config.MCPPolicy{}) {
		if toolActions[n] != actRead {
			t.Errorf("read_only exposes %s", n)
		}
	}
	got := names(&config.MCPPolicy{AllowTools: []string{"ping", "get_doc", "list_docs"}}, config.MCPPolicy{AllowTools: []string{"get_doc", "list_docs", "delete_doc"}})
	if len(got) != 2 || !contains(got, "get_doc", false) || !contains(got, "list_docs", false) {
		t.Errorf("allow_tools ∩ --allow-tools = %v", got)
	}
}

// TestMCPPolicyReadAtEveryCall: a config edit narrows a running server.
func TestMCPPolicyReadAtEveryCall(t *testing.T) {
	s, site, sc, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	sc.MCP = &config.MCPPolicy{ReadOnly: true}
	mcpTErr(t, s, "create_doc", map[string]interface{}{"doctype": "ToDo", "data": map[string]interface{}{"description": "x"}}, "MCP is read-only")
	if site.Count("ToDo") != 1 {
		t.Error("the write went through")
	}
}

func TestMCPAudit(t *testing.T) {
	s, site, _, path := mcpTPolicy(t, nil, config.MCPPolicy{})
	site.HandleMethod("ffc.test.echo", func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Validation(fmt.Sprintf("Value %v is not valid", args["api_secret"]))
	})
	names := make([]interface{}, 25)
	for i := range names {
		names[i] = fmt.Sprintf("TD-%d", i+100)
	}
	mcpTOK(t, s, "create_doc", map[string]interface{}{"doctype": "ToDo", "data": map[string]interface{}{"description": "secret plan", "new_password": "hunter2-pw"}})
	callTool(t, s, "update_doc", map[string]interface{}{"doctype": "User", "name": "u@example.com", "data": map[string]interface{}{"enabled": 0}})
	callTool(t, s, "get_doc", map[string]interface{}{"name": "x"})
	callTool(t, s, "call_method", map[string]interface{}{"method": "ffc.test.echo", "args": map[string]interface{}{"api_secret": "s3cret-value", "token": "tok-value"}})
	callTool(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": names})
	callTool(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "filters": map[string]interface{}{"password": "pw-in-filter"}})
	callTool(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "filters": []interface{}{[]interface{}{"api_key", "=", "key-in-list-filter"}}})
	callTool(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": 42})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2-pw", "s3cret-value", "tok-value", "pw-in-filter", "key-in-list-filter", "secret plan", frappetest.APISecret} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("audit log contains %q:\n%s", secret, raw)
		}
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	var recs []auditRecord
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		var r auditRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		recs = append(recs, r)
	}
	want := []struct{ tool, status string }{
		{"create_doc", auditOK}, {"update_doc", auditDenied}, {"get_doc", auditInvalid},
		{"call_method", auditError}, {"bulk_delete", auditOK}, {"list_docs", auditError}, // the fake refuses the field
		{"list_docs", auditError}, {"get_doc", auditError},
	}
	if len(recs) != len(want) {
		t.Fatalf("%d lines:\n%s", len(recs), raw)
	}
	for i, w := range want {
		r := recs[i]
		if r.Tool != w.tool || r.Status != w.status || r.Site != "prod" || r.Time.IsZero() {
			t.Errorf("line %d = %+v, want %s %s", i, r, w.tool, w.status)
		}
	}
	if d := recs[0]; len(d.Doctypes) != 1 || d.Doctypes[0] != "ToDo" {
		t.Errorf("create_doc line = %+v", d)
	}
	if args, _ := recs[0].Args.(map[string]interface{}); args["data"] == nil || strings.Contains(fmt.Sprint(args["data"]), "description:") {
		t.Errorf("data should be reduced to its keys: %v", args)
	}
	if !strings.Contains(recs[1].Error, "is sensitive") {
		t.Errorf("denied line error = %q", recs[1].Error)
	}
	if b := recs[4]; len(b.Names) != auditMaxNames || b.NamesTotal != 25 {
		t.Errorf("bulk_delete names = %d of %d", len(b.Names), b.NamesTotal)
	}
	if n := recs[7].Names; len(n) != 1 || n[0] != "42" {
		t.Errorf("numeric name = %v", n)
	}
}

func TestMCPAuditRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	path := filepath.Join(dir, auditFileName)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	(&auditLog{path: path}).write(auditRecord{Tool: "ping", Status: auditOK}, nil)
	if _, err := os.Stat(target); err == nil {
		t.Error("the audit log followed a symlink")
	}
}

func TestMCPFlagsNeedValues(t *testing.T) {
	s := cmdTSite(t)
	r := runFFC(t, fakeConfig(t, s, "apikey"), "", "mcp", "--allow-doctypes=")
	if r.Code != exitUsage || !strings.Contains(r.Stderr, "--allow-doctypes needs at least one value") {
		t.Errorf("exit %d: %s", r.Code, r.Stderr)
	}
}

func TestMCPAuditRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), auditFileName)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(auditMaxBytes); err != nil {
		t.Fatal(err)
	}
	f.Close()
	l := &auditLog{path: path}
	l.write(auditRecord{Tool: "ping", Status: auditOK}, nil)
	if fi, err := os.Stat(path + ".1"); err != nil || fi.Size() != auditMaxBytes {
		t.Fatalf("rotated file: %v, %v", fi, err)
	}
	if raw, _ := os.ReadFile(path); strings.Count(string(raw), "\n") != 1 {
		t.Errorf("new log = %q", raw)
	}
}

func TestRedactArgsCopies(t *testing.T) {
	args := map[string]interface{}{"data": map[string]interface{}{"password": "p"}}
	red := client.RedactArgs(args).(map[string]interface{})
	if args["data"].(map[string]interface{})["password"] != "p" {
		t.Error("RedactArgs changed its input")
	}
	if red["data"].(map[string]interface{})["password"] != "***" {
		t.Errorf("redacted = %v", red)
	}
}

func TestDaemonArgsCarryThePolicy(t *testing.T) {
	prevFlags, prevRO := mcpFlags, mcpReadOnly
	t.Cleanup(func() { mcpFlags, mcpReadOnly = prevFlags, prevRO })
	mcpReadOnly = true
	mcpFlags = config.MCPPolicy{AllowDoctypes: []string{"Sales Invoice", "ToDo"}, DenyMethods: []string{"frappe.client.*"}}
	got := strings.Join(daemonArgs([]string{"prod"}, 8765), " ")
	want := "mcp --port 8765 --site prod --read-only --allow-doctypes=Sales Invoice --allow-doctypes=ToDo --deny-methods=frappe.client.*"
	if got != want {
		t.Errorf("args = %q\nwant   %q", got, want)
	}
}
