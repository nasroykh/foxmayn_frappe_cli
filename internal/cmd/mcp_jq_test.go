package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func TestMCPJQ(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	site.Add("ToDo",
		map[string]interface{}{"name": "TD-1", "status": "Open"},
		map[string]interface{}{"name": "TD-2", "status": "Closed"},
	)
	args := func(jq string) map[string]interface{} {
		return map[string]interface{}{"doctype": "ToDo", "fields": []string{"name", "status"}, "order_by": "name asc", "jq": jq}
	}

	// One output is returned as is.
	if got := mcpTOK(t, s, "list_docs", args(`[.[] | select(.status == "Open") | .name]`)); got != `["TD-1"]` {
		t.Errorf("one output = %s", got)
	}
	if got := mcpTOK(t, s, "list_docs", args(`length`)); got != `2` {
		t.Errorf("length = %s", got)
	}
	// Several outputs, or none, come back as an array.
	if got := mcpTOK(t, s, "list_docs", args(`.[].name`)); got != `["TD-1","TD-2"]` {
		t.Errorf("several outputs = %s", got)
	}
	if got := mcpTOK(t, s, "list_docs", args(`.[] | select(.status == "Gone")`)); got != `[]` {
		t.Errorf("no output = %s", got)
	}
	// An empty filter is no filter.
	if got := mcpTOK(t, s, "list_docs", args(``)); !strings.Contains(got, `"TD-2"`) {
		t.Errorf("empty jq = %s", got)
	}

	// Errors: a bad query is refused before any request; a runtime error,
	// a tool without the parameter, the environment and input are refused.
	n := len(site.Requests())
	mcpTErr(t, s, "list_docs", args(`.[`), "jq:")
	if got := len(site.Requests()); got != n {
		t.Errorf("a bad query sent %d requests", got-n)
	}
	mcpTErr(t, s, "list_docs", args(`.[0] | keys | .foo`), "jq:")
	mcpTErr(t, s, "count_docs", map[string]interface{}{"doctype": "ToDo", "jq": "."}, "count_docs does not take a jq filter")
	mcpTErr(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "jq": 3}, "jq: expected a string")
	t.Setenv("FFC_JQ_SECRET", "leak")
	if got := mcpTOK(t, s, "list_docs", args(`$ENV.FFC_JQ_SECRET`)); got != `null` {
		t.Errorf("$ENV = %s, want null", got)
	}
	mcpTErr(t, s, "list_docs", args(`input`), "jq:")

	// An endless query is stopped.
	prev := jqTimeout
	jqTimeout = 200 * time.Millisecond
	t.Cleanup(func() { jqTimeout = prev })
	start := time.Now()
	mcpTErr(t, s, "list_docs", args(`[range(1e12)]`), "jq: stopped after")
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
		if has != toolSurface[name].big {
			t.Errorf("%s: jq declared %v, big %v", name, has, toolSurface[name].big)
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
