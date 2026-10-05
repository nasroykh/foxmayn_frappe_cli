package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// newMCPFake starts a fake Frappe site and an MCP server wired to it, with
// the tool set mcp.go registers (read-only or full).
func newMCPFake(t *testing.T, readOnly bool) (*server.MCPServer, *frappetest.Site) {
	t.Helper()
	site := frappetest.New(t)
	c, err := client.New(context.Background(), &config.SiteConfig{
		URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := server.NewMCPServer("test", "0")
	mcpTRegister(s, c, &config.SiteConfig{Name: "test", MCP: &config.MCPPolicy{ReadOnly: readOnly}}, nil)
	return s, site
}

// mcpTRegister registers the tools on s against c, with site's policy and
// an audit log when audit is set.
func mcpTRegister(s *server.MCPServer, c *client.FrappeClient, site *config.SiteConfig, audit *auditLog) *mcpEnv {
	env := &mcpEnv{
		sites:  []string{site.Name},
		site:   func(context.Context, string) (*config.SiteConfig, error) { return site, nil },
		client: func(context.Context, *config.SiteConfig) (*client.FrappeClient, error) { return c, nil },
		audit:  audit,
	}
	registerTools(s, env, []mcpPolicy{newMCPPolicy(site, env.flags)})
	return env
}

// mcpTOK calls a tool and fails the test on an error result.
func mcpTOK(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}) string {
	t.Helper()
	res := callTool(t, s, name, args)
	if res.IsError {
		t.Fatalf("%s: error result: %s", name, resultText(t, res))
	}
	return resultText(t, res)
}

// mcpTErr calls a tool and requires an error result containing want.
func mcpTErr(t *testing.T, s *server.MCPServer, name string, args map[string]interface{}, want string) {
	t.Helper()
	res := callTool(t, s, name, args)
	if !res.IsError {
		t.Fatalf("%s: want error result, got %s", name, resultText(t, res))
	}
	if msg := resultText(t, res); !strings.Contains(msg, want) {
		t.Fatalf("%s: error %q lacks %q", name, msg, want)
	}
}

func mcpTObj(t *testing.T, text string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("not a JSON object: %v: %.200s", err, text)
	}
	return m
}

func mcpTRows(t *testing.T, text string) []map[string]interface{} {
	t.Helper()
	var m []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("not a JSON array: %v: %.200s", err, text)
	}
	return m
}

func mcpTNames(rows []map[string]interface{}) []string {
	var out []string
	for _, r := range rows {
		out = append(out, fmt.Sprint(r["name"]))
	}
	return out
}

func mcpTBody(t *testing.T, r frappetest.Request) map[string]interface{} {
	t.Helper()
	return mcpTObj(t, r.Body)
}

func mcpTOnly(t *testing.T, site *frappetest.Site, method, path string) frappetest.Request {
	t.Helper()
	rs := site.RequestsTo(method, path)
	if len(rs) != 1 {
		t.Fatalf("%s %s: %d requests, want 1 (all: %+v)", method, path, len(rs), site.Requests())
	}
	return rs[0]
}

func mcpTSeedTodos(site *frappetest.Site) {
	site.Add("ToDo",
		map[string]interface{}{"name": "TD-1", "description": "alpha", "status": "Open"},
		map[string]interface{}{"name": "TD-2", "description": "beta", "status": "Closed"},
		map[string]interface{}{"name": "TD-3", "description": "gamma", "status": "Open"},
	)
}

func TestMCPFakePing(t *testing.T) {
	s, site := newMCPFake(t, false)
	m := mcpTObj(t, mcpTOK(t, s, "ping", nil))
	if m["status"] != "ok" || m["response"] != "pong" {
		t.Errorf("ping = %v", m)
	}
	r := mcpTOnly(t, site, "GET", "/api/method/frappe.ping")
	if r.Header.Get("Authorization") != "token "+frappetest.APIKey+":"+frappetest.APISecret {
		t.Errorf("auth header = %q", r.Header.Get("Authorization"))
	}
}

func TestMCPFakePingError(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Handle("GET /api/method/frappe.ping", frappetest.ErrorHandler(frappetest.Validation("site is in maintenance")))
	mcpTErr(t, s, "ping", nil, "site is in maintenance")
}

func TestMCPFakeGetDoc(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	m := mcpTObj(t, mcpTOK(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}))
	if m["name"] != "TD-1" || m["description"] != "alpha" || m["doctype"] != "ToDo" {
		t.Errorf("doc = %v", m)
	}
	mcpTOnly(t, site, "GET", "/api/resource/ToDo/TD-1")

	// fields narrows the result client-side.
	m = mcpTObj(t, mcpTOK(t, s, "get_doc", map[string]interface{}{
		"doctype": "ToDo", "name": "TD-2", "fields": []interface{}{"name", "status"},
	}))
	if !reflect.DeepEqual(m, map[string]interface{}{"name": "TD-2", "status": "Closed"}) {
		t.Errorf("fields = %v", m)
	}
}

func TestMCPFakeGetDocSingleDefaultsName(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("System Settings", map[string]interface{}{"name": "System Settings", "language": "en"})
	m := mcpTObj(t, mcpTOK(t, s, "get_doc", map[string]interface{}{"doctype": "System Settings"}))
	if m["language"] != "en" {
		t.Errorf("doc = %v", m)
	}
	mcpTOnly(t, site, "GET", "/api/resource/System Settings/System Settings")
}

func TestMCPFakeGetDocErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	mcpTErr(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "nope"}, "ToDo nope not found")
	mcpTErr(t, s, "get_doc", map[string]interface{}{"doctype": "Ghost", "name": "x"}, `doctype "Ghost" not found on this site`)
	mcpTErr(t, s, "get_doc", map[string]interface{}{"name": "x"}, "doctype")
}

func TestMCPFakeListDocs(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)

	rows := mcpTRows(t, mcpTOK(t, s, "list_docs", map[string]interface{}{
		"doctype":  "ToDo",
		"fields":   []interface{}{"name", "status"},
		"filters":  map[string]interface{}{"status": "Open"},
		"limit":    1,
		"order_by": "name desc",
	}))
	if len(rows) != 1 || rows[0]["name"] != "TD-3" || rows[0]["status"] != "Open" || len(rows[0]) != 2 {
		t.Errorf("rows = %v", rows)
	}
	q := mcpTOnly(t, site, "GET", "/api/resource/ToDo").Query
	want := url.Values{
		"fields":            {`["name","status"]`},
		"filters":           {`{"status":"Open"}`},
		"limit_page_length": {"1"},
		"order_by":          {"name desc"},
	}
	if !reflect.DeepEqual(q, want) {
		t.Errorf("query = %v, want %v", q, want)
	}
}

func TestMCPFakeListDocsFilterForms(t *testing.T) {
	cases := map[string]interface{}{
		"native object": map[string]interface{}{"status": "Open"},
		"json string":   `{"status":"Open"}`,
		"native array":  []interface{}{[]interface{}{"status", "=", "Open"}},
		"array string":  `[["status","=","Open"]]`,
	}
	for name, filters := range cases {
		t.Run(name, func(t *testing.T) {
			s, site := newMCPFake(t, false)
			mcpTSeedTodos(site)
			rows := mcpTRows(t, mcpTOK(t, s, "list_docs", map[string]interface{}{
				"doctype": "ToDo", "filters": filters, "order_by": "name asc",
			}))
			if got := mcpTNames(rows); !reflect.DeepEqual(got, []string{"TD-1", "TD-3"}) {
				t.Errorf("names = %v (filter dropped?)", got)
			}
			if f := mcpTOnly(t, site, "GET", "/api/resource/ToDo").Query.Get("filters"); !strings.Contains(f, "Open") {
				t.Errorf("filters query = %q", f)
			}
		})
	}
}

func TestMCPFakeListDocsPagingAndLimit(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	rows := mcpTRows(t, mcpTOK(t, s, "list_docs", map[string]interface{}{
		"doctype": "ToDo", "order_by": "name asc", "start": 1, "limit": 1,
	}))
	if got := mcpTNames(rows); !reflect.DeepEqual(got, []string{"TD-2"}) {
		t.Errorf("names = %v", got)
	}
	q := mcpTOnly(t, site, "GET", "/api/resource/ToDo").Query
	if q.Get("limit_start") != "1" || q.Get("limit_page_length") != "1" {
		t.Errorf("query = %v", q)
	}
}

func TestMCPFakeListDocsDefaultAndUnlimited(t *testing.T) {
	s, site := newMCPFake(t, false)
	for i := 0; i < 25; i++ {
		site.Add("ToDo", map[string]interface{}{"name": fmt.Sprintf("T%02d", i)})
	}
	if rows := mcpTRows(t, mcpTOK(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo"})); len(rows) != 20 {
		t.Errorf("default limit rows = %d, want 20", len(rows))
	}
	if rows := mcpTRows(t, mcpTOK(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "limit": 0})); len(rows) != 25 {
		t.Errorf("limit 0 rows = %d, want 25", len(rows))
	}
	rs := site.RequestsTo("GET", "/api/resource/ToDo")
	if len(rs) != 2 || rs[0].Query.Get("limit_page_length") != "20" || rs[1].Query.Get("limit_page_length") != "0" {
		t.Errorf("requests = %+v", rs)
	}
}

func TestMCPFakeListDocsErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	mcpTErr(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "fields": []interface{}{"bogus"}}, "Field not permitted in query: bogus")
	mcpTErr(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "filters": "5"}, "filters")
	mcpTErr(t, s, "list_docs", map[string]interface{}{"doctype": "ToDo", "limit": -1}, "limit")
	mcpTErr(t, s, "list_docs", map[string]interface{}{"doctype": "Ghost"}, "DocType Ghost not found")
	if n := len(site.Requests()); n != 2 {
		t.Errorf("requests = %d, bad args must not reach the site", n)
	}
}

func TestMCPFakeListDocsTooLarge(t *testing.T) {
	s, site := newMCPFake(t, false)
	blob := strings.Repeat("x", 10<<10)
	for i := 0; i < 60; i++ {
		site.Add("Note", map[string]interface{}{"name": fmt.Sprintf("N%02d", i), "content": blob})
	}
	// Too many rows are cut to the ones that fit (TestMCPListDocsTruncates);
	// a single row too large to return is refused.
	site.Add("Note", map[string]interface{}{"name": "Huge", "content": strings.Repeat("x", maxToolResultBytes)})
	mcpTErr(t, s, "list_docs", map[string]interface{}{"doctype": "Note", "limit": 0, "fields": []interface{}{"name", "content"}, "filters": map[string]interface{}{"name": "Huge"}},
		"narrow it with limit, fields, filters or keys")
	// Narrowing works.
	rows := mcpTRows(t, mcpTOK(t, s, "list_docs", map[string]interface{}{"doctype": "Note", "limit": 0}))
	if len(rows) != 61 {
		t.Errorf("narrowed rows = %d", len(rows))
	}
}

func TestMCPFakeCreateDoc(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddDocType("ToDo")
	m := mcpTObj(t, mcpTOK(t, s, "create_doc", map[string]interface{}{
		"doctype": "ToDo", "data": map[string]interface{}{"description": "buy milk", "priority": "High"},
	}))
	name, _ := m["name"].(string)
	if name == "" || m["description"] != "buy milk" || m["owner"] != frappetest.Username {
		t.Errorf("created = %v", m)
	}
	body := mcpTBody(t, mcpTOnly(t, site, "POST", "/api/resource/ToDo"))
	if !reflect.DeepEqual(body, map[string]interface{}{"description": "buy milk", "priority": "High"}) {
		t.Errorf("body = %v", body)
	}
	if d, ok := site.Doc("ToDo", name); !ok || d["priority"] != "High" {
		t.Errorf("stored = %v %v", d, ok)
	}
	// data as JSON string is accepted too.
	mcpTOK(t, s, "create_doc", map[string]interface{}{"doctype": "ToDo", "data": `{"description":"b"}`})
	if site.Count("ToDo") != 2 {
		t.Errorf("count = %d", site.Count("ToDo"))
	}
}

func TestMCPFakeCreateDocErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	mcpTErr(t, s, "create_doc", map[string]interface{}{"doctype": "ToDo", "data": map[string]interface{}{"name": "TD-1"}}, "ToDo TD-1 already exists")
	mcpTErr(t, s, "create_doc", map[string]interface{}{"doctype": "ToDo"}, "data")
	mcpTErr(t, s, "create_doc", map[string]interface{}{"doctype": "ToDo", "data": []interface{}{1}}, "data")
	site.Handle("POST /api/resource/ToDo", frappetest.ErrorHandler(frappetest.Validation("Description is mandatory")))
	mcpTErr(t, s, "create_doc", map[string]interface{}{"doctype": "ToDo", "data": map[string]interface{}{"x": 1}}, "Description is mandatory")
}

func TestMCPFakeUpdateDoc(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	m := mcpTObj(t, mcpTOK(t, s, "update_doc", map[string]interface{}{
		"doctype": "ToDo", "name": "TD-1", "data": map[string]interface{}{"status": "Closed", "name": "IGNORED"},
	}))
	if m["status"] != "Closed" || m["name"] != "TD-1" || m["description"] != "alpha" {
		t.Errorf("updated = %v", m)
	}
	// The name key is stripped from the body; the name is in the URL.
	body := mcpTBody(t, mcpTOnly(t, site, "PUT", "/api/resource/ToDo/TD-1"))
	if !reflect.DeepEqual(body, map[string]interface{}{"status": "Closed"}) {
		t.Errorf("body = %v", body)
	}
	if d, _ := site.Doc("ToDo", "TD-1"); d["status"] != "Closed" {
		t.Errorf("stored = %v", d)
	}
}

func TestMCPFakeUpdateDocSingle(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("Website Settings", map[string]interface{}{"name": "Website Settings", "app_name": "a"})
	mcpTOK(t, s, "update_doc", map[string]interface{}{"doctype": "Website Settings", "data": map[string]interface{}{"app_name": "b"}})
	mcpTOnly(t, site, "PUT", "/api/resource/Website Settings/Website Settings")
	if d, _ := site.Doc("Website Settings", "Website Settings"); d["app_name"] != "b" {
		t.Errorf("stored = %v", d)
	}
}

func TestMCPFakeUpdateDocErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	mcpTErr(t, s, "update_doc", map[string]interface{}{"doctype": "ToDo", "name": "nope", "data": map[string]interface{}{"a": 1}}, "ToDo nope not found")
	mcpTErr(t, s, "update_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}, "data")
	site.Handle("PUT /api/resource/ToDo/TD-1", frappetest.ErrorHandler(frappetest.Validation("Cannot edit cancelled document")))
	mcpTErr(t, s, "update_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "data": map[string]interface{}{"a": 1}}, "Cannot edit cancelled document")
}

func TestMCPFakeDeleteDoc(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	if got := mcpTOK(t, s, "delete_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-2"}); got != "Deleted ToDo TD-2" {
		t.Errorf("result = %q", got)
	}
	mcpTOnly(t, site, "DELETE", "/api/resource/ToDo/TD-2")
	if _, ok := site.Doc("ToDo", "TD-2"); ok || site.Count("ToDo") != 2 {
		t.Error("document not deleted")
	}
}

func TestMCPFakeDeleteDocErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	mcpTErr(t, s, "delete_doc", map[string]interface{}{"doctype": "ToDo", "name": "nope"}, "ToDo nope not found")
	mcpTErr(t, s, "delete_doc", map[string]interface{}{"doctype": "ToDo"}, "name")
	site.Handle("DELETE /api/resource/ToDo/TD-1", frappetest.ErrorHandler(frappetest.LinkExists("Cannot delete TD-1 because it is linked")))
	mcpTErr(t, s, "delete_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}, "because it is linked")
	if site.Count("ToDo") != 3 {
		t.Errorf("count = %d", site.Count("ToDo"))
	}
}

func TestMCPFakeCountDocs(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	m := mcpTObj(t, mcpTOK(t, s, "count_docs", map[string]interface{}{"doctype": "ToDo"}))
	if m["count"] != 3.0 || m["doctype"] != "ToDo" {
		t.Errorf("count = %v", m)
	}
	body := mcpTBody(t, mcpTOnly(t, site, "POST", "/api/method/frappe.client.get_count"))
	if !reflect.DeepEqual(body, map[string]interface{}{"doctype": "ToDo"}) {
		t.Errorf("body = %v", body)
	}

	for name, f := range map[string]interface{}{
		"native": map[string]interface{}{"status": "Open"},
		"string": `[["status","=","Open"]]`,
	} {
		m = mcpTObj(t, mcpTOK(t, s, "count_docs", map[string]interface{}{"doctype": "ToDo", "filters": f}))
		if m["count"] != 2.0 {
			t.Errorf("%s: count = %v, want 2", name, m)
		}
	}
	last := site.RequestsTo("POST", "/api/method/frappe.client.get_count")
	if b := mcpTBody(t, last[len(last)-1]); !strings.Contains(fmt.Sprint(b["filters"]), "Open") {
		t.Errorf("filters body = %v", b)
	}
}

func TestMCPFakeCountDocsErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	mcpTErr(t, s, "count_docs", map[string]interface{}{"doctype": "ToDo", "filters": map[string]interface{}{"bogus": 1}}, "Field not permitted in query: bogus")
	mcpTErr(t, s, "count_docs", map[string]interface{}{"doctype": "ToDo", "filters": "7"}, "filters")
}

func mcpTSeedSchema(site *frappetest.Site) {
	site.Add("DocType", map[string]interface{}{
		"name": "Task Item", "module": "Desk", "autoname": "hash", "issingle": json.Number("0"),
		"creation_noise": "x",
		"fields": []interface{}{
			map[string]interface{}{"fieldname": "title", "label": "Title", "fieldtype": "Data", "reqd": json.Number("1"), "hidden": json.Number("0"), "idx": json.Number("1")},
			map[string]interface{}{"fieldname": "status", "label": "Status", "fieldtype": "Select", "options": "Open\nClosed", "idx": json.Number("2")},
		},
	})
	site.Add("Custom Field", map[string]interface{}{
		"name": "Task Item-extra", "dt": "Task Item", "fieldname": "extra", "label": "Extra",
		"fieldtype": "Data", "insert_after": "title", "idx": json.Number("1"),
	})
	site.Add("Property Setter", map[string]interface{}{
		"name": "ps1", "doc_type": "Task Item", "doctype_or_field": "DocField", "field_name": "status",
		"property": "options", "property_type": "Text", "value": "Open\nClosed\nOn Hold",
	})
}

func mcpTFieldNames(t *testing.T, m map[string]interface{}) []string {
	t.Helper()
	var out []string
	fs, _ := m["fields"].([]interface{})
	for _, f := range fs {
		out = append(out, f.(map[string]interface{})["fieldname"].(string))
	}
	return out
}

func TestMCPFakeGetSchemaCompact(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedSchema(site)
	m := mcpTObj(t, mcpTOK(t, s, "get_schema", map[string]interface{}{"doctype": "Task Item"}))
	if m["name"] != "Task Item" || m["module"] != "Desk" || m["autoname"] != "hash" {
		t.Errorf("schema = %v", m)
	}
	if _, ok := m["creation_noise"]; ok {
		t.Error("compact view kept noise key")
	}
	if got := mcpTFieldNames(t, m); !reflect.DeepEqual(got, []string{"title", "extra", "status"}) {
		t.Errorf("fields = %v (custom field not merged after insert_after?)", got)
	}
	for _, f := range m["fields"].([]interface{}) {
		fm := f.(map[string]interface{})
		if fm["fieldname"] == "status" && fm["options"] != "Open\nClosed\nOn Hold" {
			t.Errorf("property setter not applied: %v", fm)
		}
		if fm["fieldname"] == "title" {
			if _, ok := fm["hidden"]; ok {
				t.Errorf("zero-value hidden kept: %v", fm)
			}
		}
	}
	mcpTOnly(t, site, "GET", "/api/resource/DocType/Task Item")
	cf := mcpTOnly(t, site, "GET", "/api/resource/Custom Field").Query
	if cf.Get("filters") != `{"dt":"Task Item"}` || cf.Get("order_by") != "idx asc" {
		t.Errorf("custom field query = %v", cf)
	}
	ps := mcpTOnly(t, site, "GET", "/api/resource/Property Setter").Query
	if ps.Get("filters") != `{"doc_type":"Task Item"}` {
		t.Errorf("property setter query = %v", ps)
	}
}

func TestMCPFakeGetSchemaKeysAndFull(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedSchema(site)
	for name, keys := range map[string]interface{}{
		"array":  []interface{}{"name", "module"},
		"json":   `["name","module"]`,
		"commas": "name,module",
	} {
		m := mcpTObj(t, mcpTOK(t, s, "get_schema", map[string]interface{}{"doctype": "Task Item", "keys": keys}))
		if !reflect.DeepEqual(m, map[string]interface{}{"name": "Task Item", "module": "Desk"}) {
			t.Errorf("keys %s: %v", name, m)
		}
	}
	// A missing key becomes a warning.
	m := mcpTObj(t, mcpTOK(t, s, "get_schema", map[string]interface{}{"doctype": "Task Item", "keys": []interface{}{"name", "nonesuch"}}))
	if w, _ := m["_warnings"].([]interface{}); len(w) != 1 || !strings.Contains(fmt.Sprint(w[0]), "nonesuch") {
		t.Errorf("warnings = %v", m["_warnings"])
	}
	full := mcpTObj(t, mcpTOK(t, s, "get_schema", map[string]interface{}{"doctype": "Task Item", "full": true}))
	if full["creation_noise"] != "x" || full["owner"] == nil {
		t.Errorf("full view lacks raw keys: %v", full)
	}
}

func TestMCPFakeGetSchemaErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddDocType("DocType")
	mcpTErr(t, s, "get_schema", map[string]interface{}{"doctype": "Ghost"}, "Ghost")
	mcpTErr(t, s, "get_schema", map[string]interface{}{"doctype": "Task Item", "keys": 5}, "keys")
	// An unreadable Custom Field list is a warning, not a failure.
	mcpTSeedSchema(site)
	site.Handle("GET /api/resource/Custom Field", frappetest.ErrorHandler(frappetest.Permission("No permission for Custom Field")))
	m := mcpTObj(t, mcpTOK(t, s, "get_schema", map[string]interface{}{"doctype": "Task Item"}))
	if w, _ := m["_warnings"].([]interface{}); len(w) != 1 || !strings.Contains(fmt.Sprint(w[0]), "custom fields could not be merged") {
		t.Errorf("warnings = %v", m["_warnings"])
	}
}

func TestMCPFakeListDoctypes(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("DocType",
		map[string]interface{}{"name": "Customer", "module": "Selling", "is_submittable": json.Number("0"), "is_tree": json.Number("0"), "description": "d"},
		map[string]interface{}{"name": "Invoice", "module": "Accounts", "is_submittable": json.Number("1"), "is_tree": json.Number("0"), "description": "d"},
		map[string]interface{}{"name": "Account", "module": "Accounts", "is_submittable": json.Number("0"), "is_tree": json.Number("1"), "description": "d"},
	)
	rows := mcpTRows(t, mcpTOK(t, s, "list_doctypes", map[string]interface{}{"module": "Accounts"}))
	if got := mcpTNames(rows); !reflect.DeepEqual(got, []string{"Account", "Invoice"}) {
		t.Errorf("names = %v", got)
	}
	q := mcpTOnly(t, site, "GET", "/api/resource/DocType").Query
	if q.Get("filters") != `{"module":"Accounts"}` || q.Get("order_by") != "name asc" ||
		q.Get("limit_page_length") != "50" || q.Get("fields") != `["name","module","is_submittable","is_tree","description"]` {
		t.Errorf("query = %v", q)
	}
	all := mcpTRows(t, mcpTOK(t, s, "list_doctypes", map[string]interface{}{"limit": 0}))
	if len(all) != 3 {
		t.Errorf("all = %d", len(all))
	}
}

func TestMCPFakeListDoctypesErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTErr(t, s, "list_doctypes", map[string]interface{}{"limit": "abc"}, "limit")
	mcpTErr(t, s, "list_doctypes", nil, "DocType DocType not found") // fake has no DocType registered
	site.Handle("GET /api/resource/DocType", frappetest.ErrorHandler(frappetest.Permission("No permission for DocType")))
	mcpTErr(t, s, "list_doctypes", nil, "No permission for DocType")
}

func TestMCPFakeListReports(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("Report",
		map[string]interface{}{"name": "Sales", "report_type": "Query Report", "module": "Selling", "is_standard": "Yes", "ref_doctype": "Sales Invoice"},
		map[string]interface{}{"name": "Ledger", "report_type": "Script Report", "module": "Accounts", "is_standard": "Yes", "ref_doctype": "GL Entry"},
	)
	rows := mcpTRows(t, mcpTOK(t, s, "list_reports", map[string]interface{}{"module": "Accounts", "limit": 5}))
	if len(rows) != 1 || rows[0]["name"] != "Ledger" || rows[0]["ref_doctype"] != "GL Entry" {
		t.Errorf("rows = %v", rows)
	}
	q := mcpTOnly(t, site, "GET", "/api/resource/Report").Query
	if q.Get("filters") != `{"module":"Accounts"}` || q.Get("limit_page_length") != "5" ||
		q.Get("fields") != `["name","report_type","module","is_standard","ref_doctype"]` {
		t.Errorf("query = %v", q)
	}
}

func TestMCPFakeListReportsErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddDocType("Report")
	mcpTErr(t, s, "list_reports", map[string]interface{}{"module": 5}, "module")
	site.Handle("GET /api/resource/Report", frappetest.ErrorHandler(frappetest.Permission("No permission for Report")))
	mcpTErr(t, s, "list_reports", nil, "No permission for Report")
}

func mcpTReport(n int) map[string]interface{} {
	rows := make([]interface{}, n)
	for i := range rows {
		rows[i] = map[string]interface{}{"name": fmt.Sprintf("R%d", i), "qty": i}
	}
	return map[string]interface{}{
		"columns":        []interface{}{map[string]interface{}{"fieldname": "name", "label": "Name"}, map[string]interface{}{"fieldname": "qty", "label": "Qty"}},
		"result":         rows,
		"report_summary": nil,
		"chart":          map[string]interface{}{"type": "bar"},
		"execution_time": 0.5,
		"message":        nil,
	}
}

func TestMCPFakeRunReportSmall(t *testing.T) {
	s, site := newMCPFake(t, false)
	rep := mcpTReport(3)
	rep["report_summary"] = []interface{}{map[string]interface{}{"label": "Total", "value": 3}}
	site.AddReport("Stock", rep)
	m := mcpTObj(t, mcpTOK(t, s, "run_report", map[string]interface{}{
		"report_name": "Stock", "filters": map[string]interface{}{"company": "Acme"},
	}))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"columns", "report_summary", "result"}) {
		t.Errorf("keys = %v (noise not stripped, or extras present)", keys)
	}
	if rows := m["result"].([]interface{}); len(rows) != 3 {
		t.Errorf("rows = %d", len(rows))
	}
	body := mcpTBody(t, mcpTOnly(t, site, "POST", "/api/method/frappe.desk.query_report.run"))
	if body["report_name"] != "Stock" || body["filters"] != `{"company":"Acme"}` || body["ignore_prepared_report"] != 1.0 {
		t.Errorf("body = %v", body)
	}
}

func TestMCPFakeRunReportDefaultCap(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddReport("Big", mcpTReport(650))
	m := mcpTObj(t, mcpTOK(t, s, "run_report", map[string]interface{}{"report_name": "Big"}))
	if rows := m["result"].([]interface{}); len(rows) != 500 {
		t.Errorf("rows = %d, want default cap 500", len(rows))
	}
	if m["total_rows"] != 650.0 || m["truncated"] != true {
		t.Errorf("total_rows=%v truncated=%v", m["total_rows"], m["truncated"])
	}
	if _, ok := m["report_summary"]; ok {
		t.Error("null report_summary must be omitted")
	}
	if _, ok := m["chart"]; ok {
		t.Error("chart not stripped")
	}

	// Explicit limit and limit 0.
	m = mcpTObj(t, mcpTOK(t, s, "run_report", map[string]interface{}{"report_name": "Big", "limit": 10}))
	if len(m["result"].([]interface{})) != 10 || m["total_rows"] != 650.0 {
		t.Errorf("limit 10 = %v", m["total_rows"])
	}
	m = mcpTObj(t, mcpTOK(t, s, "run_report", map[string]interface{}{"report_name": "Big", "limit": 0}))
	if len(m["result"].([]interface{})) != 650 {
		t.Errorf("limit 0 rows = %d", len(m["result"].([]interface{})))
	}
	if _, ok := m["truncated"]; ok {
		t.Error("untruncated result carries truncated")
	}
}

func TestMCPFakeRunReportErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddReport("Stock", mcpTReport(1))
	mcpTErr(t, s, "run_report", map[string]interface{}{"report_name": "Nope"}, "Report Nope not found")
	mcpTErr(t, s, "run_report", map[string]interface{}{"report_name": "Stock", "filters": []interface{}{1}}, "filters")
	mcpTErr(t, s, "run_report", map[string]interface{}{"report_name": "Stock", "limit": -2}, "limit")
	site.Handle("POST /api/method/frappe.desk.query_report.run", frappetest.ErrorHandler(frappetest.Validation("Please set the company filter")))
	mcpTErr(t, s, "run_report", map[string]interface{}{"report_name": "Stock"}, "Please set the company filter")
}

func TestMCPFakeCallMethod(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.HandleMethod("my.app.add", func(_ *http.Request, args map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"echo": args}, nil
	})
	m := mcpTObj(t, mcpTOK(t, s, "call_method", map[string]interface{}{
		"method": "my.app.add", "args": map[string]interface{}{"a": "x", "f": map[string]interface{}{"k": "v"}},
	}))
	echo := m["echo"].(map[string]interface{})
	if echo["a"] != "x" || echo["f"].(map[string]interface{})["k"] != "v" {
		t.Errorf("echo = %v", echo)
	}
	r := mcpTOnly(t, site, "POST", "/api/method/my.app.add")
	if b := mcpTBody(t, r); b["a"] != "x" {
		t.Errorf("body = %v", b)
	}

	// GET sends args as query parameters, non-strings JSON-encoded.
	mcpTOK(t, s, "call_method", map[string]interface{}{
		"method": "my.app.add", "get": true, "args": map[string]interface{}{"a": "x", "f": map[string]interface{}{"k": "v"}},
	})
	g := mcpTOnly(t, site, "GET", "/api/method/my.app.add")
	if g.Query.Get("a") != "x" || g.Query.Get("f") != `{"k":"v"}` || g.Body != "" {
		t.Errorf("GET req = %+v", g)
	}
}

func TestMCPFakeCallMethodErrors(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.HandleMethod("my.app.fail", func(*http.Request, map[string]interface{}) (interface{}, error) {
		return nil, frappetest.Validation("Quantity must be positive")
	})
	mcpTErr(t, s, "call_method", map[string]interface{}{"method": "my.app.fail"}, "Quantity must be positive")
	mcpTErr(t, s, "call_method", map[string]interface{}{"method": "no.such.method"}, "no.such.method")
	mcpTErr(t, s, "call_method", map[string]interface{}{"method": "x", "args": "[1]"}, "args")
}

func TestMCPFakeBulkCreate(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.Add("ToDo", map[string]interface{}{"name": "dup"})
	m := mcpTObj(t, mcpTOK(t, s, "bulk_create", map[string]interface{}{
		"doctype": "ToDo",
		"data": []interface{}{
			map[string]interface{}{"name": "a", "description": "A"},
			map[string]interface{}{"name": "dup"},
			map[string]interface{}{"name": "c", "description": "C"},
		},
	}))
	if m["created"] != 2.0 || m["failed"] != 1.0 || m["skipped"] != 0.0 {
		t.Errorf("report = %v", m)
	}
	res := m["results"].([]interface{})
	if len(res) != 3 {
		t.Fatalf("results = %v", res)
	}
	r1 := res[1].(map[string]interface{})
	if r1["status"] != "error" || r1["index"] != 2.0 || !strings.Contains(fmt.Sprint(r1["error"]), "ToDo dup already exists") {
		t.Errorf("failed item = %v", r1)
	}
	if r0 := res[0].(map[string]interface{}); r0["status"] != "created" || r0["name"] != "a" {
		t.Errorf("ok item = %v", r0)
	}
	if n := len(site.RequestsTo("POST", "/api/resource/ToDo")); n != 3 {
		t.Errorf("POSTs = %d, processing must continue after a failure", n)
	}
	if site.Count("ToDo") != 3 {
		t.Errorf("count = %d", site.Count("ToDo"))
	}
}

func TestMCPFakeBulkUpdate(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	m := mcpTObj(t, mcpTOK(t, s, "bulk_update", map[string]interface{}{
		"doctype": "ToDo",
		"data":    `[{"name":"TD-1","status":"Closed"},{"name":"missing","status":"x"},{"name":"TD-3","status":"Closed"}]`,
	}))
	if m["updated"] != 2.0 || m["failed"] != 1.0 {
		t.Errorf("report = %v", m)
	}
	res := m["results"].([]interface{})
	if r := res[1].(map[string]interface{}); r["status"] != "error" || r["name"] != "missing" || !strings.Contains(fmt.Sprint(r["error"]), "ToDo missing not found") {
		t.Errorf("failed item = %v", r)
	}
	b := mcpTBody(t, mcpTOnly(t, site, "PUT", "/api/resource/ToDo/TD-1"))
	if !reflect.DeepEqual(b, map[string]interface{}{"status": "Closed"}) {
		t.Errorf("body = %v (name must be stripped)", b)
	}
	if d, _ := site.Doc("ToDo", "TD-3"); d["status"] != "Closed" {
		t.Errorf("TD-3 = %v", d)
	}
}

func TestMCPFakeBulkDelete(t *testing.T) {
	s, site := newMCPFake(t, false)
	mcpTSeedTodos(site)
	m := mcpTObj(t, mcpTOK(t, s, "bulk_delete", map[string]interface{}{
		"doctype": "ToDo", "names": []interface{}{"TD-1", "ghost", "TD-3"},
	}))
	if m["deleted"] != 2.0 || m["failed"] != 1.0 {
		t.Errorf("report = %v", m)
	}
	res := m["results"].([]interface{})
	if r := res[1].(map[string]interface{}); r["status"] != "error" || r["name"] != "ghost" || !strings.Contains(fmt.Sprint(r["error"]), "ToDo ghost not found") {
		t.Errorf("failed item = %v", r)
	}
	mcpTOnly(t, site, "DELETE", "/api/resource/ToDo/TD-1")
	mcpTOnly(t, site, "DELETE", "/api/resource/ToDo/TD-3")
	if site.Count("ToDo") != 1 {
		t.Errorf("count = %d", site.Count("ToDo"))
	}
}

func TestMCPFakeBulkRefusals(t *testing.T) {
	s, site := newMCPFake(t, false)
	site.AddDocType("ToDo")
	items := make([]interface{}, maxMCPBulkItems+1)
	names := make([]interface{}, maxMCPBulkItems+1)
	for i := range items {
		items[i] = map[string]interface{}{"name": fmt.Sprintf("n%d", i)}
		names[i] = fmt.Sprintf("n%d", i)
	}
	mcpTErr(t, s, "bulk_create", map[string]interface{}{"doctype": "ToDo", "data": items}, "too many items (201)")
	mcpTErr(t, s, "bulk_update", map[string]interface{}{"doctype": "ToDo", "data": items}, "too many items (201)")
	mcpTErr(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo", "names": names}, "too many items (201)")
	// Malformed input.
	mcpTErr(t, s, "bulk_create", map[string]interface{}{"doctype": "ToDo", "data": []interface{}{}}, "empty")
	mcpTErr(t, s, "bulk_create", map[string]interface{}{"doctype": "ToDo", "data": "{}"}, "data")
	mcpTErr(t, s, "bulk_update", map[string]interface{}{"doctype": "ToDo", "data": []interface{}{map[string]interface{}{"status": "x"}}}, "data")
	mcpTErr(t, s, "bulk_delete", map[string]interface{}{"doctype": "ToDo"}, "names")
	if n := len(site.Requests()); n != 0 {
		t.Errorf("%d requests reached the site", n)
	}
	// Exactly the limit is accepted.
	m := mcpTObj(t, mcpTOK(t, s, "bulk_create", map[string]interface{}{"doctype": "ToDo", "data": items[:maxMCPBulkItems]}))
	if m["created"] != float64(maxMCPBulkItems) || site.Count("ToDo") != maxMCPBulkItems {
		t.Errorf("at limit: %v count=%d", m["created"], site.Count("ToDo"))
	}
}

func mcpTToolNames(t *testing.T, s *server.MCPServer) []string {
	t.Helper()
	msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	b, _ := json.Marshal(s.HandleMessage(context.Background(), msg))
	var resp struct {
		Result struct {
			Tools []mcp.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range resp.Result.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	return names
}

func TestMCPFakeToolSets(t *testing.T) {
	read := []string{"check_permission", "count_docs", "get_doc", "get_doc_context", "get_schema", "get_transitions", "list_docs", "list_doctypes", "list_reports", "list_sites", "ping", "run_report", "search", "whoami"}
	write := []string{"amend_doc", "apply_workflow", "bulk_create", "bulk_delete", "bulk_update", "call_method", "cancel_doc",
		"copy_doc", "create_doc", "delete_doc", "rename_doc", "submit_doc", "update_doc"}
	all := append(append([]string(nil), read...), write...)
	sort.Strings(all)

	s, site := newMCPFake(t, true)
	if got := mcpTToolNames(t, s); !reflect.DeepEqual(got, read) {
		t.Errorf("read-only tools = %v, want %v", got, read)
	}
	// An unregistered tool cannot be called, and nothing reaches the site.
	msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": "delete_doc", "arguments": map[string]interface{}{"doctype": "ToDo", "name": "x"}}})
	if b, _ := json.Marshal(s.HandleMessage(context.Background(), msg)); !strings.Contains(string(b), `"error"`) {
		t.Errorf("delete_doc callable in read-only mode: %s", b)
	}
	if n := len(site.Requests()); n != 0 {
		t.Errorf("%d requests reached the site", n)
	}

	s, _ = newMCPFake(t, false)
	if got := mcpTToolNames(t, s); !reflect.DeepEqual(got, all) || len(got) != 27 {
		t.Errorf("full tools = %v, want %v", got, all)
	}
}
