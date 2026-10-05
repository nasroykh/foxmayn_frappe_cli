package frappetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Aggregates in list queries, as the site's Frappe version takes them (see
// client/aggregate.go). The version is the one get_versions reports
// (SetApps): from 16 an SQL function written as a string in fields is a 417
// ValidationError and a dict {"SUM": "field", "as": "alias"} works; before
// 16 a dict is a 500 TypeError and the string works.

// functionCallRE is v16's FUNCTION_CALL_PATTERN (frappe/database/query.py).
var functionCallRE = regexp.MustCompile(`^\s*[a-zA-Z_][a-zA-Z0-9_]*\s*\(`)

// stringAggRE is the v15 aggregate text ffc and Frappe's own code send:
// func(arg) as alias.
var stringAggRE = regexp.MustCompile(`(?i)^\s*(count|sum|avg|min|max)\(\s*([^)]*?)\s*\)\s+as\s+` + "`?" + `(\w+)` + "`?" + `\s*$`)

// v16FunctionMapping lists the aggregate keys of v16's FUNCTION_MAPPING the
// fake computes; the real one has more (ABS, IFNULL, ...).
var v16FunctionMapping = map[string]bool{"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true}

func (s *Site) registerAggregate() {
	s.methods["frappe.desk.listview.get_group_by_count"] = s.getGroupByCount
}

// Postgres makes the site behave like Frappe v16 on PostgreSQL where the
// difference shows: a list query that groups ignores its order_by
// (frappe/database/query.py ~323) and returns the groups in the order they
// were first met (documents by name). Below v16 it changes nothing.
func (s *Site) Postgres() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.postgres = true
}

// highPerm reports whether field of doctype is above the permission levels
// the fake's user reads: a declared field (DocField) at a level (Permlevel)
// that none of the user's roles (SetUser) reads in the DocType's permission
// rows (DocPerm). Administrator, and a DocType without permission rows,
// read every level. In grouped and aggregate list queries v16 refuses such
// a field with a PermissionError; v15's reportview.validate_fields drops it
// from fields without a word (reportview.py:131-133) and its group_by check
// refuses it (db_query.py:1578).
func (s *Site) highPerm(doctype, field string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.docPerms[doctype]
	if s.isAdmin() || len(rows) == 0 {
		return false
	}
	level := -1
	for _, f := range s.meta[doctype] {
		if f.name == field {
			level = f.permlevel
		}
	}
	if level <= 0 {
		return false
	}
	have := map[string]bool{"All": true, "Guest": true}
	for _, r := range s.roles {
		have[r] = true
	}
	for _, row := range rows {
		role, _ := row["role"].(string)
		if have[role] && fmt.Sprint(row["permlevel"]) == strconv.Itoa(level) && fmt.Sprint(row["read"]) == "1" {
			return false
		}
	}
	return true
}

// v15 checks group_by and order_by text (db_query.py:71, 1535-1561): any
// character outside this set, after lower-casing, is "Illegal SQL Query",
// and so are the operator words, outside `tab...` table names.
var (
	v15OrderGroupRE = regexp.MustCompile("[^a-z0-9\\-_ ,`'\".()]")
	v15TableRE      = regexp.MustCompile("`tab[^`]*`")
	v15OperatorRE   = regexp.MustCompile(`\b(if|regexp|rlike|like)\b`)
)

// v15SQLFunctions are the substrings that make v15's extract_tables skip a
// field (db_query.py:771); min( and max( are not among them.
var v15SQLFunctions = []string{"dayofyear(", "extract(", "locate(", "strpos(", "count(", "sum(", "avg("}

// v15ExtractTables is DatabaseQuery.extract_tables (db_query.py:768): a
// field holding "tab" and "." names a table by its text before the first
// ".", so "min(`tabToDo`.`date`)" names "(`tabToDo`" and the DocType
// lookup fails.
func (s *Site) v15ExtractTables(doctype string, raw []interface{}) *Error {
	for _, x := range raw {
		f, ok := x.(string)
		if !ok || !strings.Contains(f, "tab") || !strings.Contains(f, ".") {
			continue
		}
		skip := false
		for _, fn := range v15SQLFunctions {
			skip = skip || strings.Contains(f, fn)
		}
		if skip {
			continue
		}
		table := strings.SplitN(f, ".", 2)[0]
		if strings.HasPrefix(strings.ToLower(table), "group_concat(") {
			table = table[13:]
		}
		if strings.HasPrefix(strings.ToLower(table), "distinct") {
			table = strings.TrimSpace(table[8:])
		}
		if !strings.HasPrefix(table, "`") {
			table = "`" + table + "`"
		}
		if table == "`tab"+doctype+"`" || len(table) < 5 {
			continue
		}
		dt := table[4 : len(table)-1]
		s.mu.Lock()
		_, known := s.doctypes[dt]
		_, declared := s.fields[dt]
		s.mu.Unlock()
		if !known && !declared {
			return NotFound(fmt.Sprintf("DocType %s not found", dt))
		}
	}
	return nil
}

func v15CheckOrderGroup(clause string) *Error {
	lower := strings.ToLower(clause)
	if v15OrderGroupRE.MatchString(lower) || v15OperatorRE.MatchString(v15TableRE.ReplaceAllString(lower, " doc ")) {
		return Validation("Illegal SQL Query")
	}
	return nil
}

// isAggregate reports whether fields hold an aggregate in either syntax.
func isAggregate(fields []interface{}) bool {
	for _, f := range fields {
		switch v := f.(type) {
		case map[string]interface{}:
			return true
		case string:
			if functionCallRE.MatchString(v) {
				return true
			}
		}
	}
	return false
}

// major returns the major version of the frappe app the site reports.
func (s *Site) major() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := strings.TrimPrefix(s.apps["frappe"].Version, "v")
	n, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return n
}

type aggCol struct {
	fn, field, alias string // fn "" for a group column
}

// unqualify turns "`tabToDo`.`status`" or "status" into "status", or
// returns ok=false for another DocType's table.
func unqualify(doctype, f string) (string, bool) {
	f = strings.TrimSpace(f)
	if prefix := "`tab" + doctype + "`."; strings.HasPrefix(f, prefix) {
		f = strings.TrimPrefix(f, prefix)
	} else if strings.Contains(f, ".") {
		return "", false
	}
	return strings.Trim(f, "`"), true
}

func fieldPermission(doctype, f string) *Error {
	return Permission(fmt.Sprintf("You do not have permission to access field: <strong>%s.%s</strong>", doctype, f))
}

// aggregateList answers a list request with group_by or aggregate fields.
func (s *Site) aggregateList(w http.ResponseWriter, q url.Values, doctype string, raw []interface{}) {
	v16 := s.major() >= 16
	if !v16 {
		for _, clause := range []string{q.Get("order_by"), q.Get("group_by")} {
			if err := v15CheckOrderGroup(clause); err != nil {
				writeError(w, err)
				return
			}
		}
		if err := s.v15ExtractTables(doctype, raw); err != nil {
			writeError(w, err)
			return
		}
	}
	var cols []aggCol
	for _, x := range raw {
		switch v := x.(type) {
		case map[string]interface{}:
			if !v16 {
				// reportview.validate_fields hands it to an lru_cache.
				writeError(w, &Error{http.StatusInternalServerError, "TypeError", "unhashable type: 'dict'"})
				return
			}
			c, err := s.dictAgg(doctype, v)
			if err != nil {
				writeError(w, err)
				return
			}
			cols = append(cols, c)
		case string:
			if functionCallRE.MatchString(v) {
				if v16 {
					writeError(w, Validation(fmt.Sprintf("SQL functions are not allowed as strings in SELECT: %s. Use dict syntax like {'COUNT': '*'} instead.", v)))
					return
				}
				m := stringAggRE.FindStringSubmatch(v)
				if m == nil {
					writeError(w, DataError("Use of sub-query or function is restricted"))
					return
				}
				f, ok := unqualify(doctype, m[2])
				if !ok || f == "*" || !s.knownField(doctype, f) {
					// count(*) included: reportview.validate_fields finds no
					// field in it.
					writeError(w, DataError("Field not permitted in query: "+f))
					return
				}
				if s.highPerm(doctype, f) {
					continue // validate_fields drops it without a word
				}
				cols = append(cols, aggCol{strings.ToUpper(m[1]), f, m[3]})
				continue
			}
			f, ok := unqualify(doctype, v)
			if !ok || !s.knownField(doctype, f) {
				writeError(w, DataError("Field not permitted in query: "+v))
				return
			}
			if s.highPerm(doctype, f) {
				if v16 {
					writeError(w, fieldPermission(doctype, f))
					return
				}
				continue
			}
			cols = append(cols, aggCol{field: f, alias: f})
		default:
			writeError(w, Validation(fmt.Sprintf("invalid field %v", x)))
			return
		}
	}
	var groupBy []string
	for _, g := range strings.Split(q.Get("group_by"), ",") {
		if strings.TrimSpace(g) == "" {
			continue
		}
		f, ok := unqualify(doctype, g)
		if !ok || !s.knownField(doctype, f) || (v16 && s.highPerm(doctype, f)) {
			writeError(w, fieldPermission(doctype, f))
			return
		}
		if s.highPerm(doctype, f) {
			writeError(w, Permission("Not permitted to sort or group by <strong>"+f+"</strong>"))
			return
		}
		groupBy = append(groupBy, f)
	}

	var filters interface{}
	if f := q.Get("filters"); f != "" {
		if err := json.Unmarshal([]byte(f), &filters); err != nil {
			writeError(w, Validation("filters must be JSON"))
			return
		}
	}
	docs, err := s.query(doctype, filters)
	if err != nil {
		writeError(w, err.(*Error))
		return
	}
	type group struct {
		first map[string]interface{}
		docs  []map[string]interface{}
	}
	groups := map[string]*group{}
	var order []string
	sort.Slice(docs, func(i, j int) bool { return fmt.Sprint(docs[i]["name"]) < fmt.Sprint(docs[j]["name"]) })
	for _, d := range docs {
		var key []string
		for _, g := range groupBy {
			key = append(key, fmt.Sprintf("%T:%v", d[g], d[g]))
		}
		k := strings.Join(key, "\x00")
		if groups[k] == nil {
			groups[k] = &group{first: d}
			order = append(order, k)
		}
		groups[k].docs = append(groups[k].docs, d)
	}
	if len(groupBy) == 0 && len(order) == 0 {
		groups[""] = &group{first: map[string]interface{}{}} // one row over no documents
		order = append(order, "")
	}
	rows := make([]map[string]interface{}, 0, len(order))
	for _, k := range order {
		g := groups[k]
		row := map[string]interface{}{}
		for _, c := range cols {
			if c.fn == "" {
				row[c.alias] = g.first[c.field]
				continue
			}
			row[c.alias] = aggregateValue(c, g.docs)
		}
		rows = append(rows, row)
	}

	orderBy := q.Get("order_by")
	var terms []string
	for _, part := range strings.Split(orderBy, ",") {
		w := strings.Fields(part)
		if len(w) == 0 {
			continue
		}
		f, _ := unqualify(doctype, w[0])
		terms = append(terms, strings.Join(append([]string{f}, w[1:]...), " "))
	}
	if !v16 {
		// MariaDB resolves ORDER BY against the select aliases and the
		// table's columns; an alias whose field validate_fields dropped is
		// neither.
		aliases := map[string]bool{}
		for _, c := range cols {
			aliases[c.alias] = true
		}
		for _, t := range terms {
			col := strings.Fields(t)[0]
			if !aliases[col] && !s.knownField(doctype, col) {
				writeError(w, &Error{http.StatusInternalServerError, "OperationalError",
					fmt.Sprintf("(1054, \"Unknown column '%s' in 'ORDER BY'\")", col)})
				return
			}
		}
	}
	if len(terms) == 0 && len(cols) > 0 {
		terms = []string{cols[0].alias + " asc"} // a stable order for tests
	}
	s.mu.Lock()
	ignoreOrder := s.postgres && v16 && len(groupBy) > 0
	s.mu.Unlock()
	if !ignoreOrder {
		if err := sortRows(rows, strings.Join(terms, ", ")); err != nil {
			writeError(w, err.(*Error))
			return
		}
	}
	limit := 20
	if v := q.Get("limit_page_length"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": rows})
}

// dictAgg reads a v16 aggregate dict.
func (s *Site) dictAgg(doctype string, d map[string]interface{}) (aggCol, *Error) {
	var c aggCol
	for k, v := range d {
		if strings.EqualFold(k, "as") {
			c.alias, _ = v.(string)
			continue
		}
		if !v16FunctionMapping[k] {
			// A lower-case key is taken for a child table query.
			return c, Validation(fmt.Sprintf("Child query fields for '%s' must be a list or tuple.", k))
		}
		c.fn = k
		c.field, _ = v.(string)
	}
	switch {
	case c.fn == "":
		return c, Validation("Invalid function dictionary format")
	case c.field == "*":
		if c.fn != "COUNT" {
			return c, Validation("'*' is only allowed in COUNT SQL function(s)")
		}
	case !s.knownField(doctype, c.field) || s.highPerm(doctype, c.field):
		return c, fieldPermission(doctype, c.field)
	}
	if c.alias == "" {
		c.alias = c.fn
	}
	return c, nil
}

// aggregateValue computes one aggregate over a group's documents. COUNT is
// an integer; SUM and AVG are decimals (Frappe sends a Decimal as 12.0);
// MIN and MAX keep the value's own type. With no value they are null.
func aggregateValue(c aggCol, docs []map[string]interface{}) interface{} {
	if c.fn == "COUNT" {
		n := 0
		for _, d := range docs {
			if c.field == "*" || d[c.field] != nil {
				n++
			}
		}
		return json.Number(strconv.Itoa(n))
	}
	var best interface{}
	sum, n := 0.0, 0
	for _, d := range docs {
		v := d[c.field]
		if v == nil {
			continue
		}
		switch c.fn {
		case "MIN":
			if best == nil || compare(v, best) < 0 {
				best = v
			}
		case "MAX":
			if best == nil || compare(v, best) > 0 {
				best = v
			}
		default:
			f, ok := number(v)
			if !ok {
				f, _ = strconv.ParseFloat(fmt.Sprint(v), 64)
			}
			sum += f
			n++
		}
	}
	switch c.fn {
	case "MIN", "MAX":
		return best
	}
	if n == 0 {
		return nil
	}
	if c.fn == "AVG" {
		sum /= float64(n)
	}
	s := strconv.FormatFloat(sum, 'f', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return json.Number(s)
}

// getGroupByCount is frappe.desk.listview.get_group_by_count: {name, count}
// per value of field, most frequent first, at most 50; "owner" puts the
// signed-in user first and "assigned_to" counts the ToDo allocations that
// are not Cancelled and whose reference_name is a matching document's name
// (reference_type is not compared, as in Frappe). A field the DocType lacks is a
// bare ValueError (500).
func (s *Site) getGroupByCount(r *http.Request, args map[string]interface{}) (interface{}, error) {
	doctype, _ := args["doctype"].(string)
	field, _ := args["field"].(string)
	filters, ok := args["current_filters"]
	if !ok {
		return nil, &Error{http.StatusInternalServerError, "TypeError", "get_group_by_count() missing 1 required positional argument: 'current_filters'"}
	}
	docs, err := s.query(doctype, filters)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	values := map[string]interface{}{}
	add := func(v interface{}) {
		k := fmt.Sprintf("%T:%v", v, v)
		counts[k]++
		values[k] = v
	}
	if field == "assigned_to" {
		names := map[string]bool{}
		for _, d := range docs {
			names[fmt.Sprint(d["name"])] = true
		}
		todos, _ := s.query("ToDo", nil)
		for _, t := range todos {
			if t["status"] != "Cancelled" && t["allocated_to"] != nil && names[fmt.Sprint(t["reference_name"])] {
				add(t["allocated_to"])
			}
		}
	} else {
		if !s.knownField(doctype, field) {
			return nil, &Error{http.StatusInternalServerError, "ValueError", "Field does not belong to doctype"}
		}
		for _, d := range docs {
			add(d[field])
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]interface{}{"name": values[k], "count": counts[k]})
	}
	if field == "owner" {
		me := s.loggedUser(s.authenticate(r))
		for i, row := range out {
			if i > 0 && row["name"] == me {
				out = append([]map[string]interface{}{row}, append(out[:i:i], out[i+1:]...)...)
				break
			}
		}
	}
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}
