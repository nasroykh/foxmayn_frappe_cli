# CLAUDE.md — Project Context for AI Agents

## Project

**ffc** (Foxmayn Frappe CLI) — A Go CLI for interacting with Frappe ERP sites via the REST API.

## Build & Run

`make build | test | lint | vet | fmt | vuln` (see the Makefile). Version info is injected through ldflags. `make lint` runs staticcheck from `tools/lint/go.mod`, its own module.

## Release

Tag `vX.Y.Z` and push it: GoReleaser + GitHub Actions (`.github/workflows/release.yml`) build, sign and publish the archives, the `.mcpb` bundle, the signed `checksums.txt` and the MCP Registry entry. Details, signing keys and rotation: `docs/development/internals/release.md`. Never commit a private key. Desktop releases use `desktop-vX.Y.Z` tags and never become GitHub's "latest": see `desktop/CLAUDE.md`.

## Architecture

Entry point `cmd/ffc/main.go` → `internal/cmd` (cobra). Find the rest with `ls internal/*` and grep; these are the rules the layout does not show:

- `internal/cmd/site_client.go`: `loadSite` (= `loadSiteConfig`, no network, + `refreshSite`, OAuth refresh under the config lock) and `newClient` are the ONLY way a command or MCP tool gets a site client.
- `internal/cmd/helpers.go`: `callSite[T]` (client + spinner), `runSpinner`, `confirm`, `readInput` ("-" = stdin), `parseObject`, `splitCSV`, `filterKeys`/`selectKeys`, `listLimit`, `docName`, `validateFiltersJSON`. `internal/cmd/render.go` is the output layer (`render`, `machineOutput`/`printResult`, `listDocs`).
- `internal/sitesetup/` never prompts or exits and has no huh/cobra; `internal/cmd` imports it, never the reverse. `internal/loopback`, `internal/sitecache`, `internal/release` and `internal/mcpinstall` are prompt- and cobra-free too (the desktop imports them). `internal/client` may still warn on stderr (plain-HTTP warning, `--debug` trace).
- `internal/client`: one `do()` request path (`client.go`), transport in `http.go`, `raw.go` (`SitePath`, `CheckHeaders`) is the credential boundary, `debug.go` holds the redaction helpers.
- `internal/config/file.go`: every config write goes through `Edit`/`Overwrite` (see Common Pitfalls).
- `internal/output` + `internal/text`: every server value passes through `text.Sanitize`.
- `internal/cmd/update_check.go` owns `rootCmd.PersistentPreRunE`.
- MCP: `mcp_args.go` `toolHandler` runs parse → scope → policy → confirm → client → call → audit line; tools are registered in `mcp_tools.go` (and its siblings), their policy tables `toolActions`/`toolSurface` live in `mcp_policy.go`.
- `desktop/` is a nested Go module; `tools/` (relsign, gendocs, mcpb, lint) is build tooling and is not shipped.

## Subsystem notes

Detail moved out of this file. Read the file before changing that area; the headline rule is repeated here.

- `docs/development/internals/mcp.md`: MCP policy, confirmation, multi-site, restore_doc, embedding, `ffc mcp install`, tool surface, jq, limits. Config may loosen the site policy but the `ffc mcp` flags only narrow it; `applyEnvOverrides` and `File.PutSite` must keep the `mcp` block (dropping it widens access). `scopeOf` refuses `ignore_permissions`/`ignore_user_permissions` in call_method args, with no config switch.
- `docs/development/internals/oauth.md`: refresh, dynamic registration, revocation. OAuth refresh lives in `loadSite` and the client's `tokenRefresher`, never in `PersistentPreRunE`.
- `docs/development/internals/erp.md`: `ffc erp` and the MCP erp tools. Never send `ignore_permissions` to an ERPNext mapper (`MapDoc` sends only `source_name`).
- `docs/development/internals/import-export.md`: export, import-template, import (client side and `--server`). Import checks everything before any write; Password fields are a usage error and their values are never printed.
- `docs/development/internals/customize-admin.md`: `customize pull/push`, `health`/`jobs`/`errors`/`scheduler`. A Password field sent empty deletes the stored secret, so push never sends one; push never deletes.
- `docs/development/internals/data-commands.md`: identity and permissions, server version cache, doctor, lifecycle, bulk-create `--atomic`, local cache and completion, get-doc `--fields`, optimistic concurrency, search, aggregates, prepared reports, doc-info, collaboration, files and PDF. The doctor check ids are a JSON contract: never rename them. A tool that needs `policyFrom` fails without one, never falls back to the zero policy.
- `docs/development/internals/release.md`: release flow, signing, key rotation, MCP Registry.
- `desktop/CLAUDE.md` loads by itself under `desktop/` (Assistant rules, safety rules, i18n, bindings).

## Testing

- **Fake Frappe (`internal/frappetest`).** An in-memory site for unit tests: `/api/resource` CRUD with filters, fields, paging and ordering, the `/api/method` endpoints ffc calls, files (upload_file, attach_file, /files and /private/files, download_pdf, get_html_and_style; files.go), API-key/Bearer/session auth, login and logout counters, and Frappe's real error shapes (`exc_type`, `_server_messages`, `exception`, captured from a v16 site). `HandleMethod` adds a whitelisted method, `Handle("METHOD /path", h)` overrides a route (`ErrorHandler`, `HTMLPage`), and `Requests()` returns what was sent.
- **CLI harness (`internal/cmd/cli_harness_test.go`).** `fakeConfig(t, site, "apikey"|"password"|"oauth")` writes a config; `runFFC(t, cfg, stdin, args...)` runs the root command with stdout/stderr captured and resets every flag afterwards.
- **MCP.** `newMCPFake(t, readOnly)` registers the tools against a fake site; `callTool` goes through JSON-RPC so argument decoding matches a real client.
- **Contract tests (`internal/cmd/contract_test.go`, build tag `contract`).** Run against a real, disposable site: `make contract SITE=<ffc config site>`, or `FFC_CONTRACT_URL` with `FFC_CONTRACT_API_KEY`/`_SECRET` or `FFC_CONTRACT_USER`/`_PASSWORD`. Each run creates the custom submittable DocType `FFC Contract Test` (child `FFC Contract Test Item`, a Custom Field and a Property Setter), and removes it again, leftovers of an interrupted run included. They pin number literals (Currency always `x.0`), the list default (`name` only), child-table PUT (replaces rows), submit/cancel/amend (`frappe.client.submit` takes the doc, an amendment is `<name>-1`, editing a submitted doc is UpdateAfterSubmitError 417), schema merging, session logout and POST without CSRF, and that the fake fails like the real site. `.github/workflows/contract.yml` runs them nightly against ERPNext v15 and v16 from frappe_docker's pwd.yml.
- Every new command or tool ships with tests against the fake. Behaviour the fake cannot prove (real Frappe semantics) belongs in the contract tests (T1.7 layer 2).

## Conventions

- **Error handling:** Wrap with `fmt.Errorf("context: %w", err)`. Never log and return; return and let caller decide.
- **Stdout vs stderr:** Data goes to stdout, diagnostics/errors go to stderr.
- **Exit codes (`exit.go`, part of the CLI contract; docs/cli/exit-codes.md):** 0 ok, 1 generic (also a failed `ffc doctor` check, via `codeError`), 2 usage, 3 auth, 4 not found, 5 permission, 6 validation/conflict, 7 network/server, 8 partial bulk, 130 interrupted. `ffc can` answering "denied" is `deniedError` (5, the result is still printed). `classify` maps typed errors: `client.APIError` (status + exc_type), `client.AuthError` (login), `client.TransportError` (no response), `client.StateError` (document in the wrong state, 6), `usageError` (`usageErrorf` for invalid flag values; anything cobra rejects before RunE is wrapped automatically), `partialError` (bulk). Return these types instead of plain errors where the class matters. With `--json`, `reportError` prints `{"error":{code,exit_code,status,exc_type,message}}` on stderr. Only the config TUI's explicit "Cancel" exits 0.
- **No prompts without a terminal.** `runForm` returns `errNoInput` (a usage error) when `--no-input` is set or stdin is not a TTY; `confirm` turns it into "pass --yes". New prompts must go through `runForm`.
- **Environment:** `applyEnv` (cobra.OnInitialize) fills `--site/--config/--timeout` from `FFC_SITE/FFC_CONFIG/FFC_TIMEOUT` when the flag is unset; an invalid value fails in the RunE wrapper (`trackRunStart`).
- **Config precedence:** flags > env vars > config file > defaults.
- **Auth:** Bearer token for OAuth sites; `Authorization: token key:secret` for API-key sites; `Cookie: sid=<sid>` for username/password sites. `client.New()` picks the method from `cfg.AccessToken` / `cfg.APIKey`+`cfg.APISecret` / `cfg.IsSessionAuth()`, in that order. `client.New()` is fallible because session sites log in inside it.
- **Adding a data command:** create `internal/cmd/<name>.go`, set `Args: cobra.NoArgs`, call `callSite(cmd, title, func(ctx, c) ...)`, register via `rootCmd.AddCommand()` in `init()`. Print the result with `render(v, fields, tableFn)` (or `if machineOutput() { return printResult(v) }` before the table code), never `output.PrintJSON` directly, so `--output`/`--jq` work. Lists use `listDocs` for `--all`. JSON-valued flags go through `parseObject`/`filtersFlag`/`jsonFlag` so `@FILE`/`@-` work. Never build a client with `client.New` directly in a command — use `newClient`/`callSite` so OAuth refresh happens. A command that writes registers `--dry-run` with `addDryRun` and skips its confirmation when `dryRunOn(cmd)`.
- **Adding an MCP tool:** use `toolHandler(env, parse)`; read JSON-valued params with `jsonArg`/`rawJSONArg`/`objectArg` (never `req.GetString` — it returns "" for a native object), integers with `intArg`. Add the tool to `toolActions` in mcp_policy.go (read, write or method; its read-only annotation must match, `TestMCPPolicyCoversEveryTool` checks both) and to `toolSurface` (tool set, title, `big`; `TestMCPToolSurface`), and teach `scopeOf` any argument that names DocTypes or documents. registerTools drops the tools the policy (read_only, allow_tools) does not allow.

## Config

Default config path: `~/.config/ffc/config.yaml` (dir 0700, file 0600, lock file `config.yaml.lock`).

Env vars:
- `FFC_API_KEY` + `FFC_API_SECRET` — only honoured as a pair; they then replace every stored credential of the selected site.
- `FFC_URL` — applies only together with that pair (never redirects stored credentials to another host); `FFC_URL` alone that differs from the site URL is an error, so a script aimed at another host never runs against the stored one. With no config file at the default path, the three env vars alone define the site.
- `FFC_NO_UPDATE_CHECK=1` — disables the background update check.
- `FFC_OAUTH_CLIENT_SECRET` — secret of the OAuth Client given with `--client-id` (setup only, `setupFlags.oauthClient`; stored as `oauth_client_secret`); ignored with a warning without `--client-id`.

## Common Pitfalls

- The `config.yaml` in the project root is gitignored — it's for local dev only. Do not commit credentials.
- **All config writes go through `config.Edit` (or `config.Overwrite` for a fresh file).** They take `config.yaml.lock`, re-read the file, apply the callback to a `config.File` (yaml.Node tree: keeps comments and key order), and write atomically at 0600, following symlinks. Return `config.ErrUnchanged` from the callback to skip the write. Any network call made while holding the lock must be bounded by `config.MaxLockHold` (30 s; the lock is considered stale after 3×), and release removes the lock only if it still holds this process's token. Never write config.yaml with `os.WriteFile` or by marshalling the struct.
- Site names keep their case and may contain dots (`Prod`, `erp.example.com`). `--site`/`default_site` match exactly first, then fall back to a unique case-insensitive match (what viper users relied on). viper was removed because it lowercased keys and split them on dots; do not reintroduce it. `Config`/`SiteConfig` carry `yaml` tags only.
- Response JSON is decoded with `UseNumber`, so numbers arrive as `json.Number` (exact large ints, `0.0` stays `0.0` in `--json`). Code that inspects a numeric value must handle `json.Number` (see `numeric` in get_schema.go); `output.formatValue` prints integer literals as-is and formats the rest with the number format. Trailing data after the JSON body is an error.
- Frappe API wraps list results in `"data"` (v14+) or `"message"` (older). The client handles both. A 2xx single-document response without `data` is an error, not an empty doc.
- Reading, creating or updating a document of a DocType that does not exist is a 500 `ImportError` ("No module named 'frappe.core.doctype.x'"), not a 404; GetDoc/CreateDoc/UpdateDoc then probe the DocType's list (`missingDocType`; a 404 there means no DocType record), set `APIError.MissingDocType`, and `classify` makes it not found (exit 4). The message cannot be trusted: sites that hide tracebacks send none. Listing and deleting give a real 404. The fake and the contract tests pin both.
- Frappe error responses contain nested JSON strings with Python tracebacks. `frappeErrorResponse.userMessage()` extracts the user-facing message; non-JSON error bodies are cut to 300 runes and sanitised.
- **Redirects:** only GET/HEAD follow redirects. A redirected POST/PUT/DELETE fails with the target URL, because Go would otherwise replay it as a body-less GET and report success (e.g. delete-doc against an `http://` URL that redirects to `https://`). A followed redirect never goes from https to http, and a request carrying `Authorization` or `Cookie` is never redirected to another host name (Go would copy them to a subdomain); both fail with a `*client.RedirectError`.
- Non-site HTTP (GitHub release API, downloads) uses `client.NewHTTPClient(timeout)`, never `resty.New()` — resty's default logger prints `WARN RESTY` lines on stderr.
- **Retries:** only GET, on 429/502/503/504 and on transport errors, never after a timeout or cancel, a refused redirect or a TLS/certificate error (`retryableTransport`). A Retry-After up to 10 s (`maxRetryAfter`) is honoured; a longer one is reported instead of retried. Do not add retries to writes.
- Username/password (session-cookie) auth has no `sid` in config. CLI commands log in once per invocation and log out when they finish (`callSite`, `bulkFlags.run` and `ping` defer `c.CloseQuietly()`, which POSTs `/api/method/logout` with a fresh 5 s context); without that, every run left a live session in the `Sessions` table. A command that builds its own client must do the same. Long-lived users of one client (MCP daemon, bulk runs) rely on `FrappeClient.relogin`: on a 401/403 with a session older than 1 minute it first asks `frappe.auth.get_logged_user` whether the session is still valid (then it is a real permission error, no new login), and logins are single-flight under `loginMu`, keyed by the sid each request used. `Close(ctx)` logs out; the MCP provider drops a replaced client without logging out (in-flight calls may still use it). Do not persist a `sid` to config.
- The plaintext `password` field in `SiteConfig` is stored at 0600 — same protection as `api_secret`. ffc has no OS-keychain integration.
- In huh v1.0.0, only `ctrl+c` is bound to Quit by default. Use `runForm` (auth_wizard.go), which adds Escape via `escQuitKeyMap()` and maps an abort to `errAborted`.
- `update_check.go` sets `rootCmd.PersistentPreRunE` in its `init()`. Do not set it anywhere else — it would silently overwrite the hook.
- The update check is skipped for `update`, `mcp` (and subcommands), `completion`, `__complete` and `help`, and when `FFC_NO_UPDATE_CHECK` is set. `checked_at` is recorded before the fetch, so a failing network costs at most one attempt per day. State file: `~/.config/ffc/.update_check.json` (0600).
- OAuth PKCE: the callback server binds `127.0.0.1` (the redirect URI is `http://127.0.0.1:<port>/callback`, not `localhost`) before the form opens, accepts exactly one result, answers duplicates with 409, and is always closed on abort.
- `IsOAuth()` returns true only if `AccessToken != ""`. `IsSessionAuth()` requires both `Username` and `Password`.
- `client.LoginPassword` does not support Frappe 2FA; it returns an error pointing to OAuth or an API key.
- `SiteConfig.Name` is a runtime-only field (`yaml:"-"`) set by `config.Load` to the exact YAML key; token writes use it as the site key.
- **`-o` is local only:** `list-docs --order-by`, and `--output-file` of `download`, `pdf`, `export` and `import-template`. The output format flag is `--output` with no shorthand (a persistent `-o` would panic on the shorthand clash). Never make `-o` persistent.
- `--output json` must stay byte-identical to the old `--json` (indented, HTML-escaped by encoding/json). YAML numbers go through `yamlNode` (yaml.v3 would quote a json.Number).
- **`ffc api` defaults to GET even with fields** (gh switches to POST): a bare `-f` on `/api/resource/X` would create a document. Only `--input` implies POST. `SitePath` refuses absolute and `//host` URLs; `CheckHeaders`/`Raw` refuse `Authorization`, `Cookie`, `Host`, `X-Frappe-Site-Name` and `X-Forwarded-Host` (the last three route to another site on a bench). Keep both checks in the client: they are the credential boundary.
- **MCP stdout is the JSON-RPC channel.** Tool handlers must never write to stdout or call `output.Print*`. Return results via `marshalResult`/`mcp.NewToolResultText` and errors via `mcp.NewToolResultError` with a nil Go error. Stderr is safe in stdio mode (clients log it) and is `mcp.log` in detached mode — never print secrets there (the daemon prints its bearer token only to a terminal; `ffc mcp status` shows it).
- **`--dry-run`** is per command (`addDryRun`), not global; MCP never dry-runs. The context mark is set in the `trackRunStart` wrapper, and `client.send` returns a `*client.DryRunError` instead of sending: scope "writes" holds back everything but GET/HEAD (reads run, so state errors still surface), scope "all" (call-method, api) holds back everything. The error stops the command at its first write; `execute` prints the plan and exits 0. A new write command must call `addDryRun` and skip its confirmation when `dryRunOn(cmd)`. Login/logout use their own resty clients and still run, and so does an OAuth refresh in `loadSite` (reads need a valid token).
- **`--debug`** sets `client.Debug` in `applyEnv` (`resolveDebug`), so it applies to clients built afterwards; the spinner is off while tracing, and the detached MCP child gets `--debug=<level>`. Any new place that logs requests or plans must use the redaction helpers in debug.go; `TestDebugNeverPrintsSecrets` covers all three auth methods.
- The stdio server uses `server.NewStdioServer(s).Listen(cmd.Context(), …)`. Do not go back to `ServeStdio` with a context func returning `cmd.Context()`: that dropped the session mcp-go puts in the context (client name, capabilities, elicitation).
- `mcp_detach_unix.go` / `mcp_detach_windows.go` use build tags. Keep `syscall`/`golang.org/x/sys/windows` fields out of untagged files.
- `get-schema --json` returns a compact view by default (`compactSchema` in get_schema.go); `--full` gives the raw response, `--keys` filters top-level keys. `fetchSchema` is shared by the CLI and MCP: it merges Custom Fields (`mergeCustomFields`, by `insert_after`) and then applies every Property Setter (`applyPropertySetters`: DocField and DocType level, values cast by `property_type` to json.Number like every number the site sends, `field_order` reorders fields). Problems become warnings (`_warnings` in CLI JSON), not failures.
- `get-doc` and `update-doc` default `--name` to the DocType name (Single DocTypes); `delete-doc` keeps `--name` required. `update-doc` strips a `name` key from `--data` with a warning (the name comes from the URL).
- `list-docs --limit 0` means no limit; negative `--limit`/`--start` are rejected. `--filters` must be a JSON object or array.
- `go.mod` has `go 1.26.0` and `toolchain go1.27.2`. CI and GoReleaser install Go from `go-version-file: go.mod`, which can resolve to the `go` line; the toolchain line guarantees the patched release either way (setup-go or `GOTOOLCHAIN=auto` switches to it). Without it, release binaries were once built with Go 1.25.0 and carried 30 reachable stdlib vulnerabilities. Bump the toolchain with each Go patch release and run `govulncheck ./...`.
- Go 1.25 is end of life, so the `go` line moved to 1.26 and `golang.org/x/*` is no longer pinned to old versions. Keep the `go` line one release behind the toolchain (it is the minimum users building from source need); move it only when a dependency requires it.
- mcp-go v1.x speaks MCP protocol 2026-07-28 and every earlier revision, chosen per request; tool registration is unchanged. Input-schema validation (`server.WithInputSchemaValidation`) stays off: the arg helpers in mcp_args.go accept numeric strings and JSON-encoded strings that a strict schema would reject.
