package cmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func TestMCPJQ(t *testing.T) {
	s, site, _, audit := mcpTPolicy(t, nil, config.MCPPolicy{})
	site.Add("ToDo",
		map[string]interface{}{"name": "TD-1", "status": "Open"},
		map[string]interface{}{"name": "TD-2", "status": "Closed"},
	)
	args := func(jq string) map[string]interface{} {
		return map[string]interface{}{"doctype": "ToDo", "fields": []string{"name", "status"}, "order_by": "name asc", "jq": jq}
	}

	// The output is JSON: one output as is, none or several as an array.
	for jq, want := range map[string]string{
		`[.[] | select(.status == "Open") | .name]`: `["TD-1"]`,
		`length`:                          `2`,
		`.[0].name`:                       `"TD-1"`,
		`.[].name`:                        `["TD-1","TD-2"]`,
		`.[] | select(.status == "Gone")`: `[]`,
		`$ENV | length`:                   `0`,
	} {
		if got := mcpTOK(t, s, "list_docs", args(jq)); got != want {
			t.Errorf("%s = %s, want %s", jq, got, want)
		}
	}
	// An empty filter is no filter.
	if got := mcpTOK(t, s, "list_docs", args(``)); !strings.Contains(got, `"TD-2"`) {
		t.Errorf("empty jq = %s", got)
	}

	// A bad query, or jq on a tool without it, is refused before any
	// request; the audit line still names the site.
	n := len(site.Requests())
	mcpTErr(t, s, "list_docs", args(`.[`), "jq:")
	mcpTErr(t, s, "count_docs", map[string]interface{}{"doctype": "ToDo", "jq": "."}, "count_docs does not take a jq filter")
	mcpTErr(t, s, "call_method", map[string]interface{}{"method": "frappe.ping", "jq": "."}, "call_method does not take a jq filter")
	mcpTErr(t, s, "list_sites", map[string]interface{}{"jq": "."}, "list_sites does not take a jq filter")
	mcpTErr(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "jq": 3}, "jq: expected a string")
	if got := len(site.Requests()); got != n {
		t.Errorf("refused queries sent %d requests", got-n)
	}
	if raw, err := os.ReadFile(audit); err != nil || !strings.Contains(string(raw), `"site":"prod","tool":"list_docs"`) {
		t.Errorf("audit: %v %s", err, raw)
	}
	// Runtime errors, input, and a result over the cap (a string too).
	mcpTErr(t, s, "list_docs", args(`.[0] | keys | .foo`), "jq:")
	mcpTErr(t, s, "list_docs", args(`input`), "jq:")
	mcpTErr(t, s, "list_docs", args(`"a" * 2000000`), "result is too large")

	// A runaway query is stopped by memory or time, in the child.
	mcpTErr(t, s, "list_docs", args(`"a" * 1000000000 | length`), "jq: stopped after using 256 MiB")
	prev := jqTimeout
	jqTimeout = 300 * time.Millisecond
	t.Cleanup(func() { jqTimeout = prev })
	start := time.Now()
	mcpTErr(t, s, "list_docs", args(`last(range(1e15))`), "jq: stopped after 300ms")
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("an endless query ran %s", d)
	}
}

// TestMCPJQSchema checks that exactly the tools with large results declare
// the jq parameter.
func TestMCPJQSchema(t *testing.T) {
	s, _, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	var with []string
	for name, st := range s.ListTools() {
		_, has := st.Tool.InputSchema.Properties[jqParam]
		if has != jqTool(name) {
			t.Errorf("%s: jq declared %v, want %v", name, has, jqTool(name))
		}
		if has {
			with = append(with, name)
		}
	}
	if len(with) == 0 {
		t.Fatal("no tool declares jq")
	}
}

func TestMCPResponseFormat(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})

	// list_docs: without fields, concise is names only, detailed every field.
	if got := mcpTOK(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo"}); strings.Contains(got, "description") {
		t.Errorf("concise = %s", got)
	}
	if got := mcpTOK(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "response_format": "detailed"}); !strings.Contains(got, `"description":"a"`) {
		t.Errorf("detailed = %s", got)
	}
	// With fields, both return those fields.
	if got := mcpTOK(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "fields": []string{"name"}, "response_format": "detailed"}); strings.Contains(got, "description") {
		t.Errorf("detailed with fields = %s", got)
	}
	mcpTErr(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "response_format": "full"}, "is not concise or detailed")

	// run_report: concise drops the execution metadata, detailed keeps it.
	site.AddReport("Todos", map[string]interface{}{
		"columns": []interface{}{"Name"}, "result": []interface{}{[]interface{}{"TD-1"}},
		"execution_time": 0.1, "chart": map[string]interface{}{"type": "bar"},
	})
	if got := mcpTOK(t, s, "run_report", map[string]interface{}{"report_name": "Todos"}); strings.Contains(got, "execution_time") || !strings.Contains(got, "TD-1") {
		t.Errorf("concise report = %s", got)
	}
	if got := mcpTOK(t, s, "run_report", map[string]interface{}{"report_name": "Todos", "response_format": "detailed"}); !strings.Contains(got, "execution_time") || !strings.Contains(got, `"chart"`) {
		t.Errorf("detailed report = %s", got)
	}
}

// TestMCPCacheHints checks the SEP-2549 hints a 2026-07-28 client gets: an
// hour on the lists, which are fixed while the server runs, always
// revalidate on resource reads.
func TestMCPCacheHints(t *testing.T) {
	s, _, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	for _, o := range mcpServerOptions() {
		o(s)
	}
	c := mcpTClient(t, s, nil, false)
	tools, err := c.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if ttl, ok := tools.TTL(); !ok || ttl != listCacheTTLMs || tools.CacheScope != mcp.CacheScopePrivate {
		t.Errorf("tools/list ttl %d (set %v), scope %q", ttl, ok, tools.CacheScope)
	}
	prompts, err := c.ListPrompts(t.Context(), mcp.ListPromptsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if ttl, _ := prompts.TTL(); ttl != listCacheTTLMs {
		t.Errorf("prompts/list ttl %d", ttl)
	}
	read, err := c.ReadResource(t.Context(), mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: "ffc://sites"}})
	if err != nil {
		t.Fatal(err)
	}
	if ttl, ok := read.TTL(); !ok || ttl != 0 {
		t.Errorf("resources/read ttl %d (set %v), want 0", ttl, ok)
	}
}
