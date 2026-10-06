---
name: ffc-dev-testing
description: Write and run tests for the ffc Go codebase - the in-memory fake Frappe site (internal/frappetest), the CLI harness (fakeConfig, runFFC), MCP test helpers (newMCPFake, callTool, mcpTClient), the guard tests that must keep passing, and the contract tests against a real Frappe v15/v16 site (make contract). Use it whenever you add or change a command, MCP tool, client call or config write in foxmayn_frappe_cli, when a test fails, or when you need to prove how real Frappe behaves. Load ffc-dev first.
---

# Testing ffc

Every new command or MCP tool ships with tests against the fake. Behaviour the fake cannot prove (real Frappe semantics) goes in the contract tests. Run `make test` (`go test -race ./...`) before you finish.

## Fake Frappe site (internal/frappetest)

`s := frappetest.New(t)` starts an in-memory site (closed when the test ends) at `s.URL`. It accepts the credentials in `frappetest.APIKey`/`APISecret`, `Username`/`Password` and `Token`/`RefreshToken`/`OAuthClientID`, and answers with Frappe's real error shapes (`exc_type`, `_server_messages`, `exception`).

| Need | Call |
| --- | --- |
| Data | `s.AddDocType(dt, fields...)`, `s.Add(dt, docs...)`, `s.Doc(dt, name)`, `s.Count(dt)`, `s.AddReport(name, result)` |
| A whitelisted method | `s.HandleMethod("dotted.path", func(r *http.Request, args map[string]interface{}) (interface{}, error) {...})`; return `frappetest.Response{...}` for methods that answer outside `message`; `HandleMethod(name, nil)` removes one |
| Override a route | `s.Handle("POST /api/resource/ToDo", h)` with `frappetest.ErrorHandler(frappetest.Validation("..."))` or `frappetest.HTMLPage(502)`; error builders `NotFound`, `Validation`, `DataError`, `Permission`, `Duplicate`, `LinkExists` |
| What was sent | `s.Requests()`, `s.RequestsTo(method, path)`, `s.Logins()`, `s.Logouts()` |
| Version and user | `s.SetApps(map[string]frappetest.App{...})` (aggregates and v16-only methods follow the frappe major), `s.SetUser(user, roles...)`, `s.Deny(dt, ptypes...)`, `s.DocPerm(dt, row)`, `s.DocTypeFlags`, `s.DisableV2()` |
| Meta | `s.DocField`, `s.Permlevel`, `s.FieldProp`, `s.NoCopy`, `s.ChildTable`, `s.AllowOnSubmit` |
| OAuth | `s.ExpireToken`, `s.ExpireTokenAfter`, `s.FailRefresh`, `s.Refreshes()`, `s.SetAuthServerMetadata`, `s.SetDynamicRegistration`, `s.FailRegistration(status)`, `s.Registrations()`, `s.Revoked()` |
| Collaboration, context | `s.DenyUser(dt, users...)`, `s.Dashboard`, `s.Onload` |
| Files | `s.File(url)`, `s.SetMaxFileSize(n)`, `s.SetPrintHTML(html, style)` |
| Search, aggregates | `s.SearchFields`, `s.GlobalSearch`, `s.Postgres()` (ignores ORDER BY on grouped v16 queries) |

When the fake disagrees with a real site, fix the fake and pin the real behaviour in a contract test.

## CLI harness (internal/cmd/cli_harness_test.go)

```go
s := frappetest.New(t)
cfg := fakeConfig(t, s, "apikey") // or "password", "oauth"; default site "t", plus site "other"
r := runFFC(t, cfg, "", "get-doc", "-d", "ToDo", "-n", "TD-1", "--json")
if r.Code != 0 { t.Fatalf("exit %d: %s", r.Code, r.Stderr) }
```

- `cliResult{Stdout, Stderr, Err, Code}`; `runFFCCtx` takes a context; set `cliEnv` for extra env vars (reset it to nil). Every flag is reset after a run.
- `TestMain` clears `FFC_SITE`/`FFC_CONFIG`/`FFC_TIMEOUT`, points `sitecache.UserCacheDir` at a temp dir and handles the jq child. Never touch the real cache or config.
- Files keep their own short helpers with a file prefix (`cmdTRun`, `edTEditor`, `cacheTEnv`). Reuse them; new helpers get the new file's prefix.
- edit-doc tests swap `runEditor` and `editInputDisabled`; `ffc mcp install` tests set HOME, USERPROFILE, APPDATA, XDG_CONFIG_HOME and swap `mcpInstallEnv`/`mcpInstallExecutable` (never run the real `claude`).

## MCP helpers

- `srv, site := newMCPFake(t, readOnly)` registers the tools against a fake site.
- `callTool(t, srv, "list_docs", map[string]interface{}{...})` goes through JSON-RPC decoding, so native vs string JSON arguments behave as with a real client.
- `mcpTClient(t, srv, asker, legacy)` (mcp_confirm_test.go) is an in-process client for confirmation flows; `legacy` uses protocol `2025-11-25` to exercise the elicitation bridge.

## Guard tests that must keep passing

| Test | Fails when |
| --- | --- |
| `TestMCPPolicyCoversEveryTool` | a tool lacks a `toolActions` entry or its read-only annotation disagrees |
| `TestMCPToolSurface` | a tool lacks a `toolSurface` entry |
| `TestMCPInstructions` | the instructions leave the 15-25 line range, lose a pinned phrase, or show read-only lines on a writable server (and the reverse) |
| `TestDebugNeverPrintsSecrets` | `--debug` output leaks a credential for any auth method |
| `TestReleaseKeysConfigured` | `internal/relsig` has no release key |

## Contract tests (real Frappe)

- File `internal/cmd/contract_test.go` (and `contract_*_test.go`), build tag `contract`.
- Run against a disposable site you may write to: `make contract SITE=<site in your ffc config>`, or `FFC_CONTRACT_URL` with `FFC_CONTRACT_API_KEY`+`FFC_CONTRACT_API_SECRET` (or `FFC_CONTRACT_USER`+`FFC_CONTRACT_PASSWORD`) and `go test -tags contract -count=1 -run TestContract -v ./internal/cmd/`.
- Each run creates the submittable DocType `FFC Contract Test` (child, Custom Field, Property Setter) and removes it, leftovers included.
- `.github/workflows/contract.yml` runs them nightly against ERPNext v15 and v16.
- Add a contract case when your change relies on how Frappe answers (number literals, child-table PUT, submit semantics, version differences, permissions).
