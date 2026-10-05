package cmd

import (
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// With DocType rules, a query may not reach another DocType through its
// filters, fields or order_by; the four-element filter's DocType is checked.
func TestMCPQueryScope(t *testing.T) {
	deny := &config.MCPPolicy{DenyDoctypes: []string{"User"}}
	allow := &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}
	const other = "filters may not name a field of a linked, child or other table"
	const otherSel = "fields and order_by take only fieldnames of the DocType"
	for _, tc := range []struct {
		name  string
		cfg   *config.MCPPolicy
		flags config.MCPPolicy
		tool  string
		args  map[string]interface{}
		want  string // "" = allowed
	}{
		{"object filter on a link field", deny, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "filters": map[string]interface{}{"allocated_to.enabled": 1}}, other},
		{"list filter with a backtick table", deny, config.MCPPolicy{}, "count_docs",
			map[string]interface{}{"doctype": "ToDo", "filters": []interface{}{[]interface{}{"`tabUser`.`name`", "=", "x"}}}, other},
		{"two-element filter", deny, config.MCPPolicy{}, "count_docs",
			map[string]interface{}{"doctype": "ToDo", "filters": []interface{}{[]interface{}{"owner.enabled", 1}}}, other},
		{"single unwrapped condition", allow, config.MCPPolicy{}, "count_docs",
			map[string]interface{}{"doctype": "ToDo", "filters": []interface{}{"owner.enabled", "=", 1}}, other},
		{"four-element filter names a denied DocType", deny, config.MCPPolicy{}, "aggregate",
			map[string]interface{}{"doctype": "ToDo", "filters": []interface{}{[]interface{}{"User", "enabled", "=", 1}}}, `DocType "User" is denied`},
		{"four-element filter outside allow_doctypes", allow, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "filters": []interface{}{[]interface{}{"User", "enabled", "=", 1}}}, `DocType "User" is not in`},
		{"four-element filter on the DocType itself", allow, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "filters": []interface{}{[]interface{}{"ToDo", "status", "=", "Open"}}}, ""},
		{"nested or-group", deny, config.MCPPolicy{}, "aggregate",
			map[string]interface{}{"doctype": "ToDo", "filters": []interface{}{[]interface{}{
				[]interface{}{"status", "=", "Open"}, "or", []interface{}{"owner.enabled", "=", 1}}}}, other},
		{"JSON-encoded filters", deny, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "filters": `[["owner.enabled","=",1]]`}, other},
		{"a flag rule is enough", nil, config.MCPPolicy{DenyDoctypes: []string{"Note"}}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "filters": map[string]interface{}{"owner.enabled": 1}}, other},
		{"dotted field", deny, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "fields": []interface{}{"name", "owner.full_name"}}, otherSel},
		{"child table field", deny, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "fields": []interface{}{"items.item_code"}}, otherSel},
		{"backtick field", allow, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "fields": `["` + "`tabUser`.`name`" + `"]`}, otherSel},
		{"dotted order_by", deny, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "order_by": "modified desc, owner.full_name asc"}, otherSel},
		{"call_method get_list filters", deny, config.MCPPolicy{}, "call_method",
			map[string]interface{}{"method": "frappe.client.get_list", "args": map[string]interface{}{
				"doctype": "ToDo", "filters": `{"owner.enabled":1}`}}, other},
		{"call_method get_list fields", deny, config.MCPPolicy{}, "call_method",
			map[string]interface{}{"method": "frappe.client.get_list", "args": map[string]interface{}{
				"doctype": "ToDo", "fields": []interface{}{"owner.full_name"}}}, otherSel},
		{"call_method four-element filter", deny, config.MCPPolicy{}, "call_method",
			map[string]interface{}{"method": "frappe.client.get_count", "args": map[string]interface{}{
				"doctype": "ToDo", "filters": []interface{}{[]interface{}{"User", "enabled", "=", 1}}}}, `DocType "User" is denied`},

		{"plain names pass", deny, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "fields": []interface{}{"name", "count(name) as n"}, "order_by": "modified desc",
				"filters": []interface{}{[]interface{}{"status", "=", "Open"}, []interface{}{"description", "like", "%a.b%"}}}, ""},
		{"without DocType rules nothing changes", &config.MCPPolicy{ReadOnly: true}, config.MCPPolicy{}, "list_docs",
			map[string]interface{}{"doctype": "ToDo", "fields": []interface{}{"owner.full_name"}, "filters": map[string]interface{}{"owner.enabled": 1}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, site, _, _ := mcpTPolicy(t, tc.cfg, tc.flags)
			n := len(site.Requests())
			res := callTool(t, s, tc.tool, tc.args)
			msg := resultText(t, res)
			if tc.want == "" {
				if res.IsError && strings.Contains(msg, "policy:") {
					t.Fatalf("refused: %s", msg)
				}
				return
			}
			if !res.IsError || !strings.Contains(msg, tc.want) {
				t.Fatalf("result %q, want an error containing %q", msg, tc.want)
			}
			if len(site.Requests()) != n {
				t.Error("a refused call reached the site")
			}
		})
	}
}
