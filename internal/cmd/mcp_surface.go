package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// The MCP surface around the tools (T2.7): tool sets, titles and size hints,
// server instructions, resources, prompts, progress notifications and the
// fitting of large row lists into a result.

// mcpToolsets is `ffc mcp --toolsets`; nil exposes every set.
var mcpToolsets []string

// cleanToolsets trims --toolsets and refuses an unknown or missing name.
func cleanToolsets(cmd *cobra.Command) error {
	var out []string
	for _, v := range mcpToolsets {
		switch v = strings.TrimSpace(v); v {
		case "":
		case toolsetCore, toolsetLifecycle:
			if !contains(out, v, false) {
				out = append(out, v)
			}
		default:
			return usageErrorf("--toolsets: unknown tool set %q (known: %s, %s)", v, toolsetCore, toolsetLifecycle)
		}
	}
	if cmd.Flags().Changed("toolsets") && len(out) == 0 {
		return usageErrorf("--toolsets needs at least one value")
	}
	mcpToolsets = out
	return nil
}

// inToolsets reports whether tool belongs to a tool set the server exposes.
// Siteless tools (list_sites) are always exposed.
func (env *mcpEnv) inToolsets(tool string) bool {
	if len(env.toolsets) == 0 || siteless[tool] {
		return true
	}
	return contains(env.toolsets, toolSurface[tool].toolset, false)
}

// maxResultSizeKey is the tool _meta key Claude Code reads to raise its own
// cut of a tool result, so a large JSON result is not cut mid-way.
const maxResultSizeKey = "anthropic/maxResultSizeChars"

// describeTools sets every tool's title (as Tool.title and the title
// annotation, for clients of either revision) and the size hint on tools
// whose result can be large.
func describeTools(s *server.MCPServer) {
	var tools []server.ServerTool
	for name, t := range s.ListTools() {
		info, ok := toolSurface[name]
		if !ok {
			continue // TestMCPToolSurface fails instead
		}
		tool := t.Tool
		tool.Title, tool.Annotations.Title = info.title, info.title
		if info.big {
			tool.Meta = &mcp.Meta{AdditionalFields: map[string]any{maxResultSizeKey: maxToolResultBytes}}
		}
		tools = append(tools, server.ServerTool{Tool: tool, Handler: t.Handler})
	}
	s.AddTools(tools...)
}

// registerSurface adds the resources and prompts whose tools are registered,
// and the server instructions, which describe what is actually exposed.
func registerSurface(s *server.MCPServer, env *mcpEnv, policies []mcpPolicy) {
	registerResources(s, env)
	registerPrompts(s, env)
	server.WithInstructions(mcpInstructions(s, env, policies))(s)
}

// ─── instructions ───────────────────────────────────────────────────────────

// mcpInstructions is the text a client gives the model on connecting. It is
// built from the served sites, their policies and the registered tools, so
// it never mentions a tool or site the server does not offer.
func mcpInstructions(s *server.MCPServer, env *mcpEnv, policies []mcpPolicy) string {
	tools := s.ListTools()
	has := func(names ...string) bool {
		for _, n := range names {
			if _, ok := tools[n]; !ok {
				return false
			}
		}
		return true
	}
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	line("ffc reaches Frappe/ERPNext sites through their REST API, with the permissions of the user ffc signs in as.")
	if len(env.sites) > 1 {
		line("This server serves %d sites: %s. Every tool call except list_sites needs a site argument naming one of them; there is no default, so ask the user when the site is unclear.", len(env.sites), env.siteNames())
	} else if len(env.sites) == 1 && env.sites[0] != "" {
		line("This server serves one site, %q.", env.sites[0])
	}
	var ro []string
	for _, p := range policies {
		if p.readOnly() {
			ro = append(ro, p.site)
		}
	}
	switch {
	case len(ro) > 0 && len(ro) == len(policies):
		line("Read-only: only read tools are available; nothing can be created, changed or deleted.")
	case len(ro) > 0:
		line("Read-only sites (write tools refuse them): %s.", strings.Join(ro, ", "))
	}
	line("A document is identified by DocType and name. name is its ID (CUST-0042, ACC-SINV-2026-00001), not the title the desk shows; a Link field holds the linked document's name.")
	line("A Single DocType (System Settings, Selling Settings) has one document: get_doc and update_doc take no name for it.")
	if has("search") {
		line("To turn a title or partial text into a name, call search with doctype first, then use the value it returns.")
	}
	line("Dates are YYYY-MM-DD and datetimes YYYY-MM-DD HH:MM:SS; Check fields are 0 or 1.")
	line(`Filters: an object {"status":"Open","docstatus":1} means equality, all conditions ANDed; a list [["grand_total",">",1000],["posting_date","between",["2026-01-01","2026-03-31"]]] takes operators =, !=, >, <, >=, <=, like (with %%), not like, in, not in, between, is ("set" or "not set"). Use fieldnames, not labels.`)
	if has("list_docs") {
		line("list_docs returns only name unless you pass fields: always pass fields and a limit (default 20, 0 = all) and page with start. A result too large to return comes back as {data, truncated, next_start, hint}: call again with start=next_start.")
	}
	if has("count_docs") {
		line("count_docs answers how many without fetching rows.")
	}
	if has("get_schema") && (has("create_doc") || has("update_doc") || has("bulk_create")) {
		line("Call get_schema before create_doc, update_doc or a bulk write: it gives fieldnames, required fields, Link targets (options), Select options and child tables (a list of row objects). update_doc changes only the fields you pass.")
	}
	line("docstatus on submittable DocTypes: 0 = draft (editable), 1 = submitted (final; only fields marked Allow on Submit can change), 2 = cancelled.")
	if has("submit_doc", "cancel_doc", "amend_doc") {
		line("Lifecycle: submit_doc (0 to 1), cancel_doc (1 to 2, cannot be undone), amend_doc (a new draft <name>-1 from a cancelled document).")
	}
	if has("get_transitions", "apply_workflow") {
		line("A DocType with an active workflow refuses submit_doc and cancel_doc: call get_transitions, then apply_workflow with one of its actions.")
	}
	line(`An error starting with "policy:" means this server's configuration refuses the call and nothing was sent; "cancelled by the user" means the user declined. Do not retry either another way.`)
	if has("get_doc") || has("get_schema") {
		site := "<site>"
		if len(env.sites) == 1 {
			site = url.PathEscape(env.sites[0])
		}
		line("Resources: ffc://sites, ffc://%s/schema/{doctype} and ffc://%s/doc/{doctype}/{name} return what get_schema and get_doc return; percent-encode each segment (a space is %%20, a / in a name %%2F).", site, site)
	}
	if prompts := s.ListPrompts(); len(prompts) > 0 {
		names := make([]string, 0, len(prompts))
		for n := range prompts {
			names = append(names, n)
		}
		sort.Strings(names)
		line("Prompts with step-by-step plans: %s.", strings.Join(names, ", "))
	}
	return b.String()
}

// ─── resources ──────────────────────────────────────────────────────────────

// Resources are read through the tool that serves the same data, so a read
// passes the same site check, scope, policy, audit line and size cap as a
// tool call. The audit line names the tool and has "via": "resource".
const (
	sitesURI       = "ffc://sites"
	schemaTemplate = "ffc://{site}/schema/{doctype}"
	docTemplate    = "ffc://{site}/doc/{doctype}/{name}"
)

type viaCtxKey struct{}

func withVia(ctx context.Context, via string) context.Context {
	return context.WithValue(ctx, viaCtxKey{}, via)
}

func viaFrom(ctx context.Context) string {
	v, _ := ctx.Value(viaCtxKey{}).(string)
	return v
}

func registerResources(s *server.MCPServer, env *mcpEnv) {
	tools := s.ListTools()
	if _, ok := tools["list_sites"]; ok {
		s.AddResource(mcp.NewResource(sitesURI, "sites",
			mcp.WithResourceTitle("Served sites"),
			mcp.WithResourceDescription("The sites this server serves: name, URL, how ffc signs in and whether MCP may only read it (the list_sites tool)."),
			mcp.WithMIMEType("application/json"),
		), resourceReader(s, env, "list_sites", func(string) (map[string]any, error) { return map[string]any{}, nil }))
	}
	if _, ok := tools["get_schema"]; ok {
		s.AddResourceTemplate(mcp.NewResourceTemplate(schemaTemplate, "schema",
			mcp.WithTemplateTitle("DocType schema"),
			mcp.WithTemplateDescription("A DocType's definition as the get_schema tool returns it (compact view). Percent-encode the segments: Sales%20Invoice."),
			mcp.WithTemplateMIMEType("application/json"),
		), server.ResourceTemplateHandlerFunc(resourceReader(s, env, "get_schema", func(uri string) (map[string]any, error) {
			site, rest, err := splitFFCURI(uri)
			if err != nil {
				return nil, err
			}
			if len(rest) != 2 || rest[0] != "schema" || rest[1] == "" {
				return nil, fmt.Errorf("resource: %s is not ffc://{site}/schema/{doctype}", uri)
			}
			return map[string]any{"site": site, "doctype": rest[1]}, nil
		})))
	}
	if _, ok := tools["get_doc"]; ok {
		s.AddResourceTemplate(mcp.NewResourceTemplate(docTemplate, "doc",
			mcp.WithTemplateTitle("Document"),
			mcp.WithTemplateDescription("A document as the get_doc tool returns it. Percent-encode the segments: a space is %20, a / in a name %2F."),
			mcp.WithTemplateMIMEType("application/json"),
		), server.ResourceTemplateHandlerFunc(resourceReader(s, env, "get_doc", func(uri string) (map[string]any, error) {
			site, rest, err := splitFFCURI(uri)
			if err != nil {
				return nil, err
			}
			if len(rest) != 3 || rest[0] != "doc" || rest[1] == "" || rest[2] == "" {
				return nil, fmt.Errorf("resource: %s is not ffc://{site}/doc/{doctype}/{name}", uri)
			}
			return map[string]any{"site": site, "doctype": rest[1], "name": rest[2]}, nil
		})))
	}
}

// splitFFCURI splits an ffc:// URI into its percent-decoded segments: the
// site, then the rest. The URI template has already matched it.
func splitFFCURI(uri string) (string, []string, error) {
	rest, ok := strings.CutPrefix(uri, "ffc://")
	if !ok || strings.ContainsAny(rest, "?#") {
		return "", nil, fmt.Errorf("resource: %s is not an ffc:// URI without query or fragment", uri)
	}
	segs := strings.Split(rest, "/")
	for i, seg := range segs {
		dec, err := url.PathUnescape(seg)
		if err != nil {
			return "", nil, fmt.Errorf("resource: %s: %w", uri, err)
		}
		segs[i] = dec
	}
	return segs[0], segs[1:], nil
}

// resourceReader serves a resource by calling tool's registered handler
// with the arguments args reads from the URI.
func resourceReader(s *server.MCPServer, env *mcpEnv, tool string, args func(uri string) (map[string]any, error)) server.ResourceHandlerFunc {
	return func(ctx context.Context, rr mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		uri := rr.Params.URI
		a, err := args(uri)
		if err != nil {
			rec := auditRecord{Time: time.Now().UTC(), Tool: tool, Client: mcpClientName(ctx), Via: "resource", Status: auditInvalid, Error: err.Error()}
			env.audit.write(rec, map[string]any{"uri": uri})
			return nil, err
		}
		st, ok := s.ListTools()[tool]
		if !ok {
			return nil, fmt.Errorf("resource: the %s tool is not available on this server", tool)
		}
		req := mcp.CallToolRequest{}
		req.Params.Name, req.Params.Arguments = tool, a
		res, err := st.Handler(withVia(ctx, "resource"), req)
		if err != nil {
			return nil, err
		}
		var text strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
		if res.IsError {
			return nil, errors.New(text.String())
		}
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: uri, MIMEType: "application/json", Text: text.String()}}, nil
	}
}

// ─── prompts ────────────────────────────────────────────────────────────────

// mcpPrompt is a prompt: guidance that names the tools to call and what to
// check. Prompts make no site calls; a prompt is offered only when the
// tools it relies on are registered.
type mcpPrompt struct {
	name, title, desc string
	needs             []string // tools
	args              []mcp.PromptOption
	required          []string
	text              func(a map[string]string) string
}

var mcpPrompts = []mcpPrompt{
	{
		name: "inspect-doctype", title: "Inspect a DocType",
		desc:  "Read a DocType's schema, counts and sample rows and explain how its documents are built. Read-only.",
		needs: []string{"get_schema", "list_docs", "count_docs"},
		args: []mcp.PromptOption{mcp.WithArgument("doctype", mcp.RequiredArgument(),
			mcp.ArgumentDescription("The DocType, e.g. Sales Invoice"))},
		required: []string{"doctype"},
		text: func(a map[string]string) string {
			dt := quoted(a["doctype"], 140)
			return fmt.Sprintf(`Inspect the Frappe DocType %[1]s with the ffc tools. Read only: create or change nothing.

1. get_schema doctype=%[1]s. Note the naming rule (autoname), is_submittable, istable, issingle, the title field and track_changes; for each field its fieldname, label, fieldtype, reqd, options (the Link target DocType or the Select values), default and read_only. For each Table field, call get_schema on its options DocType (the child table).
2. count_docs doctype=%[1]s; if it is submittable, also with filters {"docstatus":0}, {"docstatus":1} and {"docstatus":2}.
3. list_docs doctype=%[1]s with fields ["name","modified"] plus the title field and 2-4 key fields, limit 5, order_by "modified desc", to see real values.
4. If it is submittable, get_transitions on one of those documents shows whether a workflow drives it.

Report: what the DocType is for; how documents are named; the required fields with their types and Link targets; the child tables; whether it is submittable or workflow-driven; and typical values.`, dt)
		},
	},
	{
		name: "safe-bulk-import", title: "Import documents safely",
		desc:  "Plan and run a bulk import: map fields with get_schema, check links and duplicates, trial a few rows, then import in batches.",
		needs: []string{"get_schema", "bulk_create", "list_docs", "count_docs"},
		args: []mcp.PromptOption{
			mcp.WithArgument("doctype", mcp.RequiredArgument(), mcp.ArgumentDescription("The DocType to import into")),
			mcp.WithArgument("source", mcp.ArgumentDescription("What the data is: a file name, its columns, how many rows")),
		},
		required: []string{"doctype"},
		text: func(a map[string]string) string {
			dt := quoted(a["doctype"], 140)
			src := "the data the user provides"
			if a["source"] != "" {
				src = quoted(a["source"], 500)
			}
			return fmt.Sprintf(`Import %[2]s into the Frappe DocType %[1]s safely with the ffc tools.

1. get_schema doctype=%[1]s: required fields (reqd), fieldtypes, Link fields and their target DocTypes (options), Select options, unique fields and the naming rule (a series, a field, or a name you give). A Table field takes a list of row objects.
2. Map every source column to a fieldname; never send labels. Leave out columns with no field. Dates become YYYY-MM-DD, numbers JSON numbers, checkboxes 0 or 1.
3. Check references before writing: resolve each distinct Link value with search doctype=<target DocType> (or count_docs with a name filter). List the values that do not exist and ask the user before going on.
4. Check duplicates: list_docs or count_docs with an "in" filter on the naming or unique field for the incoming keys.
5. Show the user the mapping and the counts (to create, duplicates skipped, invalid rows) and wait for a go-ahead.
6. Trial: bulk_create with the first 1-3 rows only, then get_doc one created name and compare it with the source.
7. Import the rest with bulk_create, at most 200 items per call. Each item is reported as created or error and the call goes on after an error. Retry only the failed items after fixing them, never a whole batch: that would create duplicates.
8. Finish with count_docs and a summary. bulk_create makes drafts; submitting is a separate step (submit_doc per document). To undo, bulk_delete the created names.`, dt, src)
		},
	},
	{
		name: "audit-doc-changes", title: "Audit a document's changes",
		desc:  "Build a timeline of who changed a document and how, from its Version and Comment records. Read-only.",
		needs: []string{"get_doc", "list_docs"},
		args: []mcp.PromptOption{
			mcp.WithArgument("doctype", mcp.RequiredArgument(), mcp.ArgumentDescription("The document's DocType")),
			mcp.WithArgument("name", mcp.RequiredArgument(), mcp.ArgumentDescription("The document's name (its ID)")),
		},
		required: []string{"doctype", "name"},
		text: func(a map[string]string) string {
			dt, name := quoted(a["doctype"], 140), quoted(a["name"], 140)
			return fmt.Sprintf(`Find out who changed the Frappe document %[1]s %[2]s and how, with the ffc tools. Read only: change nothing.

1. get_doc doctype=%[1]s name=%[2]s fields ["name","owner","creation","modified","modified_by","docstatus","amended_from"]: who created it and who changed it last. If the name is not found, resolve it with search doctype=%[1]s.
2. list_docs doctype "Version", filters {"ref_doctype":%[1]s,"docname":%[2]s}, fields ["name","owner","creation","data"], order_by "creation asc", limit 100. Each data is a JSON string: changed is a list of [field, old, new]; added, removed and row_changed describe child-table rows. Frappe keeps Versions only when the DocType has Track Changes on (get_schema keys ["track_changes"]); with none, say that the history is not recorded.
3. list_docs doctype "Comment", filters {"reference_doctype":%[1]s,"reference_name":%[2]s}, fields ["comment_type","content","owner","creation"], order_by "creation asc": submissions, cancellations, assignments, workflow changes and user comments.
4. If amended_from is set, repeat for that document (an amendment of X is X-1, then X-2).

Report a timeline: date, user, what changed (field: old -> new) and docstatus changes. A policy error on Version or Comment means this server may not read them: say so.`, dt, name)
		},
	},
	{
		name: "explain-report", title: "Explain a report",
		desc:  "Find out what a report shows, which filters it needs and what its columns mean, then run it on a small sample. Read-only.",
		needs: []string{"run_report", "get_doc"},
		args: []mcp.PromptOption{mcp.WithArgument("report_name", mcp.RequiredArgument(),
			mcp.ArgumentDescription("The report's name, e.g. Accounts Receivable"))},
		required: []string{"report_name"},
		text: func(a map[string]string) string {
			r := quoted(a["report_name"], 140)
			return fmt.Sprintf(`Explain the Frappe report %[1]s with the ffc tools. Read only.

1. get_doc doctype "Report" name=%[1]s fields ["name","report_type","ref_doctype","module","is_standard","disabled","prepared_report"]: the report type (Report Builder, Query Report or Script Report) and the DocType it is based on. list_reports finds the exact name if this one is not found.
2. get_schema on its ref_doctype for the meaning of the fields it reports.
3. run_report report_name=%[1]s with limit 20 and a narrow filters object. Many reports need filters (company, from_date, to_date, ...): a missing mandatory filter fails with a message naming it; add it and run again. Dates are YYYY-MM-DD.
4. Name every value from columns (fieldname, label, fieldtype, options): rows are arrays in column order or objects keyed by fieldname. report_summary holds totals when present. truncated means rows were cut: never present a partial result as a total.

Explain what the report shows, what each column means, which filters matter and what the sample rows say.`, r)
		},
	},
}

func registerPrompts(s *server.MCPServer, env *mcpEnv) {
	tools := s.ListTools()
	for _, p := range mcpPrompts {
		ok := true
		for _, t := range p.needs {
			if _, has := tools[t]; !has {
				ok = false
			}
		}
		if !ok {
			continue
		}
		opts := append([]mcp.PromptOption{mcp.WithPromptTitle(p.title), mcp.WithPromptDescription(p.desc)}, p.args...)
		if len(env.sites) > 1 {
			opts = append(opts, mcp.WithArgument("site", mcp.RequiredArgument(),
				mcp.ArgumentDescription(fmt.Sprintf("The site: one of %s", env.siteNames()))))
		}
		s.AddPrompt(mcp.NewPrompt(p.name, opts...), promptHandler(env, p))
	}
}

func promptHandler(env *mcpEnv, p mcpPrompt) server.PromptHandlerFunc {
	return func(_ context.Context, gr mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		a := gr.Params.Arguments
		for _, k := range p.required {
			if strings.TrimSpace(a[k]) == "" {
				return nil, fmt.Errorf("prompt %s: argument %s is required", p.name, k)
			}
		}
		text := p.text(a)
		if len(env.sites) > 1 {
			// The same check as a tool call's site, without reading the config.
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{"site": a["site"]}
			site, err := env.siteFor(req)
			if err != nil {
				return nil, fmt.Errorf("prompt %s: %w", p.name, err)
			}
			text += fmt.Sprintf("\n\nThis server serves several sites: pass site=%q to every tool call.", site)
		}
		return mcp.NewGetPromptResult(p.desc, []mcp.PromptMessage{
			mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(text)),
		}), nil
	}
}

// ─── progress ───────────────────────────────────────────────────────────────

type progressCtxKey struct{}

// withProgress keeps the request's progress token, if it has one, for
// notifyProgress.
func withProgress(ctx context.Context, req mcp.CallToolRequest) context.Context {
	if req.Params.Meta == nil || req.Params.Meta.ProgressToken == nil {
		return ctx
	}
	return context.WithValue(ctx, progressCtxKey{}, req.Params.Meta.ProgressToken)
}

// notifyProgress sends notifications/progress for the current tool call
// when the client asked for it. A notification that cannot be sent is
// dropped: progress is advisory.
func notifyProgress(ctx context.Context, done, total int) {
	token := ctx.Value(progressCtxKey{})
	srv := server.ServerFromContext(ctx)
	if token == nil || srv == nil {
		return
	}
	_ = srv.SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), map[string]any{
		"progressToken": token, "progress": done, "total": total,
		"message": fmt.Sprintf("%d of %d done", done, total),
	})
}

// ─── fitting large results ──────────────────────────────────────────────────

// jsonSize is the marshalled size of v, or more than the cap when it cannot
// be marshalled (marshalResult then reports the error).
func jsonSize(v interface{}) int {
	b, err := json.Marshal(v)
	if err != nil {
		return maxToolResultBytes + 1
	}
	return len(b)
}

// largestFit returns the largest k in [0, n] for which build(k) fits in a
// tool result, by binary search on the marshalled size (it grows with k).
func largestFit(n int, build func(k int) interface{}) int {
	lo, hi := 0, n // build(lo) is assumed to fit; check it is
	if jsonSize(build(0)) > maxToolResultBytes {
		return 0
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if jsonSize(build(mid)) <= maxToolResultBytes {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// fitListRows returns list_docs rows as before when they fit; otherwise the
// largest leading run that fits, as {data, truncated, next_start, hint}.
// When not even one row fits, the rows go back unchanged and marshalResult
// refuses them with its hint to narrow the fields.
func fitListRows(rows []map[string]interface{}, start int) interface{} {
	if jsonSize(rows) <= maxToolResultBytes {
		return rows
	}
	page := func(k int) interface{} {
		return map[string]interface{}{
			"data": rows[:k], "truncated": true, "next_start": start + k,
			"hint": fmt.Sprintf("only the first %d of the %d rows fit in %d KiB; call list_docs again with start=%d for the next rows, or ask for fewer fields", k, len(rows), maxToolResultBytes>>10, start+k),
		}
	}
	k := largestFit(len(rows), page)
	if k == 0 {
		return rows
	}
	return page(k)
}

// fitReportRows cuts a compact run_report result to the rows that fit,
// keeping its shape (columns, result, report_summary) and saying how many
// rows were dropped. Rows that cannot be cut are left to marshalResult.
func fitReportRows(r map[string]interface{}) interface{} {
	rows, ok := r["result"].([]interface{})
	if !ok || jsonSize(r) <= maxToolResultBytes {
		return r
	}
	total := len(rows)
	if t, ok := r["total_rows"].(int); ok {
		total = t // already cut by limit
	}
	build := func(k int) interface{} {
		out := make(map[string]interface{}, len(r)+1)
		for key, v := range r {
			out[key] = v
		}
		out["result"], out["truncated"], out["total_rows"] = rows[:k], true, total
		out["hint"] = fmt.Sprintf("%d of %d rows did not fit in %d KiB and were dropped; narrow the report with filters (a shorter date range, one company or party) or a smaller limit", total-k, total, maxToolResultBytes>>10)
		return out
	}
	k := largestFit(len(rows), build)
	if k == 0 {
		return r
	}
	return build(k)
}
