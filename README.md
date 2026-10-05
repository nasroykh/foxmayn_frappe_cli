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

Both scripts detect your architecture (amd64/arm64), download the correct binary from the latest GitHub Release, and verify the SHA256 checksum before installing. `install.sh` also checks the Ed25519 signature on `checksums.txt` when OpenSSL 3 is available (otherwise it warns and relies on the checksum; macOS ships LibreSSL, so it falls back there unless OpenSSL 3 is installed). `install.ps1` checks the checksum only.

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

The wizard lets you choose between three authentication methods:

- **OAuth 2.0** (`--oauth`) — browser login, PKCE flow; no password is stored (the config keeps the OAuth client id/secret and the access and refresh tokens). You create an OAuth Client on Frappe once and authorize via the browser. The wizard prints the redirect URI to register, `http://127.0.0.1:<port>/callback` (earlier ffc versions used `localhost`; update an existing OAuth Client if it was registered that way).
- **API Key** (`--apikey`) — paste your API key and secret from **User → API Access → Generate Keys**. The keys are checked against the site before they are saved. Best choice for scripts and CI.
- **Username / password** (`--password`) — logs in with a session cookie on every run and logs out when the command ends. The password is stored in the config file (mode 0600). Two-factor authentication is not supported. On a site with **Deny Multiple Sessions** enabled (System Settings), each ffc login can end your other sessions, such as the browser one; use an API key or OAuth there.

```bash
ffc init             # menu to choose auth method
ffc init --oauth     # go straight to the OAuth browser flow
ffc init --apikey    # go straight to the API key form
ffc init --password  # go straight to the username/password form
```

Without a terminal (CI, scripts, agents), pass the site and one credential set as flags. The secret is never a flag value: pipe it with `--api-secret-stdin` / `--password-stdin` (one line; a terminal on stdin is refused, so the secret is never echoed), or set `FFC_API_SECRET` / `FFC_PASSWORD`. The credentials are checked against the site before the config is written; an existing config is replaced only with `--force`.

```bash
echo "$SECRET" | ffc init --name prod --url https://erp.example.com --api-key KEY --api-secret-stdin
echo "$PW" | ffc init --name dev --url http://localhost:8000 --username admin --password-stdin --force
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

OAuth access tokens are refreshed automatically when they expire, before any command that talks to the site — you don't need to re-run `ffc init`.

**Site Management (`ffc site`)**

Add, list, remove, or switch between sites without touching the config file manually:

```bash
ffc site list                   # show all configured sites
ffc site add                    # add a new site (menu to choose auth method)
ffc site add --oauth            # add a new site via OAuth browser flow
ffc site add --apikey           # add a new site via API key form
ffc site add --password         # add a new site via username/password form
ffc site use [name]             # set the default site (interactive menu if name omitted)
ffc site remove [name]          # remove a site (interactive menu if name omitted)
ffc site remove NAME --yes      # remove without a prompt (required without a terminal)
ffc site rename OLD NEW         # rename a site; default_site follows if OLD was the default
ffc site edit NAME --url URL    # change the URL; stored credentials are re-checked against it first (not for OAuth sites: re-add them)
```

Non-interactive `site add` (no terminal needed; `--name`, `--url` and one credential set; `--force` to replace an existing site):

```bash
echo "$SECRET" | ffc site add --name staging --url https://staging.example.com --api-key KEY --api-secret-stdin
echo "$PW" | ffc site add --name dev --url http://localhost:8000 --username admin --password-stdin
FFC_API_SECRET="$SECRET" ffc site add --name prod --url erp.example.com --api-key KEY --force
```

OAuth setup stays interactive.

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

| Variable              | Effect |
| --------------------- | ------ |
| `FFC_API_KEY` + `FFC_API_SECRET` | Set together, they replace every stored credential of the selected site (one alone is ignored with a warning). |
| `FFC_URL`             | Site URL. Only applied together with `FFC_API_KEY` + `FFC_API_SECRET`, so stored credentials are never sent to another host; set alone to a different URL it is an error. |
| `FFC_NO_UPDATE_CHECK` | Set to any value to disable the daily background update check. |

When no config file exists at the default path, `ffc` builds the site from `FFC_URL`, `FFC_API_KEY` and `FFC_API_SECRET` alone.

## Usage

```
ffc [--site <name>] [--config <path>] [--json] <command> [flags]
```

### Global Flags

| Flag        | Short | Description                                     |
| ----------- | ----- | ----------------------------------------------- |
| `--site`    | `-s`  | Site name from config (default: `default_site`) |
| `--config`  | `-c`  | Config file path                                |
| `--json`    | `-j`  | Print raw JSON instead of a table (same as `--output json`) |
| `--output`  |       | Output format: `table`, `json`, `ndjson`, `csv`, `tsv`, `yaml` (default `table`, or `$FFC_OUTPUT`) |
| `--jq`      |       | Filter the JSON result with a jq expression; strings print raw |
| `--quiet`   | `-q`  | No progress spinner (also off when stderr is not a terminal, or `NO_COLOR`/`CI` is set) |
| `--timeout` |       | HTTP timeout per request, e.g. `2m` (default `30s`) |
| `--no-input` |      | Never prompt; fail instead. Also on when stdin is not a terminal |
| `--debug`   |       | Trace every HTTP request on stderr, secrets redacted; `--debug=body` adds headers and bodies (or `$FFC_DEBUG`) |
| `--version` | `-v`  | Print version information                       |

`FFC_SITE`, `FFC_CONFIG`, `FFC_TIMEOUT`, `FFC_OUTPUT` and `FFC_DEBUG` set `--site`, `--config`, `--timeout`, `--output` and `--debug` when the flag is not given (flags > environment > config).

#### Dry run

Every command that writes (`create-doc`, `update-doc`, `delete-doc`, the bulk commands, the lifecycle commands, `workflow apply` and `bulk-apply`, `call-method` and `api`) takes `--dry-run`. It prints the request it would send, with secrets redacted, sends nothing that writes and exits 0. Reads still run, so the dry run fails where the real run would (a missing document, a draft that cannot be amended). `update-doc --dry-run` also shows which fields would change, and `delete-doc --dry-run` checks that the document exists. No confirmation is asked. `call-method` and `api` send nothing at all, since any request to a method can write. A username/password site still logs in and out.

```bash
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --dry-run
ffc bulk-delete -d Note --file names.json --dry-run --json   # {"dry_run":true,"requests":[{method,url,body}...]}
```

#### Debugging requests

`--debug` writes one line per HTTP exchange to stderr: method, URL, status, Frappe `exc_type`, time and sizes. `--debug=body` adds the headers and bodies (the first 64 KiB of each). Credentials never appear: `Authorization`, cookies and session ids, and any password, secret, token, OAuth code or verifier in a URL or body are shown as `***`. The trace is stderr only, so it is safe with `--json` and with `ffc mcp` (in detached mode it goes to `mcp.log`). Use `--debug=body`, not `--debug body`.

```text
debug #2 GET https://erp.example.com/api/resource/ToDo/nope → 404 DoesNotExistError (91ms, sent 0 B, received 316 B)
```

#### Output formats

`--output` picks how a result is printed:

- **`table`** is the default human-readable view.
- **`json`** prints indented JSON, the same as `--json`.
- **`ndjson`** prints one compact JSON value per line: one line per row for lists.
- **`csv`** and **`tsv`** print a header row and then the data.
  - The columns follow `--fields`; otherwise they are the sorted union of the keys.
  - Nested values are JSON. Numbers are printed as the server sent them (`1500.0`), with no locale formatting.
  - In TSV, tabs, newlines and backslashes inside values are escaped as `\t`, `\n` and `\\`.
  - Cells are written as they are. A value that starts with `=`, `+`, `-` or `@` is run as a formula when the file is opened in a spreadsheet, so treat data from untrusted users with care.
- **`yaml`** prints YAML.

With `json` or `ndjson`, errors are reported as JSON too.

`--jq EXPR` filters the JSON result before printing (jq syntax, via gojq).

- With the default format, each result prints as it comes, like `jq -r`: strings raw, other values as JSON.
- With `--output`, one result is rendered as itself. Several results print one after another for `json` and `yaml`, and as one list for `ndjson`, `csv` and `tsv`.
- `halt_error` exits with its code. Ctrl+C stops an endless expression.

`ffc api` keeps the response body unchanged with `--json` or `FFC_OUTPUT`; only `--jq` or an explicit `--output` renders it.

```bash
ffc list-docs -d "Sales Invoice" --all --output csv --fields name,customer,grand_total > invoices.csv
ffc list-docs -d ToDo --filters '{"status":"Open"}' --jq '.[].name'
export FFC_OUTPUT=ndjson   # scripts: machine output by default
```

**JSON from files.** Every JSON-valued flag (`--data`, `--args`, `--filters`) also takes `@FILE`, or `@-` for stdin:

```bash
ffc create-doc -d ToDo --data @todo.json
jq -n '{status:"Open"}' | ffc count-docs -d ToDo --filters @-
```

Without a terminal (pipes, cron, CI, agents) ffc never waits for input: a command that would prompt fails with exit code 2 and a hint, for example "pass --yes" for a deletion.

#### Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | Success |
| 1 | Other error (config file, unexpected response, declined confirmation) |
| 2 | Usage: unknown command or flag, wrong arguments, invalid flag value, input needed but no terminal |
| 3 | Authentication: rejected credentials (401), failed login |
| 4 | Not found (404) |
| 5 | Permission denied (403) |
| 6 | Validation or conflict: 417 (e.g. ValidationError, LinkExistsError), 409 duplicate, 400, TimestampMismatchError, or a document in the wrong state for the command (amending a draft, submitting under an active Workflow) |
| 7 | Network or server: no connection, timeout, 429, 5xx |
| 8 | Partial bulk failure: some items of a bulk command did not succeed |
| 130 | Interrupted (Ctrl+C) |

Before v1.7.0 every failure exited with 1. Scripts that test for "non-zero" are unaffected; scripts that test `-eq 1` should test `-ne 0` instead.

With `--json`, an error is printed on stderr as one JSON object, and stdout carries no data:

```json
{"error":{"code":"not_found","exit_code":4,"status":404,"exc_type":"DoesNotExistError","message":"ToDo \"x\" not found (404) — ToDo x not found (HTTP 404)"}}
```

`code` is one of `usage`, `auth`, `not_found`, `permission`, `validation`, `network`, `server`, `partial`, `interrupted`, `error`. `status` and `exc_type` are present when the site answered.

---

### Basic Setup & Settings

*   **`init`**: Interactive setup wizard — creates your initial config. Choose between OAuth 2.0 browser flow (`--oauth`), API key/secret (`--apikey`) or username/password (`--password`). Auto-adds `https://` if you omit the scheme.
*   **`site`**: Manage multiple Frappe sites without editing the config file:
    *   `ffc site list` — show all configured sites (name, URL, auth method, default; in `--json`, `default` is a boolean)
    *   `ffc site add [--oauth|--apikey|--password]` — add a new site interactively
    *   `ffc site use [name]` — set the default site (shows selection menu if name omitted)
    *   `ffc site remove [name] [--yes]` — remove a site (shows selection menu if name omitted)
    *   `ffc site rename OLD NEW` / `ffc site edit NAME --url URL` — rename a site / change its URL
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

`ffc update` installs a release only if its `checksums.txt` carries a valid Ed25519 signature (`checksums.txt.sig`) from the release key built into ffc, and the archive matches its checksum. Someone who can replace the release assets cannot also forge the signature. Versions before 1.6.1 do not check the signature, so the first update from them relies on the checksum alone. `install.sh` checks the same signature when OpenSSL 3 is present; `install.ps1` checks the checksum only. For a manual download, run `gh attestation verify <archive> --repo nasroykh/foxmayn_frappe_cli`.

ffc also checks for updates automatically (at most once a day) and prints a one-line notice to stderr when a newer version is available.

---

### Document Operations (CRUD)

**1. `get-doc`** (Read a document)

For Single DocTypes (e.g. `System Settings`, `HR Settings`), `--name` can be omitted — the DocType name is used as the document name automatically.

```bash
ffc get-doc -d "Company" -n "My Company"
ffc get-doc -d "User" -n "jane@example.com" -f '["name","email"]'
ffc get-doc -d "System Settings" --json
```

**2. `list-docs`** (List documents)
```bash
ffc list-docs -d "ToDo" --filters '{"status":"Open"}' -o "modified desc"
ffc list-docs -d "Sales Invoice" --all --output ndjson > invoices.ndjson
```

`--all` fetches every row page by page (`--page-size`, default 500). The `json`, `ndjson`, `csv` and `tsv` formats are written as each page arrives; the table, `yaml` and `--jq` wait for the whole list. If a page fails or the run is interrupted, the rows already written stay on stdout and the exit code reports the failure; a `json` array is then left without its closing bracket. Without `--order-by`, `--all` sorts by `creation asc, name asc`, because the default order (`modified desc`) moves rows between pages while documents change. `list-doctypes` and `list-reports` take `--all` too.

Note that `-o` is `--order-by` here, not the output format; use `--output` for the format.

**3. `create-doc`** (Create a document)
```bash
ffc create-doc -d "ToDo" --data '{"description":"Update CLI README","status":"Open"}'
```

**4. `update-doc`** (Update a document)

For Single DocTypes, `--name` can be omitted — the DocType name is used automatically.

```bash
ffc update-doc -d "ToDo" -n "83a12bf99c" --data '{"status":"Closed"}'
ffc update-doc -d "System Settings" --data '{"default_currency":"USD"}'
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

**7. `bulk-create`, `bulk-update`, `bulk-delete`** (Many documents in one run)

Input is a JSON array, inline with `--data` / `--names` or from a file with `--file` (`-` reads stdin). Each item is reported as created/updated/deleted, `error`, `interrupted` (cut off by Ctrl+C; the server may or may not have applied it) or `skipped`. The command exits non-zero unless every item succeeded.

```bash
ffc bulk-create -d "ToDo" --data '[{"description":"a"},{"description":"b"}]'
ffc bulk-update -d "ToDo" --file updates.json --concurrency 4   # each item needs a "name"
ffc bulk-delete -d "ToDo" --names "TD-001,TD-002" --yes
ffc bulk-delete -d "Note" --file names.json --fail-fast          # use --file for names containing commas
```

`--concurrency` (1-10, default 1) sets the number of requests in flight; `--fail-fast` stops starting new items after the first failure.

---

### Document Lifecycle

Submit, cancel, amend, copy, rename and restore documents through Frappe's own methods, so its validations, permissions and hooks run. A state error (submitting a submitted document, amending a draft) exits 6.

```bash
ffc submit-doc -d "Sales Invoice" -n ACC-SINV-2026-00001
ffc cancel-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --check   # list the submitted documents that block the cancel
ffc cancel-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --yes
ffc amend-doc  -d "Sales Invoice" -n ACC-SINV-2026-00001 --data '{"due_date":"2026-11-30"}'   # new draft ACC-SINV-2026-00001-1
ffc copy-doc   -d "Item" -n SKU-001 --data '{"item_code":"SKU-002"}'   # like the desk's Duplicate
ffc rename-doc -d Customer -n "Acme Ltd" --to "Acme Limited"            # --merge --yes merges into an existing one
ffc restore-doc -d ToDo -n TD-0001                                       # undo delete-doc (System Manager)
ffc discard-doc -d "Sales Invoice" -n ACC-SINV-2026-00002 --yes          # cancel a draft (Frappe v16+)
```

- `amend-doc` keeps the fields marked "no copy", like the desk's Amend; `copy-doc` drops them, on child rows too.
- `restore-doc` takes the document (`-d`/`-n`, its latest unrestored deletion) or the Deleted Document (`--deleted`). A DocType named by hash or naming series may restore it under a new name; the command prints it.
- `submit-doc` and `cancel-doc` refuse a DocType with an active Workflow; use `ffc workflow`.

**Workflows**

```bash
ffc workflow transitions -d "Leave Application" -n HR-LAP-2026-00001   # actions you can apply now
ffc workflow apply -d "Leave Application" -n HR-LAP-2026-00001 --action Approve
ffc workflow bulk-apply -d "Leave Application" --file names.json --action Approve --yes
ffc workflow pending -d "Leave Application"                            # open Workflow Actions
```

`bulk-apply` applies the action one document at a time and reports each result like the bulk commands (`--concurrency`, `--fail-fast`, exit 8 if any failed).

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
ffc call-method --method "frappe.desk.form.load.getdoc" --args '{"doctype":"ToDo","name":"TD-0001"}' --raw
```

`call-method` prints the response's `message`. `--raw` prints the whole response object, which includes what desk methods return next to `message` (`docs`, `docinfo`, `_server_messages`).

### Any endpoint: `ffc api`

**`api [METHOD] PATH`** sends a request with the site's credentials to any path of the site and prints the body. Use it for endpoints that ffc has no command for, and for file downloads.

```bash
ffc api /api/method/frappe.desk.form.load.getdoc -f doctype=ToDo -f name=TD-0001
ffc api /api/resource/Currency --paginate -f 'fields=["name","enabled"]'
ffc api POST /api/method/frappe.client.set_value -f doctype=ToDo -f name=TD-0001 -f fieldname=status -f value=Closed
ffc api /private/files/contract.pdf --output-file contract.pdf
echo '{"description":"x"}' | ffc api POST /api/resource/ToDo --input -
```

- **Paths only.** `PATH` is relative to the site URL. Absolute and protocol-relative URLs are refused, so the credentials never reach another host. `-H` cannot set `Authorization`, `Cookie`, `Host`, `X-Frappe-Site-Name` or `X-Forwarded-Host`: the last three pick the site on a multi-tenant bench.
- **Method.** The default is GET, or POST when `--input` is given. Unlike `gh api`, fields alone do not switch to POST: on `/api/resource` that would create a document. To write, name the method.
- **Fields.**
  - `-f key=value` sends a string.
  - `-F key=value` sends a typed value: `true`, `false`, `null`, a number, a JSON object or array, or `@FILE` / `@-`.
  - For GET and HEAD, fields go in the query string; for other methods, in a JSON body. With `--input`, the body is the file and the fields go in the query string.
- **Output.**
  - A pipe or file gets the body bytes unchanged.
  - A terminal gets indented JSON or cleaned text. A binary body is refused: pass `--output-file` or redirect stdout.
  - `-i` prints the status line and headers to stderr, with cookie values hidden.
  - `--silent` prints nothing.
  - A status of 400 or more prints the body (with `--json`, only the error JSON on stderr) and exits with the matching [exit code](#exit-codes).
- **Pagination.** `--paginate` fetches every page of a `/api/resource/<DocType>` or `/api/v2/document/<DocType>` list. The default page size is 500. It prints `{"data": [...]}`. Pages are requested by offset, so pass an `order_by` if the list may change during the run.
- **`--jq` and `--output`** render a JSON body (read into memory first), for example `--jq ".docs[0]"`.
- **Limits.** The request is not retried. `--timeout` bounds the whole download.

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
ffc mcp status   # show PID, URL, bearer token, site, start time, log path
ffc mcp stop     # stop the server and clean up (--force if it is not responding)
```

The HTTP endpoint is `http://127.0.0.1:<port>/mcp` (Streamable HTTP transport, localhost only, bearer token shown by `ffc mcp status`).

**Read-only mode** — expose only read tools (no create, update, delete, bulk, lifecycle, workflow or `call_method`):
```bash
ffc mcp --read-only --site prod
```

Available MCP tools (22): `ping`, `get_doc`, `list_docs`, `count_docs`, `get_schema`, `list_doctypes`, `list_reports`, `run_report`, `get_transitions`, and the write tools `create_doc`, `update_doc`, `delete_doc`, `bulk_create`, `bulk_update`, `bulk_delete`, `call_method` (`full_response: true` returns the whole response object), `submit_doc`, `cancel_doc`, `amend_doc`, `copy_doc`, `rename_doc`, `apply_workflow`.

Limits: a tool result over 512 KiB is refused with a hint to narrow it (`limit`, `fields`, `filters`, `keys`); `run_report` returns at most 500 rows unless `limit` is given; bulk tools take at most 200 items per call.

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
│   │   ├── site_client.go    # loadSite/newClient: the one way to get a site client (OAuth refresh)
│   │   ├── helpers.go        # callSite, spinner, confirm, input parsing
│   │   ├── auth_wizard.go    # Shared API key / password / OAuth wizard for init and site add
│   │   ├── init.go           # init
│   │   ├── oauth_flow.go     # OAuth PKCE callback server and flow
│   │   ├── site.go           # site list/add/remove/use
│   │   ├── config_cmd.go     # Interactive settings menu, config get/set
│   │   ├── ping.go, get_doc.go, list_docs.go, create_doc.go, update_doc.go,
│   │   │   delete_doc.go, count_docs.go, get_schema.go, list_doctypes.go,
│   │   │   list_reports.go, run_report.go, call_method.go   # data commands
│   │   ├── api.go            # api: raw requests to any site path
│   │   ├── bulk.go           # Bulk worker pool and input parsers
│   │   ├── bulk_create.go, bulk_update.go, bulk_delete.go
│   │   ├── submit_doc.go, cancel_doc.go, discard_doc.go, amend_doc.go (amend-doc, copy-doc),
│   │   │   rename_doc.go, restore_doc.go, workflow.go   # document lifecycle
│   │   ├── update.go         # update (self-update)
│   │   ├── update_check.go   # background update check + PersistentPreRunE
│   │   ├── mcp.go            # mcp subcommand (stdio/HTTP/detach, --read-only)
│   │   ├── mcp_args.go       # MCP argument parsing and result limits
│   │   ├── mcp_tools.go      # MCP tool definitions (22 with mcp_lifecycle_tools.go)
│   │   ├── mcp_lifecycle_tools.go  # submit/cancel/amend/copy/rename/workflow tools
│   │   ├── mcp_daemon.go     # detached server, status/stop, state file
│   │   └── mcp_detach_unix.go / mcp_detach_windows.go  # platform process handling
│   ├── client/
│   │   ├── http.go           # Transport policy: timeout, body cap, redirects, retries
│   │   ├── client.go         # Frappe REST API client (Bearer, token and session auth)
│   │   ├── raw.go            # Raw requests (ffc api): SitePath, streamed bodies
│   │   ├── lifecycle.go      # Submit, cancel, amend, copy, rename, restore, workflow
│   │   ├── oauth.go          # ExchangeOAuthCode, RefreshOAuthToken, GetOAuthUser
│   │   └── session.go        # Username/password login
│   ├── config/
│   │   ├── config.go         # Config loading and env overrides
│   │   ├── file.go           # Locked, atomic, comment-preserving config edits
│   │   └── format.go         # Number/date formatting
│   ├── output/output.go      # Table (lipgloss) and JSON formatters
│   ├── relsig/               # Ed25519 signing of checksums.txt; release public keys (keys.go)
│   ├── text/text.go          # Strips terminal control characters from server data
│   └── version/version.go    # Build-time version injection
├── tools/relsign/            # Release key generation and checksums.txt signing (not shipped)
├── config.example.yaml       # Example config
├── Makefile                  # build, install, test, lint, tidy, vet, fmt
├── .goreleaser.yaml          # Cross-compilation and release config
├── .github/workflows/        # ci.yml (vet, race tests, cross-build), release.yml
├── install.sh                # One-liner install script (Linux/macOS)
├── install.ps1               # One-liner install script (Windows PowerShell)
├── go.mod
└── go.sum
```

## Development

```bash
make tidy        # Install/update all dependencies
make build       # Compile binary to ./bin/ffc
make install     # Install to $GOPATH/bin and set up the config
make test        # Run tests with the race detector
make lint        # gofmt check, go vet, staticcheck
make vuln        # govulncheck
make contract SITE=<site>  # contract tests against a disposable real site (writes test data)
make vet         # Run go vet
make fmt         # Format code with gofmt
make clean       # Remove compiled binary
make skills-init # Link the ffc skills (skills/) into .claude/, .cursor/ and .agent/
```

## Adding New Commands

1. Create `internal/cmd/<command_name>.go`
2. Define a `*cobra.Command` variable
3. In `init()`, call `rootCmd.AddCommand(yourCmd)`
4. A command that writes calls `addDryRun(yourCmd, false)` and skips its confirmation prompt when `dryRunOn(cmd)`

The global `siteName`, `configPath`, and `jsonOutput` flags are available package-wide.

## License

MIT
