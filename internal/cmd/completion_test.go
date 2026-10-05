package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// complTSite is a fake site with DocTypes, reports and a schema that has a
// layout field and a child table.
func complTSite(t *testing.T) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	s.AddDocType("DocType", "is_submittable", "is_tree", "description")
	s.AddDocType("Report", "report_type", "is_standard", "module")
	s.Add("DocType",
		map[string]interface{}{"name": "Sales Invoice", "module": "Accounts"},
		map[string]interface{}{"name": "Sales Order", "module": "Selling"},
		map[string]interface{}{"name": "ToDo", "module": "Desk"},
		map[string]interface{}{"name": "Evil\x1b[31m", "module": "Desk"},
		map[string]interface{}{
			"name": "Ticket", "module": "Support",
			"fields": []interface{}{
				map[string]interface{}{"fieldname": "title", "label": "Title", "fieldtype": "Data"},
				map[string]interface{}{"fieldname": "sb", "fieldtype": "Section Break"},
				map[string]interface{}{"fieldname": "status", "label": "Status", "fieldtype": "Select"},
				map[string]interface{}{"fieldname": "items", "label": "Items", "fieldtype": "Table", "options": "Ticket Item"},
			},
		})
	s.Add("Report",
		map[string]interface{}{"name": "General Ledger", "ref_doctype": "GL Entry"},
		map[string]interface{}{"name": "Gross Profit", "ref_doctype": "Sales Invoice"})
	s.Add("Custom Field")
	s.Add("Property Setter")
	return s
}

// complT runs `ffc __complete <args>` against cfg and returns the offered
// values and the directive line. --config goes after __complete, where a
// shell puts it.
func complT(t *testing.T, cfg string, args ...string) ([]string, string) {
	t.Helper()
	r := runFFC(t, "", "", append([]string{"__complete", "--config", cfg}, args...)...)
	if r.Err != nil {
		t.Fatalf("__complete %v: %v\n%s", args, r.Err, r.Stderr)
	}
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, ":") {
		t.Fatalf("no directive in %q", r.Stdout)
	}
	var vals []string
	for _, l := range lines[:len(lines)-1] {
		vals = append(vals, strings.SplitN(l, "\t", 2)[0])
	}
	return vals, last
}

func complTEq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("completions %q, want %q", got, want)
	}
}

// TestCompletionNeverTouchesTheSite: with no cache, nothing is offered and
// nothing is sent, not even a login on a password site; once a command has
// filled the cache, Tab offers its names, still without a request.
func TestCompletionNeverTouchesTheSite(t *testing.T) {
	cacheTEnv(t)
	s := complTSite(t)
	cfg := fakeConfig(t, s, "password")

	for _, args := range [][]string{
		{"get-schema", "-d", ""},
		{"list-docs", "-d", "Ticket", "--fields", ""},
		{"run-report", "-n", ""},
		{"cache", "warm", "--doctypes", ""},
	} {
		vals, dir := complT(t, cfg, args...)
		complTEq(t, vals)
		if dir != ":4" && dir != ":6" {
			t.Errorf("%v: directive %s, want no file completion", args, dir)
		}
	}
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("completion sent %d requests: %+v", n, s.Requests())
	}
	// The cache dir is not created either.
	if _, err := os.Stat(filepath.Join(mustCacheBase(t), "ffc")); !os.IsNotExist(err) {
		t.Errorf("completion created the cache dir: %v", err)
	}

	cmdTOK(t, runFFC(t, cfg, "", "list-doctypes", "--limit", "0"))
	cmdTOK(t, runFFC(t, cfg, "", "list-reports", "--all"))
	cmdTOK(t, runFFC(t, cfg, "", "get-schema", "-d", "Ticket"))
	before := len(s.Requests())

	vals, dir := complT(t, cfg, "list-docs", "-d", "Sa")
	complTEq(t, vals, "Sales Invoice", "Sales Order")
	if dir != ":4" {
		t.Errorf("directive %s", dir)
	}
	vals, _ = complT(t, cfg, "get-schema", "--doctype", "")
	complTEq(t, vals, "Sales Invoice", "Sales Order", "Ticket", "ToDo") // the name with an escape is never offered
	vals, _ = complT(t, cfg, "run-report", "-n", "G")
	complTEq(t, vals, "General Ledger", "Gross Profit")
	vals, _ = complT(t, cfg, "bulk-delete", "-d", "to")
	complTEq(t, vals, "ToDo")

	// --fields: the last item, the prefix kept, given items and layout
	// fields left out; a list cannot select a child table, get-doc can.
	vals, dir = complT(t, cfg, "list-docs", "-d", "Ticket", "--fields", "title,")
	complTEq(t, vals, "title,status", "title,name", "title,owner", "title,creation", "title,modified", "title,modified_by", "title,docstatus", "title,idx")
	if dir != ":6" {
		t.Errorf("--fields directive %s, want NoSpace|NoFileComp", dir)
	}
	vals, _ = complT(t, cfg, "get-doc", "-d", "Ticket", "-f", "i")
	complTEq(t, vals, "items", "idx")
	vals, _ = complT(t, cfg, "list-docs", "-d", "Ticket", "--fields", `["ti`)
	complTEq(t, vals)
	vals, _ = complT(t, cfg, "list-docs", "--fields", "ti") // no --doctype
	complTEq(t, vals)
	// -n is a document name: never completed.
	vals, _ = complT(t, cfg, "get-doc", "-d", "ToDo", "-n", "")
	complTEq(t, vals)
	vals, _ = complT(t, cfg, "cache", "warm", "--doctypes", "ToDo,Sales I")
	complTEq(t, vals, "ToDo,Sales Invoice")

	if n := len(s.Requests()); n != before {
		t.Fatalf("completion sent %d requests: %+v", n-before, s.Requests()[before:])
	}
}

func mustCacheBase(t *testing.T) string {
	t.Helper()
	b, err := userCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestCompletionIgnoresAnExpiredOrForeignCache: an expired list, or one
// read from another URL, completes nothing (and is not refreshed).
func TestCompletionIgnoresAnExpiredOrForeignCache(t *testing.T) {
	cacheTEnv(t)
	s := complTSite(t)
	cfgPath := fakeConfig(t, s, "apikey")
	site, err := config.Load("t", cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := metaCachePath(site, doctypesCacheFile)
	write := func(url string, at time.Time) {
		b, _ := json.Marshal(listCache{URL: url, FetchedAt: at, Items: []cacheItem{{Name: "ToDo"}}})
		if err := config.WriteFileAtomic(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(s.URL, time.Now().Add(-doctypeCacheTTL-time.Minute))
	vals, _ := complT(t, cfgPath, "list-docs", "-d", "")
	complTEq(t, vals)
	write("http://elsewhere.example", time.Now())
	vals, _ = complT(t, cfgPath, "list-docs", "-d", "")
	complTEq(t, vals)
	write(s.URL, time.Now())
	vals, _ = complT(t, cfgPath, "list-docs", "-d", "")
	complTEq(t, vals, "ToDo")
	// Another site of the config has its own cache.
	vals, _ = complT(t, cfgPath, "--site", "other", "list-docs", "-d", "")
	complTEq(t, vals)
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("completion sent %d requests", n)
	}
}

func TestCompletionSitesAndEnums(t *testing.T) {
	cacheTEnv(t)
	s := frappetest.New(t)
	cfg := fakeConfig(t, s, "oauth")
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"--site", ""}, []string{"other", "t"}},
		{[]string{"list-docs", "-s", "o"}, []string{"other"}},
		{[]string{"site", "use", ""}, []string{"other", "t"}},
		{[]string{"site", "remove", "O"}, []string{"other"}},
		{[]string{"site", "edit", ""}, []string{"other", "t"}},
		{[]string{"site", "rename", ""}, []string{"other", "t"}},
		{[]string{"site", "rename", "t", ""}, nil},
		{[]string{"config", "set", "--default-site", "t"}, []string{"t"}},
		{[]string{"list-docs", "--output", ""}, []string{"table", "json", "ndjson", "csv", "tsv", "yaml"}},
		{[]string{"config", "set", "--number-format", ""}, []string{"french", "us", "german", "plain"}},
		{[]string{"config", "set", "--date-format", "dd"}, []string{"dd-mm-yyyy", "dd/mm/yyyy"}},
		{[]string{"can", "-d", "ToDo", "--perm", "s"}, []string{"select", "submit", "share"}},
		{[]string{"mcp", "--toolsets", ""}, []string{"core", "lifecycle", "collab", "admin"}},
		{[]string{"mcp", "--toolsets", "core,"}, []string{"core,lifecycle", "core,collab", "core,admin"}},
		{[]string{"assign", "-d", "ToDo", "-n", "x", "--priority", ""}, []string{"Low", "Medium", "High"}},
		{[]string{"mcp", "--confirm", ""}, []string{"always", "if-supported"}},
		{[]string{"mcp", "--sites", "t,"}, []string{"t,other"}},
		{[]string{"mcp", "--allow-tools", "get_s"}, []string{"get_schema"}},
	} {
		vals, _ := complT(t, cfg, c.args...)
		complTEq(t, vals, c.want...)
	}
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("completion sent %d requests", n)
	}
}
