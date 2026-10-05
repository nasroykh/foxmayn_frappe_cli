package cmd

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// registerAggregate adds the aggregate tool: ffc aggregate for a model. Its
// group-by, aggregate and order-by fields are plain fieldnames, as in the
// CLI, so they never join another table (Frappe v16 would follow
// "link_field.field" into the linked DocType). Its filters can, like
// list_docs': scopeOf checks them (queryScope in mcp_query_scope.go).
func registerAggregate(s *server.MCPServer, env *mcpEnv) {
	fieldList := func(name, what string) mcp.ToolOption {
		return jsonParam(name, fmt.Sprintf(`Fields to %s, as an array ["grand_total"] or a comma-separated string. Each becomes the column "%s_<field>".`, what, name))
	}
	tool := mcp.NewTool("aggregate",
		mcp.WithDescription(fmt.Sprintf("Count, sum, average, min and max documents of a DocType per group, computed on the server without fetching rows: totals by status, invoices per customer, the date range of a set. Returns {doctype, rows, truncated}: one row per group with the group_by fields and one column per aggregate (count, sum_<field>, avg_<field>, min_<field>, max_<field>); without group_by one row over every match. With no aggregate it counts. Fields must be plain fieldnames of the DocType (no link_field.field or child_table.field). Rows are sorted by the first aggregate, largest first, unless order_by says otherwise; limit caps the groups (default %d, at most %d) and truncated says whether more existed. ffc writes the aggregates as the site's Frappe version expects (v16 refuses SQL functions as text in list_docs fields).", defaultAggregateLimit, maxMCPAggregateLimit)),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype", mcp.Required(), mcp.Description("The Frappe DocType, e.g. 'Sales Invoice'")),
		jsonParam("group_by", fmt.Sprintf(`Fields to group by, as an array ["status"] or a comma-separated string (at most %d). Omit for one row over all matching documents.`, maxGroupBy)),
		mcp.WithBoolean("count", mcp.Description(`Count documents per group (column "count"). The default when no other aggregate is given.`)),
		fieldList("sum", "sum"), fieldList("avg", "average"), fieldList("min", "take the smallest value of"), fieldList("max", "take the largest value of"),
		jsonParam("filters", `Filter expression, e.g. {"docstatus":1} or [["posting_date",">=","2026-01-01"]]`),
		mcp.WithString("order_by", mcp.Description(`Sort by group_by fields or aggregate columns, e.g. "sum_grand_total desc" or "status asc, count desc". Default: first aggregate, descending.`)),
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Maximum number of groups (1-%d, default %d).", maxMCPAggregateLimit, defaultAggregateLimit))),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		var a aggregateArgs
		for _, l := range []struct {
			key string
			dst *[]string
		}{{"group_by", &a.GroupBy}, {"sum", &a.Sum}, {"avg", &a.Avg}, {"min", &a.Min}, {"max", &a.Max}} {
			if *l.dst, err = keysArg(req, l.key); err != nil {
				return nil, err
			}
		}
		a.Count = req.GetBool("count", false)
		if a.Filters, err = rawJSONArg(req, "filters"); err != nil {
			return nil, err
		}
		if a.OrderBy, err = stringArg(req, "order_by"); err != nil {
			return nil, err
		}
		if a.Limit, err = intArg(req, "limit", defaultAggregateLimit); err != nil {
			return nil, err
		}
		if a.Limit < 1 || a.Limit > maxMCPAggregateLimit {
			return nil, fmt.Errorf("limit: must be between 1 and %d", maxMCPAggregateLimit)
		}
		q, err := buildAggregateQuery(a, "")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			site, _ := siteFrom(ctx) // nil: the version is unknown, runAggregate copes
			res, err := runAggregate(ctx, c, site, doctype, q)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"doctype": doctype, "rows": res.Rows, "truncated": res.Truncated}, nil
		}, nil
	}))
}
