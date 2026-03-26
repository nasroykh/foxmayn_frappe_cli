---
name: foxmayn-frappe-cli
description: >
  How to use the ffc (Foxmayn Frappe CLI) tool to interact with Frappe/ERPNext sites
  from the command line. Use this skill whenever the user mentions "ffc", wants to
  query, list, get, create, update, or delete Frappe documents, check Sales Invoices,
  look up customers, fetch Purchase Orders, run reports, call server methods, or do
  anything involving Frappe REST API operations from the terminal. Also trigger when
  the user wants to automate Frappe data retrieval, pipe Frappe data into scripts,
  inspect DocType schemas, or troubleshoot ffc connection issues.
---

# Foxmayn Frappe CLI (ffc)

A command-line tool for interacting with Frappe/ERPNext sites via the REST API. Supports full CRUD on documents, schema introspection, report execution, and RPC method calls.

## Quick Setup

```bash
ffc init        # interactive wizard — creates ~/.config/ffc/config.yaml
ffc config      # TUI to change default site, number/date formatting
```

Config file: `~/.config/ffc/config.yaml`

```yaml
default_site: dev
number_format: french   # french | us | german | plain
date_format: yyyy-mm-dd # yyyy-mm-dd | dd-mm-yyyy | dd/mm/yyyy | mm/dd/yyyy

sites:
  dev:
    url: "http://mysite.localhost:8000"
    api_key: "your_api_key"
    api_secret: "your_api_secret"
```

Generate API keys on the Frappe site: **User > API Access > Generate Keys**.

### Managing Config from the Terminal

```bash
# Read settings
ffc config get              # styled table
ffc config get --json       # JSON
ffc config get --yaml       # YAML

# Write settings (validates values before saving)
ffc config set --default-site prod
ffc config set --number-format us
ffc config set --date-format dd/mm/yyyy
ffc config set --default-site prod --number-format french --date-format yyyy-mm-dd
```

Valid `--number-format` values: `french` (1 000 000,00), `us` (1,000,000.00), `german` (1.000.000,00), `plain` (1000000.00).
Valid `--date-format` values: `yyyy-mm-dd`, `dd-mm-yyyy`, `dd/mm/yyyy`, `mm/dd/yyyy`.

**Environment variable overrides** (useful in CI — also work without a config file):

```bash
export FFC_URL="https://erp.company.com"
export FFC_API_KEY="your_key"
export FFC_API_SECRET="your_secret"
```

## IMPORTANT: Always Use --json / -j

**MANDATORY for AI/LLM usage:** Always append `--json` (or `-j`) to every ffc command that supports it. The default table output is formatted for human reading and is not reliably parseable. JSON output is structured, complete, and easy to process.

Commands that support `--json`: `list-docs`, `get-doc`, `create-doc`, `update-doc`, `count-docs`, `get-schema`, `list-doctypes`, `list-reports`, `run-report`, `ping`. (`call-method` always outputs JSON regardless. `delete-doc` has no data output — `--json` is not applicable to either.)

```bash
# Always do this:
ffc list-docs -d "Customer" --json
ffc get-doc -d "Sales Invoice" -n "SINV-0001" --json

# Never do this (table output — hard to parse):
ffc list-docs -d "Customer"
ffc get-doc -d "Sales Invoice" -n "SINV-0001"
```

## Commands

### Global Flags

| Flag       | Short | Description                                         |
| ---------- | ----- | --------------------------------------------------- |
| `--site`   | `-s`  | Select a site from config (default: `default_site`) |
| `--config` | `-c`  | Custom config file path                             |
| `--json`   | `-j`  | Output raw JSON instead of a table                  |

---

### Document Operations (CRUD)

#### `ffc get-doc` — Get a single document

```bash
ffc get-doc -d "Company" -n "My Company" --json
ffc get-doc -d "User" -n "jane@example.com" -f "name,email,enabled" --json
```

| Flag        | Short | Required | Description                                           |
| ----------- | ----- | -------- | ----------------------------------------------------- |
| `--doctype` | `-d`  | Yes      | Frappe DocType                                        |
| `--name`    | `-n`  | Yes      | Document name (ID)                                    |
| `--fields`  | `-f`  | No       | Fields to fetch: `'["name","email"]'` or `name,email` |

#### `ffc list-docs` — List documents

```bash
ffc list-docs -d "User" -f "name,email,enabled" --limit 10 --json
ffc list-docs -d "ToDo" --filters '{"status":"Open"}' -o "modified desc" --json
```

| Flag         | Short | Required | Default | Description                                                       |
| ------------ | ----- | -------- | ------- | ----------------------------------------------------------------- |
| `--doctype`  | `-d`  | Yes      | —       | Frappe DocType to query                                           |
| `--fields`   | `-f`  | No       | all     | Fields: `'["name","email"]'` or `name,email`                      |
| `--filters`  | —     | No       | —       | JSON filter: `'{"status":"Open"}'` or `'[["status","=","Open"]]'` |
| `--limit`    | `-l`  | No       | 20      | Max records to return                                             |
| `--order-by` | `-o`  | No       | —       | Sort: `"modified desc"`, `"name asc"`                             |

#### `ffc create-doc` — Create a document

```bash
ffc create-doc -d "ToDo" --data '{"description":"Fix bug","priority":"Medium"}' --json
```

| Flag        | Short | Required | Description                 |
| ----------- | ----- | -------- | --------------------------- |
| `--doctype` | `-d`  | Yes      | Frappe DocType              |
| `--data`    | —     | Yes      | JSON object of field values |

#### `ffc update-doc` — Update a document

```bash
ffc update-doc -d "ToDo" -n "TD-0001" --data '{"status":"Closed"}' --json
```

| Flag        | Short | Required | Description                     |
| ----------- | ----- | -------- | ------------------------------- |
| `--doctype` | `-d`  | Yes      | Frappe DocType                  |
| `--name`    | `-n`  | Yes      | Document name (ID)              |
| `--data`    | —     | Yes      | JSON object of fields to update |

#### `ffc delete-doc` — Delete a document

Prompts for confirmation unless `--yes` is passed.

```bash
ffc delete-doc -d "ToDo" -n "TD-0001" --yes
```

| Flag        | Short | Required | Description              |
| ----------- | ----- | -------- | ------------------------ |
| `--doctype` | `-d`  | Yes      | Frappe DocType           |
| `--name`    | `-n`  | Yes      | Document name (ID)       |
| `--yes`     | `-y`  | No       | Skip confirmation prompt |

#### `ffc count-docs` — Count documents

```bash
ffc count-docs -d "Sales Invoice" --filters '{"status":"Unpaid"}' --json
```

| Flag        | Short | Required | Description            |
| ----------- | ----- | -------- | ---------------------- |
| `--doctype` | `-d`  | Yes      | Frappe DocType         |
| `--filters` | —     | No       | JSON filter expression |

---

### Schema & Introspection

#### `ffc get-schema` — View DocType field definitions

Shows all fields: fieldname, label, type, required flag, options, and default.

```bash
ffc get-schema -d "Sales Invoice" --json
```

| Flag        | Short | Required | Description        |
| ----------- | ----- | -------- | ------------------ |
| `--doctype` | `-d`  | Yes      | DocType to inspect |

#### `ffc list-doctypes` — List available DocTypes

```bash
ffc list-doctypes --module "Accounts" --json
```

| Flag       | Short | Required | Default | Description           |
| ---------- | ----- | -------- | ------- | --------------------- |
| `--module` | `-m`  | No       | —       | Filter by module name |
| `--limit`  | `-l`  | No       | 50      | Max records to return |

---

### Reports

#### `ffc list-reports` — List available reports

```bash
ffc list-reports --module "Accounts" --json
```

| Flag       | Short | Required | Default | Description           |
| ---------- | ----- | -------- | ------- | --------------------- |
| `--module` | `-m`  | No       | —       | Filter by module name |
| `--limit`  | `-l`  | No       | 50      | Max records to return |

#### `ffc run-report` — Execute a report

```bash
ffc run-report -n "General Ledger" --filters '{"company":"Acme","from_date":"2025-01-01"}' --json
```

| Flag        | Short | Required | Description                         |
| ----------- | ----- | -------- | ----------------------------------- |
| `--name`    | `-n`  | Yes      | Report name                         |
| `--filters` | —     | No       | JSON object of report filter values |
| `--limit`   | `-l`  | No       | Limit rows displayed (0 = all)      |

---

### RPC

#### `ffc call-method` — Call a whitelisted server method

Always outputs JSON (flag not needed).

```bash
ffc call-method --method "frappe.ping"
ffc call-method --method "frappe.client.get_count" --args '{"doctype":"ToDo","filters":{"status":"Open"}}'
```

| Flag       | Short | Required | Description                            |
| ---------- | ----- | -------- | -------------------------------------- |
| `--method` | —     | Yes      | Frappe method path, e.g. `frappe.ping` |
| `--args`   | —     | No       | JSON object of method arguments        |

---

### Connectivity

#### `ffc ping` — Check connectivity

```bash
ffc ping --json
ffc ping --site production --json
```

---

### Self-Update

#### `ffc update` — Update ffc to the latest release

Works regardless of how ffc was installed (curl, powershell, `go install`).

```bash
ffc update           # check and update (asks for confirmation)
ffc update --check   # only print whether an update is available
ffc update --yes     # update without confirmation
```

ffc also checks for updates automatically in the background at most once per day and prints a one-line notice to stderr before any command output when a newer version is available:

```
Update available: v1.2.0 → v1.3.0  (run: ffc update)
```

---

## Common Recipes

### Pipe JSON into jq

```bash
ffc list-docs -d "Customer" -f "name,customer_name" --json | jq '.[].customer_name'
ffc count-docs -d "Sales Invoice" --filters '{"status":"Unpaid"}' --json | jq '.count'
```

### Query across sites

```bash
ffc --site dev list-docs -d "Item" -f "name,item_name" --json > dev_items.json
ffc --site production list-docs -d "Item" -f "name,item_name" --json > prod_items.json
```

### Scripting with ffc

```bash
for inv in $(ffc list-docs -d "Sales Invoice" -f "name" --json | jq -r '.[].name'); do
  ffc get-doc -d "Sales Invoice" -n "$inv" --json > "invoices/$inv.json"
done
```

### Filter expressions

```bash
# Object style (simple equality)
--filters '{"status":"Open","docstatus":1}'

# Array style (operators: =, !=, >, <, >=, <=, like, in, between, is)
--filters '[["grand_total",">",1000],["status","=","Unpaid"]]'
```

## Troubleshooting

| Error                          | Cause                        | Fix                                    |
| ------------------------------ | ---------------------------- | -------------------------------------- |
| `authentication failed (401)`  | Bad API key/secret           | Regenerate keys: User > API Access     |
| `permission denied (403)`      | User lacks read access       | Check role permissions for the DocType |
| `doctype "X" not found (404)`  | Typo or module not installed | Verify the DocType name on the site    |
| `no config file found`         | Missing config               | Run `ffc init` or set `FFC_*` env vars |
| `site "X" not found in config` | Wrong `--site` value         | Check site names in config.yaml        |

## Config Precedence

Highest wins:

1. `--site` / `--config` flags
2. `FFC_*` environment variables
3. Config file (`~/.config/ffc/config.yaml`)
4. Defaults
