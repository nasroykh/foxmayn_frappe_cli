package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// aggregate flags
var (
	agDoctype string
	agGroupBy []string
	agCount   bool
	agSum     []string
	agAvg     []string
	agMin     []string
	agMax     []string
	agFilters string
	agOrderBy string
	agLimit   int
)

// defaultAggregateLimit is the number of groups ffc aggregate and the MCP
// aggregate tool return unless told otherwise; maxMCPAggregateLimit caps
// what a model may ask for. maxGroupBy caps the group-by fields.
const (
	defaultAggregateLimit = 100
	maxMCPAggregateLimit  = 1000
	maxGroupBy            = 5
)

var aggregateCmd = &cobra.Command{
	Use:   "aggregate",
	Short: "Count, sum, average, min and max per group, computed by the site",
	Long: `Group the documents of a DocType by one or more fields and compute
aggregates per group on the server, without fetching the rows.

Each aggregate becomes a column: --count gives "count", --sum F "sum_F",
--avg F "avg_F", --min F "min_F" and --max F "max_F". --sum, --avg, --min
and --max repeat or take a comma-separated list. Without any aggregate flag
ffc counts. Without --group-by the result is one row over every matching
document.

Fields are plain field names of the DocType (letters, digits, underscore).
A field of a linked or child DocType ("customer.territory", "items.qty") is
refused: Frappe v15 cannot group by one and v16 cannot aggregate one.

--order-by takes a group-by field or an aggregate column, with asc or desc
("sum_grand_total desc"); several are comma-separated. The default is the
first aggregate, largest first. --limit caps the number of groups (default
100, 0 for all); ffc says on stderr when there were more.

ffc writes the aggregates the way the site's Frappe version expects: Frappe
v16 refuses SQL functions written as text in fields (so list-docs --fields
'count(name) as n' fails there) and v15 accepts nothing else. The version
comes from the cache whoami fills (24 hours); when it is unknown or stale,
ffc tries the other form after the site refuses the first.

Examples:
  ffc aggregate -d ToDo --group-by status
  ffc aggregate -d "Sales Invoice" --group-by customer --sum grand_total --count --limit 10
  ffc aggregate -d "Sales Invoice" --group-by status,currency --sum grand_total,outstanding_amount --filters '{"docstatus":1}'
  ffc aggregate -d "Sales Invoice" --sum grand_total --avg grand_total --min posting_date --max posting_date
  ffc aggregate -d ToDo --group-by owner --order-by "owner asc" --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		filters, err := filtersFlag(agFilters)
		if err != nil {
			return err
		}
		if agLimit < 0 {
			return usageErrorf("--limit must be >= 0 (0 means no limit)")
		}
		q, err := buildAggregateQuery(aggregateArgs{
			GroupBy: splitAll(agGroupBy), Count: agCount,
			Sum: splitAll(agSum), Avg: splitAll(agAvg), Min: splitAll(agMin), Max: splitAll(agMax),
			OrderBy: agOrderBy, Limit: agLimit, Filters: filters,
		}, "--")
		if err != nil {
			return err
		}
		res, err := callSiteCfg(cmd, fmt.Sprintf("Aggregating %s…", agDoctype), func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (aggregateResult, error) {
			return runAggregate(ctx, c, cfg, agDoctype, q)
		})
		if err != nil {
			return err
		}
		if res.Warning != "" {
			output.PrintWarning(res.Warning)
		}
		if res.Truncated {
			output.PrintWarning(fmt.Sprintf("Showing the first %d group(s); there are more (raise --limit, or 0 for all).", len(res.Rows)))
		}
		cols := aggregateColumns(q)
		return render(res.Rows, cols, func() error {
			output.PrintTable(res.Rows, cols)
			return nil
		})
	},
}

func init() {
	f := aggregateCmd.Flags()
	f.StringVarP(&agDoctype, "doctype", "d", "", "Frappe DocType (required)")
	f.StringArrayVar(&agGroupBy, "group-by", nil, fmt.Sprintf("Field(s) to group by; repeat or comma-separate (at most %d); omit for one row over all documents", maxGroupBy))
	f.BoolVar(&agCount, "count", false, `Count documents per group (column "count"); the default without other aggregates`)
	f.StringArrayVar(&agSum, "sum", nil, `Sum a numeric field (column "sum_FIELD"); repeat or comma-separate`)
	f.StringArrayVar(&agAvg, "avg", nil, `Average a numeric field (column "avg_FIELD")`)
	f.StringArrayVar(&agMin, "min", nil, `Smallest value of a field (column "min_FIELD")`)
	f.StringArrayVar(&agMax, "max", nil, `Largest value of a field (column "max_FIELD")`)
	f.StringVar(&agFilters, "filters", "", `Filter expression as JSON: '{"status":"Open"}' or '[["status","=","Open"]]' (or @FILE)`)
	f.StringVar(&agOrderBy, "order-by", "", `Sort by a group-by field or aggregate column, e.g. "sum_grand_total desc" (default: first aggregate, desc)`)
	f.IntVarP(&agLimit, "limit", "l", defaultAggregateLimit, "Maximum number of groups (0 = all)")
	_ = aggregateCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(aggregateCmd)
}

// splitAll splits every value of a repeatable flag on commas.
func splitAll(vals []string) []string {
	var out []string
	for _, v := range vals {
		out = append(out, splitCSV(v)...)
	}
	return out
}

// aggregateArgs is what the CLI and the MCP tool both take.
type aggregateArgs struct {
	GroupBy            []string
	Count              bool
	Sum, Avg, Min, Max []string
	OrderBy            string
	Limit              int // 0: all groups
	Filters            string
}

// buildAggregateQuery validates the arguments and turns them into a query.
// Every field name must be a plain identifier: nothing else reaches the
// query, whatever the Frappe version does with it. prefix is "--" for flag
// names in CLI errors and "" for MCP argument names.
func buildAggregateQuery(a aggregateArgs, prefix string) (client.AggregateQuery, error) {
	flag := func(name string) string {
		if prefix == "" {
			return strings.ReplaceAll(name, "-", "_")
		}
		return prefix + name
	}
	q := client.AggregateQuery{Filters: a.Filters, Limit: a.Limit}
	cols := map[string]string{} // column → what made it, to report clashes
	if len(a.GroupBy) > maxGroupBy {
		return q, usageErrorf("%s: at most %d fields", flag("group-by"), maxGroupBy)
	}
	for _, g := range a.GroupBy {
		if err := checkAggField(flag("group-by"), g); err != nil {
			return q, err
		}
		if cols[g] != "" {
			return q, usageErrorf("%s: %q is given twice", flag("group-by"), g)
		}
		cols[g] = flag("group-by") + " " + g
		q.GroupBy = append(q.GroupBy, g)
	}
	add := func(name, fn, field, alias string) error {
		by := flag(name)
		if field != "" {
			by += " " + field
		}
		if prev := cols[alias]; prev != "" {
			if prev == by {
				return usageErrorf("%s is given twice", by)
			}
			return usageErrorf("%s: its column %q clashes with %s", by, alias, prev)
		}
		cols[alias] = by
		q.Aggregates = append(q.Aggregates, client.AggregateField{Func: fn, Field: field, Alias: alias})
		return nil
	}
	count := a.Count || len(a.Sum)+len(a.Avg)+len(a.Min)+len(a.Max) == 0
	if count {
		if err := add("count", client.AggCount, "", "count"); err != nil {
			return q, err
		}
	}
	for _, set := range []struct {
		name, fn string
		fields   []string
	}{
		{"sum", client.AggSum, a.Sum}, {"avg", client.AggAvg, a.Avg},
		{"min", client.AggMin, a.Min}, {"max", client.AggMax, a.Max},
	} {
		for _, f := range set.fields {
			if err := checkAggField(flag(set.name), f); err != nil {
				return q, err
			}
			alias := set.name + "_" + f
			if !client.ValidIdentifier(alias) {
				return q, usageErrorf("%s %s: field name too long", flag(set.name), f)
			}
			if err := add(set.name, set.fn, f, alias); err != nil {
				return q, err
			}
		}
	}
	order, err := parseAggregateOrder(a.OrderBy, cols, flag("order-by"))
	if err != nil {
		return q, err
	}
	if len(order) == 0 {
		order = append(order, client.OrderTerm{Column: q.Aggregates[0].Alias, Desc: true})
	}
	// The group fields break ties, so the order (and what a limit keeps) is
	// the same on every run and ffc's own sort agrees with the site's.
	for _, g := range q.GroupBy {
		seen := false
		for _, o := range order {
			seen = seen || o.Column == g
		}
		if !seen {
			order = append(order, client.OrderTerm{Column: g})
		}
	}
	q.OrderBy = order
	return q, nil
}

// checkAggField refuses anything but a plain field name.
func checkAggField(flag, f string) error {
	switch {
	case f == "":
		return usageErrorf("%s: empty field name", flag)
	case strings.Contains(f, "."):
		return usageErrorf("%s %q: fields of a linked or child DocType are not supported; give a field of the DocType itself", flag, f)
	case !client.ValidIdentifier(f):
		return usageErrorf("%s %q: expected a field name (letters, digits and underscores, not starting with a digit)", flag, f)
	}
	return nil
}

// parseAggregateOrder reads "col [asc|desc], …" where every col is a group-by
// field or an aggregate column. The direction defaults to asc, as in SQL.
func parseAggregateOrder(raw string, cols map[string]string, flag string) ([]client.OrderTerm, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []client.OrderTerm
	for _, part := range strings.Split(raw, ",") {
		w := strings.Fields(part)
		if len(w) == 0 || len(w) > 2 {
			return nil, usageErrorf("%s: expected \"column [asc|desc]\", got %q", flag, strings.TrimSpace(part))
		}
		t := client.OrderTerm{Column: w[0]}
		if len(w) == 2 {
			switch strings.ToLower(w[1]) {
			case "asc":
			case "desc":
				t.Desc = true
			default:
				return nil, usageErrorf("%s: direction must be asc or desc, got %q", flag, w[1])
			}
		}
		if cols[t.Column] == "" {
			known := make([]string, 0, len(cols))
			for c := range cols {
				known = append(known, c)
			}
			sort.Strings(known)
			return nil, usageErrorf("%s: %q is not a group-by field or aggregate column (have: %s)", flag, t.Column, strings.Join(known, ", "))
		}
		out = append(out, t)
	}
	return out, nil
}

// aggregateColumns returns the result's columns in order: the group-by
// fields, then the aggregates.
func aggregateColumns(q client.AggregateQuery) []string {
	cols := append([]string{}, q.GroupBy...)
	for _, a := range q.Aggregates {
		cols = append(cols, a.Alias)
	}
	return cols
}

// aggregateResult is the groups of an aggregate query. Truncated is set when
// the limit cut groups off; Warning says when the rows may not be the top
// groups (see runAggregate).
type aggregateResult struct {
	Rows      []map[string]interface{}
	Truncated bool
	Warning   string
	Syntax    client.AggregateSyntax
}

// aggregateProbeRows is the fewest groups runAggregate asks for with a
// limit, so that rows in the requested order are no accident.
const aggregateProbeRows = 20

// aggregateFetchCap caps the groups runAggregate fetches when the site
// ignored the order (a variable so tests can lower it).
var aggregateFetchCap = 10000

// runAggregate runs q and returns its rows sorted by q.OrderBy, at most
// q.Limit of them (0: all).
//
// ffc sorts the rows itself: Frappe v16 drops ORDER BY on PostgreSQL when
// the query groups (frappe/database/query.py ~323), and the site's database
// is not cheaply knowable (get_versions does not say; the System Health
// Report does, but needs System Manager and runs every health check). With
// a limit, ffc asks for at least aggregateProbeRows groups. Fewer than it
// asked for is every group, sorted here whatever the site did. A full page
// in the requested order means the site sorted (by chance, with 20 rows or
// more, is not a practical risk). A full page out of order means the site
// ignored the order: ffc then fetches up to aggregateFetchCap groups, sorts
// and cuts them itself, and warns when even that was not every group.
func runAggregate(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, doctype string, q client.AggregateQuery) (aggregateResult, error) {
	want := q.Limit
	if want > 0 {
		q.Limit = max(want+1, aggregateProbeRows) // one more tells whether the limit cut groups off
	}
	rows, syntax, err := aggregateRows(ctx, c, cfg, doctype, q)
	if err != nil {
		return aggregateResult{}, err
	}
	res := aggregateResult{Syntax: syntax}
	if want > 0 && len(rows) >= q.Limit && !rowsSorted(rows, q.OrderBy) {
		q.Limit = aggregateFetchCap + 1
		if rows, err = c.Aggregate(ctx, doctype, q, syntax); err != nil {
			return aggregateResult{}, err
		}
		if len(rows) > aggregateFetchCap {
			rows = rows[:aggregateFetchCap]
			res.Warning = fmt.Sprintf("The site ignored the sort order (Frappe v16 on PostgreSQL does when grouping) and %s has more than %d groups: these are the top groups of %d arbitrary ones. Narrow the filters.", doctype, aggregateFetchCap, aggregateFetchCap)
		}
	}
	sortAggregateRows(rows, q.OrderBy)
	res.Rows = rows
	if want > 0 && len(rows) > want {
		res.Rows, res.Truncated = rows[:want], true
	}
	return res, nil
}

// aggregateRows picks the aggregate syntax from the site's Frappe version
// (cached by serverInfo; cfg may be nil, the version is then unknown) and
// runs q. When the version is unknown or the cache is stale (an upgrade
// from v15 to v16), the site refuses the syntax tried first; the other one
// is tried once, and a stale cache is dropped. When the retry fails too,
// the error that is about the user's query is reported: the retry's, unless
// the site refused the other syntax as well (then the first syntax was the
// right one, and its error is the real one).
func aggregateRows(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig, doctype string, q client.AggregateQuery) ([]map[string]interface{}, client.AggregateSyntax, error) {
	syntax, known := client.SyntaxFor(0)
	if cfg != nil {
		if info, _, err := serverInfo(ctx, c, cfg, false); err == nil {
			syntax, known = client.SyntaxFor(info.FrappeMajor())
		}
	}
	rows, err := c.Aggregate(ctx, doctype, q, syntax)
	if err != nil && client.SyntaxRejected(err, syntax) {
		other := client.SyntaxDict
		if syntax == client.SyntaxDict {
			other = client.SyntaxString
		}
		rows2, err2 := c.Aggregate(ctx, doctype, q, other)
		switch {
		case err2 != nil && client.SyntaxRejected(err2, other):
			return nil, syntax, err
		case known && cfg != nil:
			invalidateServerCache(cfg) // the site is not the version the cache says
		}
		rows, err, syntax = rows2, err2, other
	}
	return rows, syntax, err
}

// compareAggValues orders two values of a result column as MariaDB does:
// null first, numbers by value, then strings case-insensitively (close to
// the utf8mb4 _ci collations; dates and datetimes compare as text).
func compareAggValues(a, b interface{}) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	fa, aNum := numeric(a)
	fb, bNum := numeric(b)
	switch {
	case aNum && bNum:
		return cmp.Compare(fa, fb)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	sa, sb := fmt.Sprint(a), fmt.Sprint(b)
	if c := strings.Compare(strings.ToLower(sa), strings.ToLower(sb)); c != 0 {
		return c
	}
	return strings.Compare(sa, sb)
}

// compareAggRows compares two rows by order. With lenient set, a pair whose
// deciding column holds a null compares equal: PostgreSQL puts nulls last
// where MariaDB puts them first, and either is a sorted result.
func compareAggRows(a, b map[string]interface{}, order []client.OrderTerm, lenient bool) int {
	for _, o := range order {
		va, vb := a[o.Column], b[o.Column]
		c := compareAggValues(va, vb)
		if c == 0 {
			continue
		}
		if lenient && (va == nil || vb == nil) {
			return 0
		}
		if o.Desc {
			c = -c
		}
		return c
	}
	return 0
}

// rowsSorted reports whether the site returned rows in order, up to the
// collation differences compareAggValues does not model (strings that sort
// apart by accents or punctuation; such a false alarm costs one more
// request, not a wrong result).
func rowsSorted(rows []map[string]interface{}, order []client.OrderTerm) bool {
	for i := 1; i < len(rows); i++ {
		if compareAggRows(rows[i-1], rows[i], order, true) > 0 {
			return false
		}
	}
	return true
}

func sortAggregateRows(rows []map[string]interface{}, order []client.OrderTerm) {
	sort.SliceStable(rows, func(i, j int) bool { return compareAggRows(rows[i], rows[j], order, false) < 0 })
}

// groupByCountMax is the most groups get_group_by_count returns
// (frappe/desk/listview.py: data[0:50]).
const groupByCountMax = 50

// countByGroup is count-docs --group-by: get_group_by_count's rows, with the
// group value under the field's name instead of "name".
func countByGroup(cmd *cobra.Command, doctype, filters, field string) error {
	if err := checkAggField("--group-by", field); err != nil {
		return err
	}
	rows, err := callSite(cmd, fmt.Sprintf("Counting %s by %s…", doctype, field), func(ctx context.Context, c *client.FrappeClient) ([]map[string]interface{}, error) {
		return c.GroupByCount(ctx, doctype, filters, field)
	})
	var api *client.APIError
	if errors.As(err, &api) && api.Status == http.StatusInternalServerError && api.ExcType == "ValueError" {
		// "Field does not belong to doctype", raised as a bare ValueError.
		return &codeError{exitValidation, fmt.Sprintf("%q is not a field of %s (%s)", field, doctype, api.Message)}
	}
	if err != nil {
		return err
	}
	out, cols := groupCountRows(rows, field)
	if len(out) >= groupByCountMax {
		output.PrintWarning(fmt.Sprintf("%s may have more groups (the server returns at most %d); ffc aggregate -d %q --group-by %s counts all of them.", doctype, groupByCountMax, doctype, field))
	}
	return render(out, cols, func() error {
		output.PrintTable(out, cols)
		return nil
	})
}

// groupCountRows renames get_group_by_count's "name" to the field and
// returns the columns: the field, count, and title when the site sent one
// (v16, for a Link field whose DocType shows titles in links). A field named
// count or title keeps the site's "name", so no column hides another.
func groupCountRows(rows []map[string]interface{}, field string) ([]map[string]interface{}, []string) {
	if field == "count" || field == "title" {
		field = "name"
	}
	out := make([]map[string]interface{}, 0, len(rows))
	hasTitle := false
	for _, r := range rows {
		row := map[string]interface{}{field: r["name"], "count": r["count"]}
		if t, ok := r["title"]; ok {
			row["title"] = t
			hasTitle = true
		}
		out = append(out, row)
	}
	cols := []string{field, "count"}
	if hasTitle {
		cols = append(cols, "title")
	}
	return out, cols
}
