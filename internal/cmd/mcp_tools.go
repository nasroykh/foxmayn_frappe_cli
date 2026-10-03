package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// compactReportResult strips execution noise from a RunReport response,
// keeping only columns, result, report_summary (if non-nil) and the
// truncation markers added by limitReportRows.
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
	return out
}

// registerTools adds the Frappe API tools to the MCP server. With --read-only
// only tools that cannot change data are exposed: tool annotations are hints
// a client may ignore, while an unregistered tool cannot be called at all.
func registerTools(s *server.MCPServer, getClient clientFn) {
	registerPing(s, getClient)
	registerGetDoc(s, getClient)
	registerListDocs(s, getClient)
	registerCountDocs(s, getClient)
	registerGetSchema(s, getClient)
	registerListDoctypes(s, getClient)
	registerListReports(s, getClient)
	registerRunReport(s, getClient)
	if mcpReadOnly {
		return
	}
	registerCreateDoc(s, getClient)
	registerUpdateDoc(s, getClient)
	registerDeleteDoc(s, getClient)
	registerCallMethod(s, getClient)
	registerBulkCreate(s, getClient)
	registerBulkUpdate(s, getClient)
	registerBulkDelete(s, getClient)
}

// jsonParam declares a parameter that takes a JSON value. It has no fixed
// schema type so a model may pass native JSON or a JSON-encoded string.
func jsonParam(name, desc string, opts ...mcp.PropertyOption) mcp.ToolOption {
	return mcp.WithAny(name, append([]mcp.PropertyOption{mcp.Description(desc)}, opts...)...)
}

func registerPing(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("ping",
		mcp.WithDescription("Check connectivity to the Frappe site. Returns the server response and URL. Use this first to verify the connection is working before making other calls."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	)
	s.AddTool(tool, toolHandler(getClient, func(mcp.CallToolRequest) (toolCall, error) {
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			resp, err := c.Ping(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"response": resp, "status": "ok"}, nil
		}, nil
	}))
}

func registerGetDoc(s *server.MCPServer, getClient clientFn) {
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
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		name := docNameOrSingle(req.GetString("name", ""), doctype)
		fields, err := stringsArg(req, "fields")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			doc, err := c.GetDoc(ctx, doctype, name)
			if err != nil || len(fields) == 0 {
				return doc, err
			}
			out, _ := filterKeys(doc, fields)
			return out, nil
		}, nil
	}))
}

func registerListDocs(s *server.MCPServer, getClient clientFn) {
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
			mcp.Description("Maximum number of records to return. Default: 20. 0 means no limit (results over 512 KiB are refused)."),
		),
		mcp.WithNumber("start",
			mcp.Description("Offset into the result set, for pagination. Default: 0."),
		),
		mcp.WithString("order_by",
			mcp.Description("Sort expression, e.g. 'modified desc', 'name asc'"),
		),
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		fields, err := stringsArg(req, "fields")
		if err != nil {
			return nil, err
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
		opts := client.ListOptions{
			Fields:  fields,
			Filters: filters,
			Limit:   limit,
			Start:   start,
			OrderBy: req.GetString("order_by", ""),
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.GetList(ctx, doctype, opts)
		}, nil
	}))
}

func registerCountDocs(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("count_docs",
		mcp.WithDescription("Count the number of documents in a DocType, optionally filtered. Returns a single integer count. More efficient than list_docs when you only need the count."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		jsonParam("filters", `Filter expression, e.g. {"status":"Open"} or [["status","=","Open"]]`),
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		filters, err := rawJSONArg(req, "filters")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			count, err := c.GetCount(ctx, doctype, filters)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"doctype": doctype, "count": count}, nil
		}, nil
	}))
}

func registerGetSchema(s *server.MCPServer, getClient clientFn) {
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
		mcp.WithString("keys",
			mcp.Description("Comma-separated top-level keys to include, e.g. 'fields' or 'name,module,fields'. Applied after compact/full filtering."),
		),
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		full := req.GetBool("full", false)
		keys := splitCSV(req.GetString("keys", ""))
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
func registerModuleList(s *server.MCPServer, getClient clientFn, name, desc, doctype string, fields []string) {
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
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		limit, err := intArg(req, "limit", 50)
		if err != nil {
			return nil, err
		}
		opts, err := moduleListOptions(fields, req.GetString("module", ""), limit)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.GetList(ctx, doctype, opts)
		}, nil
	}))
}

func registerListDoctypes(s *server.MCPServer, getClient clientFn) {
	registerModuleList(s, getClient, "list_doctypes",
		"List all DocTypes available on the Frappe site, optionally filtered by module. Returns name, module, and description for each DocType. Use this to discover what data types exist on the site.",
		"DocType", doctypeListFields)
}

func registerListReports(s *server.MCPServer, getClient clientFn) {
	registerModuleList(s, getClient, "list_reports",
		"List available reports on the Frappe site, optionally filtered by module. Returns report name, type, module, and reference DocType.",
		"Report", reportListFields)
}

func registerRunReport(s *server.MCPServer, getClient clientFn) {
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
			mcp.Description("Maximum number of result rows to return. Default: 500. Use 0 for all rows (results over 512 KiB are refused)."),
		),
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		reportName, err := req.RequireString("report_name")
		if err != nil {
			return nil, err
		}
		filters, err := objectArg(req, "filters", false)
		if err != nil {
			return nil, err
		}
		limit, err := intArg(req, "limit", 500)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			result, err := c.RunReport(ctx, reportName, filters)
			if err != nil {
				return nil, err
			}
			limitReportRows(result, limit)
			return compactReportResult(result), nil
		}, nil
	}))
}

func registerCreateDoc(s *server.MCPServer, getClient clientFn) {
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
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
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

func registerUpdateDoc(s *server.MCPServer, getClient clientFn) {
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
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		name := docNameOrSingle(req.GetString("name", ""), doctype)
		data, err := objectArg(req, "data", true)
		if err != nil {
			return nil, err
		}
		data = withoutName(data)
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.UpdateDoc(ctx, doctype, name, data)
		}, nil
	}))
}

func registerDeleteDoc(s *server.MCPServer, getClient clientFn) {
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
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return nil, err
		}
		name, err := req.RequireString("name")
		if err != nil {
			return nil, err
		}
		if name == "" {
			return nil, fmt.Errorf("name must not be empty")
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			if err := c.DeleteDoc(ctx, doctype, name); err != nil {
				return nil, err
			}
			return fmt.Sprintf("Deleted %s %s", doctype, name), nil
		}, nil
	}))
}

func registerCallMethod(s *server.MCPServer, getClient clientFn) {
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
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
		method, err := req.RequireString("method")
		if err != nil {
			return nil, err
		}
		args, err := objectArg(req, "args", false)
		if err != nil {
			return nil, err
		}
		get := req.GetBool("get", false)
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.CallMethod(ctx, method, args, get)
		}, nil
	}))
}

// bulkTool runs a validated bulk operation for an MCP tool.
func bulkTool(n int, done string, op func(ctx context.Context, c *client.FrappeClient, i int) (string, error)) (toolCall, error) {
	if n > maxMCPBulkItems {
		return nil, fmt.Errorf("too many items (%d): a single call may touch at most %d documents", n, maxMCPBulkItems)
	}
	return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
		rep := runBulk(ctx, n, 1, false, done, func(ctx context.Context, i int) (string, error) {
			return op(ctx, c, i)
		})
		return rep.JSON(), nil
	}, nil
}

func registerBulkCreate(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("bulk_create",
		mcp.WithDescription(fmt.Sprintf("Create multiple Frappe documents in one call (at most %d). Each element of the data array is an object of field values for one document. Returns per-item results with created names or error messages. Processing continues on individual failures.", maxMCPBulkItems)),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType, e.g. 'ToDo', 'Note'"),
		),
		jsonParam("data", `Array of field-value objects, e.g. [{"description":"Task 1"},{"description":"Task 2"}]`, mcp.Required()),
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
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
		return bulkTool(len(items), "created", func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
			doc, err := c.CreateDoc(ctx, doctype, items[i])
			if err != nil {
				return "", err
			}
			name, _ := docName(doc["name"])
			return name, nil
		})
	}))
}

func registerBulkUpdate(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("bulk_update",
		mcp.WithDescription(fmt.Sprintf("Update multiple Frappe documents in one call (at most %d). Each element of the data array must include a \"name\" field identifying the document plus any fields to change. Returns per-item results. Processing continues on individual failures.", maxMCPBulkItems)),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		jsonParam("data", `Array of objects; each must include "name" plus fields to update, e.g. [{"name":"TD-0001","status":"Closed"}]`, mcp.Required()),
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
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
		return bulkTool(len(items), "updated", func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
			_, err := c.UpdateDoc(ctx, doctype, names[i], payloads[i])
			return names[i], err
		})
	}))
}

func registerBulkDelete(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("bulk_delete",
		mcp.WithDescription(fmt.Sprintf("Permanently delete multiple Frappe documents in one call (at most %d). Provide document names as an array. Returns per-item results. Processing continues on individual failures. This action cannot be undone.", maxMCPBulkItems)),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		jsonParam("names", `Array of document names to delete, e.g. ["TD-0001","TD-0002"]`, mcp.Required()),
	)
	s.AddTool(tool, toolHandler(getClient, func(req mcp.CallToolRequest) (toolCall, error) {
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
		return bulkTool(len(names), "deleted", func(ctx context.Context, c *client.FrappeClient, i int) (string, error) {
			return names[i], c.DeleteDoc(ctx, doctype, names[i])
		})
	}))
}
