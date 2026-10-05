package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Aggregation through the list API (GET /api/resource/<doctype> with
// group_by). Frappe changed how a list query names an aggregate:
//
//   - v16 (frappe/database/query.py) rejects an SQL function written as a
//     string in fields ("SQL functions are not allowed as strings in SELECT",
//     _validate_select_field) and takes a dict instead: {"SUM": "field",
//     "as": "alias"}, the function name upper case and one of
//     FUNCTION_MAPPING (COUNT, SUM, AVG, MIN, MAX among others).
//   - v15 (frappe/model/db_query.py) runs fields as SQL text, so
//     "sum(`tabX`.`field`) as alias" works, and a dict breaks
//     reportview.validate_fields (a 500 TypeError: unhashable type 'dict').

// AggregateSyntax is the way aggregates are written in the fields of a list
// query.
type AggregateSyntax int

const (
	// SyntaxDict is {"COUNT": "*", "as": "count"}: Frappe v16 and later.
	SyntaxDict AggregateSyntax = iota
	// SyntaxString is "count(`tabX`.`name`) as count": Frappe v15 and older.
	SyntaxString
)

func (s AggregateSyntax) String() string {
	if s == SyntaxString {
		return "string"
	}
	return "dict"
}

// SyntaxFor returns the aggregate syntax of a Frappe major version, and
// false when the version is unknown (0).
func SyntaxFor(major int) (AggregateSyntax, bool) {
	switch {
	case major <= 0:
		return SyntaxDict, false
	case major >= 16:
		return SyntaxDict, true
	}
	return SyntaxString, true
}

// Aggregate functions ffc sends. All five exist in Frappe v16's
// FUNCTION_MAPPING and in MariaDB/PostgreSQL for v15's SQL text.
const (
	AggCount = "COUNT"
	AggSum   = "SUM"
	AggAvg   = "AVG"
	AggMin   = "MIN"
	AggMax   = "MAX"
)

// AggregateField is one aggregate column. Field is "" for COUNT, which
// counts rows.
type AggregateField struct {
	Func  string // AggCount, AggSum, AggAvg, AggMin or AggMax
	Field string
	Alias string
}

// OrderTerm sorts the groups by a group field or an aggregate alias.
type OrderTerm struct {
	Column string
	Desc   bool
}

// AggregateQuery is a grouped aggregate over one DocType.
type AggregateQuery struct {
	GroupBy    []string // fields to group by; none gives one row over every match
	Aggregates []AggregateField
	Filters    string // raw JSON, as for GetList
	OrderBy    []OrderTerm
	Limit      int // >0: at most this many groups; <=0: all of them
}

// identRE is a field name or alias ffc puts in a query: an ASCII
// identifier. Frappe v16 accepts no more for a function argument or an
// alias (SIMPLE_FIELD_PATTERN), and v15 copies the text into SQL. A name of
// digits alone would be a column position (GROUP BY 1) or a literal (COUNT(1)).
var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// ValidIdentifier reports whether s may be sent as a field name or alias.
func ValidIdentifier(s string) bool { return identRE.MatchString(s) }

var aggFuncs = map[string]bool{AggCount: true, AggSum: true, AggAvg: true, AggMin: true, AggMax: true}

// validate checks every name the query puts in fields, group_by and
// order_by. The command layer checks first and reports usage errors; this
// keeps the client from sending anything else whatever its caller does.
func (q AggregateQuery) validate(doctype string) error {
	if strings.ContainsAny(doctype, "`\x00") {
		return fmt.Errorf("invalid DocType name %q", doctype)
	}
	if len(q.Aggregates) == 0 {
		return errors.New("no aggregate to compute")
	}
	cols := map[string]bool{}
	for _, g := range q.GroupBy {
		if !ValidIdentifier(g) {
			return fmt.Errorf("invalid group-by field %q", g)
		}
		cols[g] = true
	}
	for _, a := range q.Aggregates {
		if !aggFuncs[a.Func] {
			return fmt.Errorf("unsupported aggregate function %q", a.Func)
		}
		if (a.Field != "" || a.Func != AggCount) && !ValidIdentifier(a.Field) {
			return fmt.Errorf("invalid aggregate field %q", a.Field)
		}
		if !ValidIdentifier(a.Alias) {
			return fmt.Errorf("invalid aggregate alias %q", a.Alias)
		}
		cols[a.Alias] = true
	}
	for _, o := range q.OrderBy {
		if !cols[o.Column] {
			return fmt.Errorf("order by %q: not a group-by field or aggregate", o.Column)
		}
	}
	return nil
}

// fields returns the list query's fields, group_by and order_by in syntax.
// The string syntax qualifies the selected fields with their table, as
// Frappe's own list view does: a filter on a child table joins it, and a
// bare "name" or "status" would then be ambiguous. group_by and order_by
// are qualified only when the filters can join another table: v15 checks
// them against ORDER_GROUP_PATTERN (db_query.py ~1542), which refuses any
// character outside [a-z0-9-_ ,`'".()] after lower-casing, so a qualified
// name of a DocType with any other character would be "Illegal SQL Query".
// Frappe has accepted only ASCII letters, digits, spaces, "_" and "-" in
// new DocType names since v15 (doctype.py START_WITH_LETTERS_PATTERN), so
// this concerns DocTypes named before that; the bare fieldname is also
// what Frappe's own get_group_by_count sends.
func (q AggregateQuery) fields(doctype string, syntax AggregateSyntax) (fields []interface{}, groupBy, orderBy string) {
	qual := func(f string) string { return f }
	clause := qual
	if syntax == SyntaxString {
		qual = func(f string) string { return "`tab" + doctype + "`.`" + f + "`" }
		if filtersJoin(q.Filters, doctype) {
			clause = qual
		}
	}
	groups := map[string]bool{}
	var gb []string
	for _, g := range q.GroupBy {
		fields = append(fields, qual(g))
		gb = append(gb, clause(g))
		groups[g] = true
	}
	for _, a := range q.Aggregates {
		switch syntax {
		case SyntaxString:
			arg := qual(a.Field)
			if a.Field == "" {
				// count(*) fails reportview.validate_fields on v15 ("*" is
				// not a field); the name column is never null.
				arg = qual("name")
			}
			fields = append(fields, fmt.Sprintf("%s(%s) as %s", strings.ToLower(a.Func), arg, a.Alias))
		default:
			arg := a.Field
			if a.Field == "" {
				arg = "*" // COUNT is the one function v16 lets take "*"
			}
			fields = append(fields, map[string]interface{}{a.Func: arg, "as": a.Alias})
		}
	}
	var ob []string
	for _, o := range q.OrderBy {
		col := o.Column
		if groups[col] {
			col = clause(col)
		}
		dir := "asc"
		if o.Desc {
			dir = "desc"
		}
		ob = append(ob, col+" "+dir)
	}
	return fields, strings.Join(gb, ", "), strings.Join(ob, ", ")
}

// filtersJoin reports whether list filters can join another table: a
// four-element filter on another DocType, or a field written
// "link.field", "child.field" or "`tabX`.`field`". Unreadable filters
// count as joining (the qualified form is the safe one).
func filtersJoin(filters, doctype string) bool {
	if strings.TrimSpace(filters) == "" {
		return false
	}
	var v interface{}
	if json.Unmarshal([]byte(filters), &v) != nil {
		return true
	}
	other := func(f interface{}) bool { s, _ := f.(string); return strings.ContainsAny(s, ".`") }
	var walk func(v interface{}, depth int) bool
	walk = func(v interface{}, depth int) bool {
		if depth > 32 {
			return true
		}
		switch x := v.(type) {
		case map[string]interface{}:
			for k := range x {
				if other(k) {
					return true
				}
			}
		case []interface{}:
			if len(x) == 0 {
				return false
			}
			if first, ok := x[0].(string); ok {
				if len(x) >= 4 {
					if _, ok := x[2].(string); ok {
						return first != doctype || other(x[1])
					}
				}
				return other(first)
			}
			for _, c := range x {
				if _, isOp := c.(string); !isOp && walk(c, depth+1) {
					return true
				}
			}
		}
		return false
	}
	return walk(v, 0)
}

// v15Operators are the words v15's validate_order_by_and_group_by refuses
// anywhere in group_by and order_by (db_query.py ~1559), field names
// included.
var v15Operators = map[string]bool{"if": true, "like": true, "regexp": true, "rlike": true}

// Aggregate runs q against doctype with the given syntax and returns one row
// per group: the group-by fields under their names and each aggregate under
// its alias. Numbers stay json.Number.
func (c *FrappeClient) Aggregate(ctx context.Context, doctype string, q AggregateQuery, syntax AggregateSyntax) ([]map[string]interface{}, error) {
	if err := q.validate(doctype); err != nil {
		return nil, err
	}
	if syntax == SyntaxString {
		for _, g := range q.GroupBy {
			if v15Operators[strings.ToLower(g)] {
				return nil, fmt.Errorf("cannot group or sort by a field named %q on Frappe v15: its query check reads the name as an SQL operator", g)
			}
		}
	}
	fields, groupBy, orderBy := q.fields(doctype, syntax)
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encoding fields: %w", err)
	}
	params := map[string]string{"fields": string(fieldsJSON), "limit_page_length": "0"}
	if q.Limit > 0 {
		params["limit_page_length"] = strconv.Itoa(q.Limit)
	}
	if groupBy != "" {
		params["group_by"] = groupBy
	}
	if orderBy != "" {
		params["order_by"] = orderBy
	}
	if q.Filters != "" {
		params["filters"] = q.Filters
	}
	var result struct {
		Data    []map[string]interface{} `json:"data"`
		Message []map[string]interface{} `json:"message"`
	}
	if err := c.do(ctx, http.MethodGet, resourcePath(doctype), nil, params, readHints(doctype), &result); err != nil {
		return nil, err
	}
	rows := result.Data
	if rows == nil {
		rows = result.Message
	}
	if rows == nil {
		return []map[string]interface{}{}, nil
	}
	return rows, q.checkColumns(doctype, rows)
}

// checkColumns fails when a requested column is missing from the rows. v15's
// reportview.validate_fields silently drops a field the user may not read
// at its permission level (reportview.py ~131), so its aggregate would just
// be absent; v16 refuses such a field with a PermissionError instead.
func (q AggregateQuery) checkColumns(doctype string, rows []map[string]interface{}) error {
	if len(rows) == 0 {
		return nil
	}
	for _, g := range q.GroupBy {
		if _, ok := rows[0][g]; !ok {
			return &APIError{Status: http.StatusForbidden, ExcType: "PermissionError",
				Message: fmt.Sprintf("the site left %s.%s out of the result: your user may not read that field (permission level)", doctype, g)}
		}
	}
	for _, a := range q.Aggregates {
		if _, ok := rows[0][a.Alias]; !ok {
			return &APIError{Status: http.StatusForbidden, ExcType: "PermissionError",
				Message: fmt.Sprintf("the site left %s(%s.%s) out of the result: your user may not read that field (permission level)", a.Func, doctype, a.Field)}
		}
	}
	return nil
}

// SyntaxRejected reports whether err is how a site that wants the other
// syntax refuses this one: v16 answers a string aggregate with a 417
// ValidationError, v15 a dict one with a 500 TypeError. The message is
// translated on a site in another language, so only the status and the
// exception type are compared; a caller that retries with the other syntax
// must report the first error when the retry fails too.
func SyntaxRejected(err error, syntax AggregateSyntax) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	if syntax == SyntaxString {
		return e.Status == http.StatusExpectationFailed && e.ExcType == "ValidationError"
	}
	return e.Status == http.StatusInternalServerError && e.ExcType == "TypeError"
}

// GroupByCount runs the list view's sidebar count
// (frappe.desk.listview.get_group_by_count): the number of documents
// matching filters per value of field, most frequent first. Frappe returns
// at most 50 groups, each {name: value, count: n}; for "owner" the
// signed-in user's group comes first; "assigned_to" (not a field) counts,
// per System User, the ToDo records allocated to them that are not
// Cancelled (Closed ones included) whose reference_name is the name of a
// matching document (reference_type is not compared, so a ToDo on a
// document of another DocType with the same name counts too); and v16 adds
// title for a Link field whose DocType shows titles in links.
func (c *FrappeClient) GroupByCount(ctx context.Context, doctype, filters, field string) ([]map[string]interface{}, error) {
	if filters == "" {
		filters = "[]" // current_filters is a required argument
	}
	res, err := c.CallMethod(ctx, "frappe.desk.listview.get_group_by_count", map[string]interface{}{
		"doctype": doctype, "current_filters": filters, "field": field,
	}, true)
	if err != nil {
		return nil, err
	}
	rows := []map[string]interface{}{}
	if res == nil {
		return rows, nil
	}
	list, ok := res.([]interface{})
	if !ok {
		return nil, errors.New("unexpected response from get_group_by_count: expected a list")
	}
	for _, r := range list {
		m, ok := r.(map[string]interface{})
		if !ok {
			return nil, errors.New("unexpected response from get_group_by_count: expected a list of objects")
		}
		rows = append(rows, m)
	}
	return rows, nil
}
