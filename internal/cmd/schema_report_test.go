package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func TestCastPropertyTypes(t *testing.T) {
	tests := []struct {
		v, typ interface{}
		want   interface{}
	}{
		{"1", "Check", 1.0},
		{"0", "Check", 0.0},
		{" 7 ", "Int", 7.0},
		{"2.5", "Float", 2.5},
		{"abc", "Int", "abc"},
		{"abc", "Float", "abc"},
		{"Hello", "Data", "Hello"},
		{"1", "Data", "1"},
		{"1", "Select", "1"},
		{float64(3), "Int", float64(3)},
		{nil, "Check", nil},
	}
	for _, tt := range tests {
		if got := castProperty(tt.v, tt.typ); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("castProperty(%#v,%v) = %#v, want %#v", tt.v, tt.typ, got, tt.want)
		}
	}
}

func fakeFrappe(t *testing.T, h http.HandlerFunc) *client.FrappeClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := client.New(context.Background(), &config.SiteConfig{URL: srv.URL, APIKey: "k", APISecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func baseDoc() map[string]interface{} {
	return map[string]interface{}{
		"name": "Thing",
		"fields": []interface{}{
			map[string]interface{}{"fieldname": "a", "label": "A", "reqd": float64(0)},
			map[string]interface{}{"fieldname": "b", "label": "B"},
			map[string]interface{}{"fieldname": "c", "label": "C"},
			map[string]interface{}{"fieldname": "d", "label": "D"},
		},
	}
}

func fieldNames(doc map[string]interface{}) []string {
	var out []string
	for _, f := range doc["fields"].([]interface{}) {
		out = append(out, f.(map[string]interface{})["fieldname"].(string))
	}
	return out
}

func TestApplyPropertySetters(t *testing.T) {
	rows := []map[string]interface{}{
		{"doctype_or_field": "DocField", "field_name": "a", "property": "reqd", "property_type": "Check", "value": "1"},
		{"doctype_or_field": "DocField", "field_name": "b", "property": "label", "property_type": "Data", "value": "New B"},
		{"doctype_or_field": "DocField", "field_name": "ghost", "property": "label", "property_type": "Data", "value": "x"},
		{"doctype_or_field": "DocType", "property": "title_field", "property_type": "Data", "value": "b"},
		{"doctype_or_field": "DocType", "property": "field_order", "property_type": "Data", "value": `["c","a"]`},
	}
	var gotPath string
	c := fakeFrappe(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": rows})
	})
	doc := baseDoc()
	if err := applyPropertySetters(context.Background(), c, "Thing", doc); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/resource/Property%20Setter" && gotPath != "/api/resource/Property Setter" {
		t.Errorf("path = %q", gotPath)
	}
	fields := doc["fields"].([]interface{})
	byName := map[string]map[string]interface{}{}
	for _, f := range fields {
		m := f.(map[string]interface{})
		byName[m["fieldname"].(string)] = m
	}
	if byName["a"]["reqd"] != 1.0 {
		t.Errorf("a.reqd = %#v, want 1.0", byName["a"]["reqd"])
	}
	if byName["b"]["label"] != "New B" {
		t.Errorf("b.label = %#v", byName["b"]["label"])
	}
	if doc["title_field"] != "b" {
		t.Errorf("title_field = %#v", doc["title_field"])
	}
	// c, a first (listed); b, d keep original relative order after.
	if got, want := fieldNames(doc), []string{"c", "a", "b", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if len(fields) != 4 {
		t.Errorf("field count = %d", len(fields))
	}
}

func TestApplyPropertySettersNoRows(t *testing.T) {
	c := fakeFrappe(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) })
	doc := baseDoc()
	if err := applyPropertySetters(context.Background(), c, "Thing", doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fieldNames(doc), []string{"a", "b", "c", "d"}) {
		t.Error("fields changed")
	}
}

func TestCompactSchemaKeepsMeaningful(t *testing.T) {
	doc := map[string]interface{}{
		"name":          "Thing",
		"title_field":   "b",
		"track_changes": float64(0),
		"allow_rename":  float64(1),
		"owner":         "Administrator",
		"permissions": []interface{}{
			map[string]interface{}{"role": "Sales", "read": float64(1), "write": float64(1), "delete": float64(0), "permlevel": float64(0)},
			"junk",
		},
		"fields": []interface{}{
			map[string]interface{}{
				"fieldname": "qty", "label": "Qty", "fieldtype": "Float",
				"precision": "3", "default": float64(5), "reqd": float64(0), "hidden": float64(0),
				"read_only": float64(0), "bold": float64(0), "idx": float64(4), "length": float64(0),
				"creation": "2020", "options": "",
				// Responses are decoded with UseNumber: flags arrive as json.Number.
				"fetch_if_empty": json.Number("0"), "no_copy": json.Number("1"), "permlevel": json.Number("0"),
			},
		},
	}
	out := compactSchema(doc)
	if out["title_field"] != "b" {
		t.Errorf("title_field dropped: %v", out)
	}
	if _, ok := out["track_changes"]; ok {
		t.Error("zero track_changes kept")
	}
	if out["allow_rename"] != 1.0 {
		t.Error("allow_rename dropped")
	}
	if _, ok := out["owner"]; ok {
		t.Error("owner kept")
	}
	perms, _ := out["permissions"].([]map[string]interface{})
	if len(perms) != 1 || perms[0]["role"] != "Sales" || !reflect.DeepEqual(perms[0]["rights"], []string{"read", "write"}) {
		t.Errorf("permissions = %#v", out["permissions"])
	}
	if _, ok := perms[0]["permlevel"]; ok {
		t.Error("zero permlevel kept")
	}
	f := out["fields"].([]map[string]interface{})[0]
	if f["precision"] != "3" || f["default"] != 5.0 {
		t.Errorf("field = %#v", f)
	}
	if f["no_copy"] != json.Number("1") {
		t.Errorf("no_copy dropped: %#v", f)
	}
	for _, k := range []string{"reqd", "hidden", "read_only", "bold", "idx", "length", "creation", "options", "fetch_if_empty", "permlevel"} {
		if _, ok := f[k]; ok {
			t.Errorf("field kept %q", k)
		}
	}
}

func TestReportTableDictColsArrayRows(t *testing.T) {
	res := map[string]interface{}{
		"columns": []interface{}{
			map[string]interface{}{"fieldname": "item", "label": "Item"},
			map[string]interface{}{"fieldname": "qty", "label": "Qty"},
		},
		"result": []interface{}{
			[]interface{}{"A", 1.0},
			[]interface{}{"B"},
		},
	}
	rows, cols := reportTable(res)
	if !reflect.DeepEqual(cols, []string{"item", "qty"}) || len(rows) != 2 {
		t.Fatalf("cols=%v rows=%v", cols, rows)
	}
	if rows[0]["item"] != "A" || rows[0]["qty"] != 1.0 || rows[1]["item"] != "B" {
		t.Errorf("rows = %v", rows)
	}
	if _, ok := rows[1]["qty"]; ok {
		t.Error("short row got qty")
	}
}

func TestReportTableLegacyStringCols(t *testing.T) {
	res := map[string]interface{}{
		"columns": []interface{}{"Item Code:Link/Item:120", "Qty:Float:80"},
		"result": []interface{}{
			map[string]interface{}{"item_code": "X", "qty": 2.0},
		},
	}
	rows, cols := reportTable(res)
	if !reflect.DeepEqual(cols, []string{"item_code", "qty"}) {
		t.Errorf("cols = %v", cols)
	}
	if len(rows) != 1 || rows[0]["item_code"] != "X" {
		t.Errorf("rows = %v", rows)
	}
}

func TestReportTableDuplicateAndUnnamedCols(t *testing.T) {
	res := map[string]interface{}{
		"columns": []interface{}{
			map[string]interface{}{"fieldname": "amount"},
			map[string]interface{}{"fieldname": "amount"},
			map[string]interface{}{"fieldname": "amount"},
			map[string]interface{}{},
		},
		"result": []interface{}{[]interface{}{1.0, 2.0, 3.0, 4.0}},
	}
	rows, cols := reportTable(res)
	want := []string{"amount", "amount_2", "amount_3", "column_4"}
	if !reflect.DeepEqual(cols, want) {
		t.Fatalf("cols = %v, want %v", cols, want)
	}
	if rows[0]["amount"] != 1.0 || rows[0]["amount_2"] != 2.0 || rows[0]["amount_3"] != 3.0 {
		t.Errorf("row = %v", rows[0])
	}
}

func TestReportTableDictRowsNoMatchingKeys(t *testing.T) {
	res := map[string]interface{}{
		"columns": []interface{}{map[string]interface{}{"fieldname": "a"}},
		"result":  []interface{}{map[string]interface{}{"zzz": 1.0}},
	}
	rows, cols := reportTable(res)
	if cols != nil || len(rows) != 1 {
		t.Errorf("cols=%v rows=%v", cols, rows)
	}
}

func TestLimitReportRows(t *testing.T) {
	mk := func() map[string]interface{} {
		return map[string]interface{}{"result": []interface{}{1, 2, 3, 4, 5}}
	}
	r := mk()
	limitReportRows(r, 2)
	if len(r["result"].([]interface{})) != 2 || r["total_rows"] != 5 || r["truncated"] != true {
		t.Errorf("truncated = %v", r)
	}
	for _, n := range []int{0, -1, 5, 10} {
		r := mk()
		limitReportRows(r, n)
		if len(r["result"].([]interface{})) != 5 {
			t.Errorf("n=%d truncated", n)
		}
		if _, ok := r["truncated"]; ok {
			t.Errorf("n=%d set truncated", n)
		}
	}
	limitReportRows(map[string]interface{}{}, 2) // no result key: must not panic
}
