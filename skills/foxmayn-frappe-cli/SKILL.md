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

A command-line tool for interacting with Frappe/ERPNext sites via the REST API. Supports full CRUD on documents, bulk create/update/delete, schema introspection, report execution, RPC method calls, and an MCP server.

## Quick Setup

```bash
ffc init        # interactive wizard — creates ~/.config/ffc/config.yaml
ffc config      # TUI to change default site, number/date formatting
```

`ffc init` offers three auth methods (or jump straight to one with a flag):

```bash
ffc init --oauth      # OAuth 2.0 browser login (PKCE); only tokens are stored
ffc init --apikey     # API key + secret (best for scripts/CI); verified against the site before saving
ffc init --password   # username/email + password; session cookie, no 2FA support
```

OAuth: create an OAuth Client on Frappe first (Integrations > OAuth Client). The wizard prints the redirect URI to register: `http://127.0.0.1:<port>/callback` (not `localhost`). Expired access tokens are refreshed automatically when a command runs.

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

Generate API keys on the Frappe site: **User > API Access > Generate Keys**. OAuth sites use `oauth_client_id` / `access_token` / `refresh_token` / `token_expiry` instead; password sites use `username` / `password` (stored in the config file at mode 0600).

### Managing Sites

```bash
ffc site list               # name, URL, auth method, default (`--json`: default is true/false)
ffc site add                # menu to choose auth method (or --oauth / --apikey / --password)
ffc site use [name]         # set default site (menu if name omitted)
ffc site remove [name]      # remove a site (menu if name omitted)
```

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

**Environment variable overrides** (useful in CI):

```bash
export FFC_URL="https://erp.company.com"
export FFC_API_KEY="your_key"
export FFC_API_SECRET="your_secret"
```

- `FFC_API_KEY` + `FFC_API_SECRET` only work as a pair; together they replace every stored credential of the selected site.
- `FFC_URL` only applies together with that pair (stored credentials are never sent to another host). Set alone with a URL different from the site's, it is an error.
- With no config file at all, the three variables alone define the site.
- `FFC_NO_UPDATE_CHECK=1` disables the daily background update check.

## IMPORTANT: Always Use --json / -j

**MANDATORY for AI/LLM usage:** Always append `--json` (or `-j`) to every ffc command that supports it. The default table output is formatted for human reading and is not reliably parseable. JSON output is structured, complete, and easy to process.

Commands that support `--json`: `list-docs`, `get-doc`, `create-doc`, `update-doc`, `delete-doc`, `count-docs`, `bulk-create`, `bulk-update`, `bulk-delete`, the lifecycle and `workflow` commands, `get-schema`, `list-doctypes`, `list-reports`, `run-report`, `search`, `ping`, `site list`. (`call-method` always outputs JSON regardless. MCP tools always return JSON by design.)

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

| Flag        | Short | Description                                                                 |
| ----------- | ----- | --------------------------------------------------------------------------- |
| `--site`    | `-s`  | Select a site from config (default: `default_site`)                         |
| `--config`  | `-c`  | Custom config file path                                                     |
| `--json`    | `-j`  | Output raw JSON instead of a table                                          |
| `--quiet`   | `-q`  | No progress spinner (also off when stderr is not a terminal, or `NO_COLOR`/`CI` is set) |
| `--timeout` | —     | HTTP timeout per request, e.g. `2m` (default `30s`; raise it for heavy reports) |
| `--debug`   | —     | Trace each HTTP request on stderr, secrets redacted; `--debug=body` adds headers and bodies (`FFC_DEBUG`) |

Commands exit non-zero on any error, declined confirmation or aborted prompt, so scripts can rely on the exit status. Data goes to stdout; spinner, warnings and errors go to stderr.

**Preview writes with `--dry-run`.** Every write command (create/update/delete, bulk, lifecycle, `workflow apply`/`bulk-apply`, `call-method`, `api`) accepts `--dry-run`: it prints the request it would send (`{"dry_run":true,"requests":[{method,url,body,changes?}]}` with `--json`), sends nothing that writes, asks no confirmation and exits 0. Reads still run, so a missing document or a wrong state fails exactly like the real run. `update-doc --dry-run` adds `changes` (field → from/to). Use it before any destructive or bulk write when unsure.

```bash
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --dry-run --json
ffc bulk-delete -d Note --file names.json --dry-run --json
```

---

### Document Operations (CRUD)

#### `ffc get-doc` — Get a single document

For **Single DocTypes** (e.g. `System Settings`, `HR Settings`), `--name` can be omitted — the DocType name is used automatically.

```bash
ffc get-doc -d "Company" -n "My Company" --json
ffc get-doc -d "User" -n "jane@example.com" -f "name,email,enabled" --json
ffc get-doc -d "System Settings" --json
```

| Flag        | Short | Required | Description                                           |
| ----------- | ----- | -------- | ----------------------------------------------------- |
| `--doctype` | `-d`  | Yes      | Frappe DocType                                        |
| `--name`    | `-n`  | No       | Document name (ID). Defaults to DocType name for Single DocTypes. |
| `--fields`  | `-f`  | No       | Fields to fetch: `'["name","email"]'` or `name,email` |
| `--keys`    | —     | No       | Comma-separated keys to keep in JSON output (takes priority over `--fields` for JSON) |

#### `ffc list-docs` — List documents

```bash
ffc list-docs -d "User" -f "name,email,enabled" --limit 10 --json
ffc list-docs -d "ToDo" --filters '{"status":"Open"}' -o "modified desc" --json
```

| Flag         | Short | Required | Default | Description                                                       |
| ------------ | ----- | -------- | ------- | ----------------------------------------------------------------- |
| `--doctype`  | `-d`  | Yes      | —       | Frappe DocType to query                                           |
| `--fields`   | `-f`  | No       | `name`  | Fields: `'["name","email"]'` or `name,email`                      |
| `--filters`  | —     | No       | —       | JSON filter: `'{"status":"Open"}'` or `'[["status","=","Open"]]'` |
| `--limit`    | `-l`  | No       | 20      | Max records to return                                             |
| `--order-by` | `-o`  | No       | —       | Sort: `"modified desc"`, `"name asc"`                             |
| `--start`    | —     | No       | 0       | Offset into the result set (pagination)                           |

`--limit 0` means no limit; negative `--limit`/`--start` are rejected. `--filters` must be a JSON object or array.

#### `ffc create-doc` — Create a document

```bash
ffc create-doc -d "ToDo" --data '{"description":"Fix bug","priority":"Medium"}' --json
```

| Flag        | Short | Required | Description                 |
| ----------- | ----- | -------- | --------------------------- |
| `--doctype` | `-d`  | Yes      | Frappe DocType              |
| `--data`    | —     | Yes      | JSON object of field values |
| `--keys`    | —     | No       | Comma-separated keys to keep in JSON output |

#### `ffc update-doc` — Update a document

For **Single DocTypes**, `--name` can be omitted — the DocType name is used automatically.

```bash
ffc update-doc -d "ToDo" -n "TD-0001" --data '{"status":"Closed"}' --json
ffc update-doc -d "System Settings" --data '{"default_currency":"USD"}' --json
```

| Flag        | Short | Required | Description                                                        |
| ----------- | ----- | -------- | ------------------------------------------------------------------ |
| `--doctype` | `-d`  | Yes      | Frappe DocType                                                     |
| `--name`    | `-n`  | No       | Document name (ID). Defaults to DocType name for Single DocTypes.  |
| `--data`    | —     | Yes      | JSON object of fields to update (a `name` key is dropped with a warning) |
| `--keys`    | —     | No       | Comma-separated keys to keep in JSON output                        |

#### `ffc delete-doc` — Delete a document

Prompts for confirmation unless `--yes` is passed. Declining (or Esc) exits non-zero. `--name` is required (Single DocTypes cannot be deleted).

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

### Bulk Operations

`bulk-create`, `bulk-update`, `bulk-delete` take a JSON array inline (`--data` / `--names`) or from a file (`--file`, `-` reads stdin). The whole input is validated before anything is sent. Each item is reported as created/updated/deleted, `error`, `interrupted` (cut off by Ctrl+C — the server may or may not have applied it, check before re-running) or `skipped` (not started because of `--fail-fast` or an abort). The command exits non-zero unless every item succeeded. `bulk-delete --filters` and `bulk-update --filters ... --set ...` list the matching names first (one list call), show up to 10 of them and ask for confirmation (`--yes` without a terminal); run them with `--dry-run` first. Empty filters (`{}`, `[]`) are refused because they match every document; an empty `@file` or `@-` is an error for every JSON flag.

```bash
ffc bulk-create -d "ToDo" --data '[{"description":"a"},{"description":"b"}]' --json
ffc bulk-update -d "ToDo" --file updates.json --concurrency 4 --json   # each item needs a "name"
ffc bulk-delete -d "ToDo" --names "TD-001,TD-002" --yes --json
ffc bulk-delete -d "ToDo" --filters '{"status":"Cancelled"}' --dry-run       # preview what matches
ffc bulk-update -d "ToDo" --filters '{"status":"Open"}' --set '{"status":"Closed"}' --yes
ffc bulk-delete -d "Note" --file names.json --yes --json               # use --file for names containing commas
```

| Flag            | Short | Applies to              | Description                                              |
| --------------- | ----- | ----------------------- | -------------------------------------------------------- |
| `--doctype`     | `-d`  | all                     | Frappe DocType (required)                                |
| `--data`        | —     | create, update          | JSON array of objects (update: each with `name`)         |
| `--names`       | —     | delete                  | Comma-separated document names                           |
| `--file`        | —     | all                     | JSON file (array of objects; delete: array of names); `-` = stdin |
| `--concurrency` | —     | all                     | Requests in flight, 1-10 (default 1)                     |
| `--fail-fast`   | —     | all                     | Stop starting new items after the first failure          |
| `--yes`         | `-y`  | delete                  | Skip confirmation prompt                                 |

---

### Document Lifecycle

State changes go through Frappe's own methods, so validations, permissions and hooks run. A state error (submitting a submitted document, amending a draft, a DocType with an active Workflow) exits 6.

```bash
ffc submit-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --json
ffc cancel-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --check --json   # submitted documents blocking the cancel; cancels nothing
ffc cancel-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --yes --json
ffc amend-doc  -d "Sales Invoice" -n ACC-SINV-2026-00001 --data '{"due_date":"2026-11-30"}' --json   # new draft <name>-1
ffc copy-doc   -d "Item" -n SKU-001 --data '{"item_code":"SKU-002"}' --json
ffc rename-doc -d Customer -n "Acme Ltd" --to "Acme Limited" --json          # --merge --yes: merge into an existing doc
ffc restore-doc -d ToDo -n TD-0001 --json                                    # or --deleted <Deleted Document>
ffc discard-doc -d "Sales Invoice" -n ACC-SINV-2026-00002 --yes             # cancel a draft; Frappe v16+
```

- `amend-doc` keeps "no copy" fields (desk Amend); `copy-doc` drops them (desk Duplicate). Child rows are copied by both.
- `restore-doc` prints the restored name: a hash- or series-named DocType may give it a new one.
- `cancel-doc`, `discard-doc` and `rename-doc --merge` ask for confirmation; pass `--yes` in scripts.

**Workflows.** `submit-doc`/`cancel-doc` refuse a DocType with an active Workflow; use:

```bash
ffc workflow transitions -d "Leave Application" -n HR-LAP-2026-00001 --json   # actions you can apply now
ffc workflow apply -d "Leave Application" -n HR-LAP-2026-00001 --action Approve --json
ffc workflow bulk-apply -d "Leave Application" --file names.json --action Approve --yes --json   # per-document report, exit 8 if any failed
ffc workflow pending [-d "Leave Application"] --json                          # open Workflow Actions
```

---

### Schema & Introspection

#### `ffc get-schema` — View DocType field definitions

Returns a **compact view** by default when using `--json`: only the meaningful DocType properties and field attributes are included. Zero-value booleans, metadata, and internal Frappe fields are stripped. Use `--full` for the raw response or `--keys` to select specific top-level keys.

**Custom fields are included.** Fields added via Frappe's Customize Form are fetched from the `Custom Field` DocType and merged into the schema at the correct positions (based on `insert_after`). No extra flags needed — custom fields appear automatically alongside standard fields.

```bash
ffc get-schema -d "Sales Invoice" --json
ffc get-schema -d "Sales Invoice" --json --full
ffc get-schema -d "Sales Invoice" --json --keys fields
ffc get-schema -d "Sales Invoice" --json --keys name,module,fields
```

| Flag        | Short | Required | Description                                              |
| ----------- | ----- | -------- | -------------------------------------------------------- |
| `--doctype` | `-d`  | Yes      | DocType to inspect                                       |
| `--full`    | —     | No       | Return the complete unfiltered Frappe response           |
| `--keys`    | —     | No       | Comma-separated top-level keys to include, e.g. `fields` |

Both `--full` and `--keys` only apply to `--json` output. Problems merging custom fields or Property Setters are reported as warnings (`_warnings` in the JSON), not failures. Property Setter overrides (DocField and DocType level, `field_order`) are applied after custom fields are merged.

**Compact JSON keeps:**
- DocType level (always): `name`, `module`, `autoname`, `naming_rule`, `is_submittable`, `issingle`, `istable`, `is_tree`, `is_virtual`, `read_only`, `custom`
- DocType level (if non-empty): `title_field`, `search_fields`, `sort_field`, `sort_order`, `image_field`, `description`
- DocType level: `permissions`, one row per DocPerm with `role`, `permlevel` (if > 0) and the granted `rights` (`read`, `write`, `create`, ...)
- DocType level (if truthy): `allow_rename`, `track_changes`
- DocType level (if non-empty): `actions`, `links`, `states`
- DocField level (always): `fieldname`, `label`, `fieldtype`
- DocField level (if truthy): `reqd`, `read_only`, `hidden`, `unique`, `is_virtual`, `non_negative`, `allow_on_submit`, `in_list_view`, `in_standard_filter`, `set_only_once`, `translatable`, `ignore_user_permissions`
- DocField level (if non-empty): `options`, `default`, `description`, `fetch_from`, `depends_on`, `mandatory_depends_on`, `read_only_depends_on`, `precision`, `link_filters`, `insert_after`
- DocField level (if truthy): `fetch_if_empty`, `no_copy`, `search_index`, `bold`, `collapsible`, `print_hide`, `report_hide`
- DocField level (if > 0): `length`, `permlevel`

#### `ffc list-doctypes` — List available DocTypes

```bash
ffc list-doctypes --module "Accounts" --json
```

| Flag       | Short | Required | Default | Description           |
| ---------- | ----- | -------- | ------- | --------------------- |
| `--module` | `-m`  | No       | —       | Filter by module name |
| `--limit`  | `-l`  | No       | 50      | Max records to return |

---

### Search and name resolution

#### `ffc search` — Find documents by text

```bash
ffc search acme -d Customer --json      # resolve "acme" to document names (search_link)
ffc search "" -d Item --limit 5 --json  # first 5 items
ffc search "overdue invoice" --json     # global search across DocTypes
```

TEXT is a positional argument (several words are joined with spaces; `--` before a text starting with a dash). Read-only, so there is no `--dry-run`.

| Flag        | Short | Required | Default | Description |
| ----------- | ----- | -------- | ------- | ----------- |
| `--doctype` | `-d`  | No       | —       | Resolve names in this DocType (search_link); omit for global search |
| `--limit`   | `-l`  | No       | 20      | Max results (at least 1) |

- With `-d`: the search a Link field runs. It honours the DocType's search fields, title field, link query and user permissions. Rows: `value` (the document name), `description`, sometimes `label`. Use it to turn a title into a name before `get-doc`. An empty TEXT lists the first documents. **Frappe marks the answer cacheable for 60 seconds** (`max-age=60`; ffc keeps no cache, but a proxy in front of the site may), so a document created a moment ago may be missing.
- Without `-d`: Frappe's global search, ranked. Rows: `doctype`, `name`, `content`, `rank` (and `title` for some DocTypes). **It covers only DocTypes listed in Global Search Settings and fields flagged "In Global Search"**: no hit does not mean the document does not exist. Fall back to `list-docs --filters '[["name","like","%x%"]]'`. `a & b` searches `a` and `b` separately (at most 5 phrases) and combines the hits, cut to `--limit`.

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
| `--limit`   | `-l`  | No       | Max result rows, table and JSON (0 = all) |
| `--keys`    | —     | No       | Comma-separated top-level keys for JSON output, e.g. `columns,result` |

Heavy reports may exceed the default 30s timeout; pass `--timeout 2m`.

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
| `--get`    | —     | No       | Send as GET (for methods whitelisted GET-only) |

---

### MCP Server (AI Agent Integration)

#### `ffc mcp` — Start an MCP server for AI agents

Exposes Frappe API operations as 24 MCP tools so LLMs and AI agents (Claude Desktop, Cursor, etc.) can interact with your Frappe site directly.

**Three modes:**

**Stdio (default)** — use this in your MCP client config. The client manages the process lifecycle:
```bash
ffc mcp --site mysite
```

**HTTP foreground** — useful for testing with the MCP Inspector:
```bash
ffc mcp --port 8765 --site mysite
# endpoint: http://127.0.0.1:8765/mcp (localhost only, bearer token required)
```

**Detached background** — runs as a background HTTP server, doesn't block the terminal:
```bash
ffc mcp --detach [--port 8765] [--site mysite]
ffc mcp status    # PID, URL, bearer token, site, start time, log file path
ffc mcp stop      # stop the server + clean up state file
ffc mcp stop --force   # stop the recorded PID even if it does not answer health checks
```

The HTTP transport binds `127.0.0.1` only and requires `Authorization: Bearer <token>`; the token is shown by `ffc mcp status` (detached) or printed to the terminal (foreground).

| Flag       | Short | Description                                                |
| ---------- | ----- | ---------------------------------------------------------- |
| `--detach` | `-d`  | Run as a background HTTP server                            |
| `--port`   | `-p`  | Port for HTTP mode (default: 8765, implies HTTP transport) |
| `--read-only` | —  | Expose only read tools (no create, update, delete, bulk, lifecycle, workflow or `call_method`) |
| `--allow-tools`, `--allow-doctypes`, `--deny-doctypes`, `--allow-methods`, `--deny-methods` | — | Narrow the site's MCP policy (never widen it) |
| `--sites`, `--all-sites` | — | Serve several sites; every tool call then needs `site` (see `list_sites`) |
| `--confirm` | — | `always` or `if-supported`: tighten when destructive calls ask the user |

**Policy and audit.** A site's config entry may hold an `mcp:` block with `read_only`, `allow_tools`, `allow_doctypes`, `deny_doctypes`, `allow_methods`, `deny_methods` and `confirm`. A misspelt key is an error. MCP may read but never write the sensitive DocTypes (User, Role, DocType, DocPerm, System Settings, Server Script, …) unless the site's `allow_doctypes` lists them. `call_method` refuses `execute_code`, `generate_keys` and the Frappe Cloud app installer unless `allow_methods` lists them. With `allow_doctypes` set, `call_method` needs `allow_methods`. A refused call returns an error starting with `policy:` that names the setting, and sends nothing. Every tool call is logged to `~/.config/ffc/mcp-audit.jsonl` with secrets redacted and document data reduced to its keys.

**Confirmation.** `delete_doc`, `bulk_delete`, `cancel_doc`, `rename_doc` with `merge`, `apply_workflow` and the matching `call_method` methods (`frappe.client.delete`, `frappe.client.cancel`, `run_doc_method` cancel, …) ask the user through the MCP client first. `confirm: if-supported` (default) asks when the client supports elicitation, `always` refuses when it cannot (the error names the `ffc` command to run in a terminal), `never` does not ask. A declined call returns `cancelled by the user; nothing was changed`: do not retry it another way.

**Available MCP tools** (used by the AI agent, not called directly):

| Tool            | Equivalent ffc command |
| --------------- | ---------------------- |
| `list_sites`    | `ffc site list` (served sites only) |
| `ping`          | `ffc ping`             |
| `get_doc`       | `ffc get-doc`          |
| `list_docs`     | `ffc list-docs`        |
| `create_doc`    | `ffc create-doc`       |
| `update_doc`    | `ffc update-doc`       |
| `delete_doc`    | `ffc delete-doc`       |
| `count_docs`    | `ffc count-docs`       |
| `get_schema`    | `ffc get-schema`       |
| `list_doctypes` | `ffc list-doctypes`    |
| `list_reports`  | `ffc list-reports`     |
| `run_report`    | `ffc run-report`       |
| `search`        | `ffc search`           |
| `call_method`   | `ffc call-method`      |
| `bulk_create`   | `ffc bulk-create`      |
| `bulk_update`   | `ffc bulk-update`      |
| `bulk_delete`   | `ffc bulk-delete`      |
| `submit_doc`    | `ffc submit-doc`       |
| `cancel_doc`    | `ffc cancel-doc`       |
| `amend_doc`     | `ffc amend-doc`        |
| `copy_doc`      | `ffc copy-doc`         |
| `rename_doc`    | `ffc rename-doc`       |
| `get_transitions` | `ffc workflow transitions` |
| `apply_workflow`  | `ffc workflow apply`       |

With `--read-only`, only `list_sites`, `ping`, `get_doc`, `list_docs`, `count_docs`, `get_schema`, `list_doctypes`, `list_reports`, `run_report`, `search` and `get_transitions` are registered.

MCP tools always return JSON — no `--json` flag needed.

**MCP-specific output behaviour (differs from CLI defaults):**
- `get_schema` returns the compact view by default (same as `ffc get-schema --json`). The LLM can pass `full=true` for the raw Frappe response, or `keys="fields"` / `keys="name,module,fields"` to select specific top-level properties.
- `run_report` returns only `columns`, `result`, `report_summary` (if non-null), plus `total_rows` and `truncated` when rows were cut — strips `execution_time`, `chart`, `add_total_row`, `message`. Defaults to 500 rows unless `limit` is given.
- `search` takes `text` (required), `doctype` (optional) and `limit` (default 20, at most 100). With `doctype` it is search_link (names; Frappe marks it cacheable for 60 s, so a proxy may serve a minute-old answer); without, global search over Global Search Settings DocTypes only, and under an `allow_doctypes`/`deny_doctypes` policy its hits are filtered to the DocTypes the policy allows. A global search answers `{results, hidden_by_policy}` (the number of hits dropped by the policy), a DocType search a plain list.
- A tool result over 512 KiB is refused with a hint to narrow it (`limit`, `fields`, `filters`, `keys`).
- Bulk tools take at most 200 items per call.
- JSON-valued arguments (`filters`, `fields`, `data`, `args`, …) may be passed as native JSON or as a JSON-encoded string.

**Example Claude Desktop config** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS):
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

The release's `checksums.txt` must carry a valid Ed25519 signature (`checksums.txt.sig`) from a key built into ffc, and the size-limited download must match its SHA256 before the binary is swapped. ffc also checks for updates automatically in the background at most once per day (skipped for `update`, `mcp`, `completion`, `help`, or when `FFC_NO_UPDATE_CHECK` is set) and prints a one-line notice to stderr when a newer version is available:

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
| `authentication failed (401)`  | Bad credentials / expired OAuth token | Regenerate keys, or re-run `ffc site add --oauth` / `ffc init` |
| `permission denied (403)`      | User lacks read access       | Check role permissions for the DocType |
| `doctype "X" not found (404)`  | Typo or module not installed | Verify the DocType name on the site    |
| `no config file found`         | Missing config               | Run `ffc init` or set `FFC_URL` + `FFC_API_KEY` + `FFC_API_SECRET` |
| `site "X" not found in config` | Wrong `--site` value         | Run `ffc site list`                    |
| `FFC_URL (...) differs from the site URL` | `FFC_URL` set without the key pair | Set all three vars, or unset `FFC_URL` |
| `warning: refreshing the OAuth token ... failed` | Refresh token revoked/expired | Re-run `ffc site add --oauth` (or `ffc init --oauth`) |
| timeout / deadline exceeded    | Slow report or site          | Raise `--timeout` (e.g. `--timeout 2m`) |

## Config Precedence

Highest wins:

1. `--site` / `--config` flags
2. `FFC_*` environment variables (key/secret pair replaces the site's stored credentials)
3. Config file (`~/.config/ffc/config.yaml`)
4. Defaults

`--site` / `default_site` match the site name exactly first, then a unique case-insensitive match. Site names keep their case and may contain dots.
