---
name: ffc-dev
description: Entry point for changing the ffc (Foxmayn Frappe CLI) Go codebase in the foxmayn_frappe_cli repository - build, lint and test commands, the rules that bite most often, release and versioning, and which focused ffc-dev-* skill to load next. Use it for any code change, bug fix, refactor or review inside this repo (internal/cmd, internal/client, internal/config, internal/mcpinstall, desktop/, Makefile, CI), even a small one. For using ffc against a site, use ffc-core instead.
---

# Developing ffc

**CLAUDE.md at the repository root is the authoritative guide** (architecture map, conventions, every pitfall with Frappe source references). Read the parts that touch your change. This skill is the short path through it.

## Load the focused skill

| Change | Skill |
| --- | --- |
| New or changed CLI command, flag, output, prompt | ffc-dev-command |
| New or changed MCP tool, policy, MCP surface, `mcp install` | ffc-dev-mcp-tool |
| HTTP client, auth, OAuth, retries, redirects, config writes, site setup | ffc-dev-client |
| Tests: fake Frappe, CLI harness, MCP tests, contract tests | ffc-dev-testing |
| The desktop app in `desktop/` | ffc-dev-desktop |

## Build and verify

```bash
make fmt && make lint && make test && make build   # before every commit
make vuln                                          # govulncheck (pinned, go run)
make contract SITE=<config site>                   # real, disposable site only: it writes test data
go build -o /tmp/ffc ./cmd/ffc && /tmp/ffc <cmd> --help   # check help text after flag changes
```

`make lint` = gofmt check + `go vet` + staticcheck (pinned, via `go run`). `make test` = `go test -race ./...`. CI also runs vet and tests on Windows (no `-race`), a tidy check and a cross-build. The root `go test ./...` skips the nested `desktop/` module.

## Rules that bite (details in CLAUDE.md "Common Pitfalls")

1. A command or MCP tool gets its client only through `callSite`/`newClient` (site_client.go), never `client.New`: that is where OAuth refresh and session logout happen.
2. Every config write goes through `config.Edit` / `config.Overwrite` (lock, re-read, atomic 0600). Never `os.WriteFile` or marshal the struct.
3. Data on stdout, diagnostics on stderr. Output through `render`/`printResult`, never `output.PrintJSON` directly, so `--output` and `--jq` work.
4. Return typed errors where the exit code matters (`usageErrorf`, `client.APIError`, `client.StateError`, `partialError`); exit codes are a contract (`exit.go`).
5. No prompts without a terminal: prompts go through `runForm`/`confirm`; writes get `--dry-run` via `addDryRun` and skip their confirmation under `dryRunOn(cmd)`.
6. MCP handlers never write to stdout (it is the JSON-RPC channel) and every tool is registered in `toolActions` and `toolSurface`.
7. Only GET is retried or follows redirects. Never add retries to writes.
8. `rootCmd.PersistentPreRunE` belongs to update_check.go; add pre-run logic inside it, never reassign it.
9. `-o` is a local shorthand only (`list-docs --order-by`, `--output-file`); never make it persistent.
10. Numbers are `json.Number` (decoded with UseNumber); handle that when inspecting values.
11. Never print secrets: use `redactedURL` and the redaction helpers in client/debug.go for anything that logs requests.
12. Keep `syscall`/`x/sys/windows` code in the build-tagged files (`mcp_detach_unix.go`, `mcp_detach_windows.go`).

## Conventions

- Errors: `fmt.Errorf("context: %w", err)`; never log and return.
- Commit messages follow conventional commits, as in `git log` (`feat(desktop): ...`, `fix(release): ...`, `refactor(sitecache): ...`).
- User-facing change: update the command's `Long` help, README.md, CLAUDE.md (architecture line or pitfall) and the matching user skill in `skills/` (ffc-core, ffc-bulk-lifecycle, ffc-reports-api, ffc-files-collab, ffc-setup, ffc-mcp).

## Shared code to reuse, not duplicate

CLI and MCP share one function per feature; extend it instead of writing a second path.

- Schema: `fetchSchema` (get_schema.go) merges Custom Fields (`mergeCustomFields`, by `insert_after`) and applies Property Setters (`applyPropertySetters`); `compactSchema` is the default `--json` view (a JSON contract). Problems become warnings, not failures.
- Self-update: `internal/release` finds (`Latest`, `v<digit>` tags only), downloads and verifies (relsig signature, then SHA-256) a release; `update.go` only confirms and swaps the binary. The desktop installer uses the same package.
- Others: `checkPermission` (can), `buildWhoami`, `fetchDocContext`/`compactDocContext` (doc-info), `buildAggregateQuery`/`runAggregate`, `runSearch`, the collab functions in collab.go. CLAUDE.md "Architecture" lists them by file.

## Release and versions

- CLI: `git tag vX.Y.Z && git push origin vX.Y.Z`; GoReleaser cross-compiles, signs `checksums.txt` (Ed25519, `tools/relsign`) and attests. `ffc update` refuses a release without a valid signature; install.sh checks it when OpenSSL 3 is available; install.ps1 checks SHA-256 only. Public keys live in `internal/relsig/keys.go` and `RELEASE_KEYS` in install.sh (kept in sync by a test). Key rotation steps: CLAUDE.md "Release".
- Version info comes from ldflags into `internal/version`.
- `go.mod`: `go` line one release behind the `toolchain` line; bump the toolchain with each Go patch release and run `make vuln`.
- Desktop releases use `desktop-v*` tags and must never be GitHub's "latest" (see ffc-dev-desktop).
