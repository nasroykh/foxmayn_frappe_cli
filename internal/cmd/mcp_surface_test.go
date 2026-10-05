package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// mcpTRPC sends one JSON-RPC request through s and returns its result, or
// the error message when the server answered with an error.
func mcpTRPC(t *testing.T, s *server.MCPServer, method string, params interface{}) (json.RawMessage, string) {
	t.Helper()
	msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	b, _ := json.Marshal(s.HandleMessage(context.Background(), msg))
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		return nil, resp.Error.Message
	}
	return resp.Result, ""
}

// mcpTRead reads a resource and returns its text, or the error message.
func mcpTRead(t *testing.T, s *server.MCPServer, uri string) (string, string) {
	t.Helper()
	raw, errMsg := mcpTRPC(t, s, "resources/read", map[string]interface{}{"uri": uri})
	if errMsg != "" {
		return "", errMsg
	}
	var res struct {
		Contents []struct {
			URI, MIMEType, Text string
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) != 1 || res.Contents[0].URI != uri || res.Contents[0].MIMEType != "application/json" {
		t.Fatalf("contents = %+v", res.Contents)
	}
	return res.Contents[0].Text, ""
}

// mcpTAudit returns the audit log's records.
func mcpTAudit(t *testing.T, path string) []auditRecord {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []auditRecord
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r auditRecord
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("audit line %q: %v", l, err)
		}
		out = append(out, r)
	}
	return out
}

// mcpTToolsets registers the tools on a fake site with the given tool sets.
func mcpTToolsets(t *testing.T, sets []string) *server.MCPServer {
	t.Helper()
	site := frappetest.New(t)
	c, err := client.New(context.Background(), &config.SiteConfig{URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	sc := &config.SiteConfig{Name: "test"}
	env := &mcpEnv{
		sites:    []string{"test"},
		site:     func(context.Context, string) (*config.SiteConfig, error) { return sc, nil },
		client:   func(context.Context, *config.SiteConfig) (*client.FrappeClient, error) { return c, nil },
		toolsets: sets,
	}
	s := server.NewMCPServer("test", "0")
	registerTools(s, env, []mcpPolicy{newMCPPolicy(sc, env.flags)})
	return s
}

func TestMCPToolSurface(t *testing.T) {
	s := server.NewMCPServer("t", "0")
	registerAllTools(s, &mcpEnv{})
	tools := s.ListTools()
	for name := range tools {
		info, ok := toolSurface[name]
		switch {
		case !ok:
			t.Errorf("tool %s has no entry in toolSurface (tool set and title)", name)
		case info.toolset != toolsetCore && info.toolset != toolsetLifecycle:
			t.Errorf("tool %s: unknown tool set %q", name, info.toolset)
		case strings.TrimSpace(info.title) == "":
			t.Errorf("tool %s has no title", name)
		}
	}
	for name := range toolSurface {
		if _, ok := tools[name]; !ok {
			t.Errorf("toolSurface lists %s, which is not registered", name)
		}
	}
	var lifecycle []string
	for name, info := range toolSurface {
		if info.toolset == toolsetLifecycle {
			lifecycle = append(lifecycle, name)
		}
	}
	sort.Strings(lifecycle)
	if want := []string{"amend_doc", "apply_workflow", "cancel_doc", "copy_doc", "get_transitions", "rename_doc", "submit_doc"}; !reflect.DeepEqual(lifecycle, want) {
		t.Errorf("lifecycle = %v, want %v", lifecycle, want)
	}

	// As a client sees them: every tool has a title (both fields), and the
	// tools with large results carry the size hint.
	s, _ = newMCPFake(t, false)
	msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	b, _ := json.Marshal(s.HandleMessage(context.Background(), msg))
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string                 `json:"name"`
				Title       string                 `json:"title"`
				Annotations map[string]interface{} `json:"annotations"`
				Meta        map[string]interface{} `json:"_meta"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Result.Tools) != len(toolSurface) {
		t.Errorf("tools/list has %d tools, want %d", len(resp.Result.Tools), len(toolSurface))
	}
	for _, tl := range resp.Result.Tools {
		want := toolSurface[tl.Name].title
		if tl.Title != want || tl.Annotations["title"] != want {
			t.Errorf("%s: title %q, annotation %v, want %q", tl.Name, tl.Title, tl.Annotations["title"], want)
		}
		got, has := tl.Meta[maxResultSizeKey]
		if toolSurface[tl.Name].big != has || has && got != float64(maxToolResultBytes) {
			t.Errorf("%s: _meta %v, big %v", tl.Name, tl.Meta, toolSurface[tl.Name].big)
		}
	}
}

func TestMCPToolsets(t *testing.T) {
	core := []string{"bulk_create", "bulk_delete", "bulk_update", "call_method", "count_docs", "create_doc", "delete_doc",
		"get_doc", "get_schema", "list_docs", "list_doctypes", "list_reports", "list_sites", "ping", "run_report", "search", "update_doc"}
	lifecycle := []string{"amend_doc", "apply_workflow", "cancel_doc", "copy_doc", "get_transitions", "list_sites", "rename_doc", "submit_doc"}
	if got := mcpTToolNames(t, mcpTToolsets(t, []string{"core"})); !reflect.DeepEqual(got, core) {
		t.Errorf("core = %v", got)
	}
	// list_sites stays; no get_doc/get_schema, so no resource templates.
	s := mcpTToolsets(t, []string{"lifecycle"})
	if got := mcpTToolNames(t, s); !reflect.DeepEqual(got, lifecycle) {
		t.Errorf("lifecycle = %v", got)
	}
	if raw, _ := mcpTRPC(t, s, "resources/templates/list", nil); strings.Contains(string(raw), "ffc://") {
		t.Errorf("templates without get_doc/get_schema: %s", raw)
	}
	if n := len(mcpTToolNames(t, mcpTToolsets(t, []string{"core", "lifecycle"}))); n != len(toolSurface) {
		t.Errorf("core,lifecycle = %d tools", n)
	}
	if n := len(mcpTToolNames(t, mcpTToolsets(t, nil))); n != len(toolSurface) {
		t.Errorf("default = %d tools", n)
	}

	// The flag: an unknown or missing name is a usage error.
	site := cmdTSite(t)
	for arg, want := range map[string]string{
		"--toolsets=core,bogus": `unknown tool set "bogus"`,
		"--toolsets=":           "--toolsets needs at least one value",
	} {
		if r := runFFC(t, fakeConfig(t, site, "apikey"), "", "mcp", arg); r.Code != exitUsage || !strings.Contains(r.Stderr, want) {
			t.Errorf("%s: exit %d: %s", arg, r.Code, r.Stderr)
		}
	}
}

func TestDaemonArgsCarryTheToolsets(t *testing.T) {
	prev := mcpToolsets
	t.Cleanup(func() { mcpToolsets = prev })
	mcpToolsets = []string{"lifecycle"}
	got := strings.Join(daemonArgs([]string{"prod"}, 8765), " ")
	if want := "mcp --port 8765 --site prod --toolsets=lifecycle"; got != want {
		t.Errorf("args = %q\nwant   %q", got, want)
	}
}

func mcpTInstructions(t *testing.T, s *server.MCPServer) string {
	t.Helper()
	raw, errMsg := mcpTRPC(t, s, "initialize", map[string]interface{}{
		"protocolVersion": "2025-11-25", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]interface{}{"name": "t", "version": "1"},
	})
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	var res struct {
		Instructions string `json:"instructions"`
		Capabilities struct {
			Resources, Prompts interface{}
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Capabilities.Resources == nil || res.Capabilities.Prompts == nil {
		t.Errorf("capabilities = %s", raw)
	}
	return res.Instructions
}

func TestMCPInstructions(t *testing.T) {
	s, _ := newMCPFake(t, false)
	text := mcpTInstructions(t, s)
	n := strings.Count(text, "\n")
	if n < 15 || n > 25 {
		t.Errorf("instructions have %d lines:\n%s", n, text)
	}
	for _, want := range []string{`one site, "test"`, `{"status":"Open","docstatus":1}`, `[["grand_total",">",1000]`, "like (with %)",
		"get_schema before create_doc", "0 = draft", "submit_doc", "apply_workflow", "search with doctype",
		"fields and a limit", "next_start", "policy:", "ffc://test/doc/{doctype}/{name}", "inspect-doctype"} {
		if !strings.Contains(text, want) {
			t.Errorf("instructions lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "needs a site argument") || strings.Contains(text, "Read-only") {
		t.Errorf("single writable site, but:\n%s", text)
	}

	s, _ = newMCPFake(t, true)
	text = mcpTInstructions(t, s)
	if !strings.Contains(text, "Read-only: only read tools") || strings.Contains(text, "get_schema before create_doc") || strings.Contains(text, "submit_doc (0 to 1)") {
		t.Errorf("read-only instructions:\n%s", text)
	}

	mcpTSites(t, []string{"prod", "dev"}, false, "")
	text = mcpTInstructions(t, mcpTStart(t))
	for _, want := range []string{`serves 2 sites: "dev", "Prod"`, "needs a site argument", "Read-only sites (write tools refuse them): Prod.", "ffc://<site>/schema/{doctype}"} {
		if !strings.Contains(text, want) {
			t.Errorf("multi-site instructions lack %q:\n%s", want, text)
		}
	}
}

func TestMCPResources(t *testing.T) {
	s, site, _, auditPath := mcpTPolicy(t, nil, config.MCPPolicy{})
	mcpTSeedSchema(site)
	site.Add("Sales Invoice", map[string]interface{}{"name": "SI/2026/1", "customer": "Acme"})
	site.Add("Task Item", map[string]interface{}{"name": "a b", "title": "x"})

	raw, _ := mcpTRPC(t, s, "resources/list", nil)
	if !strings.Contains(string(raw), `"uri":"ffc://sites"`) {
		t.Errorf("resources/list = %s", raw)
	}
	raw, _ = mcpTRPC(t, s, "resources/templates/list", nil)
	for _, want := range []string{schemaTemplate, docTemplate} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("templates lack %s: %s", want, raw)
		}
	}

	text, errMsg := mcpTRead(t, s, "ffc://sites")
	if errMsg != "" || !strings.Contains(text, `"name":"prod"`) || strings.Contains(text, "secret") {
		t.Errorf("sites = %s %s", text, errMsg)
	}
	// Spaces and slashes arrive percent-encoded.
	text, errMsg = mcpTRead(t, s, "ffc://prod/doc/Sales%20Invoice/SI%2F2026%2F1")
	if errMsg != "" || mcpTObj(t, text)["customer"] != "Acme" {
		t.Errorf("doc = %s %s", text, errMsg)
	}
	text, errMsg = mcpTRead(t, s, "ffc://prod/doc/Task%20Item/a%20b")
	if errMsg != "" || mcpTObj(t, text)["title"] != "x" {
		t.Errorf("doc with a space = %s %s", text, errMsg)
	}
	text, errMsg = mcpTRead(t, s, "ffc://prod/schema/Task%20Item")
	if errMsg != "" || !reflect.DeepEqual(mcpTFieldNames(t, mcpTObj(t, text)), []string{"title", "extra", "status"}) {
		t.Errorf("schema = %s %s", text, errMsg)
	}
	// The same as the tool.
	if tool := mcpTOK(t, s, "get_schema", map[string]interface{}{"doctype": "Task Item"}); tool != text {
		t.Errorf("schema resource differs from the tool:\n%s\n%s", text, tool)
	}

	for _, tc := range []struct{ uri, want string }{
		{"ffc://other/doc/ToDo/TD-1", `site "other" is not served`},
		{"ffc://prod/doc/ToDo/Nope", "not found"},
		// The URI template itself rejects these, before any handler.
		{"ffc://prod/doc/ToDo/TD-1/extra", "resource not found"},
		{"ffc://prod/doc/ToDo/%zz", "resource not found"},
	} {
		if _, errMsg := mcpTRead(t, s, tc.uri); !strings.Contains(errMsg, tc.want) {
			t.Errorf("%s: error %q lacks %q", tc.uri, errMsg, tc.want)
		}
	}

	// Every read left an audit line naming the tool, marked as a resource.
	recs := mcpTAudit(t, auditPath)
	var reads int
	for _, r := range recs {
		if r.Via == "resource" {
			reads++
		}
	}
	if reads != 6 {
		t.Errorf("resource audit lines = %d, want 6: %+v", reads, recs)
	}
	last := recs[len(recs)-1]
	if last.Tool != "get_doc" || last.Status != auditError || last.Error == "" {
		t.Errorf("last audit = %+v", last)
	}
	first := recs[1] // recs[0] is ffc://sites
	if first.Tool != "get_doc" || first.Status != auditOK || first.Site != "prod" || !reflect.DeepEqual(first.Names, []string{"SI/2026/1"}) || !reflect.DeepEqual(first.Doctypes, []string{"Sales Invoice"}) {
		t.Errorf("doc audit = %+v", first)
	}
	// A tool call has no via.
	if recs[4].Tool != "get_schema" || recs[4].Via != "" {
		t.Errorf("tool audit = %+v", recs[4])
	}
}

func TestMCPResourcesPolicy(t *testing.T) {
	// deny_doctypes refuses the read before anything is sent, and says so in
	// the audit log.
	s, site, _, auditPath := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{})
	before := len(site.Requests())
	if _, errMsg := mcpTRead(t, s, "ffc://prod/doc/ToDo/TD-1"); !strings.Contains(errMsg, `policy: DocType "ToDo" is denied by sites.prod.mcp.deny_doctypes`) {
		t.Errorf("denied read: %q", errMsg)
	}
	if _, errMsg := mcpTRead(t, s, "ffc://prod/schema/ToDo"); !strings.Contains(errMsg, "policy:") {
		t.Errorf("denied schema: %q", errMsg)
	}
	if n := len(site.Requests()) - before; n != 0 {
		t.Errorf("%d requests reached the site", n)
	}
	recs := mcpTAudit(t, auditPath)
	if r := recs[0]; r.Tool != "get_doc" || r.Via != "resource" || r.Status != auditDenied {
		t.Errorf("audit = %+v", r)
	}

	// read_only does not touch reads.
	s, _, _, _ = mcpTPolicy(t, &config.MCPPolicy{ReadOnly: true}, config.MCPPolicy{})
	if _, errMsg := mcpTRead(t, s, "ffc://prod/doc/ToDo/TD-1"); errMsg != "" {
		t.Errorf("read-only read: %s", errMsg)
	}

	// A policy without get_doc registers no doc template, so the read finds
	// no handler.
	s, _, _, _ = mcpTPolicy(t, &config.MCPPolicy{AllowTools: []string{"list_docs"}}, config.MCPPolicy{})
	if _, errMsg := mcpTRead(t, s, "ffc://prod/doc/ToDo/TD-1"); errMsg == "" {
		t.Error("doc template registered without get_doc")
	}
	if _, errMsg := mcpTRead(t, s, "ffc://sites"); errMsg == "" {
		t.Error("sites resource registered without list_sites")
	}
}

func TestMCPResourcesMultiSite(t *testing.T) {
	prod, dev, cfgPath := mcpTSites(t, []string{"prod", "dev"}, false, "")
	s := mcpTStart(t)
	for uri, want := range map[string]string{
		"ffc://dev/doc/ToDo/TD-1":  "",
		"ffc://DEV/doc/ToDo/TD-1":  "", // a unique case-insensitive match, as for the site argument
		"ffc://Prod/doc/ToDo/TD-1": "", // read-only sites can be read
		"ffc://x/doc/ToDo/TD-1":    `site "x" is not served by this MCP server`,
		"ffc:///doc/ToDo/TD-1":     "site is required",
	} {
		_, errMsg := mcpTRead(t, s, uri)
		if want == "" && errMsg != "" || want != "" && !strings.Contains(errMsg, want) {
			t.Errorf("%s: error %q, want %q", uri, errMsg, want)
		}
	}
	mcpTAuth(t, dev, "token "+frappetest.APIKey)
	mcpTAuth(t, prod, "Bearer")
	sites := map[string]int{}
	for _, r := range mcpTAudit(t, filepath.Join(filepath.Dir(cfgPath), auditFileName)) {
		if r.Via == "resource" && r.Status == auditOK {
			sites[r.Site]++
		}
	}
	if !reflect.DeepEqual(sites, map[string]int{"dev": 2, "Prod": 1}) {
		t.Errorf("audited sites = %v", sites)
	}
}

func TestMCPPrompts(t *testing.T) {
	s, site := newMCPFake(t, false)
	raw, _ := mcpTRPC(t, s, "prompts/list", nil)
	for _, name := range []string{"inspect-doctype", "safe-bulk-import", "audit-doc-changes", "explain-report"} {
		if !strings.Contains(string(raw), `"name":"`+name+`"`) {
			t.Errorf("prompts/list lacks %s: %s", name, raw)
		}
	}
	get := func(s *server.MCPServer, name string, args map[string]string) (string, string) {
		raw, errMsg := mcpTRPC(t, s, "prompts/get", map[string]interface{}{"name": name, "arguments": args})
		if errMsg != "" {
			return "", errMsg
		}
		var res struct {
			Messages []struct {
				Role    string
				Content struct{ Type, Text string }
			}
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Messages) != 1 || res.Messages[0].Role != "user" || res.Messages[0].Content.Type != "text" {
			t.Fatalf("messages = %s", raw)
		}
		return res.Messages[0].Content.Text, ""
	}
	cases := map[string]struct {
		args map[string]string
		want []string
	}{
		"inspect-doctype":   {map[string]string{"doctype": "Sales Invoice"}, []string{`get_schema doctype="Sales Invoice"`, "count_docs", "list_docs"}},
		"safe-bulk-import":  {map[string]string{"doctype": "Item", "source": "items.csv, 300 rows"}, []string{`"items.csv, 300 rows"`, "bulk_create", "200 items", "search doctype="}},
		"audit-doc-changes": {map[string]string{"doctype": "ToDo", "name": "TD-1"}, []string{`"ref_doctype":"ToDo","docname":"TD-1"`, "Version", "Comment"}},
		"explain-report":    {map[string]string{"report_name": "General Ledger"}, []string{`run_report report_name="General Ledger"`, "columns"}},
	}
	for name, tc := range cases {
		text, errMsg := get(s, name, tc.args)
		if errMsg != "" {
			t.Errorf("%s: %s", name, errMsg)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s lacks %q:\n%s", name, w, text)
			}
		}
	}
	if _, errMsg := get(s, "inspect-doctype", nil); !strings.Contains(errMsg, "argument doctype is required") {
		t.Errorf("missing argument: %q", errMsg)
	}
	if n := len(site.Requests()); n != 0 {
		t.Errorf("prompts sent %d requests", n)
	}

	// Read-only: no import prompt (bulk_create is not registered).
	s, _ = newMCPFake(t, true)
	if raw, _ := mcpTRPC(t, s, "prompts/list", nil); strings.Contains(string(raw), "safe-bulk-import") || !strings.Contains(string(raw), "inspect-doctype") {
		t.Errorf("read-only prompts = %s", raw)
	}

	// Several sites: site is a required argument and must be served.
	mcpTSites(t, []string{"prod", "dev"}, false, "")
	s = mcpTStart(t)
	if _, errMsg := get(s, "inspect-doctype", map[string]string{"doctype": "ToDo"}); !strings.Contains(errMsg, "site is required") {
		t.Errorf("no site: %q", errMsg)
	}
	if _, errMsg := get(s, "inspect-doctype", map[string]string{"doctype": "ToDo", "site": "x"}); !strings.Contains(errMsg, "not served") {
		t.Errorf("bad site: %q", errMsg)
	}
	if text, errMsg := get(s, "inspect-doctype", map[string]string{"doctype": "ToDo", "site": "prod"}); !strings.Contains(text, `pass site="Prod" to every tool call`) {
		t.Errorf("site prompt = %q %q", text, errMsg)
	}
}

func TestMCPListDocsTruncates(t *testing.T) {
	s, site := newMCPFake(t, false)
	blob := strings.Repeat("x", 10<<10)
	for i := 0; i < 120; i++ {
		site.Add("Note", map[string]interface{}{"name": fmt.Sprintf("N%03d", i), "content": blob})
	}
	args := map[string]interface{}{"doctype": "Note", "limit": 0, "fields": []interface{}{"name", "content"}, "order_by": "name asc"}
	var names []string
	for page := 0; ; page++ {
		res := callTool(t, s, "list_docs", args)
		text := resultText(t, res)
		if res.IsError || len(text) > maxToolResultBytes {
			t.Fatalf("page %d: error %v, %d bytes", page, res.IsError, len(text))
		}
		if strings.HasPrefix(text, "[") { // the rest fits: a plain list, as before
			names = append(names, mcpTNames(mcpTRows(t, text))...)
			break
		}
		m := mcpTObj(t, text)
		rows := m["data"].([]interface{})
		if m["truncated"] != true || len(rows) == 0 || !strings.Contains(m["hint"].(string), "start=") {
			t.Fatalf("page %d = %v %v", page, m["truncated"], m["hint"])
		}
		// It is the largest prefix: one more row would not fit.
		if len(text)+len(blob) < maxToolResultBytes-200 {
			t.Errorf("page %d: %d bytes, room for another row", page, len(text))
		}
		for _, r := range rows {
			names = append(names, r.(map[string]interface{})["name"].(string))
		}
		next := m["next_start"].(float64)
		if int(next) != len(names) {
			t.Fatalf("next_start = %v after %d rows", next, len(names))
		}
		args["start"] = next
	}
	if len(names) != 120 || names[0] != "N000" || names[119] != "N119" {
		t.Errorf("paged %d names: %v…", len(names), names[:3])
	}
	for i, n := range names {
		if n != fmt.Sprintf("N%03d", i) {
			t.Fatalf("name %d = %s: rows lost or repeated", i, n)
		}
	}
}

func TestMCPRunReportTruncates(t *testing.T) {
	s, site := newMCPFake(t, false)
	rep := mcpTReport(3000)
	for _, r := range rep["result"].([]interface{}) {
		r.(map[string]interface{})["note"] = strings.Repeat("y", 400)
	}
	site.AddReport("Big", rep)
	res := callTool(t, s, "run_report", map[string]interface{}{"report_name": "Big", "limit": 0})
	text := resultText(t, res)
	if res.IsError || len(text) > maxToolResultBytes {
		t.Fatalf("error %v, %d bytes", res.IsError, len(text))
	}
	m := mcpTObj(t, text)
	kept := len(m["result"].([]interface{}))
	if kept == 0 || kept >= 3000 || m["truncated"] != true || m["total_rows"] != 3000.0 || m["columns"] == nil {
		t.Fatalf("kept %d, %v", kept, m["total_rows"])
	}
	if want := fmt.Sprintf("%d of 3000 rows did not fit", 3000-kept); !strings.Contains(m["hint"].(string), want) {
		t.Errorf("hint %q lacks %q", m["hint"], want)
	}
	// Cut by limit first: total_rows stays the report's total.
	m = mcpTObj(t, mcpTOK(t, s, "run_report", map[string]interface{}{"report_name": "Big", "limit": 2000}))
	if m["total_rows"] != 3000.0 || !strings.Contains(m["hint"].(string), "of 3000 rows") {
		t.Errorf("limit 2000: total %v hint %v", m["total_rows"], m["hint"])
	}
}

func TestMCPStructuredContent(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	res := callTool(t, s, "count_docs", map[string]interface{}{"doctype": "ToDo", "filters": map[string]interface{}{"status": "Open"}})
	// The text is unchanged.
	if got := resultText(t, res); got != `{"count":2,"doctype":"ToDo"}` {
		t.Errorf("text = %s", got)
	}
	if sc, _ := res.StructuredContent.(map[string]interface{}); sc["count"] != 2.0 || sc["doctype"] != "ToDo" {
		t.Errorf("structured = %#v", res.StructuredContent)
	}

	res = callTool(t, s, "list_sites", nil)
	rows := mcpTRows(t, resultText(t, res))
	sc, _ := res.StructuredContent.(map[string]interface{})
	if len(rows) != 1 || rows[0]["name"] != "test" || sc == nil || len(sc["sites"].([]interface{})) != 1 {
		t.Errorf("list_sites = %v, structured %#v", rows, res.StructuredContent)
	}

	// Both declare an output schema, and the structured content matches it.
	for _, name := range []string{"count_docs", "list_sites"} {
		tool := s.ListTools()[name].Tool
		var schema map[string]interface{}
		if err := json.Unmarshal(tool.RawOutputSchema, &schema); err != nil || schema["type"] != "object" {
			t.Errorf("%s: output schema %s", name, tool.RawOutputSchema)
		}
	}
	vs := server.NewMCPServer("v", "0", server.WithOutputSchemaValidation())
	mcpTRegister(vs, nil, &config.SiteConfig{Name: "test"}, nil)
	if res := callTool(t, vs, "list_sites", nil); res.IsError {
		t.Errorf("list_sites fails its own schema: %s", resultText(t, res))
	}
}

func TestMCPBulkProgress(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddDocType("ToDo")
	c := mcpTClient(t, s, nil, false)
	var (
		mu    sync.Mutex
		got   []string
		total string
		token interface{}
	)
	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method != string(mcp.MethodNotificationProgress) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		f := n.Params.AdditionalFields
		got = append(got, fmt.Sprint(f["progress"]))
		total, token = fmt.Sprint(f["total"]), f["progressToken"]
	})
	req := mcp.CallToolRequest{}
	req.Params.Name = "bulk_create"
	req.Params.Arguments = map[string]interface{}{"doctype": "ToDo", "data": []interface{}{
		map[string]interface{}{"description": "a"}, map[string]interface{}{"description": "b"}, map[string]interface{}{"description": "c"}}}
	req.Params.Meta = &mcp.Meta{ProgressToken: "bulk-1"}
	if res, err := c.CallTool(t.Context(), req); err != nil || res.IsError {
		t.Fatalf("bulk_create: %v %+v", err, res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(got, []string{"1", "2", "3"}) || total != "3" || token != "bulk-1" {
		t.Errorf("progress = %v total %v token %v", got, total, token)
	}

	// Without a token, none are sent.
	got = nil
	mu.Unlock()
	req.Params.Meta = nil
	if _, err := c.CallTool(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if len(got) != 0 {
		t.Errorf("progress without a token: %v", got)
	}
}

func TestMCPBulkCancel(t *testing.T) {
	s, site := newMCPFake(t, false)
	for i := 1; i <= 5; i++ {
		site.Add("ToDo", map[string]interface{}{"name": fmt.Sprintf("TD-%d", i)})
	}
	c := mcpTClient(t, s, nil, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// The second delete cancels the call, as a client's cancellation would.
	site.Handle("DELETE /api/resource/ToDo/TD-2", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	req := mcp.CallToolRequest{}
	req.Params.Name = "bulk_delete"
	req.Params.Arguments = map[string]interface{}{"doctype": "ToDo", "names": []interface{}{"TD-1", "TD-2", "TD-3", "TD-4", "TD-5"}}
	_, _ = c.CallTool(ctx, req)
	var deleted []string
	for _, r := range site.Requests() {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, strings.TrimPrefix(r.Path, "/api/resource/ToDo/"))
		}
	}
	if !reflect.DeepEqual(deleted, []string{"TD-1", "TD-2"}) {
		t.Errorf("deletes after cancel = %v, want TD-1 TD-2 only", deleted)
	}
	if site.Count("ToDo") != 4 { // TD-2's override deleted nothing
		t.Errorf("ToDo count = %d", site.Count("ToDo"))
	}
}
