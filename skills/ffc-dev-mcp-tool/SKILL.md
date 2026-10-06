---
name: ffc-dev-mcp-tool
description: Add or change an MCP tool, or touch the MCP server internals, in the ffc Go codebase - toolHandler and the argument helpers, toolActions and toolSurface registration, scopeOf and the per-site policy, confirmations, multi-DocType results filtered with policyFrom, result size caps, jq, tool sets, instructions, resources and prompts, the detached HTTP daemon, multi-site serving, and ffc mcp install/uninstall (internal/mcpinstall). Use it whenever you edit internal/cmd/mcp_*.go or internal/mcpinstall, or expose a CLI feature to AI clients. Load ffc-dev first.
---

# Add or change an MCP tool

The `mcp` command is a long-running server, not a one-shot command. CLAUDE.md pitfalls "MCP policy", "MCP confirmation", "Multi-site MCP", "MCP surface" and "ffc mcp install" are the full rules.

## Hard rules

- **stdout is the JSON-RPC channel.** A handler never writes to stdout and never calls `output.Print*`. stderr is the client's log in stdio mode and `mcp.log` when detached: never print secrets there.
- Errors become tool errors (`mcp.NewToolResultError`, nil Go error). A non-nil Go error from a handler is a protocol crash; reserve it for the unexpected. `toolHandler` already does this for you.
- Read JSON-valued arguments with `jsonArg`/`rawJSONArg`/`objectArg` (native JSON or a JSON string), lists with `stringsArg`/`listArg`, integers with `intArg`, optional strings with `stringArg`. **Never `req.GetString` for a JSON parameter**: it returns "" for a native object and silently drops filters. `req.RequireString` is fine for plain required strings.
- The site client comes from the env (`toolHandler` → `env.client`), which goes through `loadSite`/`newSiteClient`. Never build one yourself.
- MCP never dry-runs.

## Checklist for a new tool

1. **Define** it in the right file (`mcp_tools.go` core, `mcp_lifecycle_tools.go`, `mcp_collab_tools.go`, `mcp_files_tools.go`, `mcp_identity_tools.go`, ...): `mcp.NewTool(name, mcp.WithDescription(...), mcp.WithReadOnlyHintAnnotation(...), params...)`, or `docTool(name, desc, readOnly, destructive, extra...)` for a tool on one document (adds `doctype*`, `name*` and the annotations). JSON parameters use `jsonParam(name, desc)` (no fixed schema type).
2. **Handler:** `s.AddTool(tool, toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) { ...parse and validate...; return func(ctx, c) (interface{}, error) {...}, nil }))`. Validate everything in the parse step so a bad call costs no login or request. Reuse the CLI's shared function (as `checkPermission`, `runSearch`, `fetchDocContext`, `buildAggregateQuery` are shared) instead of duplicating logic.
3. **Register** the function in `registerAllTools` (mcp_tools.go).
4. **`toolActions`** (mcp_policy.go): `actRead`, `actWrite` or `actMethod`, matching the read-only annotation. `TestMCPPolicyCoversEveryTool` fails otherwise.
5. **`toolSurface`**: tool set (`core`, `lifecycle`, `collab`, `admin`, `files`), title, and `big` when the result can approach 512 KiB (big read tools also get the `jq` argument). `TestMCPToolSurface` fails otherwise. `collab`, `admin`, `files` are off by default.
6. **`scopeOf`**: teach it every argument other than `doctype`/`name` that names a DocType, document, method or report. If the tool writes another DocType as a side effect (Comment, ToDo, DocShare, File), add it to `collabDoctypes` so DocType rules and the sensitive list apply.
7. **Confirmation:** a tool that destroys, merges or widens access goes in `needsConfirm`, with a `confirmMessage` and a `cliEquivalent` case.
8. **Results spanning DocTypes the call does not name** (global search hits, linked documents): filter inside the toolCall with `policyFrom(ctx)`, fail when it returns `ok == false` (the zero policy allows everything), and report what was dropped as `hidden_by_policy`.
9. **Size:** return plain values; `toolHandler` marshals them through `marshalResult` (512 KiB cap). A row list that can grow uses the `fitListRows`/`fitReportRows` pattern (largest prefix plus `truncated`, `hint`). A tool with an output schema returns `structuredOut{Text, Structured}` and keeps its text byte-identical.
10. **Surface:** if the instructions (`mcpInstructions`) or a prompt should mention the tool, gate the line on `has(tool)`.
11. **Sites:** the `site` argument is added for free when several sites are served. A tool about the server rather than a site goes in `siteless`.
12. **Tests:** `newMCPFake(t, readOnly)` + `callTool(t, s, name, args)` (goes through JSON-RPC decoding); confirmation flows with `mcpTClient`. Cover policy refusal (nothing sent), read-only registration, and argument decoding of native vs string JSON.
13. **Docs:** CLAUDE.md, README MCP section, and `skills/ffc-mcp/references/tools.md`.

Skeleton:

```go
func registerThing(s *server.MCPServer, env *mcpEnv) {
	tool := mcp.NewTool("thing",
		mcp.WithDescription("What it does, what it returns, when to use it instead of X."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("doctype", mcp.Required(), mcp.Description("The Frappe DocType")),
		jsonParam("filters", `Filter as a JSON object or array`),
		mcp.WithNumber("limit", mcp.Description("Maximum rows. Default 20.")),
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
		limit, err := intArg(req, "limit", 20)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.GetList(ctx, doctype, client.ListOptions{Filters: filters, Limit: limit})
		}, nil
	}))
}
```

## How a call flows

`toolHandler`: parse → site (`env.site`, re-read every call, so policy edits apply at once) → `scopeOf` → `policy.check` (refusal: `policy: ...`, nothing sent) → `policy.confirm` (elicitation) → client → call → one audit line in `mcp-audit.jsonl` (args redacted via `client.RedactArgs`). Resources (`ffc://...`) are served by calling the tool's own handler (`resourceReader`), never a separate client path.

Server internals (daemon, multi-site, confirmation state, jq child process, surface, `mcp install`): [references/server-internals.md](references/server-internals.md).
