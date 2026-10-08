package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// exportTSite has Order documents with two tables of unequal length, a
// Duration, values a spreadsheet would run as formulas, a layout field and
// a Password field.
func exportTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	s.DocField("Order", "customer", "Data")
	s.DocField("Order", "details", "Section Break")
	s.DocField("Order", "total", "Currency")
	s.DocField("Order", "took", "Duration")
	s.DocField("Order", "notes", "Small Text")
	s.DocField("Order", "secret", "Password")
	s.ChildTable("Order", "items", "Order Item")
	s.ChildTable("Order", "taxes", "Order Tax")
	s.DocField("Order Item", "item", "Data")
	s.DocField("Order Item", "qty", "Int")
	s.DocField("Order Tax", "rate", "Float")
	row := func(name string, kv ...interface{}) map[string]interface{} {
		m := map[string]interface{}{"name": name}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	s.Add("Order",
		map[string]interface{}{"name": "O-1", "customer": "=cmd", "total": json.Number("10.0"), "took": json.Number("93784"),
			"notes": "-5", "secret": "x",
			"items": []interface{}{row("I-1", "item", "a", "qty", json.Number("1")), row("I-2", "item", "b", "qty", json.Number("2")),
				row("I-3", "item", "c", "qty", json.Number("3"))},
			"taxes": []interface{}{row("T-1", "rate", json.Number("5.0"))}},
		map[string]interface{}{"name": "O-2", "customer": "Bob", "total": json.Number("0.0"), "took": nil,
			"notes": "line1\nline2", "items": []interface{}{},
			"taxes": []interface{}{row("T-2", "rate", json.Number("1.5")), row("T-3", "rate", json.Number("2.5"))}},
		map[string]interface{}{"name": "O-3", "customer": "@x", "total": json.Number("1.0"), "took": json.Number("-3700"),
			"notes": nil, "items": []interface{}{row("I-4", "item", "d", "qty", json.Number("4"))}, "taxes": []interface{}{}},
	)
	return s
}

// exportTCSV is the whole export of exportTSite in Frappe's layout.
const exportTCSV = `name,customer,total,took,notes,items.name,items.item,items.qty,taxes.name,taxes.rate
O-1,'=cmd,10.0,1d 2h 3m 4s,'-5,I-1,a,1,T-1,5.0
,,,,,I-2,b,2,,
,,,,,I-3,c,3,,
O-2,Bob,0.0,,"line1
line2",,,,T-2,1.5
,,,,,,,,T-3,2.5
O-3,'@x,1.0,'-1h 1m 40s,,I-4,d,4,,
`

func TestExportCSVLayout(t *testing.T) {
	s := exportTSite(t)
	r := cmdTRun(t, s, "export", "-d", "Order")
	if r.Code != 0 || r.Stdout != exportTCSV {
		t.Fatalf("exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	// One page of parents, one request per table, all with the parent's
	// permission check (parent) and every row (limit 0).
	reqs := s.RequestsTo("POST", "/api/method/frappe.client.get_list")
	if len(reqs) != 2 {
		t.Fatalf("%d child requests", len(reqs))
	}
	for _, q := range reqs {
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(q.Body), &args); err != nil {
			t.Fatal(err)
		}
		if args["parent"] != "Order" || args["order_by"] != "idx asc" || args["limit_page_length"] != float64(0) {
			t.Errorf("child request %v", args)
		}
	}

	// TSV: the same rows, escaped one per line.
	r = cmdTRun(t, s, "--output", "tsv", "export", "-d", "Order", "--no-tables")
	want := "name\tcustomer\ttotal\ttook\tnotes\nO-1\t'=cmd\t10.0\t1d 2h 3m 4s\t'-5\nO-2\tBob\t0.0\t\tline1\\nline2\nO-3\t'@x\t1.0\t'-1h 1m 40s\t\n"
	if r.Code != 0 || r.Stdout != want {
		t.Errorf("tsv: exit %d %s\n%q", r.Code, r.Stderr, r.Stdout)
	}
}

// Pages of one document give the same file; --limit counts documents.
func TestExportPaging(t *testing.T) {
	s := exportTSite(t)
	r := cmdTRun(t, s, "export", "-d", "Order", "--page-size", "1")
	if r.Code != 0 || r.Stdout != exportTCSV {
		t.Fatalf("exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	if n := len(s.RequestsTo("GET", "/api/resource/Order")); n != 4 {
		t.Errorf("%d list requests, want 4 (the last one empty)", n)
	}
	r = cmdTRun(t, s, "export", "-d", "Order", "--page-size", "1", "--limit", "2", "--tables", "taxes")
	want := "name,customer,total,took,notes,taxes.name,taxes.rate\nO-1,'=cmd,10.0,1d 2h 3m 4s,'-5,T-1,5.0\nO-2,Bob,0.0,,\"line1\nline2\",T-2,1.5\n,,,,,T-3,2.5\n"
	if r.Code != 0 || r.Stdout != want {
		t.Errorf("--limit: exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	// --order-by is passed on.
	r = cmdTRun(t, s, "export", "-d", "Order", "--no-tables", "--fields", "customer", "--order-by", "customer asc")
	if r.Code != 0 || r.Stdout != "name,customer\nO-1,'=cmd\nO-3,'@x\nO-2,Bob\n" {
		t.Errorf("--order-by: exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
}

func TestExportFieldSelection(t *testing.T) {
	s := exportTSite(t)
	r := cmdTRun(t, s, "export", "-d", "Order", "--fields", "customer,items.qty")
	want := "name,customer,items.name,items.qty\nO-1,'=cmd,I-1,1\n,,I-2,2\n,,I-3,3\nO-2,Bob,,\nO-3,'@x,I-4,4\n"
	if r.Code != 0 || r.Stdout != want {
		t.Errorf("exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	// A table named alone takes all its fields; name is not repeated.
	r = cmdTRun(t, s, "export", "-d", "Order", "--fields", "name,taxes", "--limit", "1")
	if r.Code != 0 || r.Stdout != "name,taxes.name,taxes.rate\nO-1,T-1,5.0\n" {
		t.Errorf("whole table: exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	for _, args := range [][]string{
		{"--fields", "nope"},
		{"--fields", "items.nope"},
		{"--fields", "nope.qty"},
		{"--fields", "secret"},
		{"--fields", "details"},
		{"--tables", "nope"},
		{"--tables", "taxes", "--fields", "items.qty"},
		{"--no-tables", "--fields", "items.qty"},
		{"--no-tables", "--tables", "items"},
		{"--output", "yaml"},
		{"--jq", ".[]"},
		{"--limit", "-1"},
		{"--page-size", "0"},
	} {
		r := cmdTRun(t, s, append([]string{"export", "-d", "Order"}, args...)...)
		if r.Code != exitUsage {
			t.Errorf("%v: exit %d %s", args, r.Code, r.Stderr)
		}
	}
	if n := len(s.RequestsTo("GET", "/api/resource/Order")); n != 2 {
		t.Errorf("%d list requests: a refused selection sent one", n-2)
	}
}

// A field above the user's permission level is left out by default and
// refused (exit 5) when named.
func TestExportPermlevel(t *testing.T) {
	s := exportTSite(t)
	s.Permlevel("Order", "total", 1)
	s.DocPerm("Order", map[string]interface{}{"role": "Clerk", "permlevel": 0, "read": 1})
	s.SetUser("u@x.com", "Clerk")
	r := cmdTRun(t, s, "export", "-d", "Order", "--no-tables", "--limit", "1")
	if r.Code != 0 || r.Stdout != "name,customer,took,notes\nO-1,'=cmd,1d 2h 3m 4s,'-5\n" {
		t.Errorf("default: exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	r = cmdTRun(t, s, "export", "-d", "Order", "--fields", "total")
	if r.Code != exitPermission || !strings.Contains(r.Stderr, "Order.total") {
		t.Errorf("named: exit %d %s", r.Code, r.Stderr)
	}
}

func TestExportJSON(t *testing.T) {
	s := exportTSite(t)
	r := cmdTRun(t, s, "--json", "export", "-d", "Order", "--fields", "customer,took,items.qty,taxes", "--page-size", "2")
	if r.Code != 0 {
		t.Fatalf("exit %d %s", r.Code, r.Stderr)
	}
	var docs []map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &docs); err != nil {
		t.Fatalf("%v\n%s", err, r.Stdout)
	}
	if len(docs) != 3 {
		t.Fatalf("%d documents", len(docs))
	}
	// Raw values (no Duration text, no formula escaping), rows nested
	// without their identity columns.
	b, _ := json.Marshal(docs[0])
	want := `{"customer":"=cmd","items":[{"name":"I-1","qty":1},{"name":"I-2","qty":2},{"name":"I-3","qty":3}],` +
		`"name":"O-1","taxes":[{"name":"T-1","rate":5}],"took":93784}`
	if string(b) != want {
		t.Errorf("doc = %s", b)
	}
	if items := docs[1]["items"].([]interface{}); len(items) != 0 {
		t.Errorf("O-2 items = %v", items)
	}

	r = cmdTRun(t, s, "--output", "ndjson", "export", "-d", "Order", "--no-tables", "--fields", "customer")
	if r.Code != 0 || r.Stdout != "{\"customer\":\"=cmd\",\"name\":\"O-1\"}\n{\"customer\":\"Bob\",\"name\":\"O-2\"}\n{\"customer\":\"@x\",\"name\":\"O-3\"}\n" {
		t.Errorf("ndjson: exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	// Nothing matches: an empty array, and for CSV the header.
	r = cmdTRun(t, s, "--json", "export", "-d", "Order", "--filters", `{"customer":"none"}`)
	if r.Code != 0 || r.Stdout != "[]\n" {
		t.Errorf("empty json: %q", r.Stdout)
	}
	r = cmdTRun(t, s, "export", "-d", "Order", "--filters", `{"customer":"none"}`, "--no-tables")
	if r.Code != 0 || r.Stdout != "name,customer,total,took,notes\n" {
		t.Errorf("empty csv: %q", r.Stdout)
	}
}

func TestExportOutputFile(t *testing.T) {
	s := exportTSite(t)
	cfg := fakeConfig(t, s, "apikey")
	out := filepath.Join(t.TempDir(), "orders.csv")
	r := runFFC(t, cfg, "", "export", "-d", "Order", "-o", out)
	got, _ := os.ReadFile(out)
	if r.Code != 0 || string(got) != exportTCSV || r.Stdout != "" || !strings.Contains(r.Stderr, "Exported 3 documents (6 rows)") {
		t.Fatalf("exit %d %s\nstdout %q\nfile %q", r.Code, r.Stderr, r.Stdout, got)
	}
	// An existing file is kept without --force (checked before any
	// request), replaced with it.
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(s.Requests())
	r = runFFC(t, cfg, "", "export", "-d", "Order", "-o", out)
	if got, _ := os.ReadFile(out); r.Code != exitUsage || string(got) != "old" || !strings.Contains(r.Stderr, "--force") {
		t.Errorf("no --force: exit %d %s, file %q", r.Code, r.Stderr, got)
	}
	if len(s.Requests()) != before {
		t.Error("a refused output file still sent requests")
	}
	r = runFFC(t, cfg, "", "export", "-d", "Order", "-o", out, "--force", "--no-tables", "--fields", "customer")
	if got, _ := os.ReadFile(out); r.Code != 0 || string(got) != "name,customer\nO-1,'=cmd\nO-2,Bob\nO-3,'@x\n" {
		t.Errorf("--force: exit %d %s, file %q", r.Code, r.Stderr, got)
	}
	// A failing page leaves the old file and no temporary file behind.
	s.Handle("POST /api/method/frappe.client.get_list", frappetest.ErrorHandler(frappetest.Permission("no rows for you")))
	r = runFFC(t, cfg, "", "export", "-d", "Order", "-o", out, "--force")
	if got, _ := os.ReadFile(out); r.Code != exitPermission || string(got) != "name,customer\nO-1,'=cmd\nO-2,Bob\nO-3,'@x\n" {
		t.Errorf("failed page: exit %d %s, file %q", r.Code, r.Stderr, got)
	}
	entries, _ := os.ReadDir(filepath.Dir(out))
	if len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
}

// exportTWorkbook answers download_template with a fake workbook and
// records the request body.
func exportTWorkbook(t *testing.T, s *frappetest.Site) *map[string]interface{} {
	t.Helper()
	got := &map[string]interface{}{}
	s.Handle("POST /api/method/frappe.core.doctype.data_import.data_import.download_template",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, got); err != nil {
				t.Errorf("template body %q: %v", b, err)
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="Order.xlsx"`)
			_, _ = w.Write([]byte("PK\x03\x04workbook\x00"))
		}))
	return got
}

func TestExportXLSX(t *testing.T) {
	s := exportTSite(t)
	body := exportTWorkbook(t, s)
	cfg := fakeConfig(t, s, "apikey")

	// Never to a terminal, refused before any request.
	old := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	r := runFFC(t, cfg, "", "export", "-d", "Order", "--xlsx")
	stdoutIsTerminal = old
	if r.Code != exitUsage || !strings.Contains(r.Stderr, "binary") || len(s.Requests()) != 0 {
		t.Fatalf("terminal: exit %d %s, %d requests", r.Code, r.Stderr, len(s.Requests()))
	}

	out := filepath.Join(t.TempDir(), "orders.xlsx")
	r = runFFC(t, cfg, "", "export", "-d", "Order", "--xlsx", "-o", out, "--fields", "customer,items.qty", "--filters", `{"customer":"Bob"}`)
	got, _ := os.ReadFile(out)
	if r.Code != 0 || string(got) != "PK\x03\x04workbook\x00" {
		t.Fatalf("exit %d %s, file %q", r.Code, r.Stderr, got)
	}
	b, _ := json.Marshal(*body)
	want := `{"doctype":"Order","export_fields":{"Order":["name","customer"],"items":["name","qty"]},` +
		`"export_filters":{"customer":"Bob"},"export_records":"by_filter","file_type":"Excel"}`
	if string(b) != want {
		t.Errorf("request = %s", b)
	}
	// To a pipe, the bytes as sent; filters default to {}.
	r = runFFC(t, cfg, "", "export", "-d", "Order", "--xlsx", "--no-tables")
	if r.Code != 0 || r.Stdout != "PK\x03\x04workbook\x00" {
		t.Errorf("pipe: exit %d %s %q", r.Code, r.Stderr, r.Stdout)
	}
	if f, _ := json.Marshal((*body)["export_filters"]); string(f) != "{}" {
		t.Errorf("default filters %s", f)
	}
	for _, args := range [][]string{{"--json"}, {"--output", "csv"}, {"--limit", "5"}, {"--order-by", "name"}} {
		r := runFFC(t, cfg, "", append([]string{"export", "-d", "Order", "--xlsx", "-o", out + "2"}, args...)...)
		if r.Code != exitUsage {
			t.Errorf("%v: exit %d %s", args, r.Code, r.Stderr)
		}
	}

	// An answer that is not a workbook is never saved.
	s.Handle("POST /api/method/frappe.core.doctype.data_import.data_import.download_template",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>login</html>")) }))
	r = runFFC(t, cfg, "", "export", "-d", "Order", "--xlsx", "-o", out+"3")
	if _, err := os.Stat(out + "3"); r.Code == 0 || err == nil || !strings.Contains(r.Stderr, "not an Excel workbook") {
		t.Errorf("not a workbook: exit %d %s", r.Code, r.Stderr)
	}
	// Frappe's refusal (no export permission) is exit 5.
	s.Handle("POST /api/method/frappe.core.doctype.data_import.data_import.download_template",
		frappetest.ErrorHandler(frappetest.Permission("You are not allowed to export Order doctype")))
	r = runFFC(t, cfg, "", "export", "-d", "Order", "--xlsx", "-o", out+"4")
	if r.Code != exitPermission {
		t.Errorf("refused: exit %d %s", r.Code, r.Stderr)
	}
}

func TestImportTemplate(t *testing.T) {
	s := exportTSite(t)
	r := cmdTRun(t, s, "import-template", "-d", "Order")
	if r.Code != 0 || r.Stdout != "name,customer,total,took,notes,items.name,items.item,items.qty,taxes.name,taxes.rate\n" {
		t.Errorf("exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	if n := len(s.RequestsTo("GET", "/api/resource/Order")); n != 0 {
		t.Errorf("%d list requests for a template", n)
	}
	r = cmdTRun(t, s, "import-template", "-d", "Order", "--fields", "customer", "--no-tables")
	if r.Code != 0 || r.Stdout != "name,customer\n" {
		t.Errorf("--fields: exit %d %s\n%s", r.Code, r.Stderr, r.Stdout)
	}
	for _, args := range [][]string{{"--output", "json"}, {"--fields", "nope"}} {
		if r := cmdTRun(t, s, append([]string{"import-template", "-d", "Order"}, args...)...); r.Code != exitUsage {
			t.Errorf("%v: exit %d %s", args, r.Code, r.Stderr)
		}
	}

	body := exportTWorkbook(t, s)
	out := filepath.Join(t.TempDir(), "t.xlsx")
	r = cmdTRun(t, s, "import-template", "-d", "Order", "--xlsx", "-o", out, "--tables", "taxes")
	got, _ := os.ReadFile(out)
	if r.Code != 0 || !strings.HasPrefix(string(got), "PK") {
		t.Fatalf("xlsx: exit %d %s", r.Code, r.Stderr)
	}
	b, _ := json.Marshal(*body)
	want := `{"doctype":"Order","export_fields":{"Order":["name","customer","total","took","notes"],"taxes":["name","rate"]},` +
		`"export_records":"blank_template","file_type":"Excel"}`
	if string(b) != want {
		t.Errorf("request = %s", b)
	}
}

func TestFormatDuration(t *testing.T) {
	for _, c := range []struct {
		in   interface{}
		hide bool
		want string
	}{
		{json.Number("12885"), false, "3h 34m 45s"},
		{json.Number("-12885"), false, "-3h 34m 45s"},
		{json.Number("93784"), false, "1d 2h 3m 4s"},
		{json.Number("93784"), true, "26h 3m 4s"},
		{json.Number("86400"), false, "1d"},
		{json.Number("59.9"), false, "59s"},
		{json.Number("-0.5"), false, ""},
		{json.Number("0"), false, ""},
		{nil, false, ""},
		{"abc", false, ""},
	} {
		if got := formatDuration(c.in, c.hide); got != c.want {
			t.Errorf("formatDuration(%v, %v) = %q, want %q", c.in, c.hide, got, c.want)
		}
	}
}

func TestEscapeFormula(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+33": "'+33", "-5": "'-5", "@SUM": "'@SUM", "\tx": "'\tx", "\rx": "'\rx",
		"a=b": "a=b", "": "", "'=x": "'=x", " =x": " =x",
	} {
		if got := escapeFormula(in); got != want {
			t.Errorf("escapeFormula(%q) = %q, want %q", in, got, want)
		}
	}
	// Numbers are no text: a negative number is not escaped.
	if got := exportCell(exportColumn{field: "n", fieldtype: "Int"}, json.Number("-5")); got != "-5" {
		t.Errorf("number cell = %q", got)
	}
}
