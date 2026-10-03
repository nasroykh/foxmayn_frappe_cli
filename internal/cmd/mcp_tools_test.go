package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func argReq(args map[string]interface{}) mcp.CallToolRequest {
	var r mcp.CallToolRequest
	r.Params.Arguments = args
	return r
}

func TestJSONArgNativeAndString(t *testing.T) {
	for name, v := range map[string]interface{}{
		"native": map[string]interface{}{"status": "Open"},
		"string": `{"status":"Open"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := rawJSONArg(argReq(map[string]interface{}{"f": v}), "f")
			if err != nil || got != `{"status":"Open"}` {
				t.Errorf("rawJSONArg = %q, %v", got, err)
			}
			m, err := objectArg(argReq(map[string]interface{}{"f": v}), "f", true)
			if err != nil || m["status"] != "Open" {
				t.Errorf("objectArg = %v, %v", m, err)
			}
		})
	}
	// Arrays too.
	got, err := rawJSONArg(argReq(map[string]interface{}{"f": []interface{}{[]interface{}{"a", "=", "b"}}}), "f")
	if err != nil || got != `[["a","=","b"]]` {
		t.Errorf("array: %q %v", got, err)
	}
	// Absent / empty / null.
	for _, args := range []map[string]interface{}{{}, {"f": ""}, {"f": nil}} {
		if got, err := rawJSONArg(argReq(args), "f"); got != "" || err != nil {
			t.Errorf("absent %v: %q %v", args, got, err)
		}
	}
}

func TestRawJSONArgRejectsScalars(t *testing.T) {
	for _, v := range []interface{}{"5", `"x"`, "true", float64(5), true, "not json"} {
		if _, err := rawJSONArg(argReq(map[string]interface{}{"f": v}), "f"); err == nil {
			t.Errorf("rawJSONArg(%#v): want error", v)
		}
	}
}

func TestObjectArg(t *testing.T) {
	if _, err := objectArg(argReq(nil), "d", true); err == nil {
		t.Error("required missing: want error")
	}
	if m, err := objectArg(argReq(nil), "d", false); m != nil || err != nil {
		t.Errorf("optional missing: %v %v", m, err)
	}
	if _, err := objectArg(argReq(map[string]interface{}{"d": "null"}), "d", true); err == nil {
		t.Error("null: want error")
	}
	if _, err := objectArg(argReq(map[string]interface{}{"d": []interface{}{1}}), "d", true); err == nil {
		t.Error("array: want error")
	}
}

func TestIntArg(t *testing.T) {
	ok := map[string]struct {
		in   interface{}
		want int
	}{
		"float": {float64(20), 20}, "string": {"15", 15}, "zero": {float64(0), 0}, "int": {7, 7},
	}
	for name, tt := range ok {
		got, err := intArg(argReq(map[string]interface{}{"n": tt.in}), "n", 99)
		if err != nil || got != tt.want {
			t.Errorf("%s: %d, %v", name, got, err)
		}
	}
	if got, _ := intArg(argReq(nil), "n", 99); got != 99 {
		t.Errorf("default = %d", got)
	}
	for _, in := range []interface{}{float64(-1), 1.5, "abc", true, []interface{}{}} {
		if _, err := intArg(argReq(map[string]interface{}{"n": in}), "n", 0); err == nil {
			t.Errorf("intArg(%#v): want error", in)
		}
	}
}

func TestStringsArg(t *testing.T) {
	for _, v := range []interface{}{[]interface{}{"a", "b"}, `["a","b"]`} {
		got, err := stringsArg(argReq(map[string]interface{}{"f": v}), "f")
		if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("stringsArg(%#v) = %v, %v", v, got, err)
		}
	}
	if _, err := stringsArg(argReq(map[string]interface{}{"f": []interface{}{1, 2}}), "f"); err == nil {
		t.Error("numbers: want error")
	}
	if got, err := stringsArg(argReq(nil), "f"); got != nil || err != nil {
		t.Errorf("absent: %v %v", got, err)
	}
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content type %T", res.Content[0])
	}
	return tc.Text
}

func TestMarshalResultLimit(t *testing.T) {
	small := marshalResult(map[string]string{"a": "b"})
	if small.IsError || resultText(t, small) != `{"a":"b"}` {
		t.Errorf("small = %+v", small)
	}
	big := marshalResult(strings.Repeat("x", maxToolResultBytes))
	if !big.IsError || !strings.Contains(resultText(t, big), "too large") {
		t.Errorf("big not refused: %+v", big)
	}
	if res := marshalResult(make(chan int)); !res.IsError {
		t.Error("unencodable: want error result")
	}
}

// ─── end-to-end against a fake Frappe ────────────────────────────────────────

type recorded struct {
	Method, Path, RawQuery, Body string
}

type fakeSite struct {
	mu   sync.Mutex
	reqs []recorded
}

func (f *fakeSite) all() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func newMCPTestServer(t *testing.T) (*server.MCPServer, *fakeSite) {
	t.Helper()
	fs := &fakeSite{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fs.mu.Lock()
		fs.reqs = append(fs.reqs, recorded{r.Method, r.URL.Path, r.URL.RawQuery, string(b)})
		fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "get_count"):
			_, _ = w.Write([]byte(`{"message":3}`))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"message":"ok"}`))
		default:
			_, _ = w.Write([]byte(`{"message":{"echo":true}}`))
		}
	}))
	t.Cleanup(srv.Close)
	c, err := client.New(context.Background(), &config.SiteConfig{URL: srv.URL, APIKey: "k", APISecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewMCPServer("test", "0")
	registerTools(s, func(context.Context) (*client.FrappeClient, error) { return c, nil })
	return s, fs
}

func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	// Go through the JSON-RPC layer so argument decoding matches a real client.
	params := map[string]interface{}{"name": name, "arguments": args}
	msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
	resp := s.HandleMessage(context.Background(), msg)
	b, _ := json.Marshal(resp)
	// CallToolResult.Content is an interface slice; decode via the mcp helper.
	var raw struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Error) > 0 && string(raw.Error) != "null" {
		t.Fatalf("protocol error: %s", raw.Error)
	}
	res, err := mcp.ParseCallToolResult(&raw.Result)
	if err != nil {
		t.Fatalf("parse result: %v (%s)", err, raw.Result)
	}
	return res
}

func TestMCPCountDocsNativeFilters(t *testing.T) {
	s, fs := newMCPTestServer(t)
	res := callTool(t, s, "count_docs", map[string]interface{}{
		"doctype": "ToDo",
		"filters": map[string]interface{}{"status": "Open"},
	})
	if res.IsError {
		t.Fatalf("error result: %s", resultText(t, res))
	}
	reqs := fs.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %v", reqs)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	if body["doctype"] != "ToDo" || body["filters"] != `{"status":"Open"}` {
		t.Errorf("body = %v (filter dropped?)", body)
	}
	if !strings.Contains(resultText(t, res), `"count":3`) {
		t.Errorf("result = %s", resultText(t, res))
	}
}

func TestMCPCountDocsStringFilters(t *testing.T) {
	s, fs := newMCPTestServer(t)
	callTool(t, s, "count_docs", map[string]interface{}{"doctype": "ToDo", "filters": `[["status","=","Open"]]`})
	if b := fs.all()[0].Body; !strings.Contains(b, `[[\"status\",\"=\",\"Open\"]]`) {
		t.Errorf("body = %s", b)
	}
}

func TestMCPCallMethodNativeArgs(t *testing.T) {
	s, fs := newMCPTestServer(t)
	res := callTool(t, s, "call_method", map[string]interface{}{
		"method": "my.app.fn",
		"args":   map[string]interface{}{"doctype": "ToDo", "filters": map[string]interface{}{"status": "Open"}, "n": 1000000},
	})
	if res.IsError {
		t.Fatalf("error: %s", resultText(t, res))
	}
	r := fs.all()[0]
	if r.Method != http.MethodPost || r.Path != "/api/method/my.app.fn" {
		t.Errorf("req = %+v", r)
	}
	var body map[string]interface{}
	_ = json.Unmarshal([]byte(r.Body), &body)
	f, _ := body["filters"].(map[string]interface{})
	if body["doctype"] != "ToDo" || f["status"] != "Open" || body["n"] != 1000000.0 {
		t.Errorf("body = %v", body)
	}
}

func TestMCPBulkDeleteTooMany(t *testing.T) {
	s, fs := newMCPTestServer(t)
	names := make([]interface{}, maxMCPBulkItems+1)
	for i := range names {
		names[i] = "N"
	}
	res := callTool(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": names})
	if !res.IsError || !strings.Contains(resultText(t, res), "too many") {
		t.Errorf("res = %+v", res)
	}
	if n := len(fs.all()); n != 0 {
		t.Errorf("server called %d times", n)
	}
}

func TestMCPBulkDeleteWorks(t *testing.T) {
	s, fs := newMCPTestServer(t)
	res := callTool(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": []interface{}{"A", float64(2)}})
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	reqs := fs.all()
	if len(reqs) != 2 || reqs[0].Method != http.MethodDelete || reqs[0].Path != "/api/resource/ToDo/A" || reqs[1].Path != "/api/resource/ToDo/2" {
		t.Errorf("reqs = %+v", reqs)
	}
}

func TestMCPInvalidArgsNeverReachServer(t *testing.T) {
	s, fs := newMCPTestServer(t)
	cases := []struct {
		tool string
		args map[string]interface{}
	}{
		{"list_docs", map[string]interface{}{"doctype": "ToDo", "limit": float64(-1)}},
		{"count_docs", map[string]interface{}{"doctype": "ToDo", "filters": "5"}},
		{"create_doc", map[string]interface{}{"doctype": "ToDo"}},
		{"bulk_update", map[string]interface{}{"doctype": "ToDo", "data": []interface{}{map[string]interface{}{"x": 1}}}},
		{"bulk_create", map[string]interface{}{"doctype": "ToDo", "data": []interface{}{}}},
	}
	for _, tc := range cases {
		if res := callTool(t, s, tc.tool, tc.args); !res.IsError {
			t.Errorf("%s: want tool error", tc.tool)
		}
	}
	if n := len(fs.all()); n != 0 {
		t.Errorf("server called %d times", n)
	}
}

func TestMCPReadOnlyToolList(t *testing.T) {
	old := mcpReadOnly
	defer func() { mcpReadOnly = old }()
	names := func() map[string]bool {
		s := server.NewMCPServer("t", "0")
		registerTools(s, nil)
		out := map[string]bool{}
		for n := range s.ListTools() {
			out[n] = true
		}
		return out
	}
	mcpReadOnly = false
	all := names()
	for _, n := range []string{"create_doc", "update_doc", "delete_doc", "call_method", "bulk_create", "bulk_update", "bulk_delete", "ping", "run_report"} {
		if !all[n] {
			t.Errorf("full mode missing %s", n)
		}
	}
	mcpReadOnly = true
	ro := names()
	for n := range ro {
		if strings.HasPrefix(n, "bulk_") || n == "create_doc" || n == "update_doc" || n == "delete_doc" || n == "call_method" {
			t.Errorf("read-only exposes %s", n)
		}
	}
	for _, n := range []string{"ping", "get_doc", "list_docs", "count_docs", "get_schema", "list_doctypes", "list_reports", "run_report"} {
		if !ro[n] {
			t.Errorf("read-only missing %s", n)
		}
	}
}

func TestCompactReportResult(t *testing.T) {
	out := compactReportResult(map[string]interface{}{
		"columns": []interface{}{}, "result": []interface{}{}, "chart": 1, "execution_time": 2,
		"report_summary": nil, "truncated": true, "total_rows": 9,
	})
	if _, ok := out["chart"]; ok {
		t.Error("chart kept")
	}
	if _, ok := out["report_summary"]; ok {
		t.Error("nil summary kept")
	}
	if out["truncated"] != true || out["total_rows"] != 9 {
		t.Errorf("out = %v", out)
	}
}
