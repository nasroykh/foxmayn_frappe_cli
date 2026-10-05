---
name: ffc-dev
description: >
  Development guide for the ffc (Foxmayn Frappe CLI) Go codebase. Use this skill
  whenever working inside the foxmayn_frappe_cli repository — adding commands,
  extending the API client, modifying output formatting, updating config logic,
  fixing bugs, or refactoring. Trigger on any task involving internal/cmd/,
  internal/client/, internal/output/, internal/config/, or the Makefile. Also
  trigger when the user mentions "ffc", "frappe cli", "add a command", "new
  subcommand", or any Frappe API integration work within this project.
---

# ffc Development Guide

Build and extend the ffc CLI — a Go tool for interacting with Frappe ERP sites via the REST API.

## Tech Stack

| Component        | Library        | Import                                                    |
| ---------------- | -------------- | --------------------------------------------------------- |
| CLI framework    | cobra          | `github.com/spf13/cobra`                                  |
| Config           | yaml v3        | `go.yaml.in/yaml/v3` (structs for reads, `yaml.Node` for edits) + env vars |
| HTTP client      | resty          | `github.com/go-resty/resty/v2`                            |
| Tables & styling | lipgloss v2    | `charm.land/lipgloss/v2` + `charm.land/lipgloss/v2/table` |
| Forms & prompts  | huh v1.0.0     | `github.com/charmbracelet/huh`                            |
| Spinner          | huh/spinner    | `github.com/charmbracelet/huh/spinner`                    |
| MCP server       | mcp-go v1.1.1  | `github.com/mark3labs/mcp-go/mcp` + `.../server`          |

## Project Layout

```
cmd/ffc/main.go               → calls cmd.Execute()
internal/cmd/root.go          → root cobra command, global flags (--site, --config, --json, --quiet, --timeout)
internal/cmd/site_client.go   → loadSite = loadSiteConfig (no network) + refreshSite (OAuth refresh under the config lock), newClient — the ONLY way a command/MCP tool gets a site client
internal/cmd/helpers.go       → callSite[T] (client + spinner), runSpinner, confirm, readInput ("-" = stdin), parseObject, splitCSV, filterKeys/selectKeys, listLimit, docName, validateFiltersJSON
internal/cmd/auth_wizard.go   → shared auth wizard (API key / password / OAuth) for init and site add; runForm (Esc/Ctrl+C → errAborted), validateSiteName, normalizeSiteURL, writeInitConfig, addSiteToConfig
internal/cmd/init.go          → init subcommand (--oauth / --apikey / --password)
internal/cmd/oauth_flow.go    → OAuth PKCE: callbackServer (127.0.0.1, single-use), collectOAuthSite
internal/cmd/site.go          → site list / add / remove / use
internal/cmd/config_cmd.go    → config TUI, config get, config set; escQuitKeyMap, resolveCfgPath
internal/cmd/ping.go          → ping subcommand (also names the user)
internal/cmd/whoami.go        → whoami subcommand, buildWhoami (shared with the MCP tool), authMethod
internal/cmd/can.go           → can subcommand, checkPermission (shared with the MCP tool), parsePerm, deniedError (exit 5)
internal/cmd/doctor.go        → doctor subcommand: one method per check on `doctor`, doctorCheck {check,status,message,hint}
internal/cmd/server_cache.go  → per-site server.json cache in os.UserCacheDir() (serverInfo, cacheDirName; the dir comes from the `userCacheDir` var)
internal/cmd/get_doc.go       → get-doc subcommand
internal/cmd/list_docs.go     → list-docs subcommand + parseFields()
internal/cmd/create_doc.go    → create-doc subcommand
internal/cmd/update_doc.go    → update-doc subcommand + docNameOrSingle()
internal/cmd/delete_doc.go    → delete-doc subcommand
internal/cmd/count_docs.go    → count-docs subcommand
internal/cmd/get_schema.go    → get-schema subcommand, fetchSchema, compactSchema, mergeCustomFields, applyPropertySetters
internal/cmd/list_doctypes.go → list-doctypes subcommand
internal/cmd/list_reports.go  → list-reports subcommand
internal/cmd/run_report.go    → run-report subcommand, limitReportRows
internal/cmd/search.go        → search subcommand (search_link with -d, global search without), runSearch/validateSearch shared with the MCP tool
internal/cmd/call_method.go   → call-method subcommand
internal/cmd/bulk.go          → runBulk worker pool, bulkReport, parseObjects/parseNames/splitUpdates, bulkFlags
internal/cmd/bulk_{create,update,delete}.go → bulk commands (--concurrency 1-10, --fail-fast)
internal/cmd/{submit_doc,cancel_doc,discard_doc,amend_doc,rename_doc,restore_doc}.go → lifecycle commands
                                  (amend_doc.go also has copy-doc); refuseWorkflow in submit_doc.go
internal/cmd/workflow.go          → workflow transitions / apply / bulk-apply (bulkFlags) / pending
internal/cmd/dryrun.go            → addDryRun, withDryRun (trackRunStart), printPlan, fieldChanges; planAll in bulk.go
internal/cmd/update.go            → update subcommand: size-limited download, signed checksums.txt (relsig) + SHA256 check, atomic binary swap
internal/cmd/update_check.go      → background update check; owns rootCmd.PersistentPreRunE + state file
internal/cmd/mcp.go               → mcp subcommand: stdio/HTTP/detach routing, --detach/--port/--read-only + policy flags, newMCPEnv, startMCP
internal/cmd/mcp_policy.go        → per-site MCP policy: toolActions, toolSurface (tool set, title, large-result hint), scopeOf, mcpPolicy.check, sensitive DocTypes, denied methods
internal/cmd/mcp_surface.go       → --toolsets, describeTools (titles, _meta), mcpInstructions, resources (served by the tool handlers), prompts, notifyProgress, fitListRows/fitReportRows
internal/cmd/mcp_audit.go         → MCP audit log mcp-audit.jsonl (0600, rotated at 10 MiB)
internal/cmd/mcp_sites.go         → multi-site MCP: mcpSites, siteFor, list_sites, addSiteParam
internal/cmd/mcp_confirm.go       → confirmation through MCP elicitation: needsConfirm, mcpPolicy.confirm, HMAC request state
internal/cmd/mcp_args.go          → mcpEnv, toolHandler (parse → policy → confirm → client → call → audit), marshalResult (512 KiB cap), jsonArg/rawJSONArg/objectArg/intArg/stringsArg
internal/cmd/mcp_tools.go         → MCP tools + handlers (26 with the lifecycle and identity files); registerTools(); compactReportResult
internal/cmd/mcp_identity_tools.go → whoami + check_permission read tools
internal/cmd/mcp_lifecycle_tools.go → submit/cancel/amend/copy/rename/apply_workflow + get_transitions (docTool, docHandler)
internal/cmd/mcp_daemon.go        → startDetached(), runHTTPServer(), mcpStatusCmd, mcpStopCmd, state + lock files
internal/cmd/mcp_detach_unix.go   → setSysProcAttr (Setsid=true), terminateProcess, isProcessRunning — build tag: !windows
internal/cmd/mcp_detach_windows.go → same functions for Windows — build tag: windows
internal/client/http.go           → newResty (timeout, 128 MiB body cap, no cookie jar, silent logger, GET-only redirects), retry policy, NewHTTPClient, requestError
internal/client/client.go         → FrappeClient; New() picks auth; single do() request path; session relogin; Close(); GetDoc, GetList, …
internal/client/oauth.go          → ExchangeOAuthCode, RefreshOAuthToken, GetOAuthUser (all take ctx)
internal/client/session.go        → LoginPassword (POST /api/method/login, sid cookie, 2FA detection)
internal/client/debug.go          → --debug trace (debugTransport under resty), DebugLevel; redaction helpers shared with dry runs
internal/client/dryrun.go         → WithDryRun(ctx, scope), DryRunError; send() holds back writes (scope all: every request)
internal/client/server.go         → ServerVersions/ServerInfo (FrappeMajor), LoggedUser, UserRoles (Has Role via get_list), HasPermission, DocPermissions, DocTypePermission/EvalDocTypePermission
internal/client/lifecycle.go      → SubmitDoc/CancelDoc/AmendDoc/DuplicateDoc (GetDoc + clean; no-copy fields from getdoctype),
                                    RenameDoc, RestoreDeleted (returns new_name), DiscardDoc (v16), workflow methods
internal/config/config.go         → Config/SiteConfig, Read, Load, env overrides, default paths
internal/config/file.go           → File (yaml.Node editor), Edit/Overwrite (lock + atomic 0600 write), WriteFileAtomic
internal/config/format.go         → number/date formats, FormatNumber, FormatDate
internal/output/output.go         → PrintTable, PrintDocTable, PrintJSON, PrintError, PrintSuccess (all server values pass through text.Sanitize)
internal/text/text.go             → Sanitize: strips C0/C1 control characters (terminal escape injection)
internal/version/version.go       → build-time ldflags (Version, Commit, Date)
Makefile                      → build, install, tidy, vet, fmt, test, lint, clean
```

## Adding a New Command

This is the most common task. Follow this exact pattern — it matches every existing command in the codebase.

### 1. Create the file

Create `internal/cmd/<command_name>.go` in package `cmd`. Use snake_case for filenames, kebab-case for the command's `Use` field.

### 2. Follow this structure

```go
package cmd

import (
    "context"
    "fmt"

    "github.com/nasroykh/foxmayn_frappe_cli/internal/client"
    "github.com/nasroykh/foxmayn_frappe_cli/internal/output"

    "github.com/spf13/cobra"
)

// <command>-specific flags — use a unique 2-letter prefix to avoid package-level collisions.
// Check existing files to pick an unused prefix.
var (
    xxDoctype string
    xxName    string
)

var myCmd = &cobra.Command{
    Use:   "my-command",
    Short: "One-line description",
    Long: `Longer description with examples.

Examples:
  ffc my-command --doctype "ToDo" --name "TD-001"
  ffc my-command -d "User" -n "admin@example.com" --json
`,
    Args: cobra.NoArgs,
    RunE: func(cmd *cobra.Command, args []string) error {
        // 1. Parse/validate flags first (bad input must not cost a login or request)
        // ...

        // 2. callSite loads the site (OAuth refresh), builds the client and runs fn under a spinner
        doc, err := callSite(cmd, "Doing something…", func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
            return c.GetDoc(ctx, xxDoctype, xxName)
        })
        if err != nil {
            return err
        }

        // 3. Output (respect --json global flag); PrintJSON returns an error
        if jsonOutput {
            return output.PrintJSON(doc)
        }
        output.PrintDocTable(doc, nil)
        return nil
    },
}

func init() {
    myCmd.Flags().StringVarP(&xxDoctype, "doctype", "d", "", "Frappe DocType (required)")
    myCmd.Flags().StringVarP(&xxName, "name", "n", "", "Document name (required)")

    _ = myCmd.MarkFlagRequired("doctype")
    _ = myCmd.MarkFlagRequired("name")

    rootCmd.AddCommand(myCmd)
}
```

### Key patterns to follow

- **Global flags** `siteName`, `configPath`, `jsonOutput`, `quiet` (and `client.Timeout` for `--timeout`) are package-level vars set in `root.go` — use them directly, don't redeclare.
- **Never build a client with `client.New` directly** in a command — use `callSite` / `newClient` so `loadSite` runs and an expired OAuth token is refreshed. (`ping` is the one exception: it calls `loadSite` + `client.New` itself to time the whole round-trip.) A command that also needs the site config (to key a cache, name the auth method) uses `callSiteCfg` / `newClientCfg`.
- **Site facts are cached, not refetched.** Installed apps and versions go through `serverInfo(ctx, c, cfg, refresh)` (24 h, `<user cache dir>/ffc/<name>-<hash>/server.json`, 0700/0600, atomic, keyed by URL too). A command with a `--refresh` flag passes it (doctor has none: it reads live and never touches the cache). Version-dependent behaviour uses `serverInfo(...).FrappeMajor()`. Never build a cache path from a raw site name: use `cacheDirName`. Tests repoint the `userCacheDir` seam (TestMain, `cacheTEnv`); setting XDG_CACHE_HOME would not work on macOS or Windows.
- **A new `doctor` check** is a method on `doctor` that calls `d.add(id, checkPass|checkWarn|checkFail, message, hint)`. Check ids are part of the JSON contract: never rename one. Messages must not contain secrets (config values in parse errors are stripped; URLs go through `redactedURL`). A check must not change state (no OAuth refresh, no cache write, no cleanup). The probe is one request: `net.tls` reads `resp.RawResponse.TLS`, so a proxy is honoured. Tests use the seams `doctorNow` and `doctorTLSRoots`.
- **`Args: cobra.NoArgs`** on data commands.
- **Flag variable prefixes**: Each command uses a unique 2-letter prefix for its flag vars to avoid collisions within the `cmd` package. Check existing files before choosing one.
- **RunE, not Run**: Return errors — cobra handles printing them to stderr and setting exit code 1. Any abort, declined confirmation (`errAborted`) or partial bulk failure (`bulkReport.err()`) must also be a non-zero exit.
- **Spinner pattern**: `callSite` wraps the call in `runSpinner`, which draws only on an interactive stderr (off with `--quiet`, `NO_COLOR`, `CI`, or a redirected stderr). Use `runSpinner` directly only for non-site work.
- **Output routing**: Data to stdout (`output.PrintTable`, `output.PrintJSON`). Diagnostics/errors to stderr (`output.PrintError`, `fmt.Fprintln(os.Stderr, ...)`). Server-supplied strings are sanitised (`text.Sanitize`) by the `output` package; sanitise them yourself if you print them elsewhere.
- **Register in init()**: Call `rootCmd.AddCommand(yourCmd)` inside the file's `init()` function — cobra picks it up automatically.

## Adding Subcommands to an Existing Command

For nested commands (like `config get` / `config set` under `config`), register them against the parent command in `init()`:

```go
parentCmd.AddCommand(childCmd)  // not rootCmd.AddCommand
```

The parent command can still have its own `RunE` (runs when called with no subcommand) alongside subcommands.

## Extending the API Client

The client lives in `internal/client/client.go`. It wraps resty (configured by `newResty` in `http.go`) with Frappe-specific auth and error handling. Every method goes through the single `do()` request path, which handles auth headers, session relogin and error conversion.

### Adding a new API method

```go
// Example: CreateDoc posts a new document.
func (c *FrappeClient) CreateDoc(ctx context.Context, doctype string, data map[string]interface{}) (map[string]interface{}, error) {
    hints := readHints(doctype) // 401/403/404 → actionable messages
    hints[http.StatusForbidden] = fmt.Sprintf("permission denied (403): your user may not have create access to %s", doctype)
    var env dataEnvelope
    if err := c.do(ctx, http.MethodPost, resourcePath(doctype), data, nil, hints, &env); err != nil {
        return nil, err
    }
    return env.doc() // a 2xx without "data" is an error, not an empty doc
}
```

Every method takes a `ctx` (Ctrl+C cancels the in-flight request). `do()` turns >= 400 responses into errors via `apiError`: the hint for that status (if any) plus the Frappe message, otherwise the exception type and message (`frappeErrorResponse.userMessage()`); non-JSON bodies are cut to 300 runes and sanitised.

**Transport rules** (`http.go`): only GET follows redirects (a redirected POST/PUT/DELETE fails with the target URL); retries only for GET, on 429/502/503/504 (honouring a Retry-After up to 10 s; a longer one is reported, not retried) and on transport errors other than a timeout or cancel — do not add retries to writes; non-site HTTP (GitHub API, downloads) uses `client.NewHTTPClient(timeout)`, never `resty.New()`.

### Frappe API essentials

- **Base URL pattern**: `/api/resource/{DocType}` for lists, `/api/resource/{DocType}/{name}` for single docs.
- **Auth**: `client.New(ctx, cfg)` (fallible) picks it: `Authorization: Bearer <token>` for OAuth (`cfg.AccessToken`), `Authorization: token key:secret` for API key, `Cookie: sid=...` for username/password (live login inside `New`, relogin on 401/403 for long-lived clients).
- **Response envelope**: v14+ wraps results in `"data"`, older versions use `"message"`. Both are handled for list endpoints.
- **Error responses**: Frappe returns nested JSON with Python tracebacks. `frappeErrorResponse.userMessage()` extracts the human-readable message from `_server_messages` or `exception`.
- **Whitelisted methods**: Frappe also exposes `api/method/<dotted.path>` for server-side functions. These return results in `"message"`.

## Output Formatting

The `output` package provides three rendering functions (`PrintJSON` returns an error — return it from `RunE`). Choose based on what you're displaying:

| Function                     | Use for                | Output                                   |
| ---------------------------- | ---------------------- | ---------------------------------------- |
| `PrintTable(rows, fields)`   | Multi-row lists        | Styled table with alternating row colors |
| `PrintDocTable(doc, fields)` | Single document        | Two-column FIELD \| VALUE table          |
| `PrintJSON(data)`            | Any data when `--json` | Pretty-printed JSON to stdout            |

Helper functions for stderr messages:

- `output.PrintError("message")` — red bold with cross mark
- `output.PrintSuccess("message")` — green with check mark

The color palette uses lipgloss v2 ANSI colors: purple (99), gray (245), lightGray (241), green (42), red (196), yellow (220), dim (238).

## Config Loading

**For commands that call the API**, use `callSite(cmd, title, fn)` (or `newClient(ctx)` when you need the client for several calls). Both go through `loadSite` in `site_client.go`, which calls `config.Load(siteName, configPath)` and, for an OAuth site with an expired token, refreshes it under the config lock (`refreshOAuth`; re-checks expiry after taking the lock; failures warn on stderr and the command proceeds). There is no refresh in `PersistentPreRunE`.

```go
c, err := newClient(cmd.Context())
if err != nil {
    return err
}
```

**Reading config** outside API calls: `config.Read(path)` returns a `*Config`; `resolveCfgPath()` (config_cmd.go) resolves `--config` or the default path.

**Writing config**: ALL writes go through `config.Edit(path, func(f *config.File) error {...})` (or `config.Overwrite` for a fresh file). They take `config.yaml.lock`, re-read the file, apply the callback to a `config.File` (a `yaml.Node` tree: keeps comments and key order) and write atomically at 0600, following symlinks. Helpers on `File`: `Get`, `Set`, `HasSite`, `SiteNames`, `Site`, `PutSite`, `RemoveSite`, `SetSiteTokens`. Return `config.ErrUnchanged` to skip the write. Any network call made while holding the lock must stay within `config.MaxLockHold` (30 s). Never write config.yaml with `os.WriteFile` or by marshalling the struct. viper was removed (it lowercased keys and split on dots) — do not reintroduce it.

`Config` and `SiteConfig` carry **`yaml` tags only**. Site names keep their case and may contain dots; `--site`/`default_site` match exactly first, then a unique case-insensitive match. `SiteConfig.Name` is runtime-only (`yaml:"-"`), set by `config.Load` to the exact YAML key.

**Precedence** (highest wins): `--site` flag > env vars > config file > defaults.

**Env vars**: `FFC_API_KEY` + `FFC_API_SECRET` count only as a pair and then replace every stored credential of the selected site. `FFC_URL` applies only together with that pair (alone and different from the site URL is an error, so stored credentials never go to another host). With no config file at the default path, the three vars alone define the site. `FFC_NO_UPDATE_CHECK` disables the update check.

## Interactive Forms (huh v1.0.0)

For commands that need user input (like `init`), use huh forms:

```go
form := huh.NewForm(
    huh.NewGroup(
        huh.NewInput().Title("Field").Validate(func(s string) error { ... }).Value(&variable),
    ),
)
if err := form.Run(); err != nil { return err }
```

For multi-field wizards use `runForm(groups...)` (auth_wizard.go): it attaches `escQuitKeyMap()` and maps Escape/Ctrl+C to `errAborted` (a non-zero exit). For simple confirmations use `confirm(prompt)` (helpers.go; a decline is `errAborted`). The raw pattern, if you need it:

```go
var confirmed bool
err := huh.NewForm(
    huh.NewGroup(
        huh.NewConfirm().Title("Are you sure?").Value(&confirmed),
    ),
).WithKeyMap(escQuitKeyMap()).Run()
if err != nil || !confirmed {
    // user pressed Escape, ctrl+c, or chose No — return errAborted so the exit code is non-zero
}
```

### Escape key in huh v1.0.0

**Important**: In huh v1.0.0, Escape is not mapped to Quit by default — only `ctrl+c` is. Calling `.Run()` directly on a standalone field wraps it in an implicit form you can't customize.

Whenever you need Escape to abort a form (especially in looped menus), create the form explicitly and attach a custom keymap:

```go
import "github.com/charmbracelet/bubbles/key"

func escQuitKeyMap() *huh.KeyMap {
    km := huh.NewDefaultKeyMap()
    km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
    return km
}

// Use WithKeyMap on every form where Escape should abort:
err = huh.NewForm(
    huh.NewGroup(
        huh.NewSelect[string]().Title("…").Options(opts...).Value(&chosen),
    ),
).WithKeyMap(escQuitKeyMap()).Run()

if errors.Is(err, huh.ErrUserAborted) {
    // user pressed Escape or ctrl+c
}
```

`escQuitKeyMap()` is defined in `config_cmd.go` and is available to all files in the `cmd` package — call it directly from any command file.

## Auth Wizard and OAuth (auth_wizard.go, oauth_flow.go)

`init` and `site add` share one wizard: `chooseAuthMethod` → `collectSite` → `collectAPIKeySite` / `collectPasswordSite` / `collectOAuthSite`. API keys and passwords are verified against the site before saving. Persist with `writeInitConfig` (`config.Overwrite`) or `addSiteToConfig` (`config.Edit`) — never write the file another way.

OAuth PKCE: the `callbackServer` binds `127.0.0.1` (redirect URI `http://127.0.0.1:<port>/callback`, not `localhost`) before the form opens, accepts exactly one result, answers duplicates with 409 and is always closed on abort. `IsOAuth()` is true only if `AccessToken != ""`; `IsSessionAuth()` needs both `Username` and `Password`. `client.LoginPassword` does not support Frappe 2FA. Do not persist a `sid` to config.

## MCP Server Pattern

The `mcp` command is structurally different from all other ffc commands — it's a long-running server, not a one-shot CLI action. Key constraints:

### Tool handlers (`mcp_tools.go`)

- **Never write to stdout** from a tool handler. Stdout is the MCP JSON-RPC channel.
- **Never call `output.Print*`** — those write to stdout/stderr for human consumption.
- Return results via `mcp.NewToolResultText(jsonString)` and errors via `mcp.NewToolResultError(msg)` with a **nil** Go error.
- Use `marshalResult(data)` (defined in `mcp_args.go`) for any structured response — it JSON-marshals compactly, refuses results over 512 KiB with a hint to narrow the request, and wraps in `NewToolResultText`. A toolCall may return `structuredOut{Text, Structured}` for a tool that declares an output schema (`WithRawOutputSchema`): the text stays `Text`, `Structured` becomes structuredContent.
- A row list that can exceed the cap is cut instead of refused: `fitListRows` (list_docs: `{data, truncated, next_start, hint}`) and `fitReportRows` (run_report keeps its shape) binary-search the largest prefix that fits.
- Handlers are built with `toolHandler(getClient, parse)`: `parse` validates arguments first (a bad call never costs a login or request) and returns a `toolCall`; every error becomes a tool error with a nil Go error. A non-nil Go error from a handler is a protocol-level crash — reserve it for truly unexpected failures.
- Never print secrets to stderr (it is `mcp.log` in detached mode).
- Limits: results over 512 KiB are refused (list_docs and run_report rows are cut instead); `run_report` defaults to 500 rows; bulk tools take at most 200 items (`maxMCPBulkItems`).
- Bulk tools call `notifyProgress(ctx, done, total)` after each item; it sends `notifications/progress` only when the request had a progress token (`withProgress` in `env.run`). It is best effort: mcp-go queues it without blocking, drops it when the session channel is full, and may write it after the response.

### Adding a new MCP tool

```go
func registerMyTool(s *server.MCPServer, env *mcpEnv) {
    tool := mcp.NewTool("my_tool",
        mcp.WithDescription("What it does, what it returns, when to use it."),
        mcp.WithReadOnlyHintAnnotation(true),
        mcp.WithString("doctype", mcp.Required(), mcp.Description("The DocType")),
        jsonParam("filters", `Filter as JSON object or array.`), // JSON-valued: no fixed schema type
        mcp.WithNumber("limit", mcp.Description("Max results. Default: 20")),
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

Then call `registerMyTool(s, env)` inside `registerAllTools()` in `mcp_tools.go`, add the tool to `toolActions` in `mcp_policy.go` (`actRead`, `actWrite` or `actMethod`; it must match the read-only annotation) and to `toolSurface` next to it (tool set `core` or `lifecycle`, a title, and `big` when its result can approach 512 KiB). `TestMCPPolicyCoversEveryTool` and `TestMCPToolSurface` fail until both are filled in. If the instructions (`mcpInstructions`) or a prompt should mention the tool, add it there, guarded by whether the tool is registered. `registerTools` drops what the site's policy does not allow (`read_only`, `allow_tools`), and `toolHandler` checks the policy on every call before any request. If the tool names DocTypes or documents in arguments other than `doctype`/`name`, teach `scopeOf` about them.

**Argument extraction** (`mcp_args.go`): never use `req.GetString` for JSON-valued params — it returns `""` for a native object and silently drops filters. Use `jsonArg` / `rawJSONArg` / `objectArg` (accept native JSON or a JSON-encoded string), `stringsArg` (array or CSV), and `intArg` (validated integers). `req.RequireString("key")` and `req.GetString("key", "default")` are fine for plain strings.

### Resources, prompts, instructions (`mcp_surface.go`)

`registerTools` ends with `registerSurface`, so all three follow the tools that survived the policy and `--toolsets`:
- **Resources** (`ffc://sites`, `ffc://{site}/schema/{doctype}`, `ffc://{site}/doc/{doctype}/{name}`) are served by `resourceReader`, which builds a `CallToolRequest` and calls the registered tool's handler with `withVia(ctx, "resource")`. Never give a resource its own path to the site: going through the tool handler is what applies `siteFor`, `scopeOf`, the policy, the audit line (`"via":"resource"`) and the size cap. A template is registered only when its tool is. URI segments are percent-decoded by `splitFFCURI`; the URI template already rejects extra segments and bad escapes ("resource not found"). mcp-go v1.1.1 answers every resource handler error with -32603; keep the tool's message and wrap a class with `classedError` (not found, invalid params) for hooks.
- **Prompts** (`mcpPrompts`) are static guidance and make no site calls; each is offered only when the tools in its `needs` are registered, and a step naming any other tool goes through `when(has(tool), …)`. Put argument values in with `promptJSON` and give every argument a `maxLen` (over it is refused, never cut). With several sites they take a required `site`, checked with `siteFor`.
- **Instructions** (`mcpInstructions`) are set after registration with `server.WithInstructions(text)(s)`, and mention only registered tools (gate every line that names one on `has`) and resources (the list `registerResources` returns), the served sites and which are read-only. Keep them 15-25 lines (`TestMCPInstructions`).

### Daemon/detach pattern (`mcp_daemon.go`)

- State file: `~/.config/ffc/mcp.json` (0600) — JSON with `pid`, `port`, `site`, `started_at`, `log_path`, `token`, `instance`; guarded by `~/.config/ffc/mcp.lock` (`acquireMCPLock`)
- Log file: `~/.config/ffc/mcp.log` — stderr of detached child process, truncated per start
- `startDetached(ctx, port)` re-execs the binary with `daemonArgs`: `mcp --port PORT [--site X] [--sites=…] [--config X] [--read-only] [--toolsets=…]` plus the policy and debug flags (no `--detach`), passes the bearer token and instance id via env (`FFC_MCP_TOKEN`, `FFC_MCP_INSTANCE`, never argv), calls `setSysProcAttr` (Setsid on Unix), writes the state file, and waits for a health check whose instance id matches
- The HTTP server binds `127.0.0.1` only, requires `Authorization: Bearer <token>` (`mcpAuthMiddleware`) and rejects non-local Origins; `ffc mcp status` shows the token
- `ffc mcp stop` verifies the PID via health check before terminating (`terminateProcess`); `--force` stops a PID that is alive but not health-confirmed
- Long-lived clients: `newMCPEnv` caches one client while the site credentials are unchanged; every call reads the site again (`loadSiteConfig`, so policy and credential edits apply at once) and `refreshSite` refreshes an expired OAuth token
- Platform-specific process handling (`setSysProcAttr`, `terminateProcess`, `isProcessRunning`) is isolated in `mcp_detach_unix.go` (`!windows`) and `mcp_detach_windows.go`. **Keep `syscall` / `x/sys/windows` fields out of untagged files** — they won't compile cross-platform.

### Update check skip

`update_check.go`'s `PersistentPreRunE` skips the update check for `update`, `mcp` (and its subcommands), `completion`, `__complete` and `help`, and when `FFC_NO_UPDATE_CHECK` is set. The stderr update notice would corrupt the MCP JSON-RPC stream in stdio mode. Do not remove `mcp` from that condition.

## Error Handling

- Wrap errors with context: `fmt.Errorf("loading config: %w", err)` — preserves the error chain.
- Never log and return. Return the error; let the caller (cobra's `RunE`) decide.
- HTTP errors: pass a `hints` map (`readHints` / `docHints`) to `c.do()`; it supplies specific messages for 401, 403, 404 and falls back to the Frappe exception/message for anything else >= 400.
- Exit codes: a permission check that says no is `deniedError` (5); a failed `doctor` check is `codeError{exitGeneric}` (1). Aborts, declined confirmations and partial bulk failures are non-zero (`errAborted`, `bulkReport.err()`). Only the config TUI's explicit "Cancel" exits 0.

## get-schema Compact Output

`get-schema --json` returns a compact view by default, not the raw Frappe response. The filtering is done in `get_schema.go` via helpers:

- `compactSchema(doc)` — filters the top-level DocType map
- `compactField(f)` — filters a single DocField map
- `filterSchemaKeys(doc, keys)` — keeps only the specified top-level keys (for `--keys` flag)
- `isTruthy(v)` — returns true for non-zero float64, int, or bool true
- `fetchSchema(ctx, c, doctype)` — shared by the CLI and MCP: fetches the DocType, merges Custom Fields, applies Property Setters; problems come back as warnings (`_warnings` in JSON), not failures

## Custom Fields in get-schema

`GetDoc("DocType", doctype)` only returns standard fields baked into the DocType — it does **not** include custom fields added via Customize Form or Property Setter. Custom fields are stored separately in the `Custom Field` DocType (filtered by `dt = doctype`).

`mergeCustomFields(ctx, fc, doctype, doc)` in `get_schema.go` handles this. It:
1. Calls `GetList("Custom Field", {fields: ["*"], filters: {dt: doctype}, order_by: "idx asc"})`
2. Groups results by `insert_after` fieldname
3. Rebuilds `doc["fields"]` by splicing each custom field in after its target; fields whose target doesn't exist are appended at the end

`applyPropertySetters` then applies every Property Setter (DocField and DocType level; values cast by `property_type`; `field_order` reorders fields). Both run inside `fetchSchema`, which the CLI and the MCP `get_schema` tool share — keep that order (Property Setter overrides must run after the full field list is assembled) and don't call them separately.

**Kept DocType keys (always):** `name`, `module`, `autoname`, `naming_rule`, `is_submittable`, `issingle`, `istable`, `is_tree`, `is_virtual`, `read_only`, `custom`
**Kept DocType keys (if truthy):** `allow_rename`, `track_changes`
**Kept DocType keys (if non-empty array):** `actions`, `links`, `states`

**Kept DocField keys (always):** `fieldname`, `label`, `fieldtype`
**Kept DocField keys (if truthy):** `reqd`, `read_only`, `hidden`, `unique`, `is_virtual`, `non_negative`, `allow_on_submit`, `in_list_view`, `in_standard_filter`, `set_only_once`, `translatable`, `ignore_user_permissions`
**Kept DocField keys (if non-empty string):** `options`, `default`, `description`, `fetch_from`, `depends_on`, `mandatory_depends_on`, `read_only_depends_on`
**Kept DocField keys (if > 0):** `length`, `permlevel`

Use `--full` to bypass compaction and return the raw Frappe response. Use `--keys name,fields` to filter which top-level keys appear. Do not duplicate these helpers elsewhere.

The MCP `get_schema` tool defaults to compact but exposes two optional parameters so the LLM can control the output: `full` (bool, returns raw Frappe response) and `keys` (comma-separated string, filters top-level keys). `compactReportResult` in `mcp_tools.go` applies the same noise-stripping principle to `run_report`: keeps `columns`, `result`, `report_summary` (if non-null), `total_rows` and `truncated` (set when rows were cut), strips `execution_time`, `chart`, `add_total_row`, `message`. `run_report` defaults to 500 rows (`limitReportRows`). The CLI `run-report --json` returns the full response; `--limit` applies to table and JSON, `--keys` trims JSON.

## Single DocType Handling

In Frappe, a Single DocType (e.g. `System Settings`, `HR Settings`) has exactly one record whose name equals the DocType name. The Frappe API treats it like any other document — `GET /api/resource/System Settings/System Settings` — but requiring users to repeat the DocType name as `--name` is poor UX.

**Pattern:** For any command that accepts `--doctype` and `--name` to fetch/modify a single document, make `--name` optional and default it to the DocType name:

```go
name := docNameOrSingle(xxName, xxDoctype)
```

Remove `MarkFlagRequired("name")` and update the flag description to note the default behaviour.

Use `docNameOrSingle(name, doctype)` (update_doc.go) rather than re-implementing the fallback. `update-doc` also strips a `name` key from `--data` with a warning.

**Which commands apply this:**
- `get-doc` (`get_doc.go`) ✓
- `update-doc` (`update_doc.go`) ✓
- MCP `get_doc` and `update_doc` tools in `mcp_tools.go` ✓

**`delete-doc` is intentionally excluded** — Frappe does not allow deleting Single DocTypes, so defaulting the name there would only produce a confusing API error with no valid use case.

## Field Parsing

`parseFields()` in `list_docs.go` accepts two formats:

- JSON array: `'["name","email"]'`
- CSV: `name,email`

Reuse this function in new commands that accept field lists (a JSON object is rejected; use the JSON form for expressions containing commas). It's currently not exported — if you need it in another package, consider moving it to a shared location. Related helpers in `helpers.go`: `splitCSV`, `parseObject`, `listLimit` (`--limit 0` = no limit; negatives rejected), `validateFiltersJSON` (`--filters` must be a JSON object or array).

## Build & Test

```bash
make build    # → ./bin/ffc binary with version ldflags
make vet      # → go vet ./...
make fmt      # → gofmt -w .
make test     # → go test -race ./...
make lint     # → gofmt check, go vet, staticcheck (if installed)
make tidy     # → go mod tidy
make install  # → $GOPATH/bin + config setup
```

Version is injected at build time via ldflags into `internal/version` (Version, Commit, Date).

## Self-Update Mechanism

`update.go` implements `ffc update` — it fetches the latest GitHub release, extracts the binary for the current OS/arch, and replaces the running binary in place.

Key details:
- **Asset naming** matches GoReleaser's template: `ffc_<version-without-v>_<goos>_<goarch>.tar.gz` (or `.zip` on Windows). Version comes from `release.TagName` with the `v` stripped.
- **Binary swap (Unix)**: `os.Rename(tmp, current)` — atomic on the same filesystem.
- **Binary swap (Windows)**: rename current → `ffc.exe.old` (allowed for running exe), rename new → `ffc.exe`. The `.old` file is cleaned up on the next update run.
- **Download safety**: `checksums.txt.sig` must verify against a key in `internal/relsig/keys.go`, and the size-limited download must match its SHA256 in `checksums.txt`, before the swap.
- **Permission error** on `os.CreateTemp` is caught and surfaces a `try running with sudo` message.
- **Version comparison** in `newerThan()` strips any `v` prefix and pre-release suffix before comparing major.minor.patch integers — handles GoReleaser injecting without `v` and GitHub tags using `v`.

`update_check.go` runs a background update check on every command except those in the skip list above:
- Reads `~/.config/ffc/.update_check.json` (instant, local file) and prints a notice if a newer version is cached.
- If the cache is missing or older than 24 hours, starts a goroutine to fetch the latest release tag and write the state file. `checked_at` is recorded before the fetch, so a failing network costs at most one attempt per day.
- `Execute()` in `root.go` waits up to 2 seconds for that goroutine before the process exits, so the file is written reliably.
- **`PersistentPreRunE` on `rootCmd` is owned by `update_check.go`.** Do not set it anywhere else — it would silently overwrite the hook. Add new pre-run logic inside the existing function in `update_check.go`.

## Checklist for New Features

1. Create `internal/cmd/<name>.go` with the cobra command pattern (`Args: cobra.NoArgs`, `callSite` for site access)
2. If it needs a new API call, add a method to `FrappeClient` in `client.go` that goes through `c.do(ctx, ...)`
3. If it needs new output formatting, extend `output.go` (or reuse existing functions)
4. Register the command via `rootCmd.AddCommand()` (or `parentCmd.AddCommand()` for subcommands) in `init()`
5. If the command needs pre-run logic, add it inside `rootCmd.PersistentPreRunE` in `update_check.go` — do not reassign it. (Site-related setup such as OAuth refresh belongs in `loadSite`, not there.)
   If it should also be an MCP tool, add it in `mcp_tools.go` (write tools after the `mcpReadOnly` check)
6. Run `make fmt && make lint && make test && make build` to verify
7. Update README.md, CLAUDE.md, and the skill files in `skills/` if adding user-facing commands
