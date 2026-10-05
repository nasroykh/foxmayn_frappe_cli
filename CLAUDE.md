# CLAUDE.md — Project Context for AI Agents

## Project

**ffc** (Foxmayn Frappe CLI) — A Go CLI for interacting with Frappe ERP sites via the REST API.

## Tech Stack

- **Language:** Go 1.26 (toolchain go1.27.1)
- **CLI framework:** [cobra](https://github.com/spf13/cobra)
- **Config:** [go.yaml.in/yaml/v3](https://github.com/yaml/go-yaml) (read as structs, edited as `yaml.Node` trees) + env vars
- **HTTP client:** [resty](https://github.com/go-resty/resty)
- **Table & styling:** [lipgloss v2](https://charm.land/lipgloss/v2) + built-in `table` sub-package
- **Forms & prompts:** [huh](https://github.com/charmbracelet/huh)
- **Spinner:** [huh/spinner](https://github.com/charmbracelet/huh) (standalone, no bubbletea loop needed)
- **MCP server:** [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) v1.1.1 (stdio + StreamableHTTP transports)

## Build & Run

```bash
make build          # Compile binary to ./bin/ffc
make install        # Install to $GOPATH/bin + set up config
make tidy           # go mod tidy
make vet            # go vet ./...
make fmt            # gofmt -w .
make test           # go test -race ./...
make lint           # gofmt check, go vet, staticcheck (pinned, via go run)
make vuln           # govulncheck (pinned, via go run)
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

GitHub Actions will cross-compile for linux/darwin/windows × amd64/arm64, create a GitHub Release, upload the tarballs, and generate `checksums.txt` and its Ed25519 signature `checksums.txt.sig`.

**Release signing.** GoReleaser's `signs` step runs `go run ./tools/relsign sign` with the `FFC_RELEASE_SIGNING_KEY` repository secret (base64 Ed25519 seed). `ffc update` verifies the signature against `internal/relsig/keys.go` and refuses a release without a valid one. relsign refuses a secret that does not match `ReleaseKeys`, and `TestReleaseKeysConfigured` fails while `ReleaseKeys` is empty. To rotate: generate a key with `go run ./tools/relsign keygen <file>`, add its public key to `ReleaseKeys` (and `RELEASE_KEYS` in install.sh) next to the old one, ship a release signed with the old key, then switch the secret. Never commit a private key.

**Backup key.** `ReleaseKeys` also holds an offline backup key (second entry) whose private half is not in CI. If the CI key is lost or leaked, switch `FFC_RELEASE_SIGNING_KEY` to the backup key (released binaries since v1.6.3 already trust it), remove the compromised key from `ReleaseKeys` and install.sh in that release, and generate a new offline backup.

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
- `.github/workflows/ci.yml` — gofmt, tidy check, vet, race tests, staticcheck, govulncheck and a cross-build on every push/PR
- `install.sh` — Linux/macOS: detects OS/arch, downloads tarball, verifies SHA256, installs to `/usr/local/bin` or `~/.local/bin`
- `install.ps1` — Windows: detects arch, downloads zip, verifies SHA256, installs to `%LOCALAPPDATA%\Programs\ffc`, adds to user PATH

## Architecture

```
cmd/ffc/main.go              → Entry point, calls cmd.Execute()
internal/cmd/root.go         → Root cobra command, global flags (--site, --config, --json, --quiet, --timeout)
internal/cmd/site_client.go  → loadSite = loadSiteConfig (config.Load, no network) + refreshSite (OAuth refresh under the
                                config lock), and newClient.
                                The ONLY way a command or MCP tool gets a site client.
internal/cmd/helpers.go      → callSite[T] (client + spinner), runSpinner, confirm, readInput ("-" = stdin),
                                parseObject, splitCSV, filterKeys/selectKeys, listLimit, docName, validateFiltersJSON
internal/cmd/auth_wizard.go  → Shared auth wizard: collectSite → collectAPIKeySite / collectPasswordSite /
                                collectOAuthSite; runForm (Esc/Ctrl+C → errAborted), validateSiteName,
                                normalizeSiteURL, writeInitConfig (Overwrite), addSiteToConfig (Edit)
internal/cmd/init.go         → init subcommand (--oauth/--apikey/--password)
internal/cmd/oauth_flow.go   → OAuth PKCE: callbackServer (127.0.0.1, single-use delivery), collectOAuthSite
internal/cmd/site.go         → site list / add / remove / rename / edit / use
internal/cmd/setup_flags.go  → non-interactive setup flags for init and site add (--name/--url/--api-key/--username,
                                secrets via stdin or env), verifySite (same checks as the wizard)
internal/cmd/config_cmd.go   → config TUI, config get, config set; escQuitKeyMap, resolveCfgPath
internal/cmd/{ping,get_doc,list_docs,create_doc,update_doc,delete_doc,count_docs,get_schema,
             list_doctypes,list_reports,run_report,call_method}.go → data commands (all use callSite)
internal/cmd/api.go          → ffc api: buildAPIRequest (-f/-F/--input/-H), writeBody (pipe raw, TTY formatted,
                                binary refused on a TTY), saveBody, --paginate (v1 limit_start, v2 start/has_next_page)
internal/cmd/render.go       → output layer: resolveOutput (--output/--json/--jq/FFC_OUTPUT, before RunE via applyEnv),
                                render(v, fields, table), machineOutput/printResult, renderJQ, renderAll + listDocs
                                (--all/--page-size, streamed for json/ndjson/csv/tsv), pageFlags
internal/cmd/bulk.go         → runBulk worker pool, bulkReport, parseObjects/parseNames/splitUpdates, bulkFlags
internal/cmd/bulk_{create,update,delete}.go → bulk commands (--concurrency 1-10, --fail-fast)
internal/cmd/{submit_doc,cancel_doc,discard_doc,amend_doc,rename_doc,restore_doc}.go → lifecycle commands
                                (amend_doc.go also has copy-doc); refuseWorkflow in submit_doc.go
internal/cmd/workflow.go         → workflow transitions / apply / bulk-apply (bulkFlags) / pending
internal/cmd/update.go           → self-update (size-limited download, signed checksums.txt + SHA256 check, atomic swap)
internal/cmd/update_check.go     → background update check; owns rootCmd.PersistentPreRunE
internal/cmd/mcp.go              → mcp subcommand, --read-only + policy flags (mcpFlags), newMCPEnv (site per call, cached
                                client), startMCP (credential check + registerTools filtered by policy)
internal/cmd/mcp_args.go         → mcpEnv, toolHandler (parse → scope → policy → client → call → audit line),
                                marshalResult (512 KiB cap), jsonArg/rawJSONArg/objectArg/intArg/stringsArg
internal/cmd/mcp_policy.go       → toolActions (every tool: read/write/method), scopeOf, mcpPolicy.check/checkReport,
                                sensitiveDoctypes, deniedMethods
internal/cmd/mcp_audit.go        → auditLog: mcp-audit.jsonl next to the config (0600, rotated at 10 MiB under config.Lock)
internal/cmd/mcp_tools.go        → 22 MCP tools (registerTools); lifecycle ones in mcp_lifecycle_tools.go
internal/cmd/mcp_daemon.go       → detached HTTP server, status/stop, state + lock files
internal/cmd/mcp_detach_unix.go / mcp_detach_windows.go → setSysProcAttr, terminateProcess, isProcessRunning
internal/client/http.go      → newResty (timeout, 128 MiB body cap, no cookie jar, silent logger, GET-only
                                redirects), retry policy, requestError, warnIfInsecure, stripHTML, snippet
internal/client/client.go    → FrappeClient; New() picks auth; one do() request path; session relogin; Close()
internal/client/raw.go       → Raw (any site path, streamed, no retries, no 128 MiB cap), SitePath (refuses URLs),
                                ResponseError (status+body → *APIError)
internal/client/debug.go     → --debug trace: debugTransport under resty (every attempt, login, OAuth, raw, update
                                checks), DebugLevel/Debug package var; redaction (secretKey/secretParts, redactURL/Header/Form,
                                redactJSON = text pass at any escaping depth + redactValue on the parsed tree) shared with dry runs
internal/client/dryrun.go    → WithDryRun(ctx, scope), DryRunError{Requests}; send() holds back writes (or all requests)
internal/cmd/dryrun.go       → addDryRun (flag + scope annotation), withDryRun (set in trackRunStart), printPlan,
                                fieldChanges (update-doc diff); bulk dry runs go through planAll in bulk.go
internal/client/oauth.go     → ExchangeOAuthCode, RefreshOAuthToken, GetOAuthUser (all take ctx)
internal/client/session.go   → LoginPassword (POST /api/method/login, sid cookie, 2FA detection)
internal/client/lifecycle.go → SubmitDoc/CancelDoc/AmendDoc/DuplicateDoc/RenameDoc/RestoreDeleted/DiscardDoc,
                                workflow methods; StateError (exit 6) in errors.go
internal/config/config.go    → Config/SiteConfig, Read, Load, env overrides, default paths
internal/config/file.go      → File (yaml.Node editor), Edit/Overwrite (lock + atomic 0600 write), WriteFileAtomic
internal/config/format.go    → number/date formats, FormatNumber, FormatDate
internal/output/             → lipgloss table and JSON; every server value passes through text.Sanitize.
                                render.go: Format, Write (json/ndjson/csv/tsv/yaml), ListStream, Normalize (UseNumber)
internal/text/               → Sanitize: strips C0/C1 controls (terminal escape injection), bidi overrides/isolates
                                and zero-width characters (Trojan Source); keeps LRM/RLM/ALM/ZWJ/ZWNJ
internal/relsig/             → Ed25519 sign/verify of checksums.txt (domain-separated); ReleaseKeys in keys.go
tools/relsign/               → keygen / sign / verify for the release key (run by GoReleaser, not shipped)
internal/version/            → Build-time version variables (ldflags)
```

## Testing

- **Fake Frappe (`internal/frappetest`).** An in-memory site for unit tests: `/api/resource` CRUD with filters, fields, paging and ordering, the `/api/method` endpoints ffc calls, API-key/Bearer/session auth, login and logout counters, and Frappe's real error shapes (`exc_type`, `_server_messages`, `exception`, captured from a v16 site). `HandleMethod` adds a whitelisted method, `Handle("METHOD /path", h)` overrides a route (`ErrorHandler`, `HTMLPage`), and `Requests()` returns what was sent.
- **CLI harness (`internal/cmd/cli_harness_test.go`).** `fakeConfig(t, site, "apikey"|"password"|"oauth")` writes a config; `runFFC(t, cfg, stdin, args...)` runs the root command with stdout/stderr captured and resets every flag afterwards.
- **MCP.** `newMCPFake(t, readOnly)` registers the tools against a fake site; `callTool` goes through JSON-RPC so argument decoding matches a real client.
- **Contract tests (`internal/cmd/contract_test.go`, build tag `contract`).** Run against a real, disposable site: `make contract SITE=<ffc config site>`, or `FFC_CONTRACT_URL` with `FFC_CONTRACT_API_KEY`/`_SECRET` or `FFC_CONTRACT_USER`/`_PASSWORD`. Each run creates the custom submittable DocType `FFC Contract Test` (child `FFC Contract Test Item`, a Custom Field and a Property Setter), and removes it again, leftovers of an interrupted run included. They pin number literals (Currency always `x.0`), the list default (`name` only), child-table PUT (replaces rows), submit/cancel/amend (`frappe.client.submit` takes the doc, an amendment is `<name>-1`, editing a submitted doc is UpdateAfterSubmitError 417), schema merging, session logout and POST without CSRF, and that the fake fails like the real site. `.github/workflows/contract.yml` runs them nightly against ERPNext v15 and v16 from frappe_docker's pwd.yml.
- Every new command or tool ships with tests against the fake. Behaviour the fake cannot prove (real Frappe semantics) belongs in the contract tests (T1.7 layer 2).

## Conventions

- **Error handling:** Wrap with `fmt.Errorf("context: %w", err)`. Never log and return; return and let caller decide.
- **Stdout vs stderr:** Data goes to stdout, diagnostics/errors go to stderr.
- **Exit codes (`exit.go`, part of the CLI contract; README table):** 0 ok, 1 generic, 2 usage, 3 auth, 4 not found, 5 permission, 6 validation/conflict, 7 network/server, 8 partial bulk, 130 interrupted. `classify` maps typed errors: `client.APIError` (status + exc_type), `client.AuthError` (login), `client.TransportError` (no response), `client.StateError` (document in the wrong state, 6), `usageError` (`usageErrorf` for invalid flag values; anything cobra rejects before RunE is wrapped automatically), `partialError` (bulk). Return these types instead of plain errors where the class matters. With `--json`, `reportError` prints `{"error":{code,exit_code,status,exc_type,message}}` on stderr. Only the config TUI's explicit "Cancel" exits 0.
- **No prompts without a terminal.** `runForm` returns `errNoInput` (a usage error) when `--no-input` is set or stdin is not a TTY; `confirm` turns it into "pass --yes". New prompts must go through `runForm`.
- **Environment:** `applyEnv` (cobra.OnInitialize) fills `--site/--config/--timeout` from `FFC_SITE/FFC_CONFIG/FFC_TIMEOUT` when the flag is unset; an invalid value fails in the RunE wrapper (`trackRunStart`).
- **Config precedence:** flags > env vars > config file > defaults.
- **Auth:** Bearer token for OAuth sites; `Authorization: token key:secret` for API-key sites; `Cookie: sid=<sid>` for username/password sites. `client.New()` picks the method from `cfg.AccessToken` / `cfg.APIKey`+`cfg.APISecret` / `cfg.IsSessionAuth()`, in that order. `client.New()` is fallible because session sites log in inside it.
- **Adding a data command:** create `internal/cmd/<name>.go`, set `Args: cobra.NoArgs`, call `callSite(cmd, title, func(ctx, c) ...)`, register via `rootCmd.AddCommand()` in `init()`. Print the result with `render(v, fields, tableFn)` (or `if machineOutput() { return printResult(v) }` before the table code), never `output.PrintJSON` directly, so `--output`/`--jq` work. Lists use `listDocs` for `--all`. JSON-valued flags go through `parseObject`/`filtersFlag`/`jsonFlag` so `@FILE`/`@-` work. Never build a client with `client.New` directly in a command — use `newClient`/`callSite` so OAuth refresh happens. A command that writes registers `--dry-run` with `addDryRun` and skips its confirmation when `dryRunOn(cmd)`.
- **Adding an MCP tool:** use `toolHandler(env, parse)`; read JSON-valued params with `jsonArg`/`rawJSONArg`/`objectArg` (never `req.GetString` — it returns "" for a native object), integers with `intArg`. Add the tool to `toolActions` in mcp_policy.go (read, write or method; its read-only annotation must match, `TestMCPPolicyCoversEveryTool` checks both), and teach `scopeOf` any argument that names DocTypes or documents. registerTools drops the tools the policy (read_only, allow_tools) does not allow.

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
- Response JSON is decoded with `UseNumber`, so numbers arrive as `json.Number` (exact large ints, `0.0` stays `0.0` in `--json`). Code that inspects a numeric value must handle `json.Number` (see `numeric` in get_schema.go); `output.formatValue` prints integer literals as-is and formats the rest with the number format. Trailing data after the JSON body is an error.
- Frappe API wraps list results in `"data"` (v14+) or `"message"` (older). The client handles both. A 2xx single-document response without `data` is an error, not an empty doc.
- Reading, creating or updating a document of a DocType that does not exist is a 500 `ImportError` ("No module named 'frappe.core.doctype.x'"), not a 404; GetDoc/CreateDoc/UpdateDoc then probe the DocType's list (`missingDocType`; a 404 there means no DocType record), set `APIError.MissingDocType`, and `classify` makes it not found (exit 4). The message cannot be trusted: sites that hide tracebacks send none. Listing and deleting give a real 404. The fake and the contract tests pin both.
- Frappe error responses contain nested JSON strings with Python tracebacks. `frappeErrorResponse.userMessage()` extracts the user-facing message; non-JSON error bodies are cut to 300 runes and sanitised.
- **Redirects:** only GET/HEAD follow redirects. A redirected POST/PUT/DELETE fails with the target URL, because Go would otherwise replay it as a body-less GET and report success (e.g. delete-doc against an `http://` URL that redirects to `https://`).
- Non-site HTTP (GitHub release API, downloads) uses `client.NewHTTPClient(timeout)`, never `resty.New()` — resty's default logger prints `WARN RESTY` lines on stderr.
- **Retries:** only GET, on 429/502/503/504 and on transport errors, never after a timeout or cancel. A Retry-After up to 10 s (`maxRetryAfter`) is honoured; a longer one is reported instead of retried. Do not add retries to writes.
- **OAuth refresh** happens in `loadSite` (site_client.go), only for commands that talk to a site, under the config lock; it re-checks expiry after taking the lock so concurrent processes refresh once. Failures warn on stderr and the command proceeds (it then gets a 401). There is no refresh in `PersistentPreRunE` any more.
- Username/password (session-cookie) auth has no `sid` in config. CLI commands log in once per invocation and log out when they finish (`callSite`, `bulkFlags.run` and `ping` defer `c.CloseQuietly()`, which POSTs `/api/method/logout` with a fresh 5 s context); without that, every run left a live session in the `Sessions` table. A command that builds its own client must do the same. Long-lived users of one client (MCP daemon, bulk runs) rely on `FrappeClient.relogin`: on a 401/403 with a session older than 1 minute it first asks `frappe.auth.get_logged_user` whether the session is still valid (then it is a real permission error, no new login), and logins are single-flight under `loginMu`, keyed by the sid each request used. `Close(ctx)` logs out; the MCP provider drops a replaced client without logging out (in-flight calls may still use it). Do not persist a `sid` to config.
- The plaintext `password` field in `SiteConfig` is stored at 0600 — same protection as `api_secret`. ffc has no OS-keychain integration.
- In huh v1.0.0, only `ctrl+c` is bound to Quit by default. Use `runForm` (auth_wizard.go), which adds Escape via `escQuitKeyMap()` and maps an abort to `errAborted`.
- `update_check.go` sets `rootCmd.PersistentPreRunE` in its `init()`. Do not set it anywhere else — it would silently overwrite the hook.
- The update check is skipped for `update`, `mcp` (and subcommands), `completion`, `__complete` and `help`, and when `FFC_NO_UPDATE_CHECK` is set. `checked_at` is recorded before the fetch, so a failing network costs at most one attempt per day. State file: `~/.config/ffc/.update_check.json` (0600).
- OAuth PKCE: the callback server binds `127.0.0.1` (the redirect URI is `http://127.0.0.1:<port>/callback`, not `localhost`) before the form opens, accepts exactly one result, answers duplicates with 409, and is always closed on abort.
- `IsOAuth()` returns true only if `AccessToken != ""`. `IsSessionAuth()` requires both `Username` and `Password`.
- `client.LoginPassword` does not support Frappe 2FA; it returns an error pointing to OAuth or an API key.
- `SiteConfig.Name` is a runtime-only field (`yaml:"-"`) set by `config.Load` to the exact YAML key; token writes use it as the site key.
- **`-o` is `list-docs --order-by`**, so the output format flag is `--output` with no shorthand (a persistent `-o` would panic on the shorthand clash). Do not add `-o` elsewhere.
- `--output json` must stay byte-identical to the old `--json` (indented, HTML-escaped by encoding/json). YAML numbers go through `yamlNode` (yaml.v3 would quote a json.Number).
- **`ffc api` defaults to GET even with fields** (gh switches to POST): a bare `-f` on `/api/resource/X` would create a document. Only `--input` implies POST. `SitePath` refuses absolute and `//host` URLs; `CheckHeaders`/`Raw` refuse `Authorization`, `Cookie`, `Host`, `X-Frappe-Site-Name` and `X-Forwarded-Host` (the last three route to another site on a bench). Keep both checks in the client: they are the credential boundary.
- **MCP stdout is the JSON-RPC channel.** Tool handlers must never write to stdout or call `output.Print*`. Return results via `marshalResult`/`mcp.NewToolResultText` and errors via `mcp.NewToolResultError` with a nil Go error. Stderr is safe in stdio mode (clients log it) and is `mcp.log` in detached mode — never print secrets there (the daemon prints its bearer token only to a terminal; `ffc mcp status` shows it).
- **Lifecycle:** every state change goes through Frappe's whitelisted methods (`frappe.client.submit` with the document as read, so the timestamp check applies; `frappe.client.cancel`, `rename_doc`, `frappe.model.workflow.*`, the Deleted Document `restore`). amend-doc and copy-doc build the copy client-side from GetDoc (`clean`): the v2 `/copy` route ignores its `ignore_no_copy` query parameter, so both read the meta from `frappe.desk.form.load.getdoctype` (whose data is in `docs`, not `message`; the fake returns it with `frappetest.Response`). Like the desk, a copy never carries Password fields (masked) or `lft`/`rgt` (a tree node keeping them corrupts the nested set); copy-doc also drops "no copy" fields, amend-doc keeps them and sets `amendment_date` when the DocType has it. Fake meta: `DocField`, `NoCopy`, `ChildTable` (also stamps row identity), `AllowOnSubmit`. An amendment is `<name>-1`, an amendment of `<x>-<n>` is `<x>-<n+1>`; a second amendment of the same document is a duplicate. `restore` may give a hash- or series-named document a new name (`Deleted Document.new_name`). submit-doc/cancel-doc refuse a DocType with an active Workflow (StateError); a 403 reading Workflow skips the check. `discard` exists only in v16: the missing-method error is translated, so it is detected by the method name in a 417 and wrapped with ErrNeedsV16 (exit stays 6). Workflow bulk is client-side (`bulk_workflow_approval` reports per-document failures only as msgprint).
- **`--dry-run`** is per command (`addDryRun`), not global; MCP never dry-runs. The context mark is set in the `trackRunStart` wrapper, and `client.send` returns a `*client.DryRunError` instead of sending: scope "writes" holds back everything but GET/HEAD (reads run, so state errors still surface), scope "all" (call-method, api) holds back everything. The error stops the command at its first write; `execute` prints the plan and exits 0. A new write command must call `addDryRun` and skip its confirmation when `dryRunOn(cmd)`. Login/logout use their own resty clients and still run, and so does an OAuth refresh in `loadSite` (reads need a valid token).
- **`--debug`** sets `client.Debug` in `applyEnv` (`resolveDebug`), so it applies to clients built afterwards; the spinner is off while tracing, and the detached MCP child gets `--debug=<level>`. Any new place that logs requests or plans must use the redaction helpers in debug.go; `TestDebugNeverPrintsSecrets` covers all three auth methods.
- **MCP policy (T1.4b).** `sites.<name>.mcp` (`config.MCPPolicy`, unknown keys are an error) is read on every tool call (`env.site` = `loadSiteConfig`, no network); `toolHandler` checks it before `env.client`, so a refused call sends nothing. Config may loosen the built-in rules (sensitive DocTypes, `deniedMethods`); the `ffc mcp` flags only narrow (`mcpFlags`; passed to the detached child by `daemonArgs`). `applyEnvOverrides` and `File.PutSite` keep the `mcp` block: dropping it would widen access silently. Every call writes one audit line (`auditLog.write`: args via `client.RedactArgs`, which copies, then `data`/`args` reduced to keys and size; secret argument values are also cut out of error text).
- The stdio server uses `server.NewStdioServer(s).Listen(cmd.Context(), …)`. Do not go back to `ServeStdio` with a context func returning `cmd.Context()`: that dropped the session mcp-go puts in the context (client name, capabilities, elicitation).
- MCP limits: results over 512 KiB are refused with a hint to narrow them; `run_report` defaults to 500 rows; bulk tools take at most 200 items; `--read-only` registers only read tools (call_method counts as a write).
- The MCP detached-server state file is `~/.config/ffc/mcp.json` (pid, port, site, started_at, log_path, token, instance; 0600, the token is the HTTP bearer secret), guarded by a lock file; the log is `~/.config/ffc/mcp.log`. `ffc mcp stop --force` stops a PID that is alive but not health-confirmed.
- `mcp_detach_unix.go` / `mcp_detach_windows.go` use build tags. Keep `syscall`/`golang.org/x/sys/windows` fields out of untagged files.
- `get-schema --json` returns a compact view by default (`compactSchema` in get_schema.go); `--full` gives the raw response, `--keys` filters top-level keys. `fetchSchema` is shared by the CLI and MCP: it merges Custom Fields (`mergeCustomFields`, by `insert_after`) and then applies every Property Setter (`applyPropertySetters`: DocField and DocType level, values cast by `property_type`, `field_order` reorders fields). Problems become warnings (`_warnings` in CLI JSON), not failures.
- `get-doc` and `update-doc` default `--name` to the DocType name (Single DocTypes); `delete-doc` keeps `--name` required. `update-doc` strips a `name` key from `--data` with a warning (the name comes from the URL).
- MCP `run_report` returns only `columns`, `result`, `report_summary` (if non-null), `total_rows` and `truncated` (`compactReportResult`). CLI `run-report --json` returns the full response; `--limit` applies to both table and JSON.
- `list-docs --limit 0` means no limit; negative `--limit`/`--start` are rejected. `--filters` must be a JSON object or array.
- `go.mod` has `go 1.26.0` and `toolchain go1.27.1`. CI and GoReleaser install Go from `go-version-file: go.mod`, which can resolve to the `go` line; the toolchain line guarantees the patched release either way (setup-go or `GOTOOLCHAIN=auto` switches to it). Without it, release binaries were once built with Go 1.25.0 and carried 30 reachable stdlib vulnerabilities. Bump the toolchain with each Go patch release and run `govulncheck ./...`.
- Go 1.25 is end of life, so the `go` line moved to 1.26 and `golang.org/x/*` is no longer pinned to old versions. Keep the `go` line one release behind the toolchain (it is the minimum users building from source need); move it only when a dependency requires it.
- mcp-go v1.x speaks MCP protocol 2026-07-28 and every earlier revision, chosen per request; tool registration is unchanged. Input-schema validation (`server.WithInputSchemaValidation`) stays off: the arg helpers in mcp_args.go accept numeric strings and JSON-encoded strings that a strict schema would reject.
