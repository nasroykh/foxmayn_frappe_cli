package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// searchTSite has customers that search_link finds by name or search field,
// and three DocTypes in the global search, one of them unsearchable.
func searchTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	s.Add("Customer",
		map[string]interface{}{"name": "CUST-0001", "customer_name": "Acme Corp", "territory": "Algeria"},
		map[string]interface{}{"name": "CUST-0002", "customer_name": "Beta Acme", "territory": "France"},
		map[string]interface{}{"name": "CUST-0003", "customer_name": "Gamma", "territory": "France"},
	)
	s.SearchFields("Customer", "customer_name", "territory")
	s.Add("Item", map[string]interface{}{"name": "ITEM-1", "item_name": "Acme anvil"})
	s.GlobalSearch("Customer", "customer_name")
	s.GlobalSearch("Item", "item_name")
	s.Add("Note", map[string]interface{}{"name": "N-1", "title": "acme secret"}) // not in the index
	return s
}

func TestCmdSearchLink(t *testing.T) {
	s := searchTSite(t)

	r := cmdTOK(t, cmdTRun(t, s, "--json", "search", "acme", "-d", "Customer"))
	rows := cmdTRows(t, r)
	if len(rows) != 2 || rows[0]["value"] != "CUST-0001" || rows[1]["value"] != "CUST-0002" {
		t.Fatalf("rows %v", rows)
	}
	if rows[0]["description"] != "Acme Corp, Algeria" || rows[0]["label"] != "CUST-0001" {
		t.Errorf("row %v", rows[0])
	}
	req := s.RequestsTo("GET", "/api/method/frappe.desk.search.search_link")
	if len(req) != 1 || req[0].Query.Get("doctype") != "Customer" || req[0].Query.Get("txt") != "acme" || req[0].Query.Get("page_length") != "20" {
		t.Fatalf("request %+v", req)
	}

	// Several words are one text; --limit is the page length.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "search", "Acme", "Corp", "-d", "Customer", "--limit", "1"))
	if rows = cmdTRows(t, r); len(rows) != 1 || rows[0]["value"] != "CUST-0001" {
		t.Fatalf("rows %v", rows)
	}
	req = s.RequestsTo("GET", "/api/method/frappe.desk.search.search_link")
	if last := req[len(req)-1]; last.Query.Get("txt") != "Acme Corp" || last.Query.Get("page_length") != "1" {
		t.Fatalf("request %+v", last.Query)
	}

	// An empty text lists the DocType.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "search", "", "-d", "Customer"))
	if rows = cmdTRows(t, r); len(rows) != 3 {
		t.Fatalf("empty text: %v", rows)
	}

	// No hit is an empty list, not an error.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "search", "zzz", "-d", "Customer"))
	if got := strings.TrimSpace(r.Stdout); got != "[]" {
		t.Fatalf("stdout %q", got)
	}
}

func TestCmdSearchTable(t *testing.T) {
	s := searchTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "search", "acme", "-d", "Customer"))
	cmdTHas(t, r.Stdout, "VALUE", "DESCRIPTION", "CUST-0001", "Acme Corp, Algeria")
	if strings.Contains(r.Stdout, "LABEL") {
		t.Errorf("a label that repeats the value should not be a column:\n%s", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "search", "acme"))
	cmdTHas(t, r.Stdout, "DOCTYPE", "NAME", "CONTENT", "Customer", "Item", "ITEM-1")
}

func TestCmdSearchGlobal(t *testing.T) {
	s := searchTSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "--json", "search", "acme"))
	rows := cmdTRows(t, r)
	var got []string
	for _, row := range rows {
		got = append(got, fmt.Sprint(row["doctype"], "/", row["name"]))
		if row["rank"] == nil || row["content"] == nil {
			t.Errorf("row %v lacks rank or content", row)
		}
	}
	cmdTEq(t, got, "Customer/CUST-0001", "Customer/CUST-0002", "Item/ITEM-1")
	if reqs := s.RequestsTo("GET", "/api/method/frappe.utils.global_search.search"); len(reqs) != 1 ||
		reqs[0].Query.Get("text") != "acme" || reqs[0].Query.Get("limit") != "20" {
		t.Fatalf("request %+v", reqs)
	}
	if strings.Contains(r.Stdout, "N-1") {
		t.Error("a DocType outside Global Search Settings was found")
	}

	r = cmdTOK(t, cmdTRun(t, s, "--json", "search", "acme", "--limit", "1"))
	if rows = cmdTRows(t, r); len(rows) != 1 {
		t.Fatalf("limit: %v", rows)
	}

	// jq and ndjson go through render like every list command.
	r = cmdTOK(t, cmdTRun(t, s, "search", "acme", "--jq", ".[].name"))
	cmdTHas(t, r.Stdout, "CUST-0001", "ITEM-1")
}

func TestCmdSearchErrors(t *testing.T) {
	s := searchTSite(t)
	cfg := fakeConfig(t, s, "apikey")

	r := cmdTExec(t, cfg, "", "search")
	cmdTFail(t, r, "requires at least 1 arg")
	if r.Code != 2 {
		t.Errorf("no text: exit %d, want 2", r.Code)
	}
	r = cmdTExec(t, cfg, "", "search", "")
	cmdTFail(t, r, "search text is empty")
	if r.Code != 2 {
		t.Errorf("empty global text: exit %d, want 2", r.Code)
	}
	for _, n := range []string{"0", "-3"} {
		r = cmdTExec(t, cfg, "", "search", "x", "--limit", n)
		cmdTFail(t, r, "--limit must be at least 1")
		if r.Code != 2 {
			t.Errorf("--limit %s: exit %d, want 2", n, r.Code)
		}
	}
	if n := len(s.RequestsTo("GET", "/api/method/frappe.utils.global_search.search")); n != 0 {
		t.Errorf("invalid input sent %d requests", n)
	}

	r = cmdTExec(t, cfg, "", "search", "x", "-d", "Nope")
	cmdTFail(t, r, "Nope")
	if r.Code != 4 {
		t.Errorf("unknown DocType: exit %d, want 4", r.Code)
	}

	s.HandleMethod("frappe.utils.global_search.search", func(_ *http.Request, _ map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Permission("no")
	})
	r = cmdTExec(t, cfg, "", "search", "x")
	if r.Code != 5 {
		t.Errorf("permission: exit %d, want 5", r.Code)
	}
}

func TestMCPSearch(t *testing.T) {
	s, site := newMCPFake(t, true)
	site.Add("Customer",
		map[string]interface{}{"name": "CUST-0001", "customer_name": "Acme Corp"},
		map[string]interface{}{"name": "CUST-0002", "customer_name": "Beta"})
	site.SearchFields("Customer", "customer_name")
	site.GlobalSearch("Customer", "customer_name")

	rows := mcpTRows(t, mcpTOK(t, s, "search", map[string]interface{}{"text": "acme", "doctype": "Customer"}))
	if len(rows) != 1 || rows[0]["value"] != "CUST-0001" || rows[0]["description"] != "Acme Corp" {
		t.Fatalf("link rows %v", rows)
	}
	rows, hidden := mcpTGlobal(t, mcpTOK(t, s, "search", map[string]interface{}{"text": "acme"}))
	if len(rows) != 1 || rows[0]["doctype"] != "Customer" || rows[0]["name"] != "CUST-0001" || hidden != 0 {
		t.Fatalf("global rows %v, hidden %d", rows, hidden)
	}
	// Each "&" phrase returns up to limit hits; the combined list is cut to it.
	if rows, _ = mcpTGlobal(t, mcpTOK(t, s, "search", map[string]interface{}{"text": "acme & beta", "limit": 1})); len(rows) != 1 {
		t.Errorf("two phrases with limit 1: %v", rows)
	}
	// An empty list is [], not null; an empty text lists a DocType.
	if got := mcpTOK(t, s, "search", map[string]interface{}{"text": "zzz"}); !strings.Contains(got, `"results":[]`) {
		t.Errorf("no hit = %q", got)
	}
	if rows = mcpTRows(t, mcpTOK(t, s, "search", map[string]interface{}{"text": "", "doctype": "Customer", "limit": "1"})); len(rows) != 1 {
		t.Errorf("empty text with a DocType: %v", rows)
	}

	mcpTErr(t, s, "search", map[string]interface{}{"text": ""}, "search text is empty")
	mcpTErr(t, s, "search", map[string]interface{}{}, `"text"`)
	mcpTErr(t, s, "search", map[string]interface{}{"text": "a", "limit": 0}, "--limit must be at least 1")
	mcpTErr(t, s, "search", map[string]interface{}{"text": "a", "limit": 101}, "limit: at most 100")
	mcpTErr(t, s, "search", map[string]interface{}{"text": "a", "doctype": []string{"x"}}, "doctype: expected a string")
	mcpTErr(t, s, "search", map[string]interface{}{"text": "a", "doctype": "Nope"}, "Nope")
	mcpTErr(t, s, "search", map[string]interface{}{"text": "a&b&c&d&e&f"}, "6 phrases")
}

// mcpTGlobal decodes the result of a global search.
func mcpTGlobal(t *testing.T, text string) ([]map[string]interface{}, int) {
	t.Helper()
	var m struct {
		Results []map[string]interface{} `json:"results"`
		Hidden  *int                     `json:"hidden_by_policy"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil || m.Results == nil || m.Hidden == nil {
		t.Fatalf("not a global search result (%v): %.200s", err, text)
	}
	return m.Results, *m.Hidden
}

func TestSearchPhrases(t *testing.T) {
	for text, want := range map[string]int{"a": 1, "a & b": 2, "a&a": 1, " & &": 0, "a & a": 2, "a&b&c&d&e": 5} {
		if got := searchPhrases(text); got != want {
			t.Errorf("searchPhrases(%q) = %d, want %d", text, got, want)
		}
	}
}

// TestMCPSearchPolicyFilter: a global search names no DocType, so the policy
// filters its hits instead of refusing the call.
func TestMCPSearchPolicyFilter(t *testing.T) {
	cases := []struct {
		name  string
		cfg   *config.MCPPolicy
		flags config.MCPPolicy
		want  []string
	}{
		{"no rules", nil, config.MCPPolicy{}, []string{"Customer/C-1", "Employee/E-1", "ToDo/T-1"}},
		{"deny_doctypes", &config.MCPPolicy{DenyDoctypes: []string{"employee"}}, config.MCPPolicy{}, []string{"Customer/C-1", "ToDo/T-1"}},
		{"allow_doctypes", &config.MCPPolicy{AllowDoctypes: []string{"Customer"}}, config.MCPPolicy{}, []string{"Customer/C-1"}},
		{"--deny-doctypes", nil, config.MCPPolicy{DenyDoctypes: []string{"Customer", "ToDo"}}, []string{"Employee/E-1"}},
		{"--allow-doctypes narrows the config", &config.MCPPolicy{AllowDoctypes: []string{"Customer", "ToDo"}},
			config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}, []string{"ToDo/T-1"}},
		{"everything denied", &config.MCPPolicy{AllowDoctypes: []string{"Note"}}, config.MCPPolicy{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, site, _, _ := mcpTPolicy(t, c.cfg, c.flags)
			for dt, name := range map[string]string{"Customer": "C-1", "Employee": "E-1", "ToDo": "T-1"} {
				site.Add(dt, map[string]interface{}{"name": name, "title": "findme"})
				site.GlobalSearch(dt, "title")
			}
			var got []string
			rows, hidden := mcpTGlobal(t, mcpTOK(t, s, "search", map[string]interface{}{"text": "findme"}))
			for _, r := range rows {
				got = append(got, fmt.Sprint(r["doctype"], "/", r["name"]))
			}
			cmdTEq(t, got, c.want...)
			if hidden != 3-len(c.want) {
				t.Errorf("hidden_by_policy = %d, want %d", hidden, 3-len(c.want))
			}
		})
	}
}

func TestFilterDoctypeRows(t *testing.T) {
	p := newMCPPolicy(&config.SiteConfig{Name: "x", MCP: &config.MCPPolicy{AllowDoctypes: []string{"ToDo"}}}, config.MCPPolicy{})
	rows := []map[string]interface{}{
		{"doctype": "ToDo", "name": "1"},
		{"doctype": " todo ", "name": "2"},
		{"doctype": "User", "name": "3"},
		{"name": "4"},
		{"doctype": 7, "name": "5"},
	}
	var got []string
	kept, hidden := p.filterDoctypeRows(rows)
	for _, r := range kept {
		got = append(got, r["name"].(string))
	}
	cmdTEq(t, got, "1", "2")
	if hidden != 3 {
		t.Errorf("hidden = %d, want 3", hidden)
	}
	if out, _ := (mcpPolicy{}).filterDoctypeRows(nil); out == nil || len(out) != 0 {
		t.Errorf("empty input must give an empty, non-nil list, got %#v", out)
	}
}

// policyFrom fails closed: without a stored policy the caller must not fall
// back to the zero policy, which allows everything.
func TestPolicyFrom(t *testing.T) {
	if _, ok := policyFrom(context.Background()); ok {
		t.Error("policyFrom found a policy in an empty context")
	}
	want := newMCPPolicy(&config.SiteConfig{Name: "x", MCP: &config.MCPPolicy{ReadOnly: true}}, config.MCPPolicy{})
	if got, ok := policyFrom(withPolicy(context.Background(), want)); !ok || !got.readOnly() {
		t.Errorf("policyFrom = %v, %v", got, ok)
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("a  b\n\tc", 10); got != "a b c" {
		t.Errorf("got %q", got)
	}
	if got := oneLine("héllo wörld", 6); got != "héllo…" {
		t.Errorf("got %q", got)
	}
}
