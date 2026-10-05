package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSyntaxFor(t *testing.T) {
	for major, want := range map[int]AggregateSyntax{15: SyntaxString, 14: SyntaxString, 16: SyntaxDict, 17: SyntaxDict} {
		if got, known := SyntaxFor(major); got != want || !known {
			t.Errorf("SyntaxFor(%d) = %v, %v", major, got, known)
		}
	}
	if got, known := SyntaxFor(0); got != SyntaxDict || known {
		t.Errorf("unknown version: %v, %v", got, known)
	}
}

func TestSyntaxRejected(t *testing.T) {
	v16 := &APIError{Status: http.StatusExpectationFailed, ExcType: "ValidationError"}
	v15 := &APIError{Status: http.StatusInternalServerError, ExcType: "TypeError"}
	switch {
	case !SyntaxRejected(v16, SyntaxString), SyntaxRejected(v16, SyntaxDict):
		t.Error("v16 refusal of a string aggregate")
	case !SyntaxRejected(v15, SyntaxDict), SyntaxRejected(v15, SyntaxString):
		t.Error("v15 refusal of a dict aggregate")
	case SyntaxRejected(&APIError{Status: 403, ExcType: "PermissionError"}, SyntaxDict):
		t.Error("a permission error is not a syntax refusal")
	}
}

// The client refuses what the command layer should have refused, before
// anything is sent: the string syntax copies names into SQL text.
func TestAggregateRefusesNonIdentifiers(t *testing.T) {
	c := &FrappeClient{} // no server: a request would panic or fail differently
	ok := AggregateQuery{GroupBy: []string{"status"}, Aggregates: []AggregateField{{Func: AggCount, Alias: "count"}}}
	for name, q := range map[string]AggregateQuery{
		"group":     {GroupBy: []string{"status`"}, Aggregates: ok.Aggregates},
		"field":     {Aggregates: []AggregateField{{Func: AggSum, Field: "a) or (1", Alias: "s"}}},
		"alias":     {Aggregates: []AggregateField{{Func: AggCount, Alias: "n; drop"}}},
		"function":  {Aggregates: []AggregateField{{Func: "SLEEP", Field: "a", Alias: "s"}}},
		"order":     {GroupBy: ok.GroupBy, Aggregates: ok.Aggregates, OrderBy: []OrderTerm{{Column: "modified"}}},
		"no agg":    {GroupBy: ok.GroupBy},
		"sum field": {Aggregates: []AggregateField{{Func: AggSum, Alias: "s"}}},
	} {
		if _, err := c.Aggregate(context.Background(), "ToDo", q, SyntaxString); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := c.Aggregate(context.Background(), "To`Do", ok, SyntaxString); err == nil {
		t.Error("a DocType with a backtick was accepted")
	}
}

func TestAggregateFields(t *testing.T) {
	q := AggregateQuery{
		GroupBy:    []string{"status"},
		Aggregates: []AggregateField{{Func: AggCount, Alias: "count"}, {Func: AggSum, Field: "grand_total", Alias: "sum_grand_total"}},
		OrderBy:    []OrderTerm{{Column: "count", Desc: true}, {Column: "status"}},
	}
	f, gb, ob := q.fields("Sales Invoice", SyntaxString)
	if len(f) != 3 || f[0] != "`tabSales Invoice`.`status`" || f[1] != "count(`tabSales Invoice`.`name`) as count" ||
		f[2] != "sum(`tabSales Invoice`.`grand_total`) as sum_grand_total" {
		t.Errorf("string fields %q", f)
	}
	if gb != "status" || ob != "count desc, status asc" {
		t.Errorf("string group_by %q order_by %q", gb, ob)
	}
	f, gb, ob = q.fields("Sales Invoice", SyntaxDict)
	if m, ok := f[1].(map[string]interface{}); !ok || m["COUNT"] != "*" || m["as"] != "count" || gb != "status" || ob != "count desc, status asc" {
		t.Errorf("dict fields %v group_by %q order_by %q", f, gb, ob)
	}
}

// group_by and order_by are qualified with the table only when a filter can
// join another one: v15's ORDER_GROUP_PATTERN refuses a qualified name of a
// DocType whose name is not plain ASCII.
func TestAggregateFieldsQualifyOnJoin(t *testing.T) {
	q := AggregateQuery{
		GroupBy:    []string{"status"},
		Aggregates: []AggregateField{{Func: AggCount, Alias: "count"}},
		OrderBy:    []OrderTerm{{Column: "status"}},
	}
	for filters, join := range map[string]bool{
		``:                                    false,
		`{"status":"Open"}`:                   false,
		`[["status","=","Open"]]`:             false,
		`[["ToDo","status","=","Open"]]`:      false,
		`[["status","in",["a","b"]]]`:         false,
		`{"allocated_to.enabled":1}`:          true,
		`[["owner.enabled","=",1]]`:           true,
		`[["User","enabled","=",1]]`:          true,
		`[["ToDo","owner.enabled","=",1]]`:    true,
		"[[\"`tabUser`.`enabled`\",\"=\",1]]": true,
		`[[["a","=",1],"or",["b.c","=",2]]]`:  true,
		`[[["a","=",1],"or",["b","=",2]]]`:    false,
		`not json`:                            true,
	} {
		q.Filters = filters
		_, gb, ob := q.fields("ToDo", SyntaxString)
		want := "status"
		if join {
			want = "`tabToDo`.`status`"
		}
		if gb != want || ob != want+" asc" {
			t.Errorf("filters %s: group_by %q order_by %q, want %q", filters, gb, ob, want)
		}
	}
}

// v15 refuses if/like/regexp/rlike anywhere in group_by and order_by, so a
// field of that name cannot be grouped there; ffc says why instead of
// passing on "Illegal SQL Query". v16 has no such check.
func TestAggregateV15OperatorField(t *testing.T) {
	c := &FrappeClient{}
	q := AggregateQuery{GroupBy: []string{"Like"}, Aggregates: []AggregateField{{Func: AggCount, Alias: "count"}}}
	if _, err := c.Aggregate(context.Background(), "ToDo", q, SyntaxString); err == nil || !strings.Contains(err.Error(), "SQL operator") {
		t.Errorf("err = %v", err)
	}
}
