package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// List queries can reach DocTypes their doctype argument does not name:
//
//   - a filter field "link_field.field" or "child_table.field" joins the
//     linked or child DocType, and "`tabX`.`field`" names any table (v16
//     Engine._validate_and_prepare_filter_field, frappe/database/query.py
//     ~808-847; v15 db_query reads "tabX.field" the same way);
//   - a four-element filter [doctype, field, op, value] filters on that
//     DocType (v15 and v16);
//   - fields and order_by take the same dotted and backtick forms.
//
// A filter on a denied DocType's field reads it one bit at a time, so with
// DocType rules (allow_doctypes or deny_doctypes) only plain fieldnames may
// appear in filters, fields and order_by, and the DocType of a four-element
// filter is checked like the doctype argument. A child-table field
// ("items.item_code") is refused too: ffc cannot tell a child table from a
// Link without the schema.

// queryRefs is what the query arguments of one call name.
type queryRefs struct {
	filterFields []string // must be plain fieldnames under DocType rules
	selectFields []string // fields and order_by entries: no "." or "`" under DocType rules
	doctypes     []string // element 0 of four-element filters
}

// queryScope reads the query arguments of the tools that take them.
func queryScope(req mcp.CallToolRequest) queryRefs {
	var r queryRefs
	switch req.Params.Name {
	case "list_docs", "count_docs", "aggregate":
		var f interface{}
		if ok, err := jsonArg(req, "filters", &f); ok && err == nil {
			r.filters(f, 0)
		}
		if fields, err := stringsArg(req, "fields"); err == nil {
			r.selectFields = append(r.selectFields, fields...)
		}
		if s, ok := req.GetArguments()["order_by"].(string); ok {
			r.orderBy(s)
		}
	case "call_method":
		r.methodArgs(req.GetArguments()["args"], 0)
	}
	return r
}

// filters walks a filter value: an object {field: value}, a list of
// conditions [field, op, value], [field, value] or [doctype, field, op,
// value], nested groups [cond, "or", cond], or any of them JSON-encoded.
func (r *queryRefs) filters(v interface{}, depth int) {
	if depth > 32 {
		return
	}
	switch val := v.(type) {
	case map[string]interface{}:
		for k := range val {
			r.filterFields = append(r.filterFields, k)
		}
	case []interface{}:
		if len(val) == 0 {
			return
		}
		if first, ok := val[0].(string); ok {
			if len(val) >= 4 {
				if field, ok := val[1].(string); ok {
					if _, ok := val[2].(string); ok {
						r.doctypes = append(r.doctypes, first)
						r.filterFields = append(r.filterFields, field)
						return
					}
				}
			}
			r.filterFields = append(r.filterFields, first)
			return
		}
		for _, x := range val {
			if _, isOp := x.(string); !isOp { // "and" / "or" between groups
				r.filters(x, depth+1)
			}
		}
	case string:
		var inner interface{}
		if t := strings.TrimSpace(val); (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Unmarshal([]byte(t), &inner) == nil {
			r.filters(inner, depth+1)
		}
	}
}

// orderBy adds the column of each "column [asc|desc]" term.
func (r *queryRefs) orderBy(s string) {
	for _, part := range strings.Split(s, ",") {
		w := strings.Fields(part)
		if n := len(w); n > 1 && (strings.EqualFold(w[n-1], "asc") || strings.EqualFold(w[n-1], "desc")) {
			w = w[:n-1]
		}
		if len(w) > 0 {
			r.selectFields = append(r.selectFields, strings.Join(w, " "))
		}
	}
}

// selects adds a fields value: a list of strings or v16 aggregate dicts, a
// comma-separated string, or JSON text of either.
func (r *queryRefs) selects(v interface{}, depth int) {
	if depth > 32 {
		return
	}
	switch val := v.(type) {
	case string:
		var inner interface{}
		if t := strings.TrimSpace(val); strings.HasPrefix(t, "[") && json.Unmarshal([]byte(t), &inner) == nil {
			r.selects(inner, depth+1)
			return
		}
		for _, f := range strings.Split(val, ",") {
			if f = strings.TrimSpace(f); f != "" {
				r.selectFields = append(r.selectFields, f)
			}
		}
	case []interface{}:
		for _, x := range val {
			r.selects(x, depth+1)
		}
	case map[string]interface{}:
		for k, x := range val {
			r.selectFields = append(r.selectFields, k)
			r.selects(x, depth+1)
		}
	}
}

// methodArgs finds query arguments at any depth of a call_method's args
// (frappe.client.get_list, get_count, get_value, reportview.get, ...).
func (r *queryRefs) methodArgs(v interface{}, depth int) {
	if depth > 32 {
		return
	}
	switch val := v.(type) {
	case map[string]interface{}:
		for k, x := range val {
			switch k {
			case "filters", "or_filters", "current_filters", "applied_filters":
				r.filters(x, 0)
			case "fields", "fieldname", "pluck":
				r.selects(x, 0)
			case "order_by", "group_by", "sort_by", "field":
				if s, ok := x.(string); ok {
					r.orderBy(s)
				}
			default:
				r.methodArgs(x, depth+1)
			}
		}
	case []interface{}:
		for _, x := range val {
			r.methodArgs(x, depth+1)
		}
	case string:
		var inner interface{}
		if t := strings.TrimSpace(val); (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Unmarshal([]byte(t), &inner) == nil {
			r.methodArgs(inner, depth+1)
		}
	}
}

// hasDoctypeRules reports whether allow_doctypes or deny_doctypes is set,
// in the config or by a flag.
func (p mcpPolicy) hasDoctypeRules() bool {
	return len(p.cfg.AllowDoctypes)+len(p.cfg.DenyDoctypes)+len(p.flag.AllowDoctypes)+len(p.flag.DenyDoctypes) > 0
}

// checkQueryFields refuses field references that can reach another
// DocType when DocType rules are set (see queryRefs).
func (p mcpPolicy) checkQueryFields(sc toolScope) error {
	if !p.hasDoctypeRules() {
		return nil
	}
	for _, f := range sc.FilterFields {
		if !client.ValidIdentifier(strings.TrimSpace(f)) {
			return fmt.Errorf("policy: filter field %q is not a plain fieldname; with allow_doctypes or deny_doctypes set, filters may not name a field of a linked, child or other table (\"link.field\", \"`tabX`.`field`\")", f)
		}
	}
	for _, f := range sc.SelectFields {
		if strings.ContainsAny(f, ".`") {
			return fmt.Errorf("policy: field %q names another table; with allow_doctypes or deny_doctypes set, fields and order_by take only fieldnames of the DocType", f)
		}
	}
	return nil
}
