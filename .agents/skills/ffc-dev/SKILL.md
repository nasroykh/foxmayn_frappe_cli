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

| Component | Library | Import |
|-----------|---------|--------|
| CLI framework | cobra | `github.com/spf13/cobra` |
| Config | viper | `github.com/spf13/viper` |
| HTTP client | resty | `github.com/go-resty/resty/v2` |
| Tables & styling | lipgloss v2 | `charm.land/lipgloss/v2` + `charm.land/lipgloss/v2/table` |
| Forms & prompts | huh | `github.com/charmbracelet/huh` |
| Spinner | huh/spinner | `github.com/charmbracelet/huh/spinner` |

## Project Layout

```
cmd/ffc/main.go              → calls cmd.Execute()
internal/cmd/root.go         → root cobra command, global flags
internal/cmd/init.go         → interactive setup wizard (huh form)
internal/cmd/get_doc.go      → get-doc subcommand
internal/cmd/list_docs.go    → list-docs subcommand + parseFields()
internal/client/client.go    → FrappeClient (resty), GetDoc, GetList
internal/config/config.go    → viper config loading, env var fallback
internal/output/output.go    → PrintTable, PrintDocTable, PrintJSON, PrintError, PrintSuccess
internal/version/version.go  → build-time ldflags (Version, Commit, Date)
Makefile                     → build, install, tidy, vet, fmt, clean
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

    "foxmayn_frappe_cli/internal/client"
    "foxmayn_frappe_cli/internal/config"
    "foxmayn_frappe_cli/internal/output"

    "github.com/charmbracelet/huh/spinner"
    "github.com/spf13/cobra"
)

// <command>-specific flags — use a short prefix to avoid collisions.
// Existing prefixes: gd (get-doc), ld (list-docs).
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
- **Flag variable prefixes**: Each command uses a 2-letter prefix for its flag vars (e.g., `gd` for get-doc, `ld` for list-docs) to avoid collisions within the `cmd` package.
- **RunE, not Run**: Return errors — cobra handles printing them to stderr and setting exit code 1.
- **Spinner pattern**: Wrap API calls in `spinner.New().Title("...").Action(func() { ... }).Run()`. The error is captured in a closure variable (`apiErr`), checked after the spinner finishes.
- **Output routing**: Data to stdout (`output.PrintTable`, `output.PrintJSON`). Diagnostics/errors to stderr (`output.PrintError`, `fmt.Fprintln(os.Stderr, ...)`).
- **Register in init()**: Call `rootCmd.AddCommand(yourCmd)` inside the file's `init()` function — cobra picks it up automatically.

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

| Function | Use for | Output |
|----------|---------|--------|
| `PrintTable(rows, fields)` | Multi-row lists | Styled table with alternating row colors |
| `PrintDocTable(doc, fields)` | Single document | Two-column FIELD \| VALUE table |
| `PrintJSON(data)` | Any data when `--json` | Pretty-printed JSON to stdout |

Helper functions for stderr messages:
- `output.PrintError("message")` — red bold with cross mark
- `output.PrintSuccess("message")` — green with check mark

The color palette uses lipgloss v2 ANSI colors: purple (99), gray (245), lightGray (241), green (42), red (196), yellow (220), dim (238).

## Config Loading

Call `config.Load(siteName, configPath)` at the start of any command that hits the API. Both params come from global flags.

**Precedence** (highest wins): `--site` flag > `FFC_*` env vars > config file `default_site`.

When no config file exists, the client falls back to `FFC_URL`, `FFC_API_KEY`, `FFC_API_SECRET` env vars — useful for CI.

## Error Handling

- Wrap errors with context: `fmt.Errorf("loading config: %w", err)` — preserves the error chain.
- Never log and return. Return the error; let the caller (cobra's `RunE`) decide.
- HTTP errors: Use the status code switch pattern from existing client methods. Specific messages for 401, 403, 404; `parseFrappeError` for anything else >= 400.

## Build & Test

```bash
make build    # → ./ffc binary with version ldflags
make vet      # → go vet ./...
make fmt      # → gofmt -w .
make tidy     # → go mod tidy
make install  # → $GOPATH/bin + config setup
```

Version is injected at build time via ldflags into `internal/version` (Version, Commit, Date).

## Interactive Forms (huh)

For commands that need user input (like `init`), use huh forms:

```go
form := huh.NewForm(
    huh.NewGroup(
        huh.NewInput().Title("Field").Validate(func(s string) error { ... }).Value(&variable),
    ),
)
if err := form.Run(); err != nil { return err }
```

For confirmations: `huh.NewConfirm().Title("...").Value(&boolVar).Run()`

## Field Parsing

`parseFields()` in `list_docs.go` accepts two formats:
- JSON array: `'["name","email"]'`
- CSV: `name,email`

Reuse this function in new commands that accept field lists. It's currently not exported — if you need it in another package, consider moving it to a shared location.

## Checklist for New Features

1. Create `internal/cmd/<name>.go` with the cobra command pattern
2. If it needs a new API call, add a method to `FrappeClient` in `client.go`
3. If it needs new output formatting, extend `output.go` (or reuse existing functions)
4. Register the command via `rootCmd.AddCommand()` in `init()`
5. Run `make vet && make fmt && make build` to verify
6. Update the README usage section if adding user-facing commands
