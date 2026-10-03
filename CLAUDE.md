# CLAUDE.md — Project Context for AI Agents

## Project

**ffc** (Foxmayn Frappe CLI) — A Go CLI for interacting with Frappe ERP sites via the REST API.

## Tech Stack

- **Language:** Go 1.25
- **CLI framework:** [cobra](https://github.com/spf13/cobra)
- **Config:** [go.yaml.in/yaml/v3](https://github.com/yaml/go-yaml) (read as structs, edited as `yaml.Node` trees) + env vars
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
make test           # go test -race ./...
make lint           # gofmt check, go vet, staticcheck (if installed)
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
- `.github/workflows/release.yml` — triggers on `v*` tags; runs `go mod tidy -diff`, vet and tests, then GoReleaser and a build-provenance attestation. Actions are pinned to commit SHAs.
- `.github/workflows/ci.yml` — tidy check, vet, race tests and a cross-build on every push/PR
- `install.sh` — Linux/macOS: detects OS/arch, downloads tarball, verifies SHA256, installs to `/usr/local/bin` or `~/.local/bin`
- `install.ps1` — Windows: detects arch, downloads zip, verifies SHA256, installs to `%LOCALAPPDATA%\Programs\ffc`, adds to user PATH

## Architecture

```
cmd/ffc/main.go              → Entry point, calls cmd.Execute()
internal/cmd/root.go         → Root cobra command, global flags (--site, --config, --json, --quiet, --timeout)
internal/cmd/site_client.go  → loadSite (config.Load + OAuth refresh under the config lock) and newClient.
                                The ONLY way a command or MCP tool gets a site client.
internal/cmd/helpers.go      → callSite[T] (client + spinner), runSpinner, confirm, readInput ("-" = stdin),
                                parseObject, splitCSV, filterKeys/selectKeys, listLimit, docName, validateFiltersJSON
internal/cmd/auth_wizard.go  → Shared auth wizard: collectSite → collectAPIKeySite / collectPasswordSite /
                                collectOAuthSite; runForm (Esc/Ctrl+C → errAborted), validateSiteName,
                                normalizeSiteURL, writeInitConfig (Overwrite), addSiteToConfig (Edit)
internal/cmd/init.go         → init subcommand (--oauth/--apikey/--password)
internal/cmd/oauth_flow.go   → OAuth PKCE: callbackServer (127.0.0.1, single-use delivery), collectOAuthSite
internal/cmd/site.go         → site list / add / remove / use
internal/cmd/config_cmd.go   → config TUI, config get, config set; escQuitKeyMap, resolveCfgPath
internal/cmd/{ping,get_doc,list_docs,create_doc,update_doc,delete_doc,count_docs,get_schema,
             list_doctypes,list_reports,run_report,call_method}.go → data commands (all use callSite)
internal/cmd/bulk.go         → runBulk worker pool, bulkReport, parseObjects/parseNames/splitUpdates, bulkFlags
internal/cmd/bulk_{create,update,delete}.go → bulk commands (--concurrency 1-10, --fail-fast)
internal/cmd/update.go           → self-update (size-limited download, SHA256 check, atomic swap)
internal/cmd/update_check.go     → background update check; owns rootCmd.PersistentPreRunE
internal/cmd/mcp.go              → mcp subcommand, --read-only, newMCPClientProvider (cached client)
internal/cmd/mcp_args.go         → toolHandler, marshalResult (512 KiB cap), jsonArg/rawJSONArg/objectArg/intArg/stringsArg
internal/cmd/mcp_tools.go        → 15 MCP tools (registerTools)
internal/cmd/mcp_daemon.go       → detached HTTP server, status/stop, state + lock files
internal/cmd/mcp_detach_unix.go / mcp_detach_windows.go → setSysProcAttr, terminateProcess, isProcessRunning
internal/client/http.go      → newResty (timeout, 128 MiB body cap, no cookie jar, silent logger, GET-only
                                redirects), retry policy, requestError, warnIfInsecure, stripHTML, snippet
internal/client/client.go    → FrappeClient; New() picks auth; one do() request path; session relogin; Close()
internal/client/oauth.go     → ExchangeOAuthCode, RefreshOAuthToken, GetOAuthUser (all take ctx)
internal/client/session.go   → LoginPassword (POST /api/method/login, sid cookie, 2FA detection)
internal/config/config.go    → Config/SiteConfig, Read, Load, env overrides, default paths
internal/config/file.go      → File (yaml.Node editor), Edit/Overwrite (lock + atomic 0600 write), WriteFileAtomic
internal/config/format.go    → number/date formats, FormatNumber, FormatDate
internal/output/             → lipgloss table and JSON; every server value passes through text.Sanitize
internal/text/               → Sanitize: strips C0/C1 control characters (terminal escape injection)
internal/version/            → Build-time version variables (ldflags)
```

## Conventions

- **Error handling:** Wrap with `fmt.Errorf("context: %w", err)`. Never log and return; return and let caller decide.
- **Stdout vs stderr:** Data goes to stdout, diagnostics/errors go to stderr.
- **Exit codes:** any abort, declined confirmation or partial bulk failure is a non-zero exit (`errAborted`, `bulkReport.err()`). Only the config TUI's explicit "Cancel" exits 0.
- **Config precedence:** flags > env vars > config file > defaults.
- **Auth:** Bearer token for OAuth sites; `Authorization: token key:secret` for API-key sites; `Cookie: sid=<sid>` for username/password sites. `client.New()` picks the method from `cfg.AccessToken` / `cfg.APIKey`+`cfg.APISecret` / `cfg.IsSessionAuth()`, in that order. `client.New()` is fallible because session sites log in inside it.
- **Adding a data command:** create `internal/cmd/<name>.go`, set `Args: cobra.NoArgs`, call `callSite(cmd, title, func(ctx, c) ...)`, register via `rootCmd.AddCommand()` in `init()`. Never build a client with `client.New` directly in a command — use `newClient`/`callSite` so OAuth refresh happens.
- **Adding an MCP tool:** use `toolHandler(getClient, parse)`; read JSON-valued params with `jsonArg`/`rawJSONArg`/`objectArg` (never `req.GetString` — it returns "" for a native object), integers with `intArg`. Register write tools only when `!mcpReadOnly`.

## Config

Default config path: `~/.config/ffc/config.yaml` (dir 0700, file 0600, lock file `config.yaml.lock`).

Env vars:
- `FFC_API_KEY` + `FFC_API_SECRET` — only honoured as a pair; they then replace every stored credential of the selected site.
- `FFC_URL` — applies only together with that pair (never redirects stored credentials to another host); `FFC_URL` alone that differs from the site URL is an error, so a script aimed at another host never runs against the stored one. With no config file at the default path, the three env vars alone define the site.
- `FFC_NO_UPDATE_CHECK=1` — disables the background update check.

## Common Pitfalls

- The `config.yaml` in the project root is gitignored — it's for local dev only. Do not commit credentials.
- **All config writes go through `config.Edit` (or `config.Overwrite` for a fresh file).** They take `config.yaml.lock`, re-read the file, apply the callback to a `config.File` (yaml.Node tree: keeps comments and key order), and write atomically at 0600, following symlinks. Return `config.ErrUnchanged` from the callback to skip the write. Any network call made while holding the lock must be bounded by `config.MaxLockHold` (30 s; the lock is considered stale after 3×), and release removes the lock only if it still holds this process's token. Never write config.yaml with `os.WriteFile` or by marshalling the struct.
- Site names keep their case and may contain dots (`Prod`, `erp.example.com`). `--site`/`default_site` match exactly first, then fall back to a unique case-insensitive match (what viper users relied on). viper was removed because it lowercased keys and split them on dots; do not reintroduce it. `Config`/`SiteConfig` carry `yaml` tags only.
- Frappe API wraps list results in `"data"` (v14+) or `"message"` (older). The client handles both. A 2xx single-document response without `data` is an error, not an empty doc.
- Frappe error responses contain nested JSON strings with Python tracebacks. `frappeErrorResponse.userMessage()` extracts the user-facing message; non-JSON error bodies are cut to 300 runes and sanitised.
- **Redirects:** only GET/HEAD follow redirects. A redirected POST/PUT/DELETE fails with the target URL, because Go would otherwise replay it as a body-less GET and report success (e.g. delete-doc against an `http://` URL that redirects to `https://`).
- Non-site HTTP (GitHub release API, downloads) uses `client.NewHTTPClient(timeout)`, never `resty.New()` — resty's default logger prints `WARN RESTY` lines on stderr.
- **Retries:** only GET, only on 429/502/503/504, never after a timeout or cancel. Do not add retries to writes.
- **OAuth refresh** happens in `loadSite` (site_client.go), only for commands that talk to a site, under the config lock; it re-checks expiry after taking the lock so concurrent processes refresh once. Failures warn on stderr and the command proceeds (it then gets a 401). There is no refresh in `PersistentPreRunE` any more.
- Username/password (session-cookie) auth has no `sid` in config. CLI commands log in once per invocation (short-lived process). Long-lived users of one client (MCP daemon, bulk runs) rely on `FrappeClient.relogin`: on a 401/403 with a session older than 1 minute it first asks `frappe.auth.get_logged_user` whether the session is still valid (then it is a real permission error, no new login), and logins are single-flight under `loginMu`, keyed by the sid each request used. `Close(ctx)` logs out; the MCP provider drops a replaced client without logging out (in-flight calls may still use it). Do not persist a `sid` to config.
- The plaintext `password` field in `SiteConfig` is stored at 0600 — same protection as `api_secret`. ffc has no OS-keychain integration.
- In huh v1.0.0, only `ctrl+c` is bound to Quit by default. Use `runForm` (auth_wizard.go), which adds Escape via `escQuitKeyMap()` and maps an abort to `errAborted`.
- `update_check.go` sets `rootCmd.PersistentPreRunE` in its `init()`. Do not set it anywhere else — it would silently overwrite the hook.
- The update check is skipped for `update`, `mcp` (and subcommands), `completion`, `__complete` and `help`, and when `FFC_NO_UPDATE_CHECK` is set. `checked_at` is recorded before the fetch, so a failing network costs at most one attempt per day. State file: `~/.config/ffc/.update_check.json` (0600).
- OAuth PKCE: the callback server binds `127.0.0.1` (the redirect URI is `http://127.0.0.1:<port>/callback`, not `localhost`) before the form opens, accepts exactly one result, answers duplicates with 409, and is always closed on abort.
- `IsOAuth()` returns true only if `AccessToken != ""`. `IsSessionAuth()` requires both `Username` and `Password`.
- `client.LoginPassword` does not support Frappe 2FA; it returns an error pointing to OAuth or an API key.
- `SiteConfig.Name` is a runtime-only field (`yaml:"-"`) set by `config.Load` to the exact YAML key; token writes use it as the site key.
- **MCP stdout is the JSON-RPC channel.** Tool handlers must never write to stdout or call `output.Print*`. Return results via `marshalResult`/`mcp.NewToolResultText` and errors via `mcp.NewToolResultError` with a nil Go error. Stderr is safe in stdio mode (clients log it) and is `mcp.log` in detached mode — never print secrets there (the daemon prints its bearer token only to a terminal; `ffc mcp status` shows it).
- MCP limits: results over 512 KiB are refused with a hint to narrow them; `run_report` defaults to 500 rows; bulk tools take at most 200 items; `--read-only` registers only read tools (call_method counts as a write).
- The MCP detached-server state file is `~/.config/ffc/mcp.json` (pid, port, site, started_at, log_path, instance), guarded by a lock file; the log is `~/.config/ffc/mcp.log`. `ffc mcp stop --force` stops a PID that is alive but not health-confirmed.
- `mcp_detach_unix.go` / `mcp_detach_windows.go` use build tags. Keep `syscall`/`golang.org/x/sys/windows` fields out of untagged files.
- `get-schema --json` returns a compact view by default (`compactSchema` in get_schema.go); `--full` gives the raw response, `--keys` filters top-level keys. `fetchSchema` is shared by the CLI and MCP: it merges Custom Fields (`mergeCustomFields`, by `insert_after`) and then applies every Property Setter (`applyPropertySetters`: DocField and DocType level, values cast by `property_type`, `field_order` reorders fields). Problems become warnings (`_warnings` in CLI JSON), not failures.
- `get-doc` and `update-doc` default `--name` to the DocType name (Single DocTypes); `delete-doc` keeps `--name` required. `update-doc` strips a `name` key from `--data` with a warning (the name comes from the URL).
- MCP `run_report` returns only `columns`, `result`, `report_summary` (if non-null), `total_rows` and `truncated` (`compactReportResult`). CLI `run-report --json` returns the full response; `--limit` applies to both table and JSON.
- `list-docs --limit 0` means no limit; negative `--limit`/`--start` are rejected. `--filters` must be a JSON object or array.
- `go.mod` has `toolchain go1.25.14`. CI and GoReleaser install Go from `go-version-file: go.mod`, which can resolve to the `go` line (1.25.5); the toolchain line guarantees the patched release either way (setup-go or `GOTOOLCHAIN=auto` switches to it). Without it, release binaries were built with Go 1.25.0 and carried 30 reachable stdlib vulnerabilities (`GOTOOLCHAIN=go1.25.0 govulncheck ./...`). Bump it with each Go patch release.
- Go directive stays on 1.25 (1.25.5, the minimum mcp-go v1.1.1 needs): the newest `golang.org/x/*` releases require Go 1.26, so they are pinned to the last 1.25-compatible versions (net v0.58.0, text v0.41.0, sys v0.47.0, sync v0.22.0). Check `go.mod` before bumping, and reject Dependabot PRs that move the `go` line to 1.26 unless that move is intended.
- mcp-go v1.x speaks MCP protocol 2026-07-28 and every earlier revision, chosen per request; tool registration is unchanged. Input-schema validation (`server.WithInputSchemaValidation`) stays off: the arg helpers in mcp_args.go accept numeric strings and JSON-encoded strings that a strict schema would reject.
