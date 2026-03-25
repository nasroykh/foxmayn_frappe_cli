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
internal/cmd/init.go         → init subcommand (huh form wizard)
internal/cmd/config_cmd.go   → config subcommand: TUI (no args), config get, config set
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
internal/client/             → Frappe REST API client (auth, request building, error parsing)
internal/config/             → Config loading: YAML file (~/.config/ffc/config.yaml) + FFC_* env vars
internal/output/             → Formatters: lipgloss table and JSON
internal/version/            → Build-time version variables (ldflags)
```

## Conventions

- **Error handling:** Wrap with `fmt.Errorf("context: %w", err)`. Never log and return; return and let caller decide.
- **Stdout vs stderr:** Data goes to stdout, diagnostics/errors go to stderr.
- **Config precedence:** flags > env vars > config file > defaults.
- **Auth:** Frappe token auth (`Authorization: token key:secret`). Both api_key and api_secret required.
- **Adding commands:** Create `internal/cmd/<name>.go`, define `*cobra.Command`, register via `rootCmd.AddCommand()` in `init()`.

## Config

Default config path: `~/.config/ffc/config.yaml`

Env var fallback: `FFC_URL`, `FFC_API_KEY`, `FFC_API_SECRET`

## Common Pitfalls

- The `config.yaml` in the project root is gitignored — it's for local dev only. Do not commit credentials.
- Frappe API wraps list results in `"data"` (v14+) or `"message"` (older). The client handles both.
- Frappe error responses contain nested JSON strings with Python tracebacks. The `parseFrappeError` function extracts user-friendly messages from this noise.
- `Config` and `SiteConfig` structs carry **both** `mapstructure` and `yaml` struct tags. `mapstructure` is needed by viper; `yaml` is needed by `go.yaml.in/yaml/v3` for direct unmarshal in `config_cmd.go`. Always add both when extending these structs.
- `config_cmd.go` has a `saveConfig` helper (marshals YAML node → file). `init.go` has its own `writeConfig` (generates fresh YAML from scratch). The names are intentionally different to avoid a package-level collision.
- In huh v1.0.0, only `ctrl+c` is bound to Quit by default — Escape does nothing. All `huh.NewForm` calls in `config_cmd.go` use `WithKeyMap(escQuitKeyMap())` to add Escape support.
