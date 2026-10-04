package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// outputSite has seven ToDos t0..t6 with a Currency-like amount.
func outputSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	for i := 0; i < 7; i++ {
		s.Add("ToDo", map[string]interface{}{"name": fmt.Sprintf("t%d", i), "amount": json.Number(fmt.Sprintf("%d.0", i*1000)), "status": "Open"})
	}
	return s
}

func TestOutputFormats(t *testing.T) {
	s := outputSite(t)
	args := []string{"list-docs", "-d", "ToDo", "--fields", "name,amount", "-o", "name asc", "-l", "2"}
	want := map[string]string{
		"csv":    "name,amount\nt0,0.0\nt1,1000.0\n",
		"tsv":    "name\tamount\nt0\t0.0\nt1\t1000.0\n",
		"ndjson": "{\"amount\":0.0,\"name\":\"t0\"}\n{\"amount\":1000.0,\"name\":\"t1\"}\n",
		"yaml":   "- amount: 0.0\n  name: t0\n- amount: 1000.0\n  name: t1\n",
	}
	for f, w := range want {
		r := cmdTOK(t, cmdTRun(t, s, append(args, "--output", f)...))
		if r.Stdout != w {
			t.Errorf("%s:\n got %q\nwant %q", f, r.Stdout, w)
		}
	}
	// --json is unchanged, and --output json is the same.
	a := cmdTOK(t, cmdTRun(t, s, append(args, "--json")...))
	b := cmdTOK(t, cmdTRun(t, s, append(args, "--output", "json")...))
	if a.Stdout != b.Stdout || !strings.Contains(a.Stdout, `"amount": 1000.0`) {
		t.Errorf("--json %q vs --output json %q", a.Stdout, b.Stdout)
	}

	// A single document, a count.
	r := cmdTOK(t, cmdTRun(t, s, "get-doc", "-d", "ToDo", "-n", "t2", "--fields", "name,amount", "--output", "csv"))
	if r.Stdout != "name,amount\nt2,2000.0\n" {
		t.Errorf("get-doc csv %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "count-docs", "-d", "ToDo", "--output", "yaml"))
	if r.Stdout != "count: 7\ndoctype: ToDo\n" {
		t.Errorf("count-docs yaml %q", r.Stdout)
	}
}

func TestOutputEnvAndConflicts(t *testing.T) {
	s := outputSite(t)
	t.Setenv("FFC_OUTPUT", "ndjson")
	r := cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "-l", "1", "-o", "name asc"))
	if r.Stdout != "{\"name\":\"t0\"}\n" {
		t.Errorf("FFC_OUTPUT: %q", r.Stdout)
	}
	// The flag wins over the variable; machine formats report JSON errors.
	r = cmdTRun(t, s, "get-doc", "-d", "ToDo", "-n", "missing")
	if r.Code != exitNotFound || !strings.HasPrefix(r.Stderr, `{"error":`) {
		t.Errorf("ndjson error: %d %q", r.Code, r.Stderr)
	}
	r = cmdTOK(t, cmdTRun(t, s, "count-docs", "-d", "ToDo", "--output", "table"))
	if strings.TrimSpace(r.Stdout) != "7" {
		t.Errorf("--output table: %q", r.Stdout)
	}

	before := len(s.Requests())
	for env, args := range map[string][]string{
		"xml":    {"list-docs", "-d", "ToDo"},
		"ndjson": {"create-doc", "-d", "ToDo", "--data", `{"x":1}`, "--output", "xml"},
		"":       {"create-doc", "-d", "ToDo", "--data", `{"x":1}`, "--json", "--output", "csv"},
	} {
		t.Setenv("FFC_OUTPUT", env)
		if r := cmdTRun(t, s, args...); r.Code != exitUsage {
			t.Errorf("FFC_OUTPUT=%q %v: exit %d (%v)", env, args, r.Code, r.Err)
		}
	}
	t.Setenv("FFC_OUTPUT", "")
	if r := cmdTRun(t, s, "create-doc", "-d", "ToDo", "--data", `{"x":1}`, "--jq", ".["); r.Code != exitUsage {
		t.Errorf("bad --jq: exit %d", r.Code)
	}
	if n := len(s.Requests()) - before; n != 0 {
		t.Errorf("%d requests sent despite invalid output options", n)
	}
}

func TestOutputJQ(t *testing.T) {
	s := outputSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "-o", "name asc", "-l", "3", "--jq", ".[].name"))
	if r.Stdout != "t0\nt1\nt2\n" {
		t.Errorf("raw strings: %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--fields", "name,amount", "-o", "name asc", "-l", "3", "--jq", "map(select(.amount > 500)) | .[0]"))
	if r.Stdout != "{\n  \"amount\": 1000.0,\n  \"name\": \"t1\"\n}\n" {
		t.Errorf("object: %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "-o", "name asc", "-l", "2", "--jq", ".[].name", "--output", "json"))
	if r.Stdout != "[\n  \"t0\",\n  \"t1\"\n]\n" {
		t.Errorf("jq + json: %q", r.Stdout)
	}
	if r := cmdTRun(t, s, "count-docs", "-d", "ToDo", "--jq", "error(\"boom\")"); r.Code != exitGeneric || !strings.Contains(r.Stderr, "boom") {
		t.Errorf("jq error: %d %q", r.Code, r.Stderr)
	}
}

func TestListAll(t *testing.T) {
	s := outputSite(t)
	r := cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--all", "--page-size", "3", "--output", "ndjson"))
	if n := strings.Count(r.Stdout, "\n"); n != 7 {
		t.Fatalf("%d rows: %q", n, r.Stdout)
	}
	req := s.RequestsTo("GET", "/api/resource/ToDo")
	if len(req) != 3 || req[2].Query.Get("limit_start") != "6" || req[0].Query.Get("order_by") != "creation asc, name asc" {
		t.Fatalf("requests = %+v", req)
	}

	// The JSON array is streamed with brackets; --order-by is kept.
	r = cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--all", "--page-size", "4", "--json", "-o", "name desc"))
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &rows); err != nil || len(rows) != 7 || rows[0]["name"] != "t6" {
		t.Fatalf("json: %v %q", err, r.Stdout)
	}
	// The table and --jq see the whole list.
	r = cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--all", "--page-size", "2", "--jq", "length"))
	if strings.TrimSpace(r.Stdout) != "7" {
		t.Fatalf("jq: %q", r.Stdout)
	}
	r = cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--all", "--page-size", "5"))
	if !strings.Contains(r.Stdout, "t6") || !strings.Contains(r.Stdout, "t0") {
		t.Fatalf("table: %q", r.Stdout)
	}

	for _, args := range [][]string{
		{"list-docs", "-d", "ToDo", "--all", "--limit", "5"},
		{"list-docs", "-d", "ToDo", "--all", "--start", "5"},
		{"list-docs", "-d", "ToDo", "--all", "--page-size", "0"},
		{"list-doctypes", "--all", "--limit", "5"},
	} {
		if r := cmdTRun(t, s, args...); r.Code != exitUsage {
			t.Errorf("%v: exit %d", args, r.Code)
		}
	}
}

func TestListAllEmptyAndError(t *testing.T) {
	s := frappetest.New(t)
	s.AddDocType("ToDo")
	r := cmdTOK(t, cmdTRun(t, s, "list-docs", "-d", "ToDo", "--all", "--json"))
	if r.Stdout != "[]\n" {
		t.Errorf("empty: %q", r.Stdout)
	}
	s.Handle("GET /api/resource/ToDo", frappetest.ErrorHandler(frappetest.Permission("no")))
	if r := cmdTRun(t, s, "list-docs", "-d", "ToDo", "--all", "--output", "csv"); r.Code != exitPermission {
		t.Errorf("error: exit %d", r.Code)
	}
}

func TestJSONFlagsFromFiles(t *testing.T) {
	s := outputSite(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "data.json")
	if err := os.WriteFile(data, []byte(`{"name":"from-file","n":12345678901234567890}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmdTOK(t, cmdTRun(t, s, "create-doc", "-d", "ToDo", "--data", "@"+data))
	req := s.RequestsTo("POST", "/api/resource/ToDo")
	if len(req) != 1 || !strings.Contains(req[0].Body, `"n":12345678901234567890`) {
		t.Fatalf("create from file: %+v", req)
	}

	r := cmdTOK(t, cmdTRunStdin(t, s, `{"name":"t3"}`, "list-docs", "-d", "ToDo", "--filters", "@-", "--output", "csv"))
	if r.Stdout != "name\nt3\n" {
		t.Errorf("filters from stdin: %q", r.Stdout)
	}
	cmdTOK(t, cmdTRunStdin(t, s, `[{"name":"b1"},{"name":"b2"}]`, "bulk-create", "-d", "ToDo", "--data", "@-"))
	if _, ok := s.Doc("ToDo", "b2"); !ok {
		t.Error("bulk-create --data @- did not create b2")
	}
	for _, args := range [][]string{
		{"create-doc", "-d", "ToDo", "--data", "@"},
		{"create-doc", "-d", "ToDo", "--data", "@" + filepath.Join(dir, "missing.json")},
		{"count-docs", "-d", "ToDo", "--filters", "@"},
	} {
		if r := cmdTRun(t, s, args...); r.Err == nil {
			t.Errorf("%v: want error", args)
		}
	}
}

func TestBulkReportFormats(t *testing.T) {
	s := outputSite(t)
	r := cmdTRun(t, s, "bulk-delete", "-d", "ToDo", "--names", "t0,missing", "--yes", "--output", "ndjson")
	if r.Code != exitPartial || !strings.Contains(r.Stdout, `"failed":1`) {
		t.Errorf("bulk ndjson: %d %q", r.Code, r.Stdout)
	}
}
