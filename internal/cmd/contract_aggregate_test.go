//go:build contract

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractAggregate pins T2.3: which aggregate syntax each Frappe version
// takes in list fields (v16 refuses SQL functions as strings with a 417
// ValidationError, v15 a dict with a 500 TypeError), that ffc aggregate
// works on both, and get_group_by_count's arguments and {name, count} rows.
func contractAggregate(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	info, err := c.ServerVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	major := info.FrappeMajor()
	for _, d := range []map[string]interface{}{
		{"title": "aggregate", "status": "Open", "n_cur": 10, "n_int": 1},
		{"title": "aggregate", "status": "Open", "n_cur": 10, "n_int": 2},
		{"title": "aggregate", "status": "Closed", "n_cur": 5, "n_int": 3},
	} {
		createContractDoc(t, c, d)
	}
	filters := `{"title":"aggregate"}`
	q := client.AggregateQuery{
		GroupBy:    []string{"status"},
		Aggregates: []client.AggregateField{{Func: client.AggCount, Alias: "count"}, {Func: client.AggSum, Field: "n_cur", Alias: "sum_n_cur"}},
		Filters:    filters,
		OrderBy:    []client.OrderTerm{{Column: "count", Desc: true}},
	}

	// Each syntax on its own: accepted by its version, refused by the other
	// in the way client.SyntaxRejected recognises.
	for _, syntax := range []client.AggregateSyntax{client.SyntaxDict, client.SyntaxString} {
		rows, err := c.Aggregate(ctx, contractDT, q, syntax)
		want, _ := client.SyntaxFor(major)
		if syntax == want {
			if err != nil {
				t.Errorf("v%d %s syntax: %v", major, syntax, err)
				continue
			}
			contractAggRows(t, rows)
			continue
		}
		var api *client.APIError
		switch {
		case err == nil:
			t.Errorf("v%d accepted the %s syntax: %v", major, syntax, rows)
		case !client.SyntaxRejected(err, syntax):
			t.Errorf("v%d refused the %s syntax with %v (status/exc_type not recognised)", major, syntax, err)
		case errors.As(err, &api):
			t.Logf("v%d refuses the %s syntax: HTTP %d %s", major, syntax, api.Status, api.ExcType)
		}
	}

	cfg := contractConfig(t, sc)
	// list-docs passes fields through: a string aggregate works on v15 only.
	r := runFFC(t, cfg, "", "--json", "list-docs", "-d", contractDT, "--fields", `["status","count(name) as n"]`, "--filters", filters)
	if major >= 16 {
		if r.Code != exitValidation || !strings.Contains(r.Stderr, "ValidationError") {
			t.Errorf("v%d list-docs with a string aggregate: exit %d, %s", major, r.Code, r.Stderr)
		}
	} else if r.Err != nil {
		t.Errorf("v%d list-docs with a string aggregate: %v", major, r.Err)
	}

	// ffc aggregate picks the syntax itself.
	r = runFFC(t, cfg, "", "--json", "aggregate", "-d", contractDT, "--group-by", "status", "--count", "--sum", "n_cur", "--min", "n_int", "--filters", filters)
	var rows []map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(r.Stdout))
	dec.UseNumber()
	if r.Err != nil || dec.Decode(&rows) != nil {
		t.Fatalf("ffc aggregate: %v\n%s%s", r.Err, r.Stdout, r.Stderr)
	}
	contractAggRows(t, rows)
	if rows[0]["min_n_int"] != json.Number("1") {
		t.Errorf("min_n_int = %#v, want the Int literal 1", rows[0]["min_n_int"])
	}

	// get_group_by_count: current_filters is required, rows are
	// {name, count} most frequent first, a field the DocType lacks is a bare
	// ValueError (500).
	gc, err := c.GroupByCount(ctx, contractDT, filters, "status")
	if err != nil {
		t.Fatal(err)
	}
	if len(gc) != 2 || gc[0]["name"] != "Open" || gc[0]["count"] != json.Number("2") || gc[1]["name"] != "Closed" {
		t.Errorf("get_group_by_count = %v", gc)
	}
	for _, row := range gc {
		for k := range row {
			if k != "name" && k != "count" && k != "title" {
				t.Errorf("get_group_by_count row has %q: %v", k, row)
			}
		}
	}
	var api *client.APIError
	if _, err = c.GroupByCount(ctx, contractDT, filters, "no_such_field"); !errors.As(err, &api) || api.Status != http.StatusInternalServerError || api.ExcType != "ValueError" {
		t.Errorf("unknown field: %v, want 500 ValueError", err)
	}
	if _, err = c.CallMethod(ctx, "frappe.desk.listview.get_group_by_count", map[string]interface{}{"doctype": contractDT, "field": "status"}, true); err == nil {
		t.Error("get_group_by_count without current_filters succeeded; GroupByCount sends [] for nothing")
	}
	r = runFFC(t, cfg, "", "count-docs", "-d", contractDT, "--group-by", "no_such_field")
	if r.Code != exitValidation {
		t.Errorf("count-docs --group-by no_such_field: exit %d (%v)", r.Code, r.Err)
	}

	t.Run("permission level", func(t *testing.T) { contractAggregatePermlevel(t, c, sc) })
}

// contractPermDT has a field at permission level 1. (A DocType name
// v15's ORDER_GROUP_PATTERN would refuse once qualified cannot be created:
// DocType names are ASCII letters, digits, spaces, "_" and "-".)
const contractPermDT = "FFC Contract Perm"

// contractAggregatePermlevel pins what the fake models with Permlevel and
// DocPerm:
// a list query naming a field above the caller's permission level is a
// PermissionError on v16, while v15 drops it from fields without a word
// (reportview.validate_fields) and refuses it in group_by; ffc aggregate
// exits 5 on both.
func contractAggregatePermlevel(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	ctx := contractCtx(t)
	info, err := c.ServerVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	major := info.FrappeMajor()
	teardown := func() {
		tctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		d, err := c.GetDoc(tctx, "DocType", contractPermDT)
		if err != nil {
			return
		}
		if d["description"] != contractMarker {
			t.Fatalf("DocType %q exists but was not created by these tests; refusing to delete it", contractPermDT)
		}
		if rows, err := c.GetList(tctx, contractPermDT, client.ListOptions{Limit: -1}); err == nil {
			for _, r := range rows {
				_ = c.DeleteDoc(tctx, contractPermDT, fmt.Sprint(r["name"]))
			}
		}
		if err := c.DeleteDoc(tctx, "DocType", contractPermDT); err != nil {
			t.Logf("teardown: delete DocType %s: %v", contractPermDT, err)
		}
	}
	teardown()
	t.Cleanup(teardown)
	if _, err := c.CreateDoc(ctx, "DocType", map[string]interface{}{
		"name": contractPermDT, "module": "Custom", "custom": 1, "autoname": "hash", "description": contractMarker,
		"fields": []interface{}{
			map[string]interface{}{"fieldname": "grp", "label": "Group", "fieldtype": "Data"},
			map[string]interface{}{"fieldname": "secret", "label": "Secret", "fieldtype": "Int", "permlevel": 1},
		},
		"permissions": []interface{}{
			map[string]interface{}{"role": "System Manager", "permlevel": 0, "read": 1, "write": 1, "create": 1, "delete": 1},
			map[string]interface{}{"role": "System Manager", "permlevel": 1, "read": 1, "write": 1},
			map[string]interface{}{"role": contractUserRole, "permlevel": 0, "read": 1},
		},
	}); err != nil {
		t.Fatalf("creating %s: %v", contractPermDT, err)
	}
	for i, g := range []string{"a", "a", "b"} {
		if _, err := c.CreateDoc(ctx, contractPermDT, map[string]interface{}{"grp": g, "secret": i + 1}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	cfg := contractConfig(t, sc)
	r := runFFC(t, cfg, "", "--json", "aggregate", "-d", contractPermDT, "--group-by", "grp", "--sum", "secret", "--order-by", "grp asc")
	if r.Err != nil || !strings.Contains(r.Stdout, `"grp": "a"`) || !strings.Contains(r.Stdout, `"sum_secret": 3`) {
		t.Errorf("v%d aggregate on %s: %v\n%s%s", major, contractPermDT, r.Err, r.Stdout, r.Stderr)
	}

	uc := contractNonAdmin(t, c, sc)
	syntax, _ := client.SyntaxFor(major)
	q := client.AggregateQuery{GroupBy: []string{"grp"}, Aggregates: []client.AggregateField{{Func: client.AggCount, Alias: "count"}}, OrderBy: []client.OrderTerm{{Column: "grp"}}}
	if rows, err := uc.Aggregate(ctx, contractPermDT, q, syntax); err != nil || len(rows) != 2 {
		t.Errorf("v%d count at permlevel 0: %v, %v", major, rows, err)
	}

	// The raw site: v16 refuses, v15 answers without the column.
	fields := `["grp",{"SUM":"secret","as":"s"}]`
	if syntax == client.SyntaxString {
		fields = `["grp","sum(secret) as s"]`
	}
	res, err := uc.CallMethod(ctx, "frappe.client.get_list", map[string]interface{}{
		"doctype": contractPermDT, "fields": fields, "group_by": "grp", "order_by": "grp asc",
	}, true)
	var api *client.APIError
	switch {
	case major >= 16:
		if !errors.As(err, &api) || api.Status != http.StatusForbidden || api.ExcType != "PermissionError" {
			t.Errorf("v%d SUM of a permlevel 1 field: %v, %v; want 403 PermissionError", major, res, err)
		}
	case err != nil:
		t.Errorf("v%d SUM of a permlevel 1 field: %v; want it silently dropped", major, err)
	default:
		rows, _ := res.([]interface{})
		for _, row := range rows {
			if m, _ := row.(map[string]interface{}); m == nil || m["s"] != nil {
				t.Errorf("v%d SUM of a permlevel 1 field: %v; want rows without s", major, res)
			}
		}
		t.Logf("v%d drops the permlevel 1 aggregate: %v", major, res)
		// Ordered by the dropped alias, MariaDB fails (the fake's 1054).
		_, err = uc.CallMethod(ctx, "frappe.client.get_list", map[string]interface{}{
			"doctype": contractPermDT, "fields": fields, "group_by": "grp", "order_by": "s desc",
		}, true)
		if !errors.As(err, &api) || api.Status != http.StatusInternalServerError || api.ExcType != "OperationalError" ||
			!strings.Contains(api.Message, "Unknown column 's'") {
			t.Errorf("v%d ORDER BY the dropped alias: %v; want 500 OperationalError 1054", major, err)
		}
	}

	uc2 := contractConfig(t, &config.SiteConfig{URL: sc.URL, Username: contractUser, Password: contractUserPwd})
	for _, args := range [][]string{
		{"--group-by", "grp", "--sum", "secret"},
		{"--group-by", "secret"},
	} {
		r := runFFC(t, uc2, "", append([]string{"--json", "aggregate", "-d", contractPermDT}, args...)...)
		if r.Code != exitPermission {
			t.Errorf("v%d ffc aggregate %v as the user: exit %d, %s", major, args, r.Code, r.Stderr)
		}
	}
}

// contractAggRows checks the three fixture documents grouped by status:
// Open 2 documents summing 20, Closed 1 summing 5. A Currency sum is a
// json.Number (its literal is logged: MariaDB returns a Decimal).
func contractAggRows(t *testing.T, rows []map[string]interface{}) {
	t.Helper()
	if len(rows) != 2 || rows[0]["status"] != "Open" || rows[1]["status"] != "Closed" {
		t.Fatalf("rows = %v, want Open then Closed", rows)
	}
	if rows[0]["count"] != json.Number("2") || rows[1]["count"] != json.Number("1") {
		t.Errorf("counts = %#v, %#v, want the integers 2 and 1", rows[0]["count"], rows[1]["count"])
	}
	sum, ok := rows[0]["sum_n_cur"].(json.Number)
	if f, err := sum.Float64(); !ok || err != nil || f != 20 {
		t.Errorf("sum_n_cur = %#v, want 20", rows[0]["sum_n_cur"])
	}
	t.Logf("SUM(Currency) literal: %s", sum)
}
