# CLAUDE.md — Project Context for AI Agents

## Project

**ffc** (Foxmayn Frappe CLI) — A Go CLI for interacting with Frappe ERP sites via the REST API.

## Tech Stack

- **Language:** Go 1.25
- **CLI framework:** [cobra](https://github.com/spf13/cobra)
- **Config:** [viper](https://github.com/spf13/viper) (YAML + env vars)
- **HTTP client:** [resty](https://github.com/go-resty/resty)
- **Table & styling:** [lipgloss v2](https://charm.land/lipgloss/v2) + built-in `table` sub-package
- **Forms & prompts:** [huh](https://github.com/charmbracelet/huh)
- **Spinner:** [huh/spinner](https://github.com/charmbracelet/huh) (standalone, no bubbletea loop needed)
- **MCP server:** [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) v0.46.0 (stdio + StreamableHTTP transports)

## Build & Run

```bash
make build          # Compile binary to ./bin/ffc
make install        # Install to $GOPATH/bin + set up config
make tidy           # go mod tidy
make vet            # go vet ./...
make fmt            # gofmt -w .
make clean          # Remove binary
```

Version info is injected at build time via ldflags (see Makefile).

## Release

Releases are automated via GoReleaser + GitHub Actions (`.github/workflows/release.yml`).

To cut a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions will cross-compile for linux/darwin/windows × amd64/arm64, create a GitHub Release, upload the tarballs, and generate `checksums.txt`.

End users install with:

```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.sh | sh

# Windows (PowerShell or cmd.exe)
powershell -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.ps1 | iex"
```

**Key files:**
- `.goreleaser.yaml` — build matrix, archive naming, checksum config
- `.github/workflows/release.yml` — triggers on `v*` tags, runs GoReleaser
- `install.sh` — Linux/macOS: detects OS/arch, downloads tarball, verifies SHA256, installs to `/usr/local/bin` or `~/.local/bin`
- `install.ps1` — Windows: detects arch, downloads zip, verifies SHA256, installs to `%LOCALAPPDATA%\Programs\ffc`, adds to user PATH

## Architecture

```
cmd/ffc/main.go              → Entry point, calls cmd.Execute()
internal/cmd/root.go         → Root cobra command, global flags (--site, --config, --json)
internal/cmd/init.go         → init subcommand (auth method menu, --oauth/--apikey flags, writeConfig)
internal/cmd/oauth_flow.go   → OAuth PKCE flow (runOAuthInitFlow, callbackServer, PKCE helpers,
                                writeConfigOAuth, saveOAuthTokens, tryRefreshOAuthToken,
                                upsertSiteInConfig, removeSiteFromConfig, setDefaultSite,
                                buildAPIKeySiteYAML, buildOAuthSiteYAML)
internal/cmd/site.go         → site subcommand: list, add (OAuth/API key), remove, use; pickSite helper
internal/cmd/config_cmd.go   → config subcommand: TUI (no args), config get, config set;
                                escQuitKeyMap, saveConfig, updateYAMLValue, preserveHeader, resolveCfgPath
internal/cmd/ping.go         → ping subcommand
internal/cmd/get_doc.go      → get-doc subcommand
internal/cmd/list_docs.go    → list-docs subcommand
internal/cmd/create_doc.go   → create-doc subcommand
internal/cmd/update_doc.go   → update-doc subcommand
internal/cmd/delete_doc.go   → delete-doc subcommand
internal/cmd/count_docs.go   → count-docs subcommand
internal/cmd/get_schema.go   → get-schema subcommand
internal/cmd/list_doctypes.go → list-doctypes subcommand
internal/cmd/list_reports.go → list-reports subcommand
internal/cmd/run_report.go   → run-report subcommand
internal/cmd/call_method.go  → call-method subcommand
internal/cmd/update.go           → update subcommand (self-update from GitHub releases)
internal/cmd/update_check.go     → background update check; owns rootCmd.PersistentPreRunE;
                                    calls tryRefreshOAuthToken() before every non-init/update/mcp command
internal/cmd/mcp.go              → mcp subcommand (stdio/HTTP/detach mode routing, --detach, --port flags)
internal/cmd/mcp_tools.go        → 12 MCP tool definitions + handlers (registerTools, marshalResult)
internal/cmd/mcp_daemon.go       → detach logic (startDetached, runHTTPServer), status/stop subcommands, state file I/O
internal/cmd/mcp_detach_unix.go  → setSysProcAttr with Setsid=true (Linux/macOS build tag: !windows)
internal/cmd/mcp_detach_windows.go → setSysProcAttr no-op (Windows build tag)
internal/client/client.go    → FrappeClient (resty); Bearer auth for OAuth, token auth for API key
internal/client/oauth.go     → ExchangeOAuthCode, RefreshOAuthToken, GetOAuthUser (OAuthTokens struct)
internal/config/config.go    → Config/SiteConfig structs (OAuth fields), viper loading, number/date formatting
internal/output/             → Formatters: lipgloss table and JSON
internal/version/            → Build-time version variables (ldflags)
```

## Conventions

- **Error handling:** Wrap with `fmt.Errorf("context: %w", err)`. Never log and return; return and let caller decide.
- **Stdout vs stderr:** Data goes to stdout, diagnostics/errors go to stderr.
- **Config precedence:** flags > env vars > config file > defaults.
- **Auth:** Bearer token (`Authorization: Bearer <access_token>`) for OAuth sites; Frappe token auth (`Authorization: token key:secret`) for API key sites. `client.New()` picks the right header automatically based on `cfg.AccessToken`.
- **Adding commands:** Create `internal/cmd/<name>.go`, define `*cobra.Command`, register via `rootCmd.AddCommand()` in `init()`.

## Config

Default config path: `~/.config/ffc/config.yaml`

Env var fallback: `FFC_URL`, `FFC_API_KEY`, `FFC_API_SECRET`

## Common Pitfalls

- The `config.yaml` in the project root is gitignored — it's for local dev only. Do not commit credentials.
- Frappe API wraps list results in `"data"` (v14+) or `"message"` (older). The client handles both.
- Frappe error responses contain nested JSON strings with Python tracebacks. The `parseFrappeError` function extracts user-friendly messages from this noise.
- `Config` and `SiteConfig` structs carry **both** `mapstructure` and `yaml` struct tags. `mapstructure` is needed by viper; `yaml` is needed by `go.yaml.in/yaml/v3` for direct unmarshal in `config_cmd.go`. Always add both when extending these structs.
- `config_cmd.go` has a `saveConfig` helper (marshals YAML node → file). `init.go` has its own `writeConfig` (generates fresh YAML from scratch). `oauth_flow.go` has `writeConfigOAuth` (fresh OAuth config). The names are intentionally different to avoid package-level collisions.
- In huh v1.0.0, only `ctrl+c` is bound to Quit by default — Escape does nothing. All `huh.NewForm` calls use `WithKeyMap(escQuitKeyMap())` to add Escape support. `escQuitKeyMap()` is defined in `config_cmd.go` and is available package-wide.
- `update_check.go` sets `rootCmd.PersistentPreRunE` in its `init()`. Do not set `PersistentPreRunE` on `rootCmd` anywhere else — it would silently overwrite the hook. Add new pre-run logic inside the existing hook in `update_check.go`.
- `PersistentPreRunE` also calls `tryRefreshOAuthToken()` (defined in `oauth_flow.go`) before every command except `init`, `update`, and `mcp`. This silently refreshes expired OAuth access tokens and writes the new tokens to disk before the command's own `config.Load()` call. All errors in `tryRefreshOAuthToken` are swallowed — commands handle 401s themselves.
- OAuth PKCE flow: `generateCodeVerifier` (32 random bytes, base64url) + `generateCodeChallenge` (SHA-256 S256). The callback HTTP server (`callbackServer`) binds its port immediately via `net.Listen` before the user even fills in the OAuth form — this prevents port theft between "find free port" and "start listening". `cs.wait()` blocks up to 5 minutes.
- Site management uses yaml.Node tree manipulation (not struct marshal/unmarshal) so leading `#` comments in config.yaml are preserved. `upsertSiteInConfig`, `removeSiteFromConfig`, `setDefaultSite` in `oauth_flow.go` + `preserveHeader` / `updateYAMLValue` in `config_cmd.go` are the relevant helpers. Do not duplicate these.
- `IsOAuth()` on `SiteConfig` returns true only if `AccessToken != ""` — not just `OAuthClientID != ""`. A site with only a client ID but no token would produce unauthenticated requests.
- `SiteConfig.Name` is a runtime-only field (`mapstructure:"-" yaml:"-"`) populated by `config.Load()`. `tryRefreshOAuthToken` uses `cfg.Name` as the site key when calling `saveOAuthTokens`.
- The self-update state file lives at `~/.config/ffc/.update_check.json` (JSON with `checked_at` and `latest` fields). It is refreshed in a background goroutine at most once per day. `Execute()` in `root.go` waits up to 2 seconds for that goroutine before exiting.
- `update_check.go` skips the update check for both `update` and `mcp` commands. The MCP server takes over stdin/stdout for JSON-RPC; any stderr output (including the update notice) would corrupt the stream. Do not remove `mcp` from this skip condition.
- The MCP detached-server state file lives at `~/.config/ffc/mcp.json` (JSON with `pid`, `port`, `site`, `started_at`, `log_path`). The log file is at `~/.config/ffc/mcp.log`. These are managed by `mcp_daemon.go` — `ffc mcp stop` removes the state file.
- `mcp_detach_unix.go` and `mcp_detach_windows.go` use build tags (`!windows` / `windows`) to provide platform-specific `setSysProcAttr(*exec.Cmd)`. The Unix version sets `Setsid: true` to detach from the terminal's process group. Do not add `syscall.SysProcAttr` fields directly in non-platform-tagged files — they won't compile cross-platform.
- MCP tool handlers in `mcp_tools.go` must never write to stdout or call `output.Print*` functions. Stdout is the MCP JSON-RPC channel. Return results via `mcp.NewToolResultText(json)` and errors via `mcp.NewToolResultError(msg)` with a `nil` Go error (so the LLM sees the failure, not a protocol crash).
- `get-schema --json` returns a **compact view** by default (via `compactSchema` in `get_schema.go`): only meaningful DocType and DocField properties are kept; zero-value booleans, internal metadata, and noise fields are stripped. Use `--full` to get the raw Frappe response. The `isTruthy`, `compactField`, `compactSchema`, and `filterSchemaKeys` helpers are defined in `get_schema.go` — do not duplicate them elsewhere. The MCP `get_schema` tool defaults to compact but accepts optional `full` (bool) and `keys` (string) parameters so the LLM can request the raw response or filter to specific top-level keys.
- `get-doc` and `update-doc` make `--name` optional: if omitted, the name variable defaults to the DocType name. This is the correct behaviour for Single DocTypes (where the document name equals the DocType name). The MCP `get_doc` and `update_doc` tools do the same via `req.GetString("name", "")` defaulting to `doctype`. `delete-doc` intentionally keeps `--name` required — Frappe does not allow deleting Single DocTypes, so defaulting there would only produce a confusing error.
- `GetDoc("DocType", doctype)` only returns standard fields — it does NOT include custom fields added via Customize Form. Custom fields are stored in the `Custom Field` DocType (filtered by `dt = doctype`). `mergeCustomFields(fc, doctype, doc)` in `get_schema.go` fetches them and splices them into `doc["fields"]` at the correct positions (using the `insert_after` field, ordered by `idx`). It is called in both the CLI (`get_schema.go`) and MCP (`mcp_tools.go`) handlers, always before `applyPropertySetterOverrides`.
- The MCP `run_report` tool applies `compactReportResult` (defined in `mcp_tools.go`): returns only `columns`, `result`, and `report_summary` (if non-null). Strips `execution_time`, `chart`, `add_total_row`, `message`. The CLI `run-report --json` still returns the full response (use `--keys columns,result` to trim it).
