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
| Config           | viper          | `github.com/spf13/viper`                                  |
| HTTP client      | resty          | `github.com/go-resty/resty/v2`                            |
| Tables & styling | lipgloss v2    | `charm.land/lipgloss/v2` + `charm.land/lipgloss/v2/table` |
| Forms & prompts  | huh v1.0.0     | `github.com/charmbracelet/huh`                            |
| Spinner          | huh/spinner    | `github.com/charmbracelet/huh/spinner`                    |
| MCP server       | mcp-go v0.46.0 | `github.com/mark3labs/mcp-go/mcp` + `.../server`          |

## Project Layout

```
cmd/ffc/main.go               → calls cmd.Execute()
internal/cmd/root.go          → root cobra command, global flags (--site, --config, --json)
internal/cmd/init.go          → init subcommand (huh form wizard) + writeConfig() helper
internal/cmd/config_cmd.go    → config subcommand: TUI (no args), config get, config set
internal/cmd/ping.go          → ping subcommand
internal/cmd/get_doc.go       → get-doc subcommand
internal/cmd/list_docs.go     → list-docs subcommand + parseFields()
internal/cmd/create_doc.go    → create-doc subcommand
internal/cmd/update_doc.go    → update-doc subcommand
internal/cmd/delete_doc.go    → delete-doc subcommand
internal/cmd/count_docs.go    → count-docs subcommand
internal/cmd/get_schema.go    → get-schema subcommand
internal/cmd/list_doctypes.go → list-doctypes subcommand
internal/cmd/list_reports.go  → list-reports subcommand
internal/cmd/run_report.go    → run-report subcommand
internal/cmd/call_method.go   → call-method subcommand
internal/cmd/update.go            → update subcommand: GitHub release fetch, archive extraction, binary swap
internal/cmd/update_check.go      → background update check; owns rootCmd.PersistentPreRunE + state file
internal/cmd/mcp.go               → mcp subcommand: stdio/HTTP/detach routing, --detach and --port flags
internal/cmd/mcp_tools.go         → 12 MCP tool definitions + handlers; marshalResult helper; registerTools()
internal/cmd/mcp_daemon.go        → startDetached(), runHTTPServer(), mcpStatusCmd, mcpStopCmd, state file I/O
internal/cmd/mcp_detach_unix.go   → setSysProcAttr (Setsid=true) — build tag: !windows
internal/cmd/mcp_detach_windows.go → setSysProcAttr no-op — build tag: windows
internal/client/client.go         → FrappeClient (resty), GetDoc, GetList, …
internal/config/config.go     → viper config loading, number/date formatting, env var fallback
internal/output/output.go     → PrintTable, PrintDocTable, PrintJSON, PrintError, PrintSuccess
internal/version/version.go   → build-time ldflags (Version, Commit, Date)
Makefile                      → build, install, tidy, vet, fmt, clean
```

## Adding a New Command

This is the most common task. Follow this exact pattern — it matches every existing command in the codebase.

### 1. Create the file

Create `internal/cmd/<command_name>.go` in package `cmd`. Use snake_case for filenames, kebab-case for the command's `Use` field.

### 2. Follow this structure

```go
package cmd

import (
    "fmt"

    "github.com/nasroykh/foxmayn_frappe_cli/internal/client"
    "github.com/nasroykh/foxmayn_frappe_cli/internal/config"
    "github.com/nasroykh/foxmayn_frappe_cli/internal/output"

    "github.com/charmbracelet/huh/spinner"
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
    RunE: func(cmd *cobra.Command, args []string) error {
        // 1. Load config (uses global siteName, configPath)
        cfg, err := config.Load(siteName, configPath)
        if err != nil {
            return fmt.Errorf("config: %w", err)
        }

        // 2. Parse/validate flags
        // ...

        // 3. API call wrapped in spinner
        var result map[string]interface{}
        var apiErr error
        c := client.New(cfg)
        _ = spinner.New().
            Title("Doing something…").
            Action(func() {
                result, apiErr = c.GetDoc(xxDoctype, xxName)
            }).
            Run()
        if apiErr != nil {
            return apiErr
        }

        // 4. Output (respect --json global flag)
        if jsonOutput {
            output.PrintJSON(result)
        } else {
            output.PrintDocTable(result, nil)
        }

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

- **Global flags** `siteName`, `configPath`, `jsonOutput` are package-level vars in `root.go` — use them directly, don't redeclare.
- **Flag variable prefixes**: Each command uses a unique 2-letter prefix for its flag vars to avoid collisions within the `cmd` package. Check existing files before choosing one.
- **RunE, not Run**: Return errors — cobra handles printing them to stderr and setting exit code 1.
- **Spinner pattern**: Wrap API calls in `spinner.New().Title("...").Action(func() { ... }).Run()`. The error is captured in a closure variable (`apiErr`), checked after the spinner finishes.
- **Output routing**: Data to stdout (`output.PrintTable`, `output.PrintJSON`). Diagnostics/errors to stderr (`output.PrintError`, `fmt.Fprintln(os.Stderr, ...)`).
- **Register in init()**: Call `rootCmd.AddCommand(yourCmd)` inside the file's `init()` function — cobra picks it up automatically.

## Adding Subcommands to an Existing Command

For nested commands (like `config get` / `config set` under `config`), register them against the parent command in `init()`:

```go
parentCmd.AddCommand(childCmd)  // not rootCmd.AddCommand
```

The parent command can still have its own `RunE` (runs when called with no subcommand) alongside subcommands.

## Extending the API Client

The client lives in `internal/client/client.go`. It wraps resty with Frappe-specific auth and error handling.

### Adding a new API method

```go
// Example: CreateDoc posts a new document.
func (c *FrappeClient) CreateDoc(doctype string, data map[string]interface{}) (map[string]interface{}, error) {
    endpoint := fmt.Sprintf("/api/resource/%s", doctype)

    resp, err := c.r.R().
        SetBody(data).
        Post(endpoint)
    if err != nil {
        return nil, fmt.Errorf("HTTP request failed: %w", err)
    }

    // Handle HTTP errors — reuse the same switch pattern
    switch resp.StatusCode() {
    case http.StatusUnauthorized:
        return nil, fmt.Errorf("authentication failed (401): check api_key and api_secret in your config")
    case http.StatusForbidden:
        return nil, fmt.Errorf("permission denied (403): your user may not have write access to %s", doctype)
    case http.StatusNotFound:
        return nil, fmt.Errorf("doctype %q not found on this site (404)", doctype)
    }
    if resp.StatusCode() >= 400 {
        return nil, parseFrappeError(resp.StatusCode(), resp.Body())
    }

    var result struct {
        Data map[string]interface{} `json:"data"`
    }
    if err := json.Unmarshal(resp.Body(), &result); err != nil {
        return nil, fmt.Errorf("parsing response: %w", err)
    }

    return result.Data, nil
}
```

### Frappe API essentials

- **Base URL pattern**: `/api/resource/{DocType}` for lists, `/api/resource/{DocType}/{name}` for single docs.
- **Auth header**: `Authorization: token api_key:api_secret` (set automatically by `client.New`).
- **Response envelope**: v14+ wraps results in `"data"`, older versions use `"message"`. Both are handled for list endpoints via `listResponse`.
- **Error responses**: Frappe returns nested JSON with Python tracebacks. `parseFrappeError()` extracts the human-readable message from `_server_messages` or `exception`.
- **Whitelisted methods**: Frappe also exposes `api/method/<dotted.path>` for server-side functions. These return results in `"message"`.

## Output Formatting

The `output` package provides three rendering functions. Choose based on what you're displaying:

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

**For commands that call the API**, use `config.Load(siteName, configPath)` — returns a `*SiteConfig` for the selected site:

```go
cfg, err := config.Load(siteName, configPath)
if err != nil {
    return fmt.Errorf("config: %w", err)
}
c := client.New(cfg)
```

**For commands that read/write config settings** (not API calls), load the raw YAML directly with `go.yaml.in/yaml/v3`:

```go
raw, err := os.ReadFile(cfgPath)
var vConfig config.Config
_ = yaml.Unmarshal(raw, &vConfig)
```

This works because `Config` and `SiteConfig` carry both `mapstructure` tags (for viper) and `yaml` tags (for direct yaml unmarshal). **Always add both tags when extending these structs** — missing `yaml` tags means fields unmarshal as zero values.

**Precedence** (highest wins): `--site` flag > `FFC_*` env vars > config file `default_site`.

When no config file exists, the client falls back to `FFC_URL`, `FFC_API_KEY`, `FFC_API_SECRET` env vars — useful for CI.

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

For confirmations, always wrap in `huh.NewForm` with `escQuitKeyMap()` so Escape works:

```go
var confirmed bool
err := huh.NewForm(
    huh.NewGroup(
        huh.NewConfirm().Title("Are you sure?").Value(&confirmed),
    ),
).WithKeyMap(escQuitKeyMap()).Run()
if err != nil || !confirmed {
    // user pressed Escape, ctrl+c, or chose No
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

## Config File Helpers (config_cmd.go)

`config_cmd.go` has two helpers for reading and writing the config YAML node without losing comments or key order:

- `saveConfig(path, originalBytes, *yaml.Node) error` — marshals the node back to disk, preserving any leading comment header
- `updateYAMLValue(root *yaml.Node, key, value string)` — updates a scalar value in a YAML mapping node (appends if key doesn't exist)

**Note**: `init.go` has its own `writeConfig(path, siteName, url, apiKey, apiSecret string) error` which generates a fresh config from scratch. The names are intentionally different to avoid a package-level collision — do not rename either.

## MCP Server Pattern

The `mcp` command is structurally different from all other ffc commands — it's a long-running server, not a one-shot CLI action. Key constraints:

### Tool handlers (`mcp_tools.go`)

- **Never write to stdout** from a tool handler. Stdout is the MCP JSON-RPC channel.
- **Never call `output.Print*`** — those write to stdout/stderr for human consumption.
- Return results via `mcp.NewToolResultText(jsonString)` and errors via `mcp.NewToolResultError(msg)` with a **nil** Go error.
- Use `marshalResult(data)` (defined in `mcp_tools.go`) for any structured response — it JSON-marshals and wraps in `NewToolResultText`.
- A non-nil Go error from a handler is a protocol-level crash. Reserve that for truly unexpected failures. Frappe API errors go through `NewToolResultError`.

### Adding a new MCP tool

```go
func registerMyTool(s *server.MCPServer, fc *client.FrappeClient) {
    tool := mcp.NewTool("my_tool",
        mcp.WithDescription("What it does, what it returns, when to use it."),
        mcp.WithString("doctype", mcp.Required(), mcp.Description("The DocType")),
        mcp.WithNumber("limit", mcp.Description("Max results. Default: 20")),
    )
    s.AddTool(tool, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        doctype, err := req.RequireString("doctype")
        if err != nil {
            return mcp.NewToolResultError(err.Error()), nil
        }
        limit := int(req.GetFloat("limit", 20))
        // ... call fc method ...
        result, apiErr := fc.GetList(doctype, client.ListOptions{Limit: limit})
        if apiErr != nil {
            return mcp.NewToolResultError(apiErr.Error()), nil
        }
        return marshalResult(result)
    })
}
```

Then call `registerMyTool(s, fc)` inside `registerTools()` in `mcp_tools.go`.

**Argument extraction methods** (from `mcp.CallToolRequest`):
- `req.RequireString("key")` → `(string, error)` — errors if missing or wrong type
- `req.GetString("key", "default")` → `string`
- `req.GetFloat("key", 0)` → `float64` (use for numbers — JSON numbers decode as float64)
- `req.GetInt("key", 0)` → `int`

### Daemon/detach pattern (`mcp_daemon.go`)

- State file: `~/.config/ffc/mcp.json` — JSON with `pid`, `port`, `site`, `started_at`, `log_path`
- Log file: `~/.config/ffc/mcp.log` — stderr of detached child process
- `startDetached(port)` re-execs `os.Args[0]` with `mcp --port PORT [--site X] [--config X]` (no `--detach`), sets `Setsid: true` via `setSysProcAttr`, writes state file, releases child
- `isProcessRunning(pid)` uses `syscall.Signal(0)` to check liveness without sending a real signal
- Platform-specific daemonization is isolated in `mcp_detach_unix.go` (`!windows`) and `mcp_detach_windows.go`. **Do not use `syscall.SysProcAttr` fields in non-tagged files** — they won't compile cross-platform.

### Update check skip

`update_check.go`'s `PersistentPreRunE` skips the update check for both `"update"` and `"mcp"`. The stderr update notice would corrupt the MCP JSON-RPC stream. Do not remove `mcp` from that condition.

## Error Handling

- Wrap errors with context: `fmt.Errorf("loading config: %w", err)` — preserves the error chain.
- Never log and return. Return the error; let the caller (cobra's `RunE`) decide.
- HTTP errors: Use the status code switch pattern from existing client methods. Specific messages for 401, 403, 404; `parseFrappeError` for anything else >= 400.

## get-schema Compact Output

`get-schema --json` returns a compact view by default, not the raw Frappe response. The filtering is done in `get_schema.go` via four helpers:

- `compactSchema(doc)` — filters the top-level DocType map
- `compactField(f)` — filters a single DocField map
- `filterSchemaKeys(doc, keys)` — keeps only the specified top-level keys (for `--keys` flag)
- `isTruthy(v)` — returns true for non-zero float64, int, or bool true

## Custom Fields in get-schema

`GetDoc("DocType", doctype)` only returns standard fields baked into the DocType — it does **not** include custom fields added via Customize Form or Property Setter. Custom fields are stored separately in the `Custom Field` DocType (filtered by `dt = doctype`).

`mergeCustomFields(fc, doctype, doc)` in `get_schema.go` handles this. It:
1. Calls `GetList("Custom Field", {fields: ["*"], filters: {dt: doctype}, order_by: "idx asc"})`
2. Groups results by `insert_after` fieldname
3. Rebuilds `doc["fields"]` by splicing each custom field in after its target; fields whose target doesn't exist are appended at the end

It is called **before** `applyPropertySetterOverrides` in both `get_schema.go` (CLI) and `mcp_tools.go` (MCP `get_schema` tool). Do not remove or reorder these calls — Property Setter overrides must run after the full field list is assembled.

**Kept DocType keys (always):** `name`, `module`, `autoname`, `naming_rule`, `is_submittable`, `issingle`, `istable`, `is_tree`, `is_virtual`, `read_only`, `custom`
**Kept DocType keys (if truthy):** `allow_rename`, `track_changes`
**Kept DocType keys (if non-empty array):** `actions`, `links`, `states`

**Kept DocField keys (always):** `fieldname`, `label`, `fieldtype`
**Kept DocField keys (if truthy):** `reqd`, `read_only`, `hidden`, `unique`, `is_virtual`, `non_negative`, `allow_on_submit`, `in_list_view`, `in_standard_filter`, `set_only_once`, `translatable`, `ignore_user_permissions`
**Kept DocField keys (if non-empty string):** `options`, `default`, `description`, `fetch_from`, `depends_on`, `mandatory_depends_on`, `read_only_depends_on`
**Kept DocField keys (if > 0):** `length`, `permlevel`

Use `--full` to bypass compaction and return the raw Frappe response. Use `--keys name,fields` to filter which top-level keys appear. Do not duplicate these helpers elsewhere.

The MCP `get_schema` tool defaults to compact but exposes two optional parameters so the LLM can control the output: `full` (bool, returns raw Frappe response) and `keys` (comma-separated string, filters top-level keys). `compactReportResult` in `mcp_tools.go` applies the same noise-stripping principle to `run_report`: keeps `columns`, `result`, and `report_summary` (if non-null), strips `execution_time`, `chart`, `add_total_row`, `message`.

## Single DocType Handling

In Frappe, a Single DocType (e.g. `System Settings`, `HR Settings`) has exactly one record whose name equals the DocType name. The Frappe API treats it like any other document — `GET /api/resource/System Settings/System Settings` — but requiring users to repeat the DocType name as `--name` is poor UX.

**Pattern:** For any command that accepts `--doctype` and `--name` to fetch/modify a single document, make `--name` optional and default it to the DocType name:

```go
name := xxName
if name == "" {
    name = xxDoctype
}
```

Remove `MarkFlagRequired("name")` and update the flag description to note the default behaviour.

**Which commands apply this:**
- `get-doc` (`get_doc.go`) ✓
- `update-doc` (`update_doc.go`) ✓
- MCP `get_doc` and `update_doc` tools in `mcp_tools.go` ✓

**`delete-doc` is intentionally excluded** — Frappe does not allow deleting Single DocTypes, so defaulting the name there would only produce a confusing API error with no valid use case.

## Field Parsing

`parseFields()` in `list_docs.go` accepts two formats:

- JSON array: `'["name","email"]'`
- CSV: `name,email`

Reuse this function in new commands that accept field lists. It's currently not exported — if you need it in another package, consider moving it to a shared location.

## Build & Test

```bash
make build    # → ./bin/ffc binary with version ldflags
make vet      # → go vet ./...
make fmt      # → gofmt -w .
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
- **Permission error** on `os.CreateTemp` is caught and surfaces a `try running with sudo` message.
- **Version comparison** in `newerThan()` strips any `v` prefix and pre-release suffix before comparing major.minor.patch integers — handles GoReleaser injecting without `v` and GitHub tags using `v`.

`update_check.go` runs a background update check on every non-update command:
- Reads `~/.config/ffc/.update_check.json` (instant, local file) and prints a notice if a newer version is cached.
- If the cache is missing or older than 24 hours, starts a goroutine to fetch the latest release tag and write the state file.
- `Execute()` in `root.go` waits up to 2 seconds for that goroutine before the process exits, so the file is written reliably.
- **`PersistentPreRunE` on `rootCmd` is owned by `update_check.go`.** Do not set it anywhere else — it would silently overwrite the hook. Add new pre-run logic inside the existing function in `update_check.go`.

## Checklist for New Features

1. Create `internal/cmd/<name>.go` with the cobra command pattern
2. If it needs a new API call, add a method to `FrappeClient` in `client.go`
3. If it needs new output formatting, extend `output.go` (or reuse existing functions)
4. Register the command via `rootCmd.AddCommand()` (or `parentCmd.AddCommand()` for subcommands) in `init()`
5. If the command needs pre-run logic, add it inside `rootCmd.PersistentPreRunE` in `update_check.go` — do not reassign it
6. Run `make vet && make fmt && make build` to verify
7. Update README.md, CLAUDE.md, and the skill files under `.claude/skills/` if adding user-facing commands
