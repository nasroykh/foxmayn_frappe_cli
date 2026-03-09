# ffc — Foxmayn Frappe CLI

A minimal, installable Go CLI for managing a Frappe ERP site from the command line.

## Install

```bash
# from the project root (inside your bench or anywhere with Go installed)
make install
```

This runs `go install ./cmd/ffc` which places the `ffc` binary in your `$GOPATH/bin`.

Alternatively, build a local binary:

```bash
make build
./ffc --help
```

## First-time Setup

Copy the example config and fill in your site details:

```bash
mkdir -p ~/.config/ffc
cp config.example.yaml ~/.config/ffc/config.yaml
$EDITOR ~/.config/ffc/config.yaml
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
```

**Environment variable overrides** (useful in CI):

| Variable        | Overrides                        |
|-----------------|----------------------------------|
| `FFC_URL`       | site URL                         |
| `FFC_API_KEY`   | API key                          |
| `FFC_API_SECRET`| API secret                       |

## Usage

```
ffc [--site <name>] [--config <path>] [--json] <command> [flags]
```

### Global flags

| Flag       | Short | Description                                |
|------------|-------|--------------------------------------------|
| `--site`   | `-s`  | Site name from config (default: default_site) |
| `--config` | `-c`  | Config file path                           |
| `--json`   | `-j`  | Print raw JSON instead of a table          |

---

### `list-docs`

List documents of a DocType.

```bash
ffc list-docs --doctype "Company"
ffc list-docs --doctype "User" --fields '["name","email","enabled"]' --limit 10
ffc list-docs --doctype "ToDo" --filters '{"status":"Open"}' --order-by "modified desc"
ffc list-docs --doctype "Sales Invoice" --limit 5 --json
```

| Flag          | Short | Required | Default | Description                                      |
|---------------|-------|----------|---------|--------------------------------------------------|
| `--doctype`   | `-d`  | ✅       | —       | Frappe DocType to query                          |
| `--fields`    | `-f`  | ❌       | `name`  | JSON array or CSV: `'["name","email"]'` or `name,email` |
| `--filters`   | —     | ❌       | —       | JSON filter: `'{"status":"Open"}'`               |
| `--limit`     | `-l`  | ❌       | 20      | Max records                                      |
| `--order-by`  | `-o`  | ❌       | —       | Order field, e.g. `"modified desc"`              |

---

## Development

```bash
# Install dependencies
make tidy

# Build
make build

# Verify help
./ffc --help
./ffc list-docs --help
```

## Adding New Commands

1. Create `internal/cmd/<command_name>.go`
2. Define a `*cobra.Command` variable
3. In `init()`, call `rootCmd.AddCommand(yourCmd)`

That's it. The global `siteName`, `configPath`, and `jsonOutput` flags are available package-wide.
