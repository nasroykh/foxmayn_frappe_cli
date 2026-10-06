# MCP server internals

Short map of the parts around the tools. CLAUDE.md has the reasoning and the Frappe/mcp-go details; read it before changing any of these.

## Contents

- Registration and surface
- Policy and confirmation
- Multi-site
- Transports and the detached daemon
- jq
- ffc mcp install / uninstall (internal/mcpinstall)

## Registration and surface (mcp_tools.go, mcp_surface.go, mcp_completion.go)

- `registerTools` → `registerAllTools`, then drops tools the policies (`read_only`, `allow_tools`, `anyAllows` across sites) or `--toolsets` (default `core,lifecycle`; `list_sites` always kept) exclude, then `describeTools` (titles, `_meta` `anthropic/maxResultSizeChars` on `big` tools, the `jq` parameter on big read tools), `addSiteParam`, `registerSurface` (resources, prompts, instructions).
- Instructions are set after registration (`server.WithInstructions(text)(s)`) and mention only registered tools; keep them 15-25 lines (`TestMCPInstructions`).
- Prompts make no site calls, are offered only when their `needs` tools exist, drop optional steps with `when(has(t), ...)`, put argument values in via `promptJSON`, and refuse (never cut) values over `maxLen`.
- `completion/complete` reads config and cache only (`loadSiteConfig`, never `loadSite`/`newClient`), offers only what the policy allows, at most 100 values.
- The stdio server uses `server.NewStdioServer(s).Listen(cmd.Context(), …)`; do not go back to `ServeStdio` with a context func (it loses the session: client name, capabilities, elicitation).
- Input-schema validation stays off: the arg helpers accept numeric strings and JSON-encoded strings.

## Policy and confirmation (mcp_policy.go, mcp_query_scope.go, mcp_confirm.go, mcp_audit.go)

- `config.MCPPolicy` (`sites.<name>.mcp`, unknown keys are errors) is read on every call. Config may loosen built-ins (`sensitiveDoctypes`, `deniedMethods`); flags only narrow (`mcpFlags`, passed to the daemon by `daemonArgs`). `applyEnvOverrides` and `File.PutSite` must keep the `mcp` block (dropping it widens access).
- `allow_doctypes` from the config needs `allow_methods` from the config; a flag list never stands in.
- With DocType rules, `queryScope`/`checkQueryFields` refuse joins hidden in filters, fields and order_by (`link.field`, `child.field`, backticks) and check four-element filter DocTypes; for `call_method` the whole args tree is walked.
- `needsConfirm` → `mcpPolicy.confirm` returns an elicitation; mcp-go bridges it for older clients, so the handler runs twice (audit `confirm_pending`, then `ok`/`declined`). The state is an HMAC over site, tool and args, spendable once; a retry without valid state asks again. `--confirm never` is a usage error.
- Audit: one line per call (`auditLog.write`), args redacted and reduced, caller strings sanitised and clipped; file 0600, rotated at 10 MiB.

## Multi-site (mcp_sites.go)

- `mcpEnv.sites` holds exact config names, default first. With more than one, `addSiteParam` adds a required `site` enum (except `siteless` tools) and `siteFor` refuses a call without it.
- Each site has its own cached client and mutex; only the default signs in at start.
- `env.site` refuses a Load result whose name differs from the pinned one (case-insensitive fallback must not reach another site). `FFC_API_KEY`/`SECRET` are refused with several sites.

## Transports and the detached daemon (mcp.go, mcp_daemon.go, mcp_detach_*.go)

- stdio by default; `--port` = Streamable HTTP on 127.0.0.1 only, `Authorization: Bearer <token>` required, non-local Origins rejected.
- `--detach` re-execs the binary with `daemonArgs` (no `--detach`), passes token and instance id through env (`FFC_MCP_TOKEN`, `FFC_MCP_INSTANCE`, never argv), writes `~/.config/ffc/mcp.json` (0600, holds the token) under a lock, logs to `mcp.log`, and waits for a health check with the matching instance id.
- `ffc mcp stop` health-checks the PID before terminating; `--force` stops an alive but unconfirmed PID.
- Process handling is build-tagged (`mcp_detach_unix.go` `!windows`, `mcp_detach_windows.go`): no `syscall`/`x/sys/windows` in untagged files.
- The update check is skipped for `mcp` and its subcommands: its stderr notice would pollute the stdio stream.
- The cached client is rebuilt when the stored token changes; a session client re-logs in on a stale session (see ffc-dev-client).

## jq (mcp_jq.go)

`jqArg` validates the expression in `env.run` before scope and policy. `runJQ` runs it in a child process (`os.Executable()` with only `FFC_JQ_CHILD=ffc-mcp-jq-v1` in its env, handled at the top of `Execute()` and `TestMain`), killed after 5 s, watchdog at 256 MiB heap. Never run gojq in-process for MCP: it has no memory bound. The output still goes through `marshalResult`.

## ffc mcp install / uninstall (mcp_install.go, mcp_uninstall.go, internal/mcpinstall)

- `internal/mcpinstall` stays prompt-free and cobra-free (the desktop app calls it): `Plan(client, Server, Env)` / `PlanRemove(client, name, Env)` → `Change`; the caller shows `Diff()`/`CommandLines()`, asks, then `Apply()` (re-reads, refuses if the file changed, backs up to `<path>.ffc-<YYYYMMDD-HHMMSS>.bak`, writes with `config.WriteFileAtomic` keeping the mode).
- Never edit `~/.claude.json`: claude-code changes go through `claude mcp add-json --scope user` / `claude mcp remove --scope user`; add-json never runs through a `.cmd`/`.bat` shim.
- JSON/JSONC (claude-desktop, cursor, vscode): edit the hujson tree, never `Format()` it; keep indentation, comments, trailing-comma style and strictness. Codex TOML: edit as text, never decode/re-encode; refuse inline-table `mcp_servers`, dotted keys, keys under `[mcp_servers]`.
- Never replace a file that does not parse. The command is the absolute path of the running binary (a `go run` build is refused); `--site` is pinned to `SiteConfig.Name`; a non-default config adds `--config <abs>`.
- Tests: `mcpinstall.Env` over a temp dir per GOOS; cmd tests set HOME, USERPROFILE, APPDATA, XDG_CONFIG_HOME and swap `mcpInstallEnv` (fake LookPath/Run, never the real `claude`) and `mcpInstallExecutable`.
