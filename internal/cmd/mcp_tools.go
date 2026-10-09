package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// compactReportResult strips execution noise from a RunReport response,
// keeping only columns, result, report_summary (if non-nil), the truncation
// markers added by limitReportRows and, for a prepared report's result, the
// Prepared Report it came from.
func compactReportResult(r map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"columns": r["columns"],
		"result":  r["result"],
	}
	for _, k := range []string{"report_summary", "total_rows", "truncated"} {
		if v, ok := r[k]; ok && v != nil {
			out[k] = v
		}
	}
	if doc, ok := r["doc"].(map[string]interface{}); ok {
		out["prepared_report"] = map[string]interface{}{"name": doc["name"], "finished": doc["report_end_time"]}
	}
	return out
}

// mcpPreparedWait bounds how long run_report waits for a prepared report's
// job; a longer one is answered as queued, and the model calls again. Tests
// lower it.
var mcpPreparedWait = time.Minute

// runPreparedMCP is run_report with prepared. A read-only server only
// reuses: a finished result for these filters, or a job already queued for
// them (make_prepared_report inserts a document and starts a job). Progress
// notifications go out while it waits; a job still running when the wait
// ends is the answer {status: "queued", prepared_report: {name}, hint}, not
// an error (queued true).
func runPreparedMCP(ctx context.Context, c *client.FrappeClient, report string, filters map[string]interface{}, name string, fresh bool) (result map[string]interface{}, queued bool, err error) {
	policy, ok := policyFrom(ctx)
	if !ok {
		return nil, false, fmt.Errorf("policy: no policy to decide whether a prepared report may be started")
	}
	reuse := policy.readOnly()
	if reuse && fresh {
		return nil, false, fmt.Errorf("policy: fresh starts a new prepared report, and MCP is read-only for this site, so it only reuses one")
	}
	start, polls := time.Now(), 0
	opt := client.PreparedOptions{Fresh: fresh, Name: name, ReuseOnly: reuse, Wait: mcpPreparedWait,
		OnWait: func(n string) {
			polls++
			sendProgress(ctx, polls, 0, fmt.Sprintf("prepared report %s is queued or running (%s)", n, time.Since(start).Round(time.Second)))
		}}
	res, err := c.RunPreparedReport(ctx, report, filters, opt)
	var pe *client.PreparedReportError
	switch {
	case errors.As(err, &pe) && pe.Pending:
		return map[string]interface{}{
			"status":          "queued",
			"prepared_report": map[string]interface{}{"name": pe.Name},
			"hint": fmt.Sprintf("The site's background job is still running after %s. Call run_report again with prepared_report %q and the same filters to get the result. A job that never finishes usually means the site has no worker on its \"long\" queue.",
				pe.Waited, pe.Name),
		}, true, nil
	case errors.Is(err, client.ErrNoPreparedReport):
		return nil, false, fmt.Errorf("%w; MCP is read-only for this site, so it does not start one (ffc run-report -n %s --prepared does)", err, shellQuote(report))
	}
	return res, false, err
}

// registerTools adds the Frappe API tools the policy allows to the MCP
// server. With read_only (or --read-only) only tools that cannot change data
// are exposed: tool annotations are hints a client may ignore, while an
// unregistered tool cannot be called at all. The policy is checked again on
// every call, so a later config edit can still narrow it (widening it needs
// a restart).
//
// With several sites, a tool is registered when some site's policy allows
// it, each call is checked against its own site's policy, and every site
// tool gets a required `site` argument. --toolsets narrows the tools too.
// The resources, prompts and instructions (mcp_surface.go) follow the tools
// that remain.
func registerTools(s *server.MCPServer, env *mcpEnv, policies []mcpPolicy) {
	registerAllTools(s, env)
	var drop []string
	for name := range s.ListTools() {
		if anyAllows(policies, name) != nil || !env.inToolsets(name) {
			drop = append(drop, name)
		}
	}
	s.DeleteTools(drop...)
	describeTools(s)
	addSiteParam(s, env)
	registerSurface(s, env, policies)
}

func registerAllTools(s *server.MCPServer, env *mcpEnv) {
	registerListSites(s, env)
	registerPing(s, env)
	registerGetDoc(s, env)
	registerListDocs(s, env)
	registerCountDocs(s, env)
	registerAggregate(s, env)
	registerGetSchema(s, env)
	registerListDoctypes(s, env)
	registerListReports(s, env)
	registerRunReport(s, env)
	registerSearch(s, env)
	registerGetDocContext(s, env)
	registerGetTransitions(s, env)
	registerCreateDoc(s, env)
	registerUpdateDoc(s, env)
	registerDeleteDoc(s, env)
	registerCallMethod(s, env)
	registerBulkCreate(s, env)
	registerBulkUpdate(s, env)
	registerBulkDelete(s, env)
	registerLifecycleTools(s, env)
	registerIdentityTools(s, env)
	registerCollabTools(s, env)
	registerAdminTools(s, env)
	registerFileTools(s, env)
	registerERPTools(s, env)
}

// jsonParam declares a parameter that takes a JSON value. It has no fixed
// schema type so a model may pass native JSON or a JSON-encoded string.
func jsonParam(name, desc string, opts ...mcp.PropertyOption) mcp.ToolOption {
	return mcp.WithAny(name, append([]mcp.PropertyOption{mcp.Description(desc)}, opts...)...)
}

func registerPing(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("ping",
		mcp.WithDescription("Check that the Frappe site is reachable. frappe.ping answers without credentials, so a pong does not prove the configured login works; any other tool call does."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	)
	s.AddTool(tool, toolHandler(env, func(mcp.CallToolRequest) (toolCall, error) {
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			resp, err := c.Ping(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"response": resp, "status": "ok"}, nil
		}, nil
	}))
}

func registerGetDoc(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("get_doc",
		mcp.WithDescription("Retrieve a single Frappe document by its DocType and name. Returns all fields of the document, or only the requested fields. Use this when you know the exact document identifier. For Single DocTypes (e.g. 'System Settings', 'HR Settings'), omit name — the DocType name is used automatically."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType, e.g. 'Sales Invoice', 'Customer', 'ToDo'"),
		),
		mcp.WithString("name",
			mcp.Description("The unique name/ID of the document, e.g. 'SINV-00001', 'jane@example.com'. Omit for Single DocTypes."),
		),
		jsonParam("fields", `Array of top-level field names to return, e.g. ["name","status","grand_total"]. Omit for all fields.`),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		name, err := nameArg(req, "name", false)
		if err != nil {
			return nil, err
		}
		name = docNameOrSingle(name, doctype)
		fields, err := stringsArg(req, "fields")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			if len(fields) > 0 {
				return fetchDocFields(ctx, c, doctype, name, fields)
			}
			return c.GetDoc(ctx, doctype, name)
		}, nil
	}))
}

func registerListDocs(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("list_docs",
		mcp.WithDescription("List documents from a Frappe DocType with optional filtering, field selection, ordering, and pagination. Returns an array of document objects. Use this to search and browse records; page with start and limit rather than fetching everything."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType to list, e.g. 'Sales Invoice', 'Customer'"),
		),
		jsonParam("fields", `Array of field names to return, e.g. ["name","status","grand_total"]. If omitted, returns default fields.`),
		jsonParam("filters", `Filter expression. Object format: {"status":"Paid"} or array format: [["status","=","Paid"]]`),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of records to return. Default: 20. 0 means no limit. Rows that do not fit in 512 KiB are cut: the answer is then {data, truncated, next_start, hint}; call again with start=next_start for the rest."),
		),
		mcp.WithNumber("start",
			mcp.Description("Offset into the result set, for pagination. Default: 0."),
		),
		mcp.WithString("order_by",
			mcp.Description("Sort expression, e.g. 'modified desc', 'name asc'"),
		),
		responseFormatOption(`without fields, concise returns each document's name only and detailed every field ("*"); with fields, both return those fields.`),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		fields, err := stringsArg(req, "fields")
		if err != nil {
			return nil, err
		}
		detailed, err := detailedArg(req)
		if err != nil {
			return nil, err
		}
		if detailed && len(fields) == 0 {
			fields = []string{"*"}
		}
		filters, err := rawJSONArg(req, "filters")
		if err != nil {
			return nil, err
		}
		limit, err := intArg(req, "limit", 20)
		if err != nil {
			return nil, err
		}
		start, err := intArg(req, "start", 0)
		if err != nil {
			return nil, err
		}
		limit, _ = listLimit("limit", limit)
		orderBy, err := stringArg(req, "order_by")
		if err != nil {
			return nil, err
		}
		opts := client.ListOptions{
			Fields:  fields,
			Filters: filters,
			Limit:   limit,
			Start:   start,
			OrderBy: orderBy,
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			rows, err := c.GetList(ctx, doctype, opts)
			if err != nil {
				return nil, err
			}
			return fitListRows(rows, start), nil
		}, nil
	}))
}

// countDocsSchema is count_docs' output schema (its structuredContent).
// With at_least, count is null when the site gave up counting.
var countDocsSchema = json.RawMessage(`{"type":"object","properties":{"doctype":{"type":"string"},"count":{"type":["integer","null"],"minimum":0},"at_least":{"type":"integer","minimum":1},"result":{"type":["boolean","null"]},"warning":{"type":"string"}},"required":["doctype","count"]}`)

func registerCountDocs(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("count_docs",
		mcp.WithDescription("Count the number of documents in a DocType, optionally filtered. Returns a single integer count. More efficient than list_docs when you only need the count."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		jsonParam("filters", `Filter expression, e.g. {"status":"Open"} or [["status","=","Open"]]`),
		mcp.WithNumber("at_least",
			mcp.Description("Only answer whether at least this many documents match: the site stops counting there, which is cheaper on large tables. Returns result (true/false) and count (exact below at_least, at_least otherwise); both are null, with a warning, when the site gave up counting (1 second limit on MariaDB)."),
		),
		mcp.WithRawOutputSchema(countDocsSchema),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		filters, err := rawJSONArg(req, "filters")
		if err != nil {
			return nil, err
		}
		atLeast, err := intArg(req, "at_least", 0)
		if err != nil {
			return nil, err
		}
		if v := req.GetArguments()["at_least"]; v != nil && atLeast < 1 {
			return nil, fmt.Errorf("at_least: must be at least 1")
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			if atLeast > 0 {
				m, err := atLeastResult(ctx, c, doctype, filters, atLeast)
				if err != nil {
					return nil, err
				}
				return structuredOut{Text: m, Structured: m}, nil
			}
			count, err := c.GetCount(ctx, doctype, filters)
			if err != nil {
				return nil, err
			}
			m := map[string]interface{}{"doctype": doctype, "count": count}
			return structuredOut{Text: m, Structured: m}, nil
		}, nil
	}))
}

func registerGetSchema(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("get_schema",
		mcp.WithDescription("Get the definition of a Frappe DocType as the desk sees it (custom fields and Customize Form overrides applied): module, naming rule, submittability, permissions and all field metadata (fieldname, label, fieldtype, required, options, defaults, constraints). By default returns a compact view with zero-value noise and internal Frappe metadata stripped. Pass full=true for the raw response, or keys to select specific top-level properties."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The DocType to inspect, e.g. 'Sales Invoice', 'Customer'"),
		),
		mcp.WithBoolean("full",
			mcp.Description("Set to true to return the complete unfiltered Frappe response instead of the compact view."),
		),
		jsonParam("keys", `Top-level keys to include, as an array (["name","module","fields"]) or a comma-separated string ("name,module,fields"). Applied after compact/full filtering.`),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		full := req.GetBool("full", false)
		keys, err := keysArg(req, "keys")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			doc, warnings, err := fetchSchema(ctx, c, doctype)
			if err != nil {
				return nil, err
			}
			result := doc
			if !full {
				result = compactSchema(doc)
			}
			if len(keys) > 0 {
				var missing []string
				result, missing = filterKeys(result, keys)
				if len(missing) > 0 {
					warnings = append(warnings, "keys not present: "+strings.Join(missing, ", "))
				}
			}
			if len(warnings) > 0 {
				result["_warnings"] = warnings
			}
			return result, nil
		}, nil
	}))
}

// registerModuleList registers list_doctypes / list_reports, which share
// their arguments and differ only in the DocType listed and fields fetched.
func registerModuleList(s *server.MCPServer, env *mcpEnv, name, desc, doctype string, fields []string) {
	tool := mcp.NewTool(name,
		mcp.WithDescription(desc),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("module",
			mcp.Description("Filter by module name, e.g. 'Accounts', 'Selling', 'HR'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of rows to return. Default: 50. Use 0 for no limit."),
		),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		limit, err := intArg(req, "limit", 50)
		if err != nil {
			return nil, err
		}
		module, err := stringArg(req, "module")
		if err != nil {
			return nil, err
		}
		opts, err := moduleListOptions(fields, module, limit)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.GetList(ctx, doctype, opts)
		}, nil
	}))
}

func registerListDoctypes(s *server.MCPServer, env *mcpEnv) {
	registerModuleList(s, env, "list_doctypes",
		"List all DocTypes available on the Frappe site, optionally filtered by module. Returns name, module, and description for each DocType. Use this to discover what data types exist on the site.",
		"DocType", doctypeListFields)
}

func registerListReports(s *server.MCPServer, env *mcpEnv) {
	registerModuleList(s, env, "list_reports",
		"List available reports on the Frappe site, optionally filtered by module. Returns report name, type, module, and reference DocType.",
		"Report", reportListFields)
}

func registerRunReport(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("run_report",
		mcp.WithDescription("Execute a Frappe query report and return its columns and data rows. Strips execution metadata (timing, chart config). Includes report_summary if the report provides one. When rows are cut by limit, total_rows and truncated are included. Use list_reports first to discover available report names."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("report_name",
			mcp.Required(),
			mcp.Description("The name of the report to run, e.g. 'General Ledger', 'Accounts Receivable'"),
		),
		jsonParam("filters", `Report filter values as an object, e.g. {"company":"My Company","from_date":"2025-01-01"}`),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of result rows to return. Default: 500. Use 0 for all rows. Rows that do not fit in 512 KiB are dropped from the end (truncated, total_rows and a hint say so): narrow the filters instead."),
		),
		responseFormatOption("concise drops execution metadata (timing, chart, message noise); detailed returns the report as Frappe answered it, rows still limited."),
		mcp.WithBoolean("prepared",
			mcp.Description(fmt.Sprintf("Use Frappe's prepared reports for a heavy report that outlasts a request: a background job on the site's \"long\" queue runs it. Returns your finished result for the same filters (prepared_report names it and when it finished; it may be old), else waits for your queued job or starts one, for at most %s. A job still running then is answered {status: \"queued\", prepared_report: {name}, hint}: call again with prepared_report. A read-only server never starts a job, it only reuses one. A report not marked prepared runs normally.", mcpPreparedWait)),
		),
		mcp.WithBoolean("fresh",
			mcp.Description("With prepared: ignore a finished result and start a new job (not on a read-only server)."),
		),
		mcp.WithString("prepared_report",
			mcp.Description("Wait for this Prepared Report (from an earlier queued answer, same filters) and return its result. Implies prepared."),
		),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		reportName, err := req.RequireString("report_name")
		if err != nil {
			return nil, err
		}
		preparedName, err := stringArg(req, "prepared_report")
		if err != nil {
			return nil, err
		}
		prepared, fresh := req.GetBool("prepared", false) || preparedName != "", req.GetBool("fresh", false)
		switch {
		case fresh && preparedName != "":
			return nil, fmt.Errorf("fresh and prepared_report cannot be combined")
		case fresh && !prepared:
			return nil, fmt.Errorf("fresh needs prepared")
		}
		filters, err := objectArg(req, "filters", false)
		if err != nil {
			return nil, err
		}
		limit, err := intArg(req, "limit", 500)
		if err != nil {
			return nil, err
		}
		detailed, err := detailedArg(req)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			var result map[string]interface{}
			if prepared {
				var queued bool
				result, queued, err = runPreparedMCP(ctx, c, reportName, filters, preparedName, fresh)
				if queued {
					return result, nil
				}
			} else {
				result, err = c.RunReport(ctx, reportName, filters)
			}
			if err != nil {
				return nil, err
			}
			limitReportRows(result, limit)
			if detailed {
				return fitReportRows(result), nil
			}
			return fitReportRows(compactReportResult(result)), nil
		}, nil
	}))
}

func registerSearch(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("search",
		mcp.WithDescription(fmt.Sprintf("Find documents by text. With doctype, it resolves a name or title to document names the way a Link field does (search fields, title, link query and user permissions apply): each result has value (the document name), description and sometimes label; use it to turn \"Acme\" into the name \"CUST-0042\". An empty text lists the first documents of that DocType. Without doctype it runs Frappe's global search across DocTypes, ranked by relevance, and returns {results, hidden_by_policy}: each result has doctype, name, content, rank and sometimes title, and hidden_by_policy counts the hits dropped because this server may not read their DocType. \"a & b\" searches each phrase separately (at most %d phrases). Global search covers only DocTypes in Global Search Settings and fields flagged In Global Search, so a DocType missing there never matches (use list_docs with filters instead). A doctype search is cacheable for 60 seconds, so a proxy may serve a document created a moment ago late.", maxSearchPhrases)),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("text",
			mcp.Required(),
			mcp.Description("Text to search for"),
		),
		mcp.WithString("doctype",
			mcp.Description("Resolve names in this DocType only. Omit to search every DocType."),
		),
		mcp.WithNumber("limit",
			mcp.Description(fmt.Sprintf("Maximum number of results. Default: %d, at most %d.", defaultSearchLimit, maxMCPSearchLimit)),
		),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		text, err := req.RequireString("text")
		if err != nil {
			return nil, err
		}
		doctype, err := stringArg(req, "doctype")
		if err != nil {
			return nil, err
		}
		doctype = strings.TrimSpace(doctype)
		limit, err := intArg(req, "limit", defaultSearchLimit)
		if err != nil {
			return nil, err
		}
		if limit > maxMCPSearchLimit {
			return nil, fmt.Errorf("limit: at most %d", maxMCPSearchLimit)
		}
		if err := validateSearch(text, doctype, limit); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			rows, err := runSearch(ctx, c, text, doctype, limit)
			if err != nil {
				return nil, err
			}
			if doctype != "" {
				return rows, nil
			}
			policy, ok := policyFrom(ctx)
			if !ok {
				return nil, fmt.Errorf("policy: no policy to filter the global search with")
			}
			rows, hidden := policy.filterDoctypeRows(rows)
			return map[string]interface{}{"results": rows, "hidden_by_policy": hidden}, nil
		}, nil
	}))
}

func registerCreateDoc(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("create_doc",
		mcp.WithDescription("Create a new document in a Frappe DocType. Provide field values as a JSON object. Returns the created document with all server-generated fields (name, creation date, etc.)."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType, e.g. 'ToDo', 'Note'"),
		),
		jsonParam("data", `Object of field values, e.g. {"description":"Buy milk","priority":"Medium"}`, mcp.Required()),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		data, err := objectArg(req, "data", true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.CreateDoc(ctx, doctype, data)
		}, nil
	}))
}

func registerUpdateDoc(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("update_doc",
		mcp.WithDescription("Update an existing Frappe document. Provide only the fields you want to change as a JSON object (a \"name\" key in data is ignored). Returns the full updated document. For Single DocTypes (e.g. 'System Settings'), omit name — the DocType name is used automatically."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		mcp.WithString("name",
			mcp.Description("The name/ID of the document to update. Omit for Single DocTypes."),
		),
		jsonParam("data", `Object of fields to update, e.g. {"status":"Closed"}`, mcp.Required()),
		mcp.WithString("if_unmodified",
			mcp.Description(`The document's "modified" value as you read it (get_doc). If someone saved the document since, nothing is saved and the call fails with TimestampMismatchError: read it again and retry.`),
		),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		name, err := nameArg(req, "name", false)
		if err != nil {
			return nil, err
		}
		name = docNameOrSingle(name, doctype)
		data, err := objectArg(req, "data", true)
		if err != nil {
			return nil, err
		}
		data = withoutName(data)
		since := "the modified timestamp given"
		if v := req.GetString("if_unmodified", ""); v != "" {
			data["modified"] = v
			since = fmt.Sprintf("if_unmodified %q", v)
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			d, err := c.UpdateDoc(ctx, doctype, name, data)
			return d, conflictError(err, doctype, name, since)
		}, nil
	}))
}

func registerDeleteDoc(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("delete_doc",
		mcp.WithDescription("Permanently delete a Frappe document. This action cannot be undone. Returns a confirmation message on success."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("The name/ID of the document to delete"),
		),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		name, err := nameArg(req, "name", true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			if err := c.DeleteDoc(ctx, doctype, name); err != nil {
				return nil, err
			}
			return fmt.Sprintf("Deleted %s %s", doctype, name), nil
		}, nil
	}))
}

func registerCallMethod(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("call_method",
		mcp.WithDescription("Call a whitelisted Frappe server method. This is a low-level escape hatch for operations not covered by other tools. The method must be whitelisted (@frappe.whitelist()) on the server. It may read or mutate data depending on the method."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("method",
			mcp.Required(),
			mcp.Description("The dotted method path, e.g. 'frappe.client.get_count', 'erpnext.api.get_currency'"),
		),
		jsonParam("args", `Object of method arguments, e.g. {"doctype":"ToDo","filters":{"status":"Open"}}`),
		mcp.WithBoolean("get",
			mcp.Description("Send as a GET request (for methods whitelisted GET-only). Default: false (POST)."),
		),
		mcp.WithBoolean("full_response",
			mcp.Description(`Return the whole response object instead of only "message": desk methods put "docs", "docinfo" and "_server_messages" next to it. Default: false.`),
		),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		method, err := req.RequireString("method")
		if err != nil {
			return nil, err
		}
		args, err := objectArg(req, "args", false)
		if err != nil {
			return nil, err
		}
		get, full := req.GetBool("get", false), req.GetBool("full_response", false)
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			if full {
				return c.CallMethodFull(ctx, method, args, get)
			}
			return c.CallMethod(ctx, method, args, get)
		}, nil
	}))
}

// bulkTool runs a validated bulk operation for an MCP tool with the given
// number of workers. Results stay in input order whatever the workers do.
func bulkTool(n, workers int, done string, op func(ctx context.Context, c *client.FrappeClient, i int) (string, error)) (toolCall, error) {
	if n > maxMCPBulkItems {
		return nil, fmt.Errorf("too many items (%d): a single call may touch at most %d documents", n, maxMCPBulkItems)
	}
	return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
		// Count and notify under one lock, so the progress values a client
		// sees only go up.
		var mu sync.Mutex
		finished := 0
		rep := runBulk(ctx, n, workers, false, done, func(ctx context.Context, i int) (string, error) {
			name, err := op(ctx, c, i)
			mu.Lock()
			finished++
			notifyProgress(ctx, finished, n)
			mu.Unlock()
			return name, err
		})
		return rep.JSON(), nil
	}, nil
}

// concurrencyArg reads the optional concurrency of a bulk tool, 1 to
// maxMCPBulkWorkers. The default, 1, runs the items in input order, one at a
// time: parallel items must not depend on each other (links between them, a
// shared parent), which only the caller knows.
func concurrencyArg(req mcp.CallToolRequest) (int, error) {
	n, err := intArg(req, "concurrency", 1)
	if err != nil {
		return 0, usageErrorf("%v", err)
	}
	if n < 1 || n > maxMCPBulkWorkers {
		return 0, usageErrorf("concurrency: expected 1 to %d, got %d", maxMCPBulkWorkers, n)
	}
	return n, nil
}

// concurrencyParam declares the concurrency argument of a bulk tool.
func concurrencyParam() mcp.ToolOption {
	return mcp.WithNumber("concurrency",
		mcp.Description(fmt.Sprintf("Items to run at once, 1 to %d. Default 1: in input order, one at a time. Raise it only when the items do not depend on each other; order is then not kept.", maxMCPBulkWorkers)),
	)
}

// refuseDuplicateNames is a usage error when two items name the same
// document: with several workers their writes would race. Frappe compares
// names case-insensitively on MariaDB, so so does this.
func refuseDuplicateNames(names []string) error {
	seen := make(map[string]int, len(names))
	for i, n := range names {
		key := strings.ToLower(strings.TrimSpace(n))
		if first, ok := seen[key]; ok {
			return usageErrorf("items %d and %d name the same document, %q: with concurrency above 1 items run in parallel, so name each document once per call", first, i+1, n)
		}
		seen[key] = i + 1
	}
	return nil
}

func registerBulkCreate(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("bulk_create",
		mcp.WithDescription(fmt.Sprintf("Create multiple Frappe documents in one call (at most %d). Each element of the data array is an object of field values for one document. Returns per-item results with created names or error messages. Items are created in input order, one at a time, unless you set concurrency (up to %d) for items that do not depend on each other (no links between them, no tree parent among them); order is then not kept. Processing continues on individual failures. Set atomic to true for all or none: the items go to the site in one request and one database transaction (at most %d), so if any item is invalid nothing is created and the call fails with that item's error instead of a per-item report (a hook that commits, or a schema change such as a Custom Field, ends the transaction early). atomic cannot be combined with concurrency, and every item must be of the given doctype.", maxMCPBulkItems, maxMCPBulkWorkers, atomicLimit)),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType, e.g. 'ToDo', 'Note'"),
		),
		jsonParam("data", `Array of field-value objects, e.g. [{"description":"Task 1"},{"description":"Task 2"}]`, mcp.Required()),
		concurrencyParam(),
		mcp.WithBoolean("atomic",
			mcp.Description(fmt.Sprintf("Create all items in one request and one transaction: all or none, at most %d items. Not with concurrency. Default: false.", atomicLimit)),
		),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		raw, err := rawArg(req, "data")
		if err != nil {
			return nil, err
		}
		items, err := parseObjects(raw)
		if err != nil {
			return nil, fmt.Errorf("data: %w", err)
		}
		atomic, err := atomicArg(req)
		if err != nil {
			return nil, err
		}
		if atomic {
			return atomicCreate(req, doctype, items)
		}
		workers, err := concurrencyArg(req)
		if err != nil {
			return nil, err
		}
		return bulkTool(len(items), workers, "created", func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
			doc, err := c.CreateDoc(ctx, doctype, items[i])
			if err != nil {
				return "", err
			}
			name, _ := docName(doc["name"])
			return name, nil
		})
	}))
}

// atomicLimit is the most items an atomic bulk_create takes: what insert_many
// accepts, and no more than any bulk tool touches.
var atomicLimit = min(maxMCPBulkItems, client.MaxInsertMany)

// atomicArg reads the optional atomic flag of bulk_create. A value that is
// not a boolean is an error and never read as false: the caller asked for all
// or none, and must not get one-by-one creation instead.
func atomicArg(req mcp.CallToolRequest) (bool, error) {
	return strictBoolArg(req, "atomic")
}

// atomicCreate is bulk_create with atomic: one insert_many request. Everything
// that can be refused is refused before it, so a bad call sends nothing. The
// result has the shape of a per-item run; a failure is a tool error that says
// what it means for the batch (atomicError, as for ffc bulk-create --atomic).
// There is one request, so no progress is reported; the call's context ends it.
func atomicCreate(req mcp.CallToolRequest, doctype string, items []map[string]interface{}) (toolCall, error) {
	if v, ok := req.GetArguments()["concurrency"]; ok && v != nil {
		return nil, usageErrorf("atomic sends one request, so concurrency does not apply")
	}
	if len(items) > atomicLimit {
		return nil, fmt.Errorf("too many items (%d): an atomic call may create at most %d documents", len(items), atomicLimit)
	}
	docs, err := client.InsertManyDocs(doctype, items)
	if err != nil {
		return nil, usageErrorf("atomic: %w", err)
	}
	return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
		names, err := c.InsertMany(ctx, doctype, docs)
		if err != nil {
			return nil, atomicError(err)
		}
		return createdReport(names).JSON(), nil
	}, nil
}

func registerBulkUpdate(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("bulk_update",
		mcp.WithDescription(fmt.Sprintf("Update multiple Frappe documents in one call (at most %d). Each element of the data array must include a \"name\" field identifying the document plus any fields to change. Returns per-item results. Items are updated in input order, one at a time, unless you set concurrency (up to %d) for items that do not depend on each other (no shared parent); order is then not kept and each document may be named only once. Processing continues on individual failures.", maxMCPBulkItems, maxMCPBulkWorkers)),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		jsonParam("data", `Array of objects; each must include "name" plus fields to update, e.g. [{"name":"TD-0001","status":"Closed"}]`, mcp.Required()),
		concurrencyParam(),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		raw, err := rawArg(req, "data")
		if err != nil {
			return nil, err
		}
		items, err := parseObjects(raw)
		if err != nil {
			return nil, fmt.Errorf("data: %w", err)
		}
		names, payloads, err := splitUpdates(items)
		if err != nil {
			return nil, fmt.Errorf("data: %w", err)
		}
		workers, err := concurrencyArg(req)
		if err != nil {
			return nil, err
		}
		if workers > 1 {
			if err := refuseDuplicateNames(names); err != nil {
				return nil, err
			}
		}
		return bulkTool(len(items), workers, "updated", func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
			_, err := c.UpdateDoc(ctx, doctype, names[i], payloads[i])
			return names[i], err
		})
	}))
}

func registerBulkDelete(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("bulk_delete",
		mcp.WithDescription(fmt.Sprintf("Permanently delete multiple Frappe documents in one call (at most %d). Provide document names as an array. Returns per-item results. Documents are deleted in the order given, one at a time (delete a document that links to another one first), unless you set concurrency (up to %d) for documents that are not linked to each other; order is then not kept and each may be named only once. Processing continues on individual failures. This action cannot be undone.", maxMCPBulkItems, maxMCPBulkWorkers)),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		jsonParam("names", `Array of document names to delete, e.g. ["TD-0001","TD-0002"]`, mcp.Required()),
		concurrencyParam(),
	)
	s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		raw, err := rawArg(req, "names")
		if err != nil {
			return nil, err
		}
		names, err := parseNames(raw)
		if err != nil {
			return nil, fmt.Errorf("names: %w", err)
		}
		workers, err := concurrencyArg(req)
		if err != nil {
			return nil, err
		}
		if workers > 1 {
			if err := refuseDuplicateNames(names); err != nil {
				return nil, err
			}
		}
		return bulkTool(len(names), workers, "deleted", func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
			return names[i], c.DeleteDoc(ctx, doctype, names[i])
		})
	}))
}
