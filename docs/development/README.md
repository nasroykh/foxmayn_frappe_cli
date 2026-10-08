# Development

Build ffc from source, find your way around the code, and contribute changes.

## Build from source

Needs Go (the `go` line in `go.mod` is the minimum; the `toolchain` line is what CI uses, and Go downloads it automatically) and `make` on Linux/macOS.

```bash
git clone https://github.com/nasroykh/foxmayn_frappe_cli.git
cd foxmayn_frappe_cli
make build        # ./bin/ffc, with version info from git
./bin/ffc --version
```

On Windows without `make`:

```bash
go build -o ffc.exe ./cmd/ffc
```

| Target | What it does |
| --- | --- |
| `make build` | Compile to `./bin/ffc` with the version, commit and date injected. |
| `make install` | `go install` to `$GOPATH/bin`, and create `~/.config/ffc/config.yaml` (0600) from `config.example.yaml` if it does not exist. |
| `make test` | `go test -race ./...` |
| `make lint` | gofmt check, `go vet`, staticcheck (pinned version). |
| `make vuln` | govulncheck (pinned version). |
| `make contract SITE=<site>` | Contract tests against a real, disposable site. See [Testing](testing.md). |
| `make tidy`, `make vet`, `make fmt` | `go mod tidy`, `go vet ./...`, `gofmt -w .` |
| `make clean` | Remove `./bin/ffc`. |
| `make skills-init` | Link the agent skills in `skills/` into `.claude/`, `.cursor/` and `.agent/`. |

## Project layout

```text
cmd/ffc/main.go          entry point
internal/cmd/            cobra commands and the MCP server (one file per command; mcp_*.go)
internal/client/         Frappe REST client: auth, retries, redirects, files, OAuth, --debug redaction
internal/config/         config.yaml loading, env overrides, locked atomic edits, formats
internal/sitesetup/      prompt-free site setup: URL checks, credential check, OAuth flow and registration
internal/mcpinstall/     editing AI clients' MCP configs (JSON/JSONC, Codex TOML, claude CLI)
internal/release/        finding, downloading and verifying releases (ffc update, desktop installer)
internal/relsig/         Ed25519 signing of checksums.txt; trusted release keys
internal/sitecache/      local cache layout (shared with the desktop app)
internal/output/         tables and machine formats
internal/text/           sanitising server text for the terminal
internal/frappetest/     in-memory fake Frappe site for tests
internal/version/        build-time version variables
tools/relsign/           release key generation, signing, verification (not shipped)
tools/gendocs/           completion scripts and man pages for the release archives (not shipped)
desktop/                 Foxmayn Frappe Desktop (separate Go module; see desktop.md)
skills/                  agent skills for ffc users and contributors
```

`CLAUDE.md` at the repository root holds the detailed architecture notes, conventions and Frappe behaviour the code relies on. Read it before larger changes.

## Conventions

- Data on stdout, diagnostics on stderr. Commands print results through the shared render layer so `--output` and `--jq` work everywhere.
- Every command that talks to a site gets its client through the shared helpers (so OAuth refresh and session logout happen); never build a client directly.
- Exit codes are part of the CLI contract; return the typed errors that map to them.
- No prompts without a terminal: prompts go through the shared form helper, which fails with a usage error under `--no-input`.
- A command that writes registers `--dry-run` and skips its confirmation in a dry run.
- Config writes always go through the locked, atomic editor; never write `config.yaml` directly.
- Every new command or MCP tool ships with tests against the fake site; Frappe behaviour the fake cannot prove goes in the contract tests.

## Adding a command

1. Create `internal/cmd/<name>.go` with a `*cobra.Command` (`Args: cobra.NoArgs` unless it takes positionals).
2. Register it with `rootCmd.AddCommand` in `init()`.
3. Use `callSite` for the request and `render` for the output.
4. If it writes, call `addDryRun` and skip the confirmation when `dryRunOn(cmd)`.
5. Add tests with the CLI harness and the fake site ([Testing](testing.md)).

New flags named `doctype` and `fields` get shell completion automatically.

## Contributing

- Branch from `main`; keep pull requests focused. CI must pass: gofmt, `go mod tidy -diff`, vet, race tests, staticcheck, govulncheck, tests on Windows, and a cross-build.
- Commit messages follow Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `ci:`, ...). `docs:`, `test:` and `chore:` commits are left out of release changelogs.
- Update the docs in `docs/` when behaviour changes.
- License: MIT.

## See also

- [Testing](testing.md)
- [Releasing](releasing.md)
- [Desktop app development](desktop.md)
