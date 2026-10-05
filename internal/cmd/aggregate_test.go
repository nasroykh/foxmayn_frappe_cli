package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// aggTSite has seven ToDos over three statuses, with hours to sum.
func aggTSite(t *testing.T, frappeVersion string) *frappetest.Site {
	t.Helper()
	s := frappetest.New(t)
	if frappeVersion != "" {
		s.SetApps(map[string]frappetest.App{"frappe": {Title: "Frappe Framework", Version: frappeVersion}})
	} else {
		s.SetApps(map[string]frappetest.App{}) // get_versions answers nothing usable
	}
	docs := []struct {
		status, owner string
		hours         string
	}{
		{"Open", "a@x.com", "1.5"}, {"Open", "b@x.com", "2"}, {"Open", "a@x.com", "0.5"}, {"Open", "Administrator", "1"},
		{"Closed", "a@x.com", "3"}, {"Closed", "b@x.com", "4"}, {"Cancelled", "a@x.com", "10"},
	}
	for i, d := range docs {
		s.Add("ToDo", map[string]interface{}{"name": fmt.Sprintf("TD-%d", i+1), "status": d.status, "owner": d.owner,
			"hours": json.Number(d.hours), "date": fmt.Sprintf("2026-01-0%d", i+1)})
	}
	return s
}

// aggTFields decodes the fields of the last aggregate list request.
func aggTLast(t *testing.T, s *frappetest.Site) (fields []interface{}, groupBy, orderBy, limit string) {
	t.Helper()
	reqs := s.RequestsTo("GET", "/api/resource/ToDo")
	if len(reqs) == 0 {
		t.Fatal("no list request")
	}
	q := reqs[len(reqs)-1].Query
	if err := json.Unmarshal([]byte(q.Get("fields")), &fields); err != nil {
		t.Fatalf("fields %q: %v", q.Get("fields"), err)
	}
	return fields, q.Get("group_by"), q.Get("order_by"), q.Get("limit_page_length")
}

func TestCmdAggregateV16Dict(t *testing.T) {
	s := aggTSite(t, "16.36.1")
	r := cmdTOK(t, cmdTRun(t, s, "--json", "aggregate", "-d", "ToDo", "--group-by", "status", "--sum", "hours", "--count"))
	rows := cmdTRows(t, r)
	if len(rows) != 3 {
		t.Fatalf("rows %v", rows)
	}
	// Sorted by the first aggregate (count), largest first.
	if rows[0]["status"] != "Open" || rows[0]["count"] != float64(4) || rows[0]["sum_hours"] != float64(5.0) {
		t.Errorf("first row %v", rows[0])
	}
	// The decimal literal reaches stdout unchanged.
	cmdTHas(t, r.Stdout, `"sum_hours": 5.0`, `"sum_hours": 10.0`)
	fields, groupBy, orderBy, limit := aggTLast(t, s)
	want := `["status",{"COUNT":"*","as":"count"},{"SUM":"hours","as":"sum_hours"}]`
	if b, _ := json.Marshal(fields); string(b) != want {
		t.Errorf("fields %s, want %s", b, want)
	}
	if groupBy != "status" || orderBy != "count desc, status asc" || limit != "101" {
		t.Errorf("group_by %q order_by %q limit %q", groupBy, orderBy, limit)
	}
	if n := len(s.RequestsTo("GET", "/api/resource/ToDo")); n != 1 {
		t.Errorf("%d list requests, want 1", n)
	}
}

func TestCmdAggregateV15String(t *testing.T) {
	s := aggTSite(t, "15.121.3")
	r := cmdTOK(t, cmdTRun(t, s, "--json", "aggregate", "-d", "ToDo", "--group-by", "status",
		"--avg", "hours", "--min", "date", "--max", "date", "--order-by", "status asc"))
	rows := cmdTRows(t, r)
	if len(rows) != 3 || rows[0]["status"] != "Cancelled" || rows[0]["avg_hours"] != float64(10.0) ||
		rows[2]["min_date"] != "2026-01-01" || rows[2]["max_date"] != "2026-01-04" {
		t.Fatalf("rows %v", rows)
	}
	fields, groupBy, orderBy, _ := aggTLast(t, s)
	want := []interface{}{"`tabToDo`.`status`", "avg(`tabToDo`.`hours`) as avg_hours",
		"min(`tabToDo`.`date`) as min_date", "max(`tabToDo`.`date`) as max_date"}
	if fmt.Sprint(fields) != fmt.Sprint(want) {
		t.Errorf("fields %q", fields)
	}
	if groupBy != "`tabToDo`.`status`" || orderBy != "`tabToDo`.`status` asc" {
		t.Errorf("group_by %q order_by %q", groupBy, orderBy)
	}
	if n := len(s.RequestsTo("GET", "/api/resource/ToDo")); n != 1 {
		t.Errorf("%d list requests, want 1", n)
	}

	// A count on v15 counts the name column: count(*) fails its
	// reportview.validate_fields.
	cmdTOK(t, cmdTRun(t, s, "--json", "aggregate", "-d", "ToDo"))
	if fields, _, _, _ = aggTLast(t, s); fields[0] != "count(`tabToDo`.`name`) as count" {
		t.Errorf("count field %q", fields[0])
	}
}

// The fake fails like the real site for the other syntax.
func TestFakeAggregateWrongSyntax(t *testing.T) {
	for _, tc := range []struct {
		version, fields string
		status          int
		excType, msg    string
	}{
		{"16.36.1", `["status","count(name) as n"]`, 417, "ValidationError", "SQL functions are not allowed as strings in SELECT"},
		{"15.121.3", `["status",{"COUNT":"*","as":"n"}]`, 500, "TypeError", "unhashable type"},
	} {
		s := aggTSite(t, tc.version)
		r := cmdTRun(t, s, "--json", "api", "/api/resource/ToDo", "-f", "fields="+tc.fields, "-f", "group_by=status")
		if r.Err == nil {
			t.Fatalf("%s: no error: %s", tc.version, r.Stdout)
		}
		var api *client.APIError
		if !errors.As(r.Err, &api) || api.Status != tc.status || api.ExcType != tc.excType || !strings.Contains(api.Message, tc.msg) {
			t.Errorf("%s: %v", tc.version, r.Err)
		}
	}
}

func TestCmdAggregateUnknownVersionFallsBack(t *testing.T) {
	// No version: dict first; the site (old, as far as the fake knows)
	// refuses it with a TypeError, and the string syntax follows.
	s := aggTSite(t, "")
	r := cmdTOK(t, cmdTRun(t, s, "--json", "aggregate", "-d", "ToDo", "--group-by", "status"))
	if rows := cmdTRows(t, r); len(rows) != 3 || rows[0]["count"] != float64(4) {
		t.Fatalf("rows %v", rows)
	}
	reqs := s.RequestsTo("GET", "/api/resource/ToDo")
	if len(reqs) != 2 || !strings.Contains(reqs[0].Query.Get("fields"), `"COUNT"`) || !strings.Contains(reqs[1].Query.Get("fields"), "count(") {
		t.Fatalf("requests %v", reqs)
	}

	// When the other syntax fails for another reason, that error is the
	// user's: here an unknown field.
	r = cmdTRun(t, s, "aggregate", "-d", "ToDo", "--sum", "nope")
	cmdTFail(t, r, "Field not permitted in query: nope")
	if r.Code != exitValidation {
		t.Errorf("exit %d", r.Code)
	}
}

func TestRunAggregateStaleCache(t *testing.T) {
	site := aggTSite(t, "16.36.1")
	c, err := client.New(context.Background(), &config.SiteConfig{URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.SiteConfig{Name: "stale", URL: site.URL}
	writeServerCache(cfg, &client.ServerInfo{URL: site.URL, FetchedAt: time.Now(), Apps: map[string]client.AppVersion{"frappe": {Version: "15.0.0"}}})
	q, err := buildAggregateQuery(aggregateArgs{GroupBy: []string{"status"}}, "--")
	if err != nil {
		t.Fatal(err)
	}
	res, err := runAggregate(context.Background(), c, cfg, "ToDo", q)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 || res.Syntax != client.SyntaxDict {
		t.Errorf("result %+v", res)
	}
	if n := len(site.RequestsTo("GET", "/api/resource/ToDo")); n != 2 {
		t.Errorf("%d list requests, want 2 (string refused, then dict)", n)
	}
	if readServerCache(cfg, time.Now()) != nil {
		t.Error("the stale cache was kept")
	}

	// The right syntax's own error is reported, not the retry's refusal.
	writeServerCache(cfg, &client.ServerInfo{URL: site.URL, FetchedAt: time.Now(), Apps: map[string]client.AppVersion{"frappe": {Version: "16.36.1"}}})
	q.Filters = `{"nope":1}`
	_, err = runAggregate(context.Background(), c, cfg, "ToDo", q)
	if err == nil || !strings.Contains(err.Error(), "Field not permitted in query: nope") {
		t.Errorf("error %v", err)
	}
}

func TestCmdAggregateNoGroupAndTable(t *testing.T) {
	s := aggTSite(t, "16.36.1")
	r := cmdTOK(t, cmdTRun(t, s, "--json", "aggregate", "-d", "ToDo", "--sum", "hours", "--filters", `{"status":"Open"}`))
	if rows := cmdTRows(t, r); len(rows) != 1 || rows[0]["sum_hours"] != float64(5.0) {
		t.Fatalf("rows %v", rows)
	}
	if _, groupBy, orderBy, _ := aggTLast(t, s); groupBy != "" || orderBy != "sum_hours desc" {
		t.Errorf("group_by %q order_by %q", groupBy, orderBy)
	}

	r = cmdTOK(t, cmdTRun(t, s, "aggregate", "-d", "ToDo", "--group-by", "status,owner", "--max", "hours"))
	cmdTHas(t, r.Stdout, "STATUS", "OWNER", "MAX_HOURS", "Cancelled", "a@x.com")
	r = cmdTOK(t, cmdTRun(t, s, "aggregate", "-d", "ToDo", "--group-by", "status", "--output", "csv"))
	if got := strings.TrimSpace(r.Stdout); got != "status,count\nOpen,4\nClosed,2\nCancelled,1" {
		t.Errorf("csv %q", got)
	}
}

func TestCmdAggregateLimit(t *testing.T) {
	s := aggTSite(t, "16.36.1")
	r := cmdTOK(t, cmdTRun(t, s, "--json", "aggregate", "-d", "ToDo", "--group-by", "status", "--limit", "2"))
	if rows := cmdTRows(t, r); len(rows) != 2 {
		t.Fatalf("rows %v", rows)
	}
	cmdTHas(t, r.Stderr, "Showing the first 2 group(s)")
	if _, _, _, limit := aggTLast(t, s); limit != "3" {
		t.Errorf("limit %q", limit)
	}
	// Exactly the limit: no warning.
	r = cmdTOK(t, cmdTRun(t, s, "--json", "aggregate", "-d", "ToDo", "--group-by", "status", "--limit", "3"))
	if len(cmdTRows(t, r)) != 3 || strings.Contains(r.Stderr, "Showing") {
		t.Errorf("stdout %s stderr %s", r.Stdout, r.Stderr)
	}
	// 0 is every group.
	cmdTOK(t, cmdTRun(t, s, "--json", "aggregate", "-d", "ToDo", "--group-by", "status", "--limit", "0"))
	if _, _, _, limit := aggTLast(t, s); limit != "0" {
		t.Errorf("limit %q", limit)
	}
}

func TestCmdAggregateRefusesBadNames(t *testing.T) {
	s := aggTSite(t, "16.36.1")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--group-by", "customer.territory"}, "fields of a linked or child DocType are not supported"},
		{[]string{"--group-by", "status) or (1=1"}, "expected a field name"},
		{[]string{"--sum", "hours`"}, "expected a field name"},
		{[]string{"--sum", "1"}, "expected a field name"},
		{[]string{"--avg", "items.qty"}, "linked or child DocType"},
		{[]string{"--group-by", "status", "--order-by", "hours desc"}, `"hours" is not a group-by field or aggregate column`},
		{[]string{"--group-by", "status", "--order-by", "count sideways"}, "direction must be asc or desc"},
		{[]string{"--group-by", "status", "--order-by", "count desc; drop"}, "expected \"column [asc|desc]\""},
		{[]string{"--sum", "hours", "--sum", "hours"}, "--sum hours is given twice"},
		{[]string{"--group-by", "status,status"}, "given twice"},
		{[]string{"--group-by", "count", "--count"}, `clashes with --group-by count`},
		{[]string{"--group-by", "a,b,c,d,e,f"}, "at most 5"},
		{[]string{"--limit", "-1"}, "--limit must be >= 0"},
		{[]string{"--filters", "nope"}, "--filters"},
	} {
		r := cmdTRun(t, s, append([]string{"aggregate", "-d", "ToDo"}, tc.args...)...)
		cmdTFail(t, r, tc.want)
		if r.Code != exitUsage {
			t.Errorf("%v: exit %d", tc.args, r.Code)
		}
	}
	if n := len(s.RequestsTo("GET", "/api/resource/ToDo")); n != 0 {
		t.Errorf("%d requests reached the site", n)
	}
}

func TestCmdCountDocsGroupBy(t *testing.T) {
	s := aggTSite(t, "16.36.1")
	r := cmdTOK(t, cmdTRun(t, s, "--json", "count-docs", "-d", "ToDo", "--group-by", "status"))
	rows := cmdTRows(t, r)
	if len(rows) != 3 || rows[0]["status"] != "Open" || rows[0]["count"] != float64(4) || rows[2]["status"] != "Cancelled" {
		t.Fatalf("rows %v", rows)
	}
	req := s.RequestsTo("GET", "/api/method/frappe.desk.listview.get_group_by_count")
	if len(req) != 1 || req[0].Query.Get("current_filters") != "[]" || req[0].Query.Get("field") != "status" || req[0].Query.Get("doctype") != "ToDo" {
		t.Fatalf("request %+v", req)
	}

	// owner: the signed-in user's group comes first, whatever its size.
	r = cmdTOK(t, cmdTRun(t, s, "count-docs", "-d", "ToDo", "--group-by", "owner", "--filters", `{"status":["!=","Cancelled"]}`))
	cmdTHas(t, r.Stdout, "OWNER", "COUNT")
	if i, j := strings.Index(r.Stdout, frappetest.Username), strings.Index(r.Stdout, "a@x.com"); i < 0 || j < 0 || i > j {
		t.Errorf("own group not first:\n%s", r.Stdout)
	}
	if last := s.RequestsTo("GET", "/api/method/frappe.desk.listview.get_group_by_count"); last[len(last)-1].Query.Get("current_filters") != `{"status":["!=","Cancelled"]}` {
		t.Errorf("filters %q", last[len(last)-1].Query.Get("current_filters"))
	}

	// assigned_to counts assignments, not a field.
	s.Add("ToDo", map[string]interface{}{"name": "A-1", "status": "Open", "allocated_to": "z@x.com", "reference_name": "TD-1"},
		map[string]interface{}{"name": "A-2", "status": "Cancelled", "allocated_to": "y@x.com", "reference_name": "TD-2"})
	r = cmdTOK(t, cmdTRun(t, s, "--json", "count-docs", "-d", "ToDo", "--group-by", "assigned_to"))
	if rows = cmdTRows(t, r); len(rows) != 1 || rows[0]["assigned_to"] != "z@x.com" || rows[0]["count"] != float64(1) {
		t.Errorf("assigned_to rows %v", rows)
	}

	// A field the DocType lacks: the site's bare ValueError becomes a
	// validation error.
	r = cmdTRun(t, s, "count-docs", "-d", "ToDo", "--group-by", "nope")
	cmdTFail(t, r, `"nope" is not a field of ToDo`)
	if r.Code != exitValidation {
		t.Errorf("exit %d", r.Code)
	}
	r = cmdTRun(t, s, "count-docs", "-d", "ToDo", "--group-by", "owner.full_name")
	if cmdTFail(t, r, "linked or child DocType"); r.Code != exitUsage {
		t.Errorf("exit %d", r.Code)
	}

	// Without --group-by nothing changes.
	r = cmdTOK(t, cmdTRun(t, s, "count-docs", "-d", "ToDo"))
	if strings.TrimSpace(r.Stdout) != "9" {
		t.Errorf("count %q", r.Stdout)
	}
}

func TestCmdCountDocsGroupByCap(t *testing.T) {
	s := frappetest.New(t)
	for i := 0; i < 60; i++ {
		s.Add("Note", map[string]interface{}{"name": fmt.Sprintf("N-%02d", i), "title": fmt.Sprintf("t%02d", i)})
	}
	r := cmdTOK(t, cmdTRun(t, s, "--json", "count-docs", "-d", "Note", "--group-by", "title"))
	if rows := cmdTRows(t, r); len(rows) != 50 {
		t.Fatalf("%d rows", len(rows))
	}
	cmdTHas(t, r.Stderr, "at most 50 groups", `ffc aggregate -d "Note" --group-by title`)
	// A field named title would clash with the title column: the group
	// keeps the site's "name".
	if rows := cmdTRows(t, r); rows[0]["name"] == nil || rows[0]["title"] != nil {
		t.Errorf("row %v", rows[0])
	}
}

func TestMCPAggregate(t *testing.T) {
	s, site := newMCPFake(t, true)
	for i, st := range []string{"Open", "Open", "Closed"} {
		site.Add("ToDo", map[string]interface{}{"name": fmt.Sprintf("TD-%d", i), "status": st, "hours": json.Number("2")})
	}
	out := mcpTOK(t, s, "aggregate", map[string]interface{}{"doctype": "ToDo", "group_by": "status", "sum": []interface{}{"hours"}, "count": true})
	var res struct {
		Doctype   string                   `json:"doctype"`
		Rows      []map[string]interface{} `json:"rows"`
		Truncated bool                     `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Doctype != "ToDo" || res.Truncated || len(res.Rows) != 2 || res.Rows[0]["status"] != "Open" || res.Rows[0]["sum_hours"] != 4.0 {
		t.Fatalf("result %s", out)
	}
	cmdTHas(t, out, `"sum_hours":4.0`) // the literal survives

	out = mcpTOK(t, s, "aggregate", map[string]interface{}{"doctype": "ToDo", "group_by": []interface{}{"status"}, "limit": 1})
	if !strings.Contains(out, `"truncated":true`) {
		t.Errorf("limit 1: %s", out)
	}

	n := len(site.Requests())
	mcpTErr(t, s, "aggregate", map[string]interface{}{"doctype": "ToDo", "group_by": "owner.full_name"}, "group_by \"owner.full_name\": fields of a linked or child DocType are not supported")
	mcpTErr(t, s, "aggregate", map[string]interface{}{"doctype": "ToDo", "sum": "hours) from tabUser --"}, "expected a field name")
	mcpTErr(t, s, "aggregate", map[string]interface{}{"doctype": "ToDo", "order_by": "hours desc"}, "order_by")
	mcpTErr(t, s, "aggregate", map[string]interface{}{"doctype": "ToDo", "limit": 0}, "limit: must be between 1 and 1000")
	if len(site.Requests()) != n {
		t.Errorf("a refused call reached the site")
	}
}

func TestMCPAggregatePolicy(t *testing.T) {
	s, site, _, _ := mcpTPolicy(t, &config.MCPPolicy{DenyDoctypes: []string{"ToDo"}}, config.MCPPolicy{})
	n := len(site.Requests())
	mcpTErr(t, s, "aggregate", map[string]interface{}{"doctype": "ToDo", "group_by": "status"}, `DocType "ToDo" is denied`)
	if len(site.Requests()) != n {
		t.Error("a denied call reached the site")
	}
	mcpTOK(t, s, "aggregate", map[string]interface{}{"doctype": "User"})
}
