# ffc — Foxmayn Frappe CLI

A minimal, installable Go CLI for managing Frappe ERP sites from the command line.

## Install

Install from source:

```bash
go install github.com/nasroykh/foxmayn_frappe_cli/cmd/ffc@latest
```

Or clone the repo then:

```bash
cd foxmayn_frappe_cli

# Install binary to $GOPATH/bin (or ~/go/bin)
make install
```

Or build a local binary:

```bash
make build
./bin/ffc --help
```

## First-time Setup

Copy the example config and fill in your site details:

```bash
mkdir -p ~/.config/ffc
cp config.example.yaml ~/.config/ffc/config.yaml
$EDITOR ~/.config/ffc/config.yaml
```

Or use the interactive setup wizard:

```bash
ffc init
```

Generate API keys on your Frappe site: **User → API Access → Generate Keys**.

## Configuration

**`~/.config/ffc/config.yaml`**

```yaml
default_site: dev

sites:
  dev:
    url: "http://mysite.localhost:8000"
    api_key: "your_api_key"
    api_secret: "your_api_secret"

  # production:
  #   url: "https://erp.company.com"
  #   api_key: "prod_api_key"
  #   api_secret: "prod_api_secret"
```

**Environment variable overrides** (useful in CI):

| Variable         | Overrides  |
| ---------------- | ---------- |
| `FFC_URL`        | Site URL   |
| `FFC_API_KEY`    | API key    |
| `FFC_API_SECRET` | API secret |

When no config file exists, `ffc` falls back to these env vars entirely.

## Usage

```
ffc [--site <name>] [--config <path>] [--json] <command> [flags]
```

### Global Flags

| Flag        | Short | Description                                     |
| ----------- | ----- | ----------------------------------------------- |
| `--site`    | `-s`  | Site name from config (default: `default_site`) |
| `--config`  | `-c`  | Config file path                                |
| `--json`    | `-j`  | Print raw JSON instead of a table               |
| `--version` | `-v`  | Print version information                       |

---

### `init`

Interactive setup wizard — creates `~/.config/ffc/config.yaml`.

```bash
ffc init
```

You will be prompted for the site name, URL, API key, and API secret. If a config file already exists, you will be asked to confirm before overwriting.

---

### `get-doc`

Get a single document by name.

```bash
ffc get-doc --doctype "Company" --name "My Company"
ffc get-doc -d "User" -n "jane@example.com" --fields '["name","email","enabled"]'
ffc get-doc -d "ToDo" -n "TDP-2024-001" --json
```

| Flag        | Short | Required | Default | Description                                             |
| ----------- | ----- | -------- | ------- | ------------------------------------------------------- |
| `--doctype` | `-d`  | ✅       | —       | Frappe DocType                                          |
| `--name`    | `-n`  | ✅       | —       | Name (ID) of the document                               |
| `--fields`  | `-f`  | ❌       | all     | JSON array or CSV: `'["name","email"]'` or `name,email` |

---

### `list-docs`

List documents of a DocType.

```bash
ffc list-docs --doctype "Company"
ffc list-docs --doctype "User" --fields '["name","email","enabled"]' --limit 10
ffc list-docs --doctype "ToDo" --filters '{"status":"Open"}' --order-by "modified desc"
ffc list-docs --doctype "Sales Invoice" --limit 5 --json
```

| Flag         | Short | Required | Default | Description                                             |
| ------------ | ----- | -------- | ------- | ------------------------------------------------------- |
| `--doctype`  | `-d`  | ✅       | —       | Frappe DocType to query                                 |
| `--fields`   | `-f`  | ❌       | all     | JSON array or CSV: `'["name","email"]'` or `name,email` |
| `--filters`  | —     | ❌       | —       | JSON filter: `'{"status":"Open"}'`                      |
| `--limit`    | `-l`  | ❌       | 20      | Max records                                             |
| `--order-by` | `-o`  | ❌       | —       | Order field, e.g. `"modified desc"`                     |

---

## Project Structure

```
foxmayn_frappe_cli/
├── cmd/ffc/main.go           # Entry point
├── internal/
│   ├── cmd/                  # Cobra command definitions
│   │   ├── root.go           # Root command + global flags
│   │   ├── init.go           # init subcommand (huh form wizard)
│   │   ├── get_doc.go        # get-doc subcommand
│   │   └── list_docs.go      # list-docs subcommand
│   ├── client/client.go      # Frappe REST API client (resty)
│   ├── config/config.go      # Config loading (viper + env vars)
│   ├── output/output.go      # Table (lipgloss) and JSON formatters
│   └── version/version.go    # Build-time version injection
├── config.example.yaml       # Example config
├── Makefile                  # Build, install, deps, tidy, vet, fmt
├── go.mod
└── go.sum
```

## Development

```bash
make deps       # Install Charmbracelet dependencies
make tidy       # Install/update all dependencies
make build      # Compile binary
make vet        # Run go vet
make fmt        # Format code with gofmt
make clean      # Remove compiled binary
```

## Adding New Commands

1. Create `internal/cmd/<command_name>.go`
2. Define a `*cobra.Command` variable
3. In `init()`, call `rootCmd.AddCommand(yourCmd)`

The global `siteName`, `configPath`, and `jsonOutput` flags are available package-wide.

## License

MIT
