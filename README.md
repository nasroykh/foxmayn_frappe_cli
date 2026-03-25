# ffc — Foxmayn Frappe CLI

A minimal, installable Go CLI for managing Frappe ERP sites from the command line.

## Install

**Linux & macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.sh | sh
```

Installs to `/usr/local/bin` (or `~/.local/bin` as a fallback).

**Windows (PowerShell):**

```powershell
powershell -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/main/install.ps1 | iex"
```

Works from PowerShell or `cmd.exe`. Installs to `%LOCALAPPDATA%\Programs\ffc` and adds it to your user `PATH` automatically — restart your terminal after running.

Both scripts detect your architecture (amd64/arm64), download the correct binary from the latest GitHub Release, and verify the SHA256 checksum before installing.

**Manually** — download a pre-built binary from the [Releases page](https://github.com/nasroykh/foxmayn_frappe_cli/releases), extract it, and place `ffc` somewhere on your `PATH`.

**From source:**

```bash
go install github.com/nasroykh/foxmayn_frappe_cli/cmd/ffc@latest
```

Or clone and build locally:

```bash
git clone https://github.com/nasroykh/foxmayn_frappe_cli.git
cd foxmayn_frappe_cli
make install   # installs to $GOPATH/bin and creates ~/.config/ffc/config.yaml
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
number_format: french
date_format: yyyy-mm-dd

sites:
  dev:
    url: "http://mysite.localhost:8000"
    api_key: "your_api_key"
    api_secret: "your_api_secret"

  # production: ...
```

**Settings Management (`ffc config`)**

Manage CLI settings interactively or directly from the command line:

```bash
ffc config                                          # interactive TUI
ffc config get                                      # show all settings (table)
ffc config get --json                               # show as JSON
ffc config get --yaml                               # show as YAML
ffc config set --default-site prod                  # set default site
ffc config set --number-format us --date-format dd/mm/yyyy
```

*   **Number Formats:** `french` (default: 1 000 000,00), `us`, `german`, `plain`.
*   **Date Formats:** `yyyy-mm-dd` (ISO), `dd-mm-yyyy` (European), `dd/mm/yyyy` (Euro Slash), `mm/dd/yyyy` (US).

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

### Basic Setup & Settings

*   **`init`**: Interactive setup wizard — creates your initial config.
*   **`config`**: Interactive TUI to tweak settings, or non-interactive via subcommands:
    *   `ffc config get [--json|--yaml]` — print all settings
    *   `ffc config set --default-site <name> --number-format <fmt> --date-format <fmt>` — update settings
*   **`ping`**: Quickly check connection to the active Frappe site.

---

### Document Operations (CRUD)

**1. `get-doc`** (Read a document)
```bash
ffc get-doc -d "Company" -n "My Company"
ffc get-doc -d "User" -n "jane@example.com" -f '["name","email"]'
```

**2. `list-docs`** (List documents)
```bash
ffc list-docs -d "ToDo" --filters '{"status":"Open"}' -o "modified desc"
```

**3. `create-doc`** (Create a document)
```bash
ffc create-doc -d "ToDo" --data '{"description":"Update CLI README","status":"Open"}'
```

**4. `update-doc`** (Update a document)
```bash
ffc update-doc -d "ToDo" -n "83a12bf99c" --data '{"status":"Closed"}'
```

**5. `delete-doc`** (Delete a document)
```bash
ffc delete-doc -d "ToDo" -n "83a12bf99c" --yes
```
*(The `--yes` / `-y` flag skips the interactive confirmation prompt).*

**6. `count-docs`** (Count documents)
```bash
ffc count-docs -d "Sales Invoice" --filters '{"status":"Unpaid"}'
```

---

### Schema & Introspection

**1. `list-doctypes`** (List available DocTypes)
```bash
ffc list-doctypes --module "Accounts"
```

**2. `get-schema`** (View DocType fields and structure)
```bash
ffc get-schema -d "Sales Invoice"
```

---

### RPC calling

**`call-method`** (Execute a whitelisted server script)
```bash
ffc call-method --method "frappe.ping"
ffc call-method --method "my_app.api.custom_action" --args '{"user":"john"}'
```

---

### Reports

**1. `list-reports`** (List available query and script reports)
```bash
ffc list-reports --module "Accounts"
```

**2. `run-report`** (Execute a report)
```bash
ffc run-report -n "General Ledger" --filters '{"company":"Acme","from_date":"2026-01-01"}' -l 10
```
*(The `--limit` / `-l` flag truncates long report outputs in the terminal).*

---

## Project Structure

```text
foxmayn_frappe_cli/
├── cmd/ffc/main.go           # Entry point
├── internal/
│   ├── cmd/                  # Cobra command definitions
│   │   ├── root.go           # Root command + global flags
│   │   ├── init.go           # Interactive setup wizard
│   │   ├── config_cmd.go     # Interactive settings menu
│   │   ├── ping.go           # ping
│   │   ├── get_doc.go        # get-doc
│   │   ├── list_docs.go      # list-docs
│   │   ├── create_doc.go     # create-doc
│   │   ├── update_doc.go     # update-doc
│   │   ├── delete_doc.go     # delete-doc
│   │   ├── count_docs.go     # count-docs
│   │   ├── get_schema.go     # get-schema
│   │   ├── list_doctypes.go  # list-doctypes
│   │   ├── list_reports.go   # list-reports
│   │   ├── run_report.go     # run-report
│   │   └── call_method.go    # call-method
│   ├── client/client.go      # Frappe REST API client methods (resty)
│   ├── config/config.go      # Config loading and number/date formatting logic
│   ├── output/output.go      # Table (lipgloss) and JSON formatters
│   └── version/version.go    # Build-time version injection
├── config.example.yaml       # Example config
├── Makefile                  # Build, install, deps, tidy, vet, fmt
├── .goreleaser.yaml          # Cross-compilation and release config
├── install.sh                # One-liner install script (Linux/macOS)
├── install.ps1               # One-liner install script (Windows PowerShell)
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
