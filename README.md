<div align="center">
  <img width="150" height="150" alt="logo-foxmayn" src="https://github.com/user-attachments/assets/fa9f3727-dd5c-4748-92e9-f527a740366a" />
</div>

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

Use the interactive setup wizard — it creates `~/.config/ffc/config.yaml`:

```bash
ffc init
```

The wizard lets you choose between two authentication methods:

- **OAuth 2.0** (`--oauth`) — browser login, PKCE flow, no credentials stored. You create an OAuth Client on Frappe once and authorize via the browser.
- **API Key** (`--apikey`) — paste your API key and secret from **User → API Access → Generate Keys**.

```bash
ffc init            # menu to choose auth method
ffc init --oauth    # go straight to the OAuth browser flow
ffc init --apikey   # go straight to the API key form
```

## Configuration

**`~/.config/ffc/config.yaml`**

API key authentication:

```yaml
default_site: dev
number_format: french
date_format: yyyy-mm-dd

sites:
  dev:
    url: "http://mysite.localhost:8000"
    api_key: "your_api_key"
    api_secret: "your_api_secret"
```

OAuth 2.0 authentication (tokens are written automatically by `ffc init --oauth`):

```yaml
default_site: dev

sites:
  dev:
    url: "https://mysite.example.com"
    oauth_client_id: "your_client_id"
    oauth_client_secret: "your_client_secret"   # omit for public clients
    access_token: "..."
    refresh_token: "..."
    token_expiry: 1234567890
```

OAuth access tokens are refreshed automatically before every command when they expire — you don't need to re-run `ffc init`.

**Site Management (`ffc site`)**

Add, list, remove, or switch between sites without touching the config file manually:

```bash
ffc site list                   # show all configured sites
ffc site add                    # add a new site (menu to choose auth method)
ffc site add --oauth            # add a new site via OAuth browser flow
ffc site add --apikey           # add a new site via API key form
ffc site use [name]             # set the default site (interactive menu if name omitted)
ffc site remove [name]          # remove a site (interactive menu if name omitted)
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

*   **`init`**: Interactive setup wizard — creates your initial config. Choose between OAuth 2.0 browser flow (`--oauth`) or API key/secret (`--apikey`). Auto-adds `https://` if you omit the scheme.
*   **`site`**: Manage multiple Frappe sites without editing the config file:
    *   `ffc site list` — show all configured sites (name, URL, auth method, default)
    *   `ffc site add [--oauth|--apikey]` — add a new site interactively
    *   `ffc site use [name]` — set the default site (shows selection menu if name omitted)
    *   `ffc site remove [name]` — remove a site (shows selection menu if name omitted)
*   **`config`**: Interactive TUI to tweak settings, or non-interactive via subcommands:
    *   `ffc config get [--json|--yaml]` — print all settings
    *   `ffc config set --default-site <name> --number-format <fmt> --date-format <fmt>` — update settings
*   **`ping`**: Quickly check connection to the active Frappe site.
*   **`update`**: Update ffc to the latest release in place — works regardless of how it was installed.

```bash
ffc update           # check for update and confirm before installing
ffc update --check   # only print whether an update is available
ffc update --yes     # update without confirmation
```

ffc also checks for updates automatically (at most once a day) and prints a one-line notice to stderr when a newer version is available.

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

`--json` returns a **compact view** by default — only meaningful DocType properties and field attributes (zero-value noise and metadata are stripped). Use `--full` for the raw Frappe response, or `--keys` to select specific top-level keys. Custom fields added via Customize Form are included automatically.

```bash
ffc get-schema -d "Sales Invoice"
ffc get-schema -d "Sales Invoice" --json
ffc get-schema -d "Sales Invoice" --json --full
ffc get-schema -d "Sales Invoice" --json --keys fields
ffc get-schema -d "Sales Invoice" --json --keys name,module,fields
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

### MCP Server (AI Agent Integration)

**`mcp`**: Start an MCP (Model Context Protocol) server so AI agents and LLMs can interact with your Frappe site directly.

**Stdio mode** — use this in your MCP client config (Claude Desktop, Cursor, etc.):
```bash
ffc mcp --site mysite
```

**HTTP mode** — foreground, useful for testing with the MCP Inspector:
```bash
ffc mcp --port 8765 --site mysite
```

**Detached mode** — background HTTP server, doesn't block the terminal:
```bash
ffc mcp --detach [--port 8765] [--site mysite]
ffc mcp status   # show PID, URL, uptime, log path
ffc mcp stop     # send SIGTERM and clean up
```

The HTTP endpoint is `http://localhost:<port>/mcp` (Streamable HTTP transport).

Available MCP tools: `ping`, `get_doc`, `list_docs`, `create_doc`, `update_doc`, `delete_doc`, `count_docs`, `get_schema`, `list_doctypes`, `list_reports`, `run_report`, `call_method`.

**Example Claude Desktop config:**
```json
{
  "mcpServers": {
    "frappe": {
      "command": "ffc",
      "args": ["mcp", "--site", "mysite"]
    }
  }
}
```

---

## Project Structure

```text
foxmayn_frappe_cli/
├── cmd/ffc/main.go           # Entry point
├── internal/
│   ├── cmd/                  # Cobra command definitions
│   │   ├── root.go           # Root command + global flags
│   │   ├── init.go           # Interactive setup wizard (API key or OAuth)
│   │   ├── oauth_flow.go     # OAuth PKCE flow, token refresh, site YAML helpers
│   │   ├── site.go           # ffc site list/add/remove/use
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
│   │   ├── call_method.go    # call-method
│   │   ├── update.go         # update (self-update)
│   │   ├── update_check.go   # background update check + PersistentPreRunE
│   │   ├── mcp.go            # mcp subcommand (stdio/HTTP/detach modes)
│   │   ├── mcp_tools.go      # 12 MCP tool definitions + handlers
│   │   ├── mcp_daemon.go     # detach logic, status/stop subcommands, state file
│   │   ├── mcp_detach_unix.go    # setSysProcAttr (Setsid, Linux/macOS)
│   │   └── mcp_detach_windows.go # setSysProcAttr no-op (Windows)
│   ├── client/
│   │   ├── client.go         # Frappe REST API client (resty, Bearer + token auth)
│   │   └── oauth.go          # ExchangeOAuthCode, RefreshOAuthToken, GetOAuthUser
│   ├── config/config.go      # Config loading, OAuth fields, number/date formatting
│   ├── output/output.go      # Table (lipgloss) and JSON formatters
│   └── version/version.go    # Build-time version injection
├── config.example.yaml       # Example config
├── Makefile                  # Build, install, tidy, vet, fmt
├── .goreleaser.yaml          # Cross-compilation and release config
├── install.sh                # One-liner install script (Linux/macOS)
├── install.ps1               # One-liner install script (Windows PowerShell)
├── go.mod
└── go.sum
```

## Development

```bash
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
