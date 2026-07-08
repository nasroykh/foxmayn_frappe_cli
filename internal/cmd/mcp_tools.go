package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// clientFn returns a ready-to-use FrappeClient for a single tool call. It is
// invoked per request so that OAuth tokens are refreshed and username/password
// sessions re-logged-in, instead of using one client captured at startup that
// goes stale after ~1 hour (H5).
type clientFn func(context.Context) (*client.FrappeClient, error)

// marshalResult serializes data as indented JSON and returns it as an MCP text result.
// Frappe API errors are returned via mcp.NewToolResultError, not Go errors, so the LLM
// can see the failure and self-correct rather than treating it as a protocol error.
func marshalResult(data interface{}) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding result: %s", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// compactReportResult strips execution noise from a RunReport response,
// keeping only columns, result, and report_summary (if non-nil).
func compactReportResult(r map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"columns": r["columns"],
		"result":  r["result"],
	}
	if s, ok := r["report_summary"]; ok && s != nil {
		out["report_summary"] = s
	}
	return out
}

// validateJSON returns an error result if raw is a non-empty, non-JSON string.
// Used to validate free-form filter arguments locally before hitting the server
// so the LLM gets a clear message (I6).
func validateJSON(label, raw string) *mcp.CallToolResult {
	if raw == "" {
		return nil
	}
	var tmp interface{}
	if err := json.Unmarshal([]byte(raw), &tmp); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid %s JSON: %s", label, err))
	}
	return nil
}

// registerTools adds all Frappe API tools to the MCP server.
func registerTools(s *server.MCPServer, getClient clientFn) {
	registerPing(s, getClient)
	registerGetDoc(s, getClient)
	registerListDocs(s, getClient)
	registerCreateDoc(s, getClient)
	registerUpdateDoc(s, getClient)
	registerDeleteDoc(s, getClient)
	registerCountDocs(s, getClient)
	registerGetSchema(s, getClient)
	registerListDoctypes(s, getClient)
	registerListReports(s, getClient)
	registerRunReport(s, getClient)
	registerCallMethod(s, getClient)
	registerBulkCreate(s, getClient)
	registerBulkUpdate(s, getClient)
	registerBulkDelete(s, getClient)
}

func registerPing(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("ping",
		mcp.WithDescription("Check connectivity to the Frappe site. Returns the server response and URL. Use this first to verify the connection is working before making other calls."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	)
	s.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		resp, err := fc.Ping(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return marshalResult(map[string]interface{}{"response": resp, "status": "ok"})
	})
}

func registerGetDoc(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("get_doc",
		mcp.WithDescription("Retrieve a single Frappe document by its DocType and name. Returns all fields of the document. Use this when you know the exact document identifier. For Single DocTypes (e.g. 'System Settings', 'HR Settings'), omit name — the DocType name is used automatically."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType, e.g. 'Sales Invoice', 'Customer', 'ToDo'"),
		),
		mcp.WithString("name",
			mcp.Description("The unique name/ID of the document, e.g. 'SINV-00001', 'jane@example.com'. Omit for Single DocTypes."),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		name := req.GetString("name", "")
		if name == "" {
			name = doctype
		}
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		doc, apiErr := fc.GetDoc(ctx, doctype, name)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(doc)
	})
}

func registerListDocs(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("list_docs",
		mcp.WithDescription("List documents from a Frappe DocType with optional filtering, field selection, ordering, and pagination. Returns an array of document objects. Use this to search and browse records."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType to list, e.g. 'Sales Invoice', 'Customer'"),
		),
		mcp.WithString("fields",
			mcp.Description(`JSON array of field names to return, e.g. ["name","status","grand_total"]. If omitted, returns default fields.`),
		),
		mcp.WithString("filters",
			mcp.Description(`Filter expression as JSON. Object format: {"status":"Paid"} or array format: [["status","=","Paid"]]`),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of records to return. Default: 20. Use 0 for no limit."),
		),
		mcp.WithNumber("start",
			mcp.Description("Offset into the result set, for pagination. Default: 0."),
		),
		mcp.WithString("order_by",
			mcp.Description("Sort expression, e.g. 'modified desc', 'name asc'"),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		var fields []string
		fieldsRaw := req.GetString("fields", "")
		if fieldsRaw != "" {
			if jsonErr := json.Unmarshal([]byte(fieldsRaw), &fields); jsonErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid fields JSON: %s", jsonErr)), nil
			}
		}
		filters := req.GetString("filters", "")
		if bad := validateJSON("filters", filters); bad != nil {
			return bad, nil
		}

		// limit: 0 (default) leaves Frappe's default 20; an explicit 0 from the
		// caller can't be distinguished, so a negative value means "no limit".
		limit := int(req.GetFloat("limit", 0))
		if req.GetFloat("limit", -1) == 0 {
			limit = -1 // caller explicitly asked for no limit
		}

		opts := client.ListOptions{
			Fields:  fields,
			Filters: filters,
			Limit:   limit,
			Start:   int(req.GetFloat("start", 0)),
			OrderBy: req.GetString("order_by", ""),
		}

		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rows, apiErr := fc.GetList(ctx, doctype, opts)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(rows)
	})
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
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description(`JSON object of field values, e.g. {"description":"Buy milk","priority":"Medium"}`),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		dataRaw, err := req.RequireString("data")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var data map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(dataRaw), &data); jsonErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid data JSON: %s", jsonErr)), nil
		}
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		doc, apiErr := fc.CreateDoc(ctx, doctype, data)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(doc)
	})
}

func registerUpdateDoc(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("update_doc",
		mcp.WithDescription("Update an existing Frappe document. Provide only the fields you want to change as a JSON object. Returns the full updated document. For Single DocTypes (e.g. 'System Settings'), omit name — the DocType name is used automatically."),
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
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description(`JSON object of fields to update, e.g. {"status":"Closed"}`),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		name := req.GetString("name", "")
		if name == "" {
			name = doctype
		}
		dataRaw, err := req.RequireString("data")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var data map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(dataRaw), &data); jsonErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid data JSON: %s", jsonErr)), nil
		}
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		doc, apiErr := fc.UpdateDoc(ctx, doctype, name, data)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(doc)
	})
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
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if apiErr := fc.DeleteDoc(ctx, doctype, name); apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Deleted %s %s", doctype, name)), nil
	})
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
		mcp.WithString("filters",
			mcp.Description(`Filter expression as JSON, e.g. {"status":"Open"}`),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		filters := req.GetString("filters", "")
		if bad := validateJSON("filters", filters); bad != nil {
			return bad, nil
		}
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		count, apiErr := fc.GetCount(ctx, doctype, filters)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(map[string]interface{}{"doctype": doctype, "count": count})
	})
}

func registerGetSchema(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("get_schema",
		mcp.WithDescription("Get the definition of a Frappe DocType: module, naming rule, submittability, and all field metadata (fieldname, label, fieldtype, required, options, defaults, constraints). By default returns a compact view with zero-value noise and internal Frappe metadata stripped. Pass full=true for the raw response, or keys to select specific top-level properties."),
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
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		doc, apiErr := fc.GetDoc(ctx, "DocType", doctype)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		// Custom fields / Property Setters are best-effort — a 403 on them
		// degrades to the base schema rather than failing the whole call (L12).
		var notes []string
		if cfErr := mergeCustomFields(ctx, fc, doctype, doc); cfErr != nil {
			notes = append(notes, "custom fields could not be merged: "+cfErr.Error())
		}
		if psErr := applyPropertySetterOverrides(ctx, fc, doctype, doc); psErr != nil {
			notes = append(notes, "property setter overrides could not be applied: "+psErr.Error())
		}

		result := map[string]interface{}(doc)
		if !req.GetBool("full", false) {
			result = compactSchema(doc)
		}
		if keys := req.GetString("keys", ""); keys != "" {
			result = filterSchemaKeys(result, strings.Split(keys, ","))
		}
		if len(notes) > 0 {
			result["_warnings"] = notes
		}
		return marshalResult(result)
	})
}

func registerListDoctypes(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("list_doctypes",
		mcp.WithDescription("List all DocTypes available on the Frappe site, optionally filtered by module. Returns name, module, and description for each DocType. Use this to discover what data types exist on the site."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("module",
			mcp.Description("Filter by module name, e.g. 'Accounts', 'Selling', 'HR'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of DocTypes to return. Default: 50. Use 0 for no limit."),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filters, ferr := moduleFilter(req.GetString("module", ""))
		if ferr != nil {
			return mcp.NewToolResultError(ferr.Error()), nil
		}
		limit := int(req.GetFloat("limit", 50))
		if req.GetFloat("limit", -1) == 0 {
			limit = -1
		}

		opts := client.ListOptions{
			Fields:  []string{"name", "module", "is_submittable", "is_tree", "description"},
			Filters: filters,
			Limit:   limit,
			OrderBy: "name asc",
		}
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rows, apiErr := fc.GetList(ctx, "DocType", opts)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(rows)
	})
}

func registerListReports(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("list_reports",
		mcp.WithDescription("List available reports on the Frappe site, optionally filtered by module. Returns report name, type, module, and reference DocType."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("module",
			mcp.Description("Filter by module name, e.g. 'Accounts', 'Selling'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of reports to return. Default: 50. Use 0 for no limit."),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filters, ferr := moduleFilter(req.GetString("module", ""))
		if ferr != nil {
			return mcp.NewToolResultError(ferr.Error()), nil
		}
		limit := int(req.GetFloat("limit", 50))
		if req.GetFloat("limit", -1) == 0 {
			limit = -1
		}

		opts := client.ListOptions{
			Fields:  []string{"name", "report_type", "module", "is_standard", "ref_doctype"},
			Filters: filters,
			Limit:   limit,
			OrderBy: "name asc",
		}
		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rows, apiErr := fc.GetList(ctx, "Report", opts)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(rows)
	})
}

func registerRunReport(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("run_report",
		mcp.WithDescription("Execute a Frappe query report and return its columns and data rows. Strips execution metadata (timing, chart config). Includes report_summary if the report provides one. Use list_reports first to discover available report names."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("report_name",
			mcp.Required(),
			mcp.Description("The name of the report to run, e.g. 'General Ledger', 'Accounts Receivable'"),
		),
		mcp.WithString("filters",
			mcp.Description(`Report filter values as a JSON object, e.g. {"company":"My Company","from_date":"2025-01-01"}`),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		reportName, err := req.RequireString("report_name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		var filters map[string]interface{}
		filtersRaw := req.GetString("filters", "")
		if filtersRaw != "" {
			if jsonErr := json.Unmarshal([]byte(filtersRaw), &filters); jsonErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid filters JSON: %s", jsonErr)), nil
			}
		}

		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, apiErr := fc.RunReport(ctx, reportName, filters)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(compactReportResult(result))
	})
}

func registerCallMethod(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("call_method",
		mcp.WithDescription("Call a whitelisted Frappe server method. This is a low-level escape hatch for operations not covered by other tools. The method must be whitelisted (@frappe.whitelist()) on the server. It may read or mutate data depending on the method."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("method",
			mcp.Required(),
			mcp.Description("The dotted method path, e.g. 'frappe.client.get_count', 'erpnext.api.get_currency'"),
		),
		mcp.WithString("args",
			mcp.Description(`JSON object of method arguments, e.g. {"doctype":"ToDo"}`),
		),
		mcp.WithBoolean("get",
			mcp.Description("Send as a GET request (for methods whitelisted GET-only). Default: false (POST)."),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		method, err := req.RequireString("method")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		var args map[string]interface{}
		argsRaw := req.GetString("args", "")
		if argsRaw != "" {
			if jsonErr := json.Unmarshal([]byte(argsRaw), &args); jsonErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid args JSON: %s", jsonErr)), nil
			}
		}

		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		result, apiErr := fc.CallMethod(ctx, method, args, req.GetBool("get", false))
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(result)
	})
}

func registerBulkCreate(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("bulk_create",
		mcp.WithDescription("Create multiple Frappe documents in one call. Each element of the data array is a JSON object of field values for one document. Returns per-item results with created names or error messages. Processing continues on individual failures."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType, e.g. 'ToDo', 'Note'"),
		),
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description(`JSON array of field-value objects, e.g. [{"description":"Task 1"},{"description":"Task 2"}]`),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		dataRaw, err := req.RequireString("data")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var items []map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(dataRaw), &items); jsonErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid data JSON array: %s", jsonErr)), nil
		}
		if len(items) == 0 {
			return mcp.NewToolResultError("data array is empty"), nil
		}

		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		type itemResult struct {
			Index  int    `json:"index"`
			Status string `json:"status"`
			Name   string `json:"name,omitempty"`
			Error  string `json:"error,omitempty"`
		}
		results := make([]itemResult, len(items))
		succeeded, failed, skipped := 0, 0, 0

		for i, item := range items {
			r := itemResult{Index: i + 1}
			if ctx.Err() != nil { // client cancelled/disconnected — stop (L14)
				r.Status = "skipped"
				skipped++
				results[i] = r
				continue
			}
			doc, apiErr := fc.CreateDoc(ctx, doctype, item)
			if apiErr != nil {
				r.Status = "error"
				r.Error = apiErr.Error()
				failed++
			} else {
				r.Status = "created"
				if n, ok := docName(doc["name"]); ok {
					r.Name = n
				}
				succeeded++
			}
			results[i] = r
		}

		return marshalResult(map[string]interface{}{
			"created": succeeded,
			"failed":  failed,
			"skipped": skipped,
			"results": results,
		})
	})
}

func registerBulkUpdate(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("bulk_update",
		mcp.WithDescription("Update multiple Frappe documents in one call. Each element of the data array must include a \"name\" field identifying the document plus any fields to change. Returns per-item results. Processing continues on individual failures."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description(`JSON array of objects; each must include "name" plus fields to update, e.g. [{"name":"TD-0001","status":"Closed"}]`),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		dataRaw, err := req.RequireString("data")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var items []map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(dataRaw), &items); jsonErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid data JSON array: %s", jsonErr)), nil
		}
		if len(items) == 0 {
			return mcp.NewToolResultError("data array is empty"), nil
		}

		// Accept numeric names too (integer-named DocTypes), not only strings (L27).
		names := make([]string, len(items))
		for i, item := range items {
			n, ok := docName(item["name"])
			if !ok {
				return mcp.NewToolResultError(fmt.Sprintf("item %d is missing a \"name\" field", i+1)), nil
			}
			names[i] = n
		}

		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		type itemResult struct {
			Index  int    `json:"index"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		results := make([]itemResult, len(items))
		succeeded, failed, skipped := 0, 0, 0

		for i, item := range items {
			r := itemResult{Index: i + 1, Name: names[i]}
			if ctx.Err() != nil { // L14
				r.Status = "skipped"
				skipped++
				results[i] = r
				continue
			}
			payload := make(map[string]interface{}, len(item))
			for k, v := range item {
				if k != "name" {
					payload[k] = v
				}
			}
			if _, apiErr := fc.UpdateDoc(ctx, doctype, names[i], payload); apiErr != nil {
				r.Status = "error"
				r.Error = apiErr.Error()
				failed++
			} else {
				r.Status = "updated"
				succeeded++
			}
			results[i] = r
		}

		return marshalResult(map[string]interface{}{
			"updated": succeeded,
			"failed":  failed,
			"skipped": skipped,
			"results": results,
		})
	})
}

func registerBulkDelete(s *server.MCPServer, getClient clientFn) {
	tool := mcp.NewTool("bulk_delete",
		mcp.WithDescription("Permanently delete multiple Frappe documents in one call. Provide document names as a JSON array. Returns per-item results. Processing continues on individual failures. This action cannot be undone."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		mcp.WithString("names",
			mcp.Required(),
			mcp.Description(`JSON array of document names to delete, e.g. ["TD-0001","TD-0002"]`),
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		namesRaw, err := req.RequireString("names")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Decode into interface{} so integer-named documents are accepted (L27).
		var rawNames []interface{}
		if jsonErr := json.Unmarshal([]byte(namesRaw), &rawNames); jsonErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("names must be a JSON array: %s", jsonErr)), nil
		}
		if len(rawNames) == 0 {
			return mcp.NewToolResultError("names array is empty"), nil
		}
		names := make([]string, len(rawNames))
		for i, rn := range rawNames {
			n, ok := docName(rn)
			if !ok {
				return mcp.NewToolResultError(fmt.Sprintf("name at index %d is empty or not a string/number", i)), nil
			}
			names[i] = n
		}

		fc, err := getClient(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		type itemResult struct {
			Index  int    `json:"index"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		results := make([]itemResult, len(names))
		succeeded, failed, skipped := 0, 0, 0

		for i, name := range names {
			r := itemResult{Index: i + 1, Name: name}
			if ctx.Err() != nil { // stop deleting once the caller cancels (L14)
				r.Status = "skipped"
				skipped++
				results[i] = r
				continue
			}
			if apiErr := fc.DeleteDoc(ctx, doctype, name); apiErr != nil {
				r.Status = "error"
				r.Error = apiErr.Error()
				failed++
			} else {
				r.Status = "deleted"
				succeeded++
			}
			results[i] = r
		}

		return marshalResult(map[string]interface{}{
			"deleted": succeeded,
			"failed":  failed,
			"skipped": skipped,
			"results": results,
		})
	})
}
