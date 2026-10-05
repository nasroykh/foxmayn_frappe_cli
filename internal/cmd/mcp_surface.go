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
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// The MCP surface around the tools (T2.7): tool sets, titles and size hints,
// server instructions, resources, prompts, progress notifications and the
// fitting of large row lists into a result.

// mcpToolsets is `ffc mcp --toolsets`; nil exposes defaultToolsets.
var mcpToolsets []string

// cleanToolsets trims --toolsets and refuses an unknown or missing name.
func cleanToolsets(cmd *cobra.Command) error {
	var out []string
	for _, v := range mcpToolsets {
		switch v = strings.TrimSpace(v); v {
		case "":
		case toolsetCore, toolsetLifecycle, toolsetCollab, toolsetAdmin, toolsetFiles:
			if !contains(out, v, false) {
				out = append(out, v)
			}
		default:
			return usageErrorf("--toolsets: unknown tool set %q (known: %s)", v, strings.Join(knownToolsets, ", "))
		}
	}
	if cmd.Flags().Changed("toolsets") && len(out) == 0 {
		return usageErrorf("--toolsets needs at least one value")
	}
	mcpToolsets = out
	return nil
}

// inToolsets reports whether tool belongs to a tool set the server exposes
// (defaultToolsets without --toolsets). Siteless tools (list_sites) are
// always exposed.
func (env *mcpEnv) inToolsets(tool string) bool {
	if siteless[tool] {
		return true
	}
	sets := env.toolsets
	if len(sets) == 0 {
		sets = defaultToolsets
	}
	return contains(sets, toolSurface[tool].toolset, false)
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
	resources := registerResources(s, env)
	registerPrompts(s, env)
	registerMCPCompletions(s, env)
	server.WithInstructions(mcpInstructions(s, env, policies, resources))(s)
}

// ─── instructions ───────────────────────────────────────────────────────────

// mcpInstructions is the text a client gives the model on connecting. It is
// built from the served sites, their policies, the registered tools and the
// registered resources, so it never mentions a tool, resource or site the
// server does not offer.
func mcpInstructions(s *server.MCPServer, env *mcpEnv, policies []mcpPolicy, resources []string) string {
	tools := s.ListTools()
	has := func(names ...string) bool {
		for _, n := range names {
			if _, ok := tools[n]; !ok {
				return false
			}
		}
		return true
	}
	// which lists the registered tools among names, as "a, b or c".
	which := func(names ...string) string {
		var got []string
		for _, n := range names {
			if has(n) {
				got = append(got, n)
			}
		}
		switch len(got) {
		case 0:
			return ""
		case 1:
			return got[0]
		}
		return strings.Join(got[:len(got)-1], ", ") + " or " + got[len(got)-1]
	}
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	line("ffc reaches Frappe/ERPNext sites through their REST API, with the permissions of the user ffc signs in as.")
	if len(env.sites) > 1 {
		except := ""
		if has("list_sites") {
			except = " except list_sites"
		}
		line("This server serves %d sites: %s. Every tool call%s needs a site argument naming one of them; there is no default, so ask the user when the site is unclear.", len(env.sites), env.siteNames(), except)
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
	if has("whoami") {
		line("whoami names the user ffc signs in as, its roles and the site's Frappe version.")
	}
	if has("check_permission") {
		line("check_permission tells whether that user may read, write, create, submit, cancel or delete, before you try.")
	}
	line("A document is identified by DocType and name. name is its ID (CUST-0042, ACC-SINV-2026-00001), not the title the desk shows; a Link field holds the linked document's name.")
	switch w := which("get_doc", "update_doc"); {
	case strings.Contains(w, " or "):
		line("A Single DocType (System Settings, Selling Settings) has one document: get_doc and update_doc take no name for it.")
	case w != "":
		line("A Single DocType (System Settings, Selling Settings) has one document: %s takes no name for it.", w)
	}
	if has("search") {
		line("To turn a title or partial text into a name, call search with doctype first, then use the value it returns.")
	}
	if has("get_doc_context") {
		line("get_doc_context answers who changed a document and when, its comments, attachments and assignments, and what links to it, in one call.")
	}
	line("Dates are YYYY-MM-DD and datetimes YYYY-MM-DD HH:MM:SS; Check fields are 0 or 1.")
	line(`Filters: an object {"status":"Open","docstatus":1} means equality, all conditions ANDed; a list [["grand_total",">",1000],["posting_date","between",["2026-01-01","2026-03-31"]]] takes operators =, !=, >, <, >=, <=, like (with %%), not like, in, not in, between, is ("set" or "not set"). Use fieldnames, not labels.`)
	if has("list_docs") {
		line("list_docs returns only name unless you pass fields: always pass fields and a limit (default 20, 0 = all) and page with start. A result too large to return comes back as {data, truncated, next_start, hint}: call again with start=next_start.")
	}
	if has("count_docs") {
		line("count_docs answers how many without fetching rows.")
	}
	if has("aggregate") {
		line("aggregate computes counts, sums, averages, min and max per group on the server: use it for totals instead of fetching rows with list_docs.")
	}
	if w := which("create_doc", "update_doc", "bulk_create", "bulk_update"); w != "" && has("get_schema") {
		line("Call get_schema before %s: it gives fieldnames, required fields, Link targets (options), Select options and child tables (a list of row objects).", w)
	}
	if has("update_doc") {
		line("update_doc changes only the fields you pass.")
	}
	line("docstatus on submittable DocTypes: 0 = draft (editable), 1 = submitted (final; only fields marked Allow on Submit can change), 2 = cancelled.")
	if has("submit_doc", "cancel_doc", "amend_doc") {
		line("Lifecycle: submit_doc (0 to 1), cancel_doc (1 to 2, cannot be undone), amend_doc (a new draft <name>-1 from a cancelled document).")
	}
	if has("get_transitions", "apply_workflow") {
		line("A DocType with an active workflow cannot be submitted or cancelled directly: call get_transitions, then apply_workflow with one of its actions.")
	}
	if w := which("add_comment", "assign_to", "remove_assignment", "add_tag", "remove_tag"); w != "" {
		line("Collaboration: %s. Users are User IDs (usually emails), not full names; add_comment takes plain text.", w)
	}
	if has("share_doc") {
		line("share_doc gives a user or everyone access to one document beyond their roles, so the user may be asked to confirm.")
		if has("assign_to") {
			line("When someone should act on a document, assign_to it rather than share_doc it.")
		}
	}
	if w := which("list_attachments", "attach_file", "get_print_html"); w != "" {
		line("Files: %s. No tool returns file contents or PDFs: the user downloads them with the CLI (ffc download, ffc pdf).", w)
	}
	line(`An error starting with "policy:" means this server's configuration refuses the call and nothing was sent; "cancelled by the user" means the user declined. Do not retry either another way.`)
	if len(resources) > 0 {
		line("Resources (the same JSON as the tools): %s. Percent-encode each segment (a space is %%20, a / in a name %%2F).", strings.Join(resources, ", "))
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

// resourceRead marks a tool call made for a resource read; toolHandler
// fills in how the call ended, so the read can report it.
type resourceRead struct {
	status string
	err    error
}

type resourceReadKey struct{}

func withResourceRead(ctx context.Context, rr *resourceRead) context.Context {
	return context.WithValue(ctx, resourceReadKey{}, rr)
}

func resourceReadFrom(ctx context.Context) *resourceRead {
	rr, _ := ctx.Value(resourceReadKey{}).(*resourceRead)
	return rr
}

// viaFrom is the audit record's via: "resource" for a resource read.
func viaFrom(ctx context.Context) string {
	if resourceReadFrom(ctx) != nil {
		return "resource"
	}
	return ""
}

// registerResources registers the resources whose tool is registered and
// returns how the instructions should name them.
func registerResources(s *server.MCPServer, env *mcpEnv) []string {
	tools := s.ListTools()
	site := "{site}"
	if len(env.sites) == 1 {
		site = url.PathEscape(env.sites[0])
	}
	var names []string
	if _, ok := tools["list_sites"]; ok {
		s.AddResource(mcp.NewResource(sitesURI, "sites",
			mcp.WithResourceTitle("Served sites"),
			mcp.WithResourceDescription("The sites this server serves: name, URL, how ffc signs in and whether MCP may only read it (the list_sites tool)."),
			mcp.WithMIMEType("application/json"),
		), resourceReader(s, env, "list_sites", func(string) (map[string]any, error) { return map[string]any{}, nil }))
		names = append(names, sitesURI)
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
		names = append(names, "ffc://"+site+"/schema/{doctype} (get_schema)")
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
		names = append(names, "ffc://"+site+"/doc/{doctype}/{name} (get_doc)")
	}
	return names
}

// splitFFCURI splits an ffc:// URI into its percent-decoded segments: the
// site, then the rest.
func splitFFCURI(uri string) (string, []string, error) {
	rest, ok := strings.CutPrefix(uri, "ffc://")
	// Defence in depth: the URI templates registered today already refuse a
	// query or fragment (and extra segments) before a handler runs, but a
	// later template with a {?query} part must not slip one into a name.
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
//
// mcp-go v1.1.1 answers every error a resource handler returns with
// -32603 (server.go handleReadResource), whatever it wraps, so the code
// cannot say "not found" or "refused"; the message does. A not-found
// document or DocType wraps server.ErrResourceNotFound (for OnError hooks
// and a later mcp-go that maps it), a policy refusal or a bad argument
// mcp.ErrInvalidParams; the message is the tool's, unchanged.
func resourceReader(s *server.MCPServer, env *mcpEnv, tool string, args func(uri string) (map[string]any, error)) server.ResourceHandlerFunc {
	return func(ctx context.Context, rr mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		uri := rr.Params.URI
		a, err := args(uri)
		if err != nil {
			rec := auditRecord{Time: time.Now().UTC(), Tool: tool, Client: mcpClientName(ctx), Via: "resource", Status: auditInvalid, Error: err.Error()}
			env.audit.write(rec, map[string]any{"uri": uri})
			return nil, classedError{err.Error(), mcp.ErrInvalidParams}
		}
		st, ok := s.ListTools()[tool]
		if !ok {
			return nil, classedError{fmt.Sprintf("resource: the %s tool is not available on this server", tool), server.ErrResourceNotFound}
		}
		req := mcp.CallToolRequest{}
		req.Params.Name, req.Params.Arguments = tool, a
		read := &resourceRead{}
		res, err := st.Handler(withResourceRead(ctx, read), req)
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
			return nil, resourceError(read, text.String())
		}
		return []mcp.ResourceContents{mcp.TextResourceContents{URI: uri, MIMEType: "application/json", Text: text.String()}}, nil
	}
}

// classedError is an error whose message is msg alone (a policy refusal
// keeps starting with "policy:") while errors.Is still finds class.
type classedError struct {
	msg   string
	class error
}

func (e classedError) Error() string { return e.msg }
func (e classedError) Unwrap() error { return e.class }

// resourceError is the error for a failed resource read: the tool's message,
// wrapping a sentinel for its class.
func resourceError(read *resourceRead, msg string) error {
	switch {
	case read.status == auditDenied || read.status == auditInvalid:
		return classedError{msg, mcp.ErrInvalidParams}
	case read.err != nil:
		if code, _ := classify(read.err); code == exitNotFound {
			return classedError{msg, server.ErrResourceNotFound}
		}
	}
	return errors.New(msg)
}

// ─── prompts ────────────────────────────────────────────────────────────────

// mcpPrompt is a prompt: guidance that names the tools to call and what to
// check. Prompts make no site calls. A prompt is offered only when the
// tools in needs are registered; a step that uses another tool is left out
// when that tool is not registered.
type mcpPrompt struct {
	name, title, desc string
	needs             []string // tools the plan cannot do without
	args              []mcp.PromptOption
	maxLen            map[string]int // every argument, with its longest value in runes
	required          []string
	text              func(a map[string]string, has func(tool string) bool) string
}

// promptJSON is v as JSON, for a value shown in a prompt (and inside the
// JSON filters a prompt spells out): exact, and with every control, format
// (bidi, zero-width) or other unprintable character as a \u escape, so the
// value cannot hide or reorder the text around it.
func promptJSON(v string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	var out strings.Builder
	for _, r := range strings.TrimSuffix(b.String(), "\n") {
		if unicode.IsPrint(r) {
			out.WriteRune(r)
			continue
		}
		for _, u := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&out, "\\u%04x", u)
		}
	}
	return out.String()
}

// steps numbers the non-empty steps.
func steps(list ...string) string {
	var out []string
	for _, s := range list {
		if s != "" {
			out = append(out, fmt.Sprintf("%d. %s", len(out)+1, s))
		}
	}
	return strings.Join(out, "\n")
}

// when returns s when ok, else "".
func when(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

var mcpPrompts = []mcpPrompt{
	{
		name: "inspect-doctype", title: "Inspect a DocType",
		desc:  "Read a DocType's schema, counts and sample rows and explain how its documents are built. Read-only.",
		needs: []string{"get_schema", "list_docs", "count_docs"},
		args: []mcp.PromptOption{mcp.WithArgument("doctype", mcp.RequiredArgument(),
			mcp.ArgumentDescription("The DocType, e.g. Sales Invoice"))},
		maxLen:   map[string]int{"doctype": 140},
		required: []string{"doctype"},
		text: func(a map[string]string, has func(string) bool) string {
			dt := promptJSON(a["doctype"])
			return fmt.Sprintf("Inspect the Frappe DocType %[1]s with the ffc tools. Read only: create or change nothing.\n\n", dt) + steps(
				fmt.Sprintf(`get_schema doctype=%[1]s. Note the naming rule (autoname), is_submittable, istable, issingle, the title field and track_changes; for each field its fieldname, label, fieldtype, reqd, options (the Link target DocType or the Select values), default and read_only. For each Table field, call get_schema on its options DocType (the child table).`, dt),
				fmt.Sprintf(`count_docs doctype=%[1]s; if it is submittable, also with filters {"docstatus":0}, {"docstatus":1} and {"docstatus":2}.`, dt),
				fmt.Sprintf(`list_docs doctype=%[1]s with fields ["name","modified"] plus the title field and 2-4 key fields, limit 5, order_by "modified desc", to see real values.`, dt),
				when(has("get_transitions"), "If it is submittable, get_transitions on one of those documents shows whether a workflow drives it."),
				when(has("check_permission"), fmt.Sprintf("check_permission doctype=%[1]s for read, write and create shows what the signed-in user may do.", dt)),
			) + "\n\nReport: what the DocType is for; how documents are named; the required fields with their types and Link targets; the child tables; whether it is submittable or workflow-driven; and typical values."
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
		maxLen:   map[string]int{"doctype": 140, "source": 500},
		required: []string{"doctype"},
		text: func(a map[string]string, has func(string) bool) string {
			dt := promptJSON(a["doctype"])
			src := "the data the user provides"
			if a["source"] != "" {
				src = promptJSON(a["source"])
			}
			resolve := "count_docs doctype=<target DocType> with a name filter"
			if has("search") {
				resolve = "search doctype=<target DocType> (or count_docs with a name filter)"
			}
			undo := ""
			if has("bulk_delete") {
				undo = " To undo, bulk_delete the created names."
			}
			submit := ""
			if has("submit_doc") {
				submit = " bulk_create makes drafts; submitting is a separate step (submit_doc per document)."
			}
			return fmt.Sprintf("Import %[2]s into the Frappe DocType %[1]s safely with the ffc tools.\n\n", dt, src) + steps(
				fmt.Sprintf("get_schema doctype=%[1]s: required fields (reqd), fieldtypes, Link fields and their target DocTypes (options), Select options, unique fields and the naming rule (a series, a field, or a name you give). A Table field takes a list of row objects.", dt),
				"Map every source column to a fieldname; never send labels. Leave out columns with no field. Dates become YYYY-MM-DD, numbers JSON numbers, checkboxes 0 or 1.",
				fmt.Sprintf("Check references before writing: resolve each distinct Link value with %s. List the values that do not exist and ask the user before going on.", resolve),
				`Check duplicates: list_docs or count_docs with an "in" filter on the naming or unique field for the incoming keys.`,
				when(has("check_permission"), fmt.Sprintf("check_permission doctype=%[1]s perm_type=create confirms the user may create them.", dt)),
				"Show the user the mapping and the counts (to create, duplicates skipped, invalid rows) and wait for a go-ahead.",
				"Trial: bulk_create with the first 1-3 rows only"+when(has("get_doc"), ", then get_doc one created name and compare it with the source")+".",
				"Import the rest with bulk_create, at most 200 items per call. Each item is reported as created or error and the call goes on after an error. Retry only the failed items after fixing them, never a whole batch: that would create duplicates.",
				"Finish with count_docs and a summary."+submit+undo,
			)
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
		maxLen:   map[string]int{"doctype": 140, "name": 140},
		required: []string{"doctype", "name"},
		text: func(a map[string]string, has func(string) bool) string {
			dt, name := promptJSON(a["doctype"]), promptJSON(a["name"])
			tracked := ""
			if has("get_schema") {
				tracked = ` (get_schema keys ["track_changes"])`
			}
			return fmt.Sprintf("Find out who changed the Frappe document %[1]s %[2]s and how, with the ffc tools. Read only: change nothing.\n\n", dt, name) + steps(
				when(has("get_doc_context"), fmt.Sprintf(`get_doc_context doctype=%[1]s name=%[2]s timeline=true: the last 10 versions (field, from, to), comments, the workflow log and the timeline in one call. When it shows fewer than 10 versions you have them all; skip the Version step below.`, dt, name)),
				fmt.Sprintf(`get_doc doctype=%[1]s name=%[2]s fields ["name","owner","creation","modified","modified_by","docstatus","amended_from"]: who created it and who changed it last.`, dt, name)+
					when(has("search"), fmt.Sprintf(" If the name is not found, resolve it with search doctype=%[1]s.", dt)),
				fmt.Sprintf(`list_docs doctype "Version", filters {"ref_doctype":%[1]s,"docname":%[2]s}, fields ["name","owner","creation","data"], order_by "creation asc", limit 100. Each data is a JSON string: changed is a list of [field, old, new]; added, removed and row_changed describe child-table rows. Frappe keeps Versions only when the DocType has Track Changes on%[3]s; with none, say that the history is not recorded.`, dt, name, tracked),
				fmt.Sprintf(`list_docs doctype "Comment", filters {"reference_doctype":%[1]s,"reference_name":%[2]s}, fields ["comment_type","content","owner","creation"], order_by "creation asc": submissions, cancellations, assignments, workflow changes and user comments.`, dt, name),
				"If amended_from is set, repeat for that document (an amendment of X is X-1, then X-2).",
			) + "\n\nReport a timeline: date, user, what changed (field: old -> new) and docstatus changes. A policy error on Version or Comment means this server may not read them: say so."
		},
	},
	{
		name: "explain-report", title: "Explain a report",
		desc:  "Find out what a report shows, which filters it needs and what its columns mean, then run it on a small sample. Read-only.",
		needs: []string{"run_report", "get_doc"},
		args: []mcp.PromptOption{mcp.WithArgument("report_name", mcp.RequiredArgument(),
			mcp.ArgumentDescription("The report's name, e.g. Accounts Receivable"))},
		maxLen:   map[string]int{"report_name": 140},
		required: []string{"report_name"},
		text: func(a map[string]string, has func(string) bool) string {
			r := promptJSON(a["report_name"])
			return fmt.Sprintf("Explain the Frappe report %[1]s with the ffc tools. Read only.\n\n", r) + steps(
				fmt.Sprintf(`get_doc doctype "Report" name=%[1]s fields ["name","report_type","ref_doctype","module","is_standard","disabled","prepared_report"]: the report type (Report Builder, Query Report or Script Report) and the DocType it is based on.`, r)+
					when(has("list_reports"), " list_reports finds the exact name if this one is not found."),
				when(has("get_schema"), "get_schema on its ref_doctype for the meaning of the fields it reports."),
				fmt.Sprintf("run_report report_name=%[1]s with limit 20 and a narrow filters object. Many reports need filters (company, from_date, to_date, ...): a missing mandatory filter fails with a message naming it; add it and run again. Dates are YYYY-MM-DD.", r),
				"Name every value from columns (fieldname, label, fieldtype, options): rows are arrays in column order or objects keyed by fieldname. report_summary holds totals when present. truncated means rows were cut: never present a partial result as a total.",
			) + "\n\nExplain what the report shows, what each column means, which filters matter and what the sample rows say."
		},
	},
}

func registerPrompts(s *server.MCPServer, env *mcpEnv) {
	tools := s.ListTools()
	has := func(t string) bool { _, ok := tools[t]; return ok }
	for _, p := range mcpPrompts {
		ok := true
		for _, t := range p.needs {
			ok = ok && has(t)
		}
		if !ok {
			continue
		}
		opts := append([]mcp.PromptOption{mcp.WithPromptTitle(p.title), mcp.WithPromptDescription(p.desc)}, p.args...)
		if len(env.sites) > 1 {
			opts = append(opts, mcp.WithArgument("site", mcp.RequiredArgument(),
				mcp.ArgumentDescription(fmt.Sprintf("The site: one of %s", env.siteNames()))))
		}
		s.AddPrompt(mcp.NewPrompt(p.name, opts...), promptHandler(env, p, has))
	}
}

func promptHandler(env *mcpEnv, p mcpPrompt, has func(string) bool) server.PromptHandlerFunc {
	return func(_ context.Context, gr mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		a := gr.Params.Arguments
		for _, k := range p.required {
			if strings.TrimSpace(a[k]) == "" {
				return nil, fmt.Errorf("prompt %s: argument %s is required", p.name, k)
			}
		}
		// A value is refused, not cut: a cut name would name another document.
		for k, max := range p.maxLen {
			if n := utf8.RuneCountInString(a[k]); n > max {
				return nil, fmt.Errorf("prompt %s: argument %s is %d characters long, at most %d", p.name, k, n, max)
			}
		}
		text := p.text(a, has)
		if len(env.sites) > 1 {
			// The same check as a tool call's site, without reading the config.
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{"site": a["site"]}
			site, err := env.siteFor(req)
			if err != nil {
				return nil, fmt.Errorf("prompt %s: %w", p.name, err)
			}
			text += fmt.Sprintf("\n\nThis server serves several sites: pass site=%s to every tool call.", promptJSON(site))
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
// when the client asked for it. It is best effort: mcp-go queues the
// notification on the session's channel without waiting (stdio buffers 100;
// a full channel drops it) and writes it from another goroutine, so a
// notification can be lost or arrive after the tool's response. Clients
// must not count on seeing every step or the last one.
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
	limitCut := ""
	if t, ok := r["total_rows"].(int); ok && t > len(rows) {
		total = t // limit already cut the report's rows to len(rows)
		limitCut = fmt.Sprintf(" (limit had already cut the report's %d rows to %d)", t, len(rows))
	}
	build := func(k int) interface{} {
		out := make(map[string]interface{}, len(r)+1)
		for key, v := range r {
			out[key] = v
		}
		out["result"], out["truncated"], out["total_rows"] = rows[:k], true, total
		out["hint"] = fmt.Sprintf("%d of the %d rows did not fit in %d KiB and were dropped%s; narrow the report with filters (a shorter date range, one company or party) or a smaller limit", len(rows)-k, len(rows), maxToolResultBytes>>10, limitCut)
		return out
	}
	k := largestFit(len(rows), build)
	if k == 0 {
		return r
	}
	return build(k)
}
