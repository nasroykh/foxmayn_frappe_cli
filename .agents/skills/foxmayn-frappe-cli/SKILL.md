---
name: foxmayn-frappe-cli
description: >
  How to use the ffc (Foxmayn Frappe CLI) tool to interact with Frappe ERP sites
  from the command line. Use this skill whenever the user mentions "ffc", asks to
  query Frappe data, list or get documents from ERPNext/Frappe, check Sales Invoices,
  look up customers, fetch Purchase Orders, or any task involving Frappe REST API
  queries from the terminal. Also trigger when the user has ffc installed and wants
  to automate Frappe data retrieval, pipe Frappe data into scripts, or troubleshoot
  ffc connection issues.
---

# Foxmayn Frappe CLI (ffc)

A command-line tool for querying Frappe ERP sites via the REST API. Use it to list documents, fetch individual records, and pipe structured data into scripts.

## Setup

### Install

```bash
# From source (requires Go)
make install

# Or build locally
make build
./ffc --help
```

### Configure

Run the interactive wizard:

```bash
ffc init
```

Or manually create `~/.config/ffc/config.yaml`:

```yaml
default_site: dev

sites:
  dev:
    url: "http://mysite.localhost:8000"
    api_key: "your_api_key"
    api_secret: "your_api_secret"

  production:
    url: "https://erp.company.com"
    api_key: "prod_key"
    api_secret: "prod_secret"
```

Generate API keys on the Frappe site: **User > API Access > Generate Keys**.

### Environment Variables (CI/scripts)

When no config file exists, ffc falls back to these:

```bash
export FFC_URL="https://erp.company.com"
export FFC_API_KEY="your_key"
export FFC_API_SECRET="your_secret"
```

These also override config file values when set.

## Commands

### Global Flags

Every command accepts these:

| Flag | Short | Description |
|------|-------|-------------|
| `--site` | `-s` | Select a site from config (default: `default_site`) |
| `--config` | `-c` | Custom config file path |
| `--json` | `-j` | Output raw JSON instead of a table |

### `ffc list-docs` — List documents

Query multiple records from any DocType.

```bash
# List all companies
ffc list-docs --doctype "Company"

# List users with specific fields
ffc list-docs -d "User" -f "name,email,enabled" --limit 10

# Filter open ToDos, sorted by modified date
ffc list-docs -d "ToDo" --filters '{"status":"Open"}' -o "modified desc"

# Get Sales Invoices as JSON for scripting
ffc list-docs -d "Sales Invoice" -l 5 --json
```

| Flag | Short | Required | Default | Description |
|------|-------|----------|---------|-------------|
| `--doctype` | `-d` | Yes | — | Frappe DocType to query |
| `--fields` | `-f` | No | all | Fields: `'["name","email"]'` or `name,email` |
| `--filters` | — | No | — | JSON filter: `'{"status":"Open"}'` or `'[["status","=","Open"]]'` |
| `--limit` | `-l` | No | 20 | Max records to return |
| `--order-by` | `-o` | No | — | Sort: `"modified desc"`, `"name asc"` |

### `ffc get-doc` — Get a single document

Fetch one document by its exact name.

```bash
# Get a company record
ffc get-doc --doctype "Company" --name "My Company"

# Get a user with specific fields
ffc get-doc -d "User" -n "jane@example.com" -f "name,email,enabled"

# Get as JSON
ffc get-doc -d "ToDo" -n "TDP-2024-001" --json
```

| Flag | Short | Required | Default | Description |
|------|-------|----------|---------|-------------|
| `--doctype` | `-d` | Yes | — | Frappe DocType |
| `--name` | `-n` | Yes | — | Document name (ID) |
| `--fields` | `-f` | No | all | Fields to display |

### `ffc init` — Interactive setup

Creates or updates `~/.config/ffc/config.yaml` with a guided wizard. Prompts for site name, URL, API key, and API secret. Warns before overwriting an existing config.

## Common Recipes

### Pipe JSON into jq

```bash
# Get all customer names
ffc list-docs -d "Customer" -f "name,customer_name" --json | jq '.[].customer_name'

# Count open invoices
ffc list-docs -d "Sales Invoice" --filters '{"docstatus":0}' --json | jq 'length'
```

### Query across sites

```bash
# Compare data between dev and production
ffc --site dev list-docs -d "Item" -f "name,item_name" --json > dev_items.json
ffc --site production list-docs -d "Item" -f "name,item_name" --json > prod_items.json
```

### Scripting with ffc

```bash
# Loop over invoices
for inv in $(ffc list-docs -d "Sales Invoice" -f "name" --json | jq -r '.[].name'); do
  echo "Processing $inv..."
  ffc get-doc -d "Sales Invoice" -n "$inv" --json > "invoices/$inv.json"
done
```

### Filter expressions

Frappe supports two filter formats:

```bash
# Object style (simple equality)
--filters '{"status":"Open","docstatus":1}'

# Array style (operators: =, !=, >, <, >=, <=, like, in, between, is)
--filters '[["grand_total",">",1000],["status","=","Unpaid"]]'
```

## Troubleshooting

| Error | Cause | Fix |
|-------|-------|-----|
| `authentication failed (401)` | Bad API key/secret | Regenerate keys: User > API Access |
| `permission denied (403)` | User lacks read access | Check role permissions for the DocType |
| `doctype "X" not found (404)` | Typo or module not installed | Verify the DocType name on the site |
| `no config file found` | Missing config | Run `ffc init` or set `FFC_*` env vars |
| `site "X" not found in config` | Wrong `--site` value | Check site names in config.yaml |

## Config Precedence

From lowest to highest priority:

1. Config file (`~/.config/ffc/config.yaml`)
2. `FFC_*` environment variables
3. `--site` / `--config` flags

This means you can have a base config file and override credentials per-environment using env vars, which is useful for CI pipelines and Docker deployments.
