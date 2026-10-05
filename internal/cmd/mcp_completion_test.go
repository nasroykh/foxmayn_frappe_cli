package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// mcpTComplete sends completion/complete through the JSON-RPC layer. ref is
// "ref/resource" with a URI or "ref/prompt" with a prompt name.
func mcpTComplete(t *testing.T, s *server.MCPServer, refType, ref, arg, value string, ctxArgs map[string]string) mcp.Completion {
	t.Helper()
	r := map[string]any{"type": refType}
	if refType == "ref/resource" {
		r["uri"] = ref
	} else {
		r["name"] = ref
	}
	params := map[string]any{"ref": r, "argument": map[string]any{"name": arg, "value": value}}
	if ctxArgs != nil {
		params["context"] = map[string]any{"arguments": ctxArgs}
	}
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "completion/complete", "params": params})
	b, _ := json.Marshal(s.HandleMessage(context.Background(), msg))
	var resp struct {
		Result *mcp.CompleteResult `json:"result"`
		Error  json.RawMessage     `json:"error"`
	}
	if err := json.Unmarshal(b, &resp); err != nil || resp.Result == nil {
		t.Fatalf("completion/complete: %s (%v)", b, err)
	}
	return resp.Result.Completion
}

func mcpTValues(t *testing.T, c mcp.Completion, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(c.Values, want) {
		t.Errorf("completion %q, want %q", c.Values, want)
	}
}

// TestMCPCompletionFollowsThePolicy: the schema template's {doctype}, the
// prompts' doctype and report_name complete from the cache, leaving out
// what the site's policy refuses, and nothing is sent to the site.
func TestMCPCompletionFollowsThePolicy(t *testing.T) {
	cacheTEnv(t)
	s, site, sc, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"Note"}}, config.MCPPolicy{})
	_ = writeListCache(sc, "DocType", []map[string]interface{}{{"name": "ToDo"}, {"name": "Note"}, {"name": "User"}, {"name": "Task"}, {"name": "Bad\u202e"}})
	_ = writeListCache(sc, "Report", []map[string]interface{}{
		{"name": "Todos", "ref_doctype": "ToDo"}, {"name": "Notes", "ref_doctype": "Note"}, {"name": "Orphan"}})
	before := len(site.Requests())

	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "doctype", "", nil), "Task", "ToDo", "User")
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "doctype", "t", nil), "Task", "ToDo")
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", docTemplate, "doctype", "U", nil), "User")
	// Document names are never completed.
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", docTemplate, "name", "", map[string]string{"doctype": "ToDo"}))
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", "ffc://sites", "doctype", "", nil))
	// A single-site server has no site to complete but its own.
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "site", "", nil), "prod")

	// A read prompt offers the sensitive User; the import prompt writes and
	// does not.
	mcpTValues(t, mcpTComplete(t, s, "ref/prompt", "inspect-doctype", "doctype", "", nil), "Task", "ToDo", "User")
	mcpTValues(t, mcpTComplete(t, s, "ref/prompt", "safe-bulk-import", "doctype", "", nil), "Task", "ToDo")
	// With DocType rules, a report is offered only when its DocType is known
	// and allowed (as run_report checks it).
	mcpTValues(t, mcpTComplete(t, s, "ref/prompt", "explain-report", "report_name", "", nil), "Todos")

	if n := len(site.Requests()) - before; n != 0 {
		t.Fatalf("completion sent %d requests", n)
	}
}

func TestMCPCompletionWithoutRulesOrCache(t *testing.T) {
	cacheTEnv(t)
	s, _, sc, _ := mcpTPolicy(t, nil, config.MCPPolicy{})
	// No cache: nothing, and no error.
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "doctype", "", nil))
	_ = writeListCache(sc, "Report", []map[string]interface{}{{"name": "Todos", "ref_doctype": "ToDo"}, {"name": "Orphan"}})
	mcpTValues(t, mcpTComplete(t, s, "ref/prompt", "explain-report", "report_name", "", nil), "Orphan", "Todos")

	// At most 100 values, with the total.
	var rows []map[string]interface{}
	for i := 0; i < 150; i++ {
		rows = append(rows, map[string]interface{}{"name": fmt.Sprintf("DT %03d", i)})
	}
	_ = writeListCache(sc, "DocType", rows)
	c := mcpTComplete(t, s, "ref/resource", schemaTemplate, "doctype", "", nil)
	if len(c.Values) != 100 || c.Total != 150 || !c.HasMore {
		t.Errorf("got %d values, total %d, hasMore %v", len(c.Values), c.Total, c.HasMore)
	}
}

// TestMCPCompletionMultiSite: {site} offers the served sites whose policy
// allows the tools, and {doctype} reads the named site's cache.
func TestMCPCompletionMultiSite(t *testing.T) {
	cacheTEnv(t)
	_, _, cfgPath := mcpTSites(t, []string{"prod", "dev"}, false, "")
	s := mcpTStart(t)
	prod, err := config.Load("Prod", cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := config.Load("dev", cfgPath)
	_ = writeListCache(prod, "DocType", []map[string]interface{}{{"name": "Prod Only"}})
	_ = writeListCache(dev, "DocType", []map[string]interface{}{{"name": "Dev Only"}})

	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "site", "", nil), "dev", "Prod")
	// Prod is read-only: the import prompt (bulk_create) cannot run there.
	mcpTValues(t, mcpTComplete(t, s, "ref/prompt", "safe-bulk-import", "site", "", nil), "dev")
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "doctype", "", map[string]string{"site": "Prod"}), "Prod Only")
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "doctype", "", map[string]string{"site": "dev"}), "Dev Only")
	// Without a site there is no site to read.
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "doctype", "", nil))
	mcpTValues(t, mcpTComplete(t, s, "ref/resource", schemaTemplate, "doctype", "", map[string]string{"site": "nope"}))
}
