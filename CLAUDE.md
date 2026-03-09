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
make build          # Compile binary to ./ffc
make install        # Install to $GOPATH/bin + set up config
make tidy           # go mod tidy
make vet            # go vet ./...
make fmt            # gofmt -w .
make clean          # Remove binary
```

Version info is injected at build time via ldflags (see Makefile).

## Architecture

```
cmd/ffc/main.go         → Entry point, calls cmd.Execute()
internal/cmd/root.go    → Root cobra command, global flags (--site, --config, --json)
internal/cmd/init.go    → init subcommand (huh form wizard)
internal/cmd/get_doc    → get-doc subcommand
internal/cmd/list_docs  → list-docs subcommand
internal/client/        → Frappe REST API client (auth, request building, error parsing)
internal/config/        → Config loading: YAML file (~/.config/ffc/config.yaml) + FFC_* env vars
internal/output/        → Formatters: ASCII table (tablewriter) and JSON
internal/version/       → Build-time version variables (ldflags)
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
