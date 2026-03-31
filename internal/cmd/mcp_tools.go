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

// registerTools adds all Frappe API tools to the MCP server.
func registerTools(s *server.MCPServer, fc *client.FrappeClient) {
	registerPing(s, fc)
	registerGetDoc(s, fc)
	registerListDocs(s, fc)
	registerCreateDoc(s, fc)
	registerUpdateDoc(s, fc)
	registerDeleteDoc(s, fc)
	registerCountDocs(s, fc)
	registerGetSchema(s, fc)
	registerListDoctypes(s, fc)
	registerListReports(s, fc)
	registerRunReport(s, fc)
	registerCallMethod(s, fc)
}

func registerPing(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("ping",
		mcp.WithDescription("Check connectivity to the Frappe site. Returns the server response and URL. Use this first to verify the connection is working before making other calls."),
	)
	s.AddTool(tool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp, err := fc.Ping()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return marshalResult(map[string]interface{}{"response": resp, "status": "ok"})
	})
}

func registerGetDoc(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("get_doc",
		mcp.WithDescription("Retrieve a single Frappe document by its DocType and name. Returns all fields of the document. Use this when you know the exact document identifier."),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType, e.g. 'Sales Invoice', 'Customer', 'ToDo'"),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("The unique name/ID of the document, e.g. 'SINV-00001', 'jane@example.com'"),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		doc, apiErr := fc.GetDoc(doctype, name)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(doc)
	})
}

func registerListDocs(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("list_docs",
		mcp.WithDescription("List documents from a Frappe DocType with optional filtering, field selection, ordering, and pagination. Returns an array of document objects. Use this to search and browse records."),
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
			mcp.Description("Maximum number of records to return. Default: 20"),
		),
		mcp.WithString("order_by",
			mcp.Description("Sort expression, e.g. 'modified desc', 'name asc'"),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		opts := client.ListOptions{
			Fields:  fields,
			Filters: req.GetString("filters", ""),
			Limit:   int(req.GetFloat("limit", 0)),
			OrderBy: req.GetString("order_by", ""),
		}

		rows, apiErr := fc.GetList(doctype, opts)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(rows)
	})
}

func registerCreateDoc(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("create_doc",
		mcp.WithDescription("Create a new document in a Frappe DocType. Provide field values as a JSON object. Returns the created document with all server-generated fields (name, creation date, etc.)."),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType, e.g. 'ToDo', 'Note'"),
		),
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description(`JSON object of field values, e.g. {"description":"Buy milk","priority":"Medium"}`),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		doc, apiErr := fc.CreateDoc(doctype, data)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(doc)
	})
}

func registerUpdateDoc(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("update_doc",
		mcp.WithDescription("Update an existing Frappe document. Provide only the fields you want to change as a JSON object. Returns the full updated document."),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("The name/ID of the document to update"),
		),
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description(`JSON object of fields to update, e.g. {"status":"Closed"}`),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		name, err := req.RequireString("name")
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
		doc, apiErr := fc.UpdateDoc(doctype, name, data)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(doc)
	})
}

func registerDeleteDoc(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("delete_doc",
		mcp.WithDescription("Permanently delete a Frappe document. This action cannot be undone. Returns a confirmation message on success."),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("The name/ID of the document to delete"),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if apiErr := fc.DeleteDoc(doctype, name); apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Deleted %s %s", doctype, name)), nil
	})
}

func registerCountDocs(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("count_docs",
		mcp.WithDescription("Count the number of documents in a DocType, optionally filtered. Returns a single integer count. More efficient than list_docs when you only need the count."),
		mcp.WithString("doctype",
			mcp.Required(),
			mcp.Description("The Frappe DocType"),
		),
		mcp.WithString("filters",
			mcp.Description(`Filter expression as JSON, e.g. {"status":"Open"}`),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		count, apiErr := fc.GetCount(doctype, req.GetString("filters", ""))
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(map[string]interface{}{"doctype": doctype, "count": count})
	})
}

func registerGetSchema(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("get_schema",
		mcp.WithDescription("Get the definition of a Frappe DocType: module, naming rule, submittability, and all field metadata (fieldname, label, fieldtype, required, options, defaults, constraints). By default returns a compact view with zero-value noise and internal Frappe metadata stripped. Pass full=true for the raw response, or keys to select specific top-level properties."),
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
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		doctype, err := req.RequireString("doctype")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		doc, apiErr := fc.GetDoc("DocType", doctype)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		if psErr := applyPropertySetterOverrides(fc, doctype, doc); psErr != nil {
			return mcp.NewToolResultError(psErr.Error()), nil
		}

		result := map[string]interface{}(doc)
		if !req.GetBool("full", false) {
			result = compactSchema(doc)
		}
		if keys := req.GetString("keys", ""); keys != "" {
			result = filterSchemaKeys(result, strings.Split(keys, ","))
		}
		return marshalResult(result)
	})
}

func registerListDoctypes(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("list_doctypes",
		mcp.WithDescription("List all DocTypes available on the Frappe site, optionally filtered by module. Returns name, module, and description for each DocType. Use this to discover what data types exist on the site."),
		mcp.WithString("module",
			mcp.Description("Filter by module name, e.g. 'Accounts', 'Selling', 'HR'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of DocTypes to return. Default: 50"),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		module := req.GetString("module", "")
		limit := int(req.GetFloat("limit", 50))

		filters := ""
		if module != "" {
			b, _ := json.Marshal(map[string]interface{}{"module": module})
			filters = string(b)
		}

		opts := client.ListOptions{
			Fields:  []string{"name", "module", "is_submittable", "is_tree", "description"},
			Filters: filters,
			Limit:   limit,
			OrderBy: "name asc",
		}
		rows, apiErr := fc.GetList("DocType", opts)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(rows)
	})
}

func registerListReports(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("list_reports",
		mcp.WithDescription("List available reports on the Frappe site, optionally filtered by module. Returns report name, type, module, and reference DocType."),
		mcp.WithString("module",
			mcp.Description("Filter by module name, e.g. 'Accounts', 'Selling'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of reports to return. Default: 50"),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		module := req.GetString("module", "")
		limit := int(req.GetFloat("limit", 50))

		filters := ""
		if module != "" {
			b, _ := json.Marshal(map[string]interface{}{"module": module})
			filters = string(b)
		}

		opts := client.ListOptions{
			Fields:  []string{"name", "report_type", "module", "is_standard", "ref_doctype"},
			Filters: filters,
			Limit:   limit,
			OrderBy: "name asc",
		}
		rows, apiErr := fc.GetList("Report", opts)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(rows)
	})
}

func registerRunReport(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("run_report",
		mcp.WithDescription("Execute a Frappe query report and return its columns and data rows. Strips execution metadata (timing, chart config). Includes report_summary if the report provides one. Use list_reports first to discover available report names."),
		mcp.WithString("report_name",
			mcp.Required(),
			mcp.Description("The name of the report to run, e.g. 'General Ledger', 'Accounts Receivable'"),
		),
		mcp.WithString("filters",
			mcp.Description(`Report filter values as a JSON object, e.g. {"company":"My Company","from_date":"2025-01-01"}`),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		result, apiErr := fc.RunReport(reportName, filters)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(compactReportResult(result))
	})
}

func registerCallMethod(s *server.MCPServer, fc *client.FrappeClient) {
	tool := mcp.NewTool("call_method",
		mcp.WithDescription("Call a whitelisted Frappe server method. This is a low-level escape hatch for operations not covered by other tools. The method must be whitelisted (@frappe.whitelist()) on the server."),
		mcp.WithString("method",
			mcp.Required(),
			mcp.Description("The dotted method path, e.g. 'frappe.client.get_count', 'erpnext.api.get_currency'"),
		),
		mcp.WithString("args",
			mcp.Description(`JSON object of method arguments, e.g. {"doctype":"ToDo"}`),
		),
	)
	s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		result, apiErr := fc.CallMethod(method, args)
		if apiErr != nil {
			return mcp.NewToolResultError(apiErr.Error()), nil
		}
		return marshalResult(result)
	})
}
