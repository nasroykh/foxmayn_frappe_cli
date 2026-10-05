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

OAuth access tokens are refreshed automatically when they expire, before any command that talks to the site — you don't need to re-run `ffc init`. A long run (a bulk command, `list-docs --all`, `api --paginate`, `workflow bulk-apply`, the MCP server) that outlives the token (about an hour) refreshes it when the site rejects it with a 401 and repeats that request once; concurrent workers and other ffc processes share one refresh, and the new tokens are saved to the config. If the refresh fails, the command fails with the 401 and a hint to run `ffc site add --oauth` again.

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

Every command that writes (`create-doc`, `update-doc`, `edit-doc`, `delete-doc`, the bulk commands, the lifecycle commands, `workflow apply` and `bulk-apply`, the collaboration commands, `call-method` and `api`) takes `--dry-run`. It prints the request it would send, with secrets redacted, sends nothing that writes and exits 0. Reads still run, so the dry run fails where the real run would (a missing document, a draft that cannot be amended). `update-doc --dry-run` also shows which fields would change, and `delete-doc --dry-run` checks that the document exists. No confirmation is asked. `call-method` and `api` send nothing at all, since any request to a method can write. A username/password site still logs in and out, and an OAuth site with an expired token still refreshes it, so the reads can run.

```bash
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --dry-run
ffc bulk-delete -d Note --file names.json --dry-run --json   # {"dry_run":true,"requests":[{method,url,body}...]}
```

#### Debugging requests

`--debug` writes one line per HTTP exchange to stderr: method, URL, status, Frappe `exc_type`, time and sizes. `--debug=body` adds the headers and bodies (the first 64 KiB of each). Credentials never appear: `Authorization`, cookies and session ids, API keys, and any password, secret, token, OAuth code or verifier in a URL, header or body are shown as `***`, also inside JSON passed as a string argument and in a body cut at the 64 KiB limit. A `frappe.client.set_value` of a password field hides the value too. The trace is stderr only, so it is safe with `--json` and with `ffc mcp` (in detached mode it goes to `mcp.log`). Use `--debug=body`, not `--debug body`.

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
| 1 | Other error (config file, unexpected response, declined confirmation, a failed `ffc doctor` check) |
| 2 | Usage: unknown command or flag, wrong arguments, invalid flag value, input needed but no terminal |
| 3 | Authentication: rejected credentials (401), failed login, or `ffc ping` / `ffc whoami` finding the site sees the credentials as Guest |
| 4 | Not found (404) |
| 5 | Permission denied (403), or `ffc can` found the permission is not held |
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
*   **`ping`**: Check the connection to the active Frappe site. `frappe.ping` answers without credentials, so ping also asks who the credentials belong to and prints the user (`--json` adds `user`); credentials the site rejects, or that it sees as Guest, fail with exit 3. **Behaviour change:** before this, `ffc ping` succeeded on any pong, so it also "passed" with a wrong API key. A script that only wants to know whether the site answers should call `curl -fsS <site>/api/method/frappe.ping` instead.
*   **`whoami`**, **`can`**, **`doctor`**: who you are on the site, what you may do, and whether the setup is healthy (see below).
*   **`update`**: Update ffc to the latest release in place — works regardless of how it was installed.

```bash
ffc update           # check for update and confirm before installing
ffc update --check   # only print whether an update is available
ffc update --yes     # update without confirmation
```

`ffc update` installs a release only if its `checksums.txt` carries a valid Ed25519 signature (`checksums.txt.sig`) from the release key built into ffc, and the archive matches its checksum. Someone who can replace the release assets cannot also forge the signature. Versions before 1.6.1 do not check the signature, so the first update from them relies on the checksum alone. `install.sh` checks the same signature when OpenSSL 3 is present; `install.ps1` checks the checksum only. For a manual download, run `gh attestation verify <archive> --repo nasroykh/foxmayn_frappe_cli`.

ffc also checks for updates automatically (at most once a day) and prints a one-line notice to stderr when a newer version is available.

---

### Identity, Permissions and Health

```bash
ffc whoami [--refresh] [--json]      # user, roles, installed apps and versions
ffc can -d "Sales Invoice" --perm create          # may I create one at all?
ffc can -d ToDo -n TD-0001 --perm write [--all]   # may I change this document?
ffc doctor [--json]                  # check config, network, login, server, local state (changes nothing)
```

**`whoami`** asks the site for the user (`frappe.auth.get_logged_user`), the user's roles and the installed apps (`frappe.utils.change_log.get_versions`). A user who is not a System Manager can read their own User document but not its roles table (permission level 1), so the roles are read from the user's Has Role rows (`frappe.client.get_list` with the parent DocType `User`), which they may list. If the site refuses that too, `roles_source` is `unavailable` and `notes` says why. The automatic roles (All, Guest, Desk User) are not Has Role rows and are not listed. When the site sees the credentials as Guest, the result is printed and the command exits 3, like `ping`. The URL is printed with any password in it hidden.

The app versions are cached for 24 hours per site in `<user cache dir>/ffc/<site>-<hash>/<credential>/server.json` (`~/.cache/ffc` on Linux; directory 0700, file 0600). `--refresh` reads them again. The cache is dropped when a request suggests the server changed (404, 5xx, no answer) and ignored when the site URL changed.

**`can`** exits 0 when the permission is held, **5** when it is not, 4 when the DocType or document does not exist. The answer is printed either way (`{"doctype","name","perm","allowed","basis"}` with `--json`). `--perm` is one of `select`, `read` (default), `write`, `create`, `delete`, `submit`, `cancel`, `amend`, `print`, `email`, `report`, `import`, `export`, `share`; with `-n` any lower-case permission type the site defines (custom types, Frappe v16) is accepted too.

*   With `-n NAME` the site judges that document (`frappe.client.has_permission`): user permissions, sharing and controller rules count. `basis` is `document`. `--all` also lists every right the site returns for it (`frappe.client.get_doc_permissions`, role rules only; custom types included; the table is printed when the answer is "denied" too; the two can differ, e.g. a User may edit their own User document).
*   Without `-n` the site cannot judge a DocType alone (`has_permission` needs a document), so ffc applies the DocType's permission rows (permission level 0) to the user's roles: `basis` is `doctype`. That answers "may I create a Sales Invoice", but does not evaluate user permissions, sharing or controller rules, and does not check the System Settings option `disable_document_sharing`. The rows are applied as Frappe's role permission system does (`frappe/permissions.py` `get_role_permissions`): an `if_owner` row limits a right to the user's own documents only when no other row grants it, and never `create`; then `select` and `read` stay allowed and `owner_only` is set, while any other right is denied with `owner_only` set (it holds only per document). `select` is implied by `read`. `submit`, `cancel` and `amend` need a submittable DocType and `import` an importable one. A child table is refused (exit 2): check its parent DocType. The automatic roles All and Guest count; Desk User counts only when the User document shows a System User. A user who cannot read their own `user_type` (permission level 1, i.e. anyone but a user manager) is evaluated without it, and `note` says so when a Desk User row would have changed the answer.
*   The user `Administrator` is allowed everything, whether or not the document exists.

**`doctor`** runs these checks and prints each as pass, warn or fail, with a hint:

| Check | What it looks at |
| ----- | ---------------- |
| `config.file`, `config.dir` | the config file is 0600 (fail otherwise: it holds credentials) and its directory 0700 (warn) |
| `config.parse` | the file parses and a site resolves (values in parse errors are never printed) |
| `config.lock` | no stale `config.yaml.lock` (warn; ffc breaks it on the next write) |
| `net.tls` | the certificate, taken from the same request as `net.reachable` (so a proxy from the environment is honoured), verifies and is not about to end (14 days: warn); plain `http://` to a non-loopback host is a warning |
| `net.reachable` | the URL answers `frappe.ping` with a pong and does not redirect (a redirect fails: reads follow it, writes fail with "site redirected"; the hint names the final URL) |
| `net.clock` | the local clock against the server's `Date` header: over 1 minute warns, over 10 minutes fails |
| `auth.valid` | the credentials log in; names the user |
| `auth.oauth_token` | an OAuth site's token expiry. An expired token is reported (warn: the next command refreshes it; fail when there is no refresh token), never renewed |
| `server.versions` | the installed apps, read live (warns for Frappe older than v15) |
| `server.api_v2` | whether `/api/v2` exists (warn only on Frappe v16+, which provides it) |
| `mcp.daemon`, `mcp.state_file` | the detached MCP server's health; its state file (it holds the bearer token) is 0600, whether or not the server is running |
| `update.check` | whether the background update check saw a newer ffc |

`doctor` exits **1** when any check fails, 0 otherwise (warnings do not fail it). Checks that need the site are skipped when it cannot be reached. With `--json` the output is an array of `{"check","status","message","hint"}` (`hint` is `""` when there is nothing to do). No secret is printed. `doctor` changes nothing: it does not renew an expired OAuth token, it neither reads nor writes the version cache (use `ffc whoami --refresh` for that), and a password site signs in and out again.

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

**Aggregates in `--fields` depend on the Frappe version.** Frappe v16 refuses an SQL function written as a string (`--fields '["status","count(name) as n"]'` fails with `ValidationError: SQL functions are not allowed as strings in SELECT`, exit 6) and wants a dict (`{"COUNT":"name","as":"n"}`) instead, which v15 does not accept (500 `TypeError`). `list-docs` sends `--fields` as given and has no `group_by`; use `ffc aggregate` (below), which writes the form the site's version takes.

**3. `create-doc`** (Create a document)
```bash
ffc create-doc -d "ToDo" --data '{"description":"Update CLI README","status":"Open"}'
```

**4. `update-doc`** (Update a document)

For Single DocTypes, `--name` can be omitted — the DocType name is used automatically.

```bash
ffc update-doc -d "ToDo" -n "83a12bf99c" --data '{"status":"Closed"}'
ffc update-doc -d "System Settings" --data '{"default_currency":"USD"}'
ffc update-doc -d "ToDo" -n "83a12bf99c" --data '{"status":"Closed"}' --diff
ffc update-doc -d "ToDo" -n "83a12bf99c" --data '{"status":"Closed"}' --if-unmodified "2026-10-05 16:41:58.083711"
```

`--diff` reads the document first and, once the update is saved, prints each field it changed (old → new) on stderr (add `--dry-run` to only look). The update carries the `modified` it read, so a save in between fails with exit 6 rather than making the diff wrong. `--if-unmodified` sends the `modified` value you read (`get-doc --keys modified`): if anyone saved the document since, Frappe refuses the update and nothing is saved (exit 6).

**4b. `edit-doc`** (Edit a document in your editor)

```bash
ffc edit-doc -d "Sales Order" -n SO-0001
EDITOR="code --wait" ffc edit-doc -d ToDo -n 83a12bf99c
```

Like `kubectl edit`: the editable fields open as YAML in `$VISUAL` or `$EDITOR` (`vi`, or `notepad` on Windows). Read-only, hidden, computed, system and Password fields are left out, and so are fields Frappe would not let you change: a permission level your roles do not write, or (v16) a masked field. On a submitted document only the fields allowed on submit are shown, and rows of a table that is not allowed on submit cannot be added, removed or moved. Child tables are lists of rows keyed by row `name`: delete a row to remove it, add one without `name` to add it (a row whose `name:` line was deleted but is otherwise unchanged is refused: it would replace the row and lose its other columns). After you save, ffc shows the changes (removed rows stand out) and asks before saving (`--yes` skips the question, `--dry-run` shows the request). Only the changed fields are sent, with the `modified` timestamp you opened: if someone saved the document in between, nothing is saved (exit 6). A changed child table is sent whole, because Frappe replaces a table with the rows it gets. A file with a YAML error opens again with the error on top (save it unchanged to give up); an unchanged or empty file cancels (with `--json`: `{"cancelled":true,"reason":...}`). If the saved document does not have a value as sent, ffc warns instead of reporting success. The file lives in a private temporary directory (0600) that is removed afterwards. `edit-doc` needs a terminal and fails with `--no-input`. `$VISUAL`/`$EDITOR` is split into words like a shell would (quotes group a path with spaces: `EDITOR="'/opt/my editor/ed' --wait"`), without running a shell.

**5. `delete-doc`** (Delete a document)
```bash
ffc delete-doc -d "ToDo" -n "83a12bf99c" --yes
```
*(The `--yes` / `-y` flag skips the interactive confirmation prompt).*

**6. `count-docs`** (Count documents)
```bash
ffc count-docs -d "Sales Invoice" --filters '{"status":"Unpaid"}'
ffc count-docs -d "ToDo" --group-by status          # one row per status, most frequent first
ffc count-docs -d "Sales Invoice" --group-by assigned_to
```

`--group-by FIELD` runs the list view sidebar count (`frappe.desk.listview.get_group_by_count`): rows `{FIELD, count}`, most frequent first, at most 50 groups (with 50, ffc warns that there may be more; `ffc aggregate` has no such cap). `owner` puts your own group first; `assigned_to` is not a field: it counts, per System User, the ToDo records allocated to them that are not Cancelled (Open and Closed) and whose `reference_name` is a matching document's name. Frappe does not compare `reference_type`, so a ToDo on a document of another DocType with the same name counts too. On v16 a Link field whose DocType shows titles in links also gets a `title` column. A field the DocType lacks exits 6.

**`aggregate`** (Count, sum, average, min and max per group, on the server)
```bash
ffc aggregate -d ToDo --group-by status                                   # counts
ffc aggregate -d "Sales Invoice" --group-by customer --sum grand_total --count --limit 10
ffc aggregate -d "Sales Invoice" --group-by status,currency --sum grand_total,outstanding_amount --filters '{"docstatus":1}'
ffc aggregate -d "Sales Invoice" --sum grand_total --min posting_date --max posting_date   # one row, no grouping
```

Each aggregate is a column: `count`, `sum_F`, `avg_F`, `min_F`, `max_F`; without an aggregate flag ffc counts. `--sum/--avg/--min/--max` repeat or take a comma list; `--group-by` takes up to 5 fields. Field names must be plain fieldnames of the DocType (letters, digits, underscore): anything else, including `link_field.field` or `child_table.field`, is a usage error before any request (v15 cannot group by those and v16 cannot aggregate them). `--order-by` names a group-by field or an aggregate column (`"sum_grand_total desc"`, default: the first aggregate, descending). `--limit` caps the groups (default 100, `0` = all); ffc warns on stderr when groups were cut. ffc sorts the rows itself (ties by the group fields; nulls first, numbers by value, text case-insensitively): Frappe v16 on PostgreSQL ignores the order when grouping, so with a limit ffc asks for at least 20 groups and, when they come back out of order, fetches up to 10000 groups and keeps the top ones (it warns if there were more than 10000). Numbers keep the server's literal (a Currency sum is `20.0`).

ffc writes the aggregates for the site's version: dicts on v16, `sum(f) as sum_f` text on v15 (qualified as ``sum(`tabX`.`f`)`` only when a filter joins another table; `min`/`max` always take the bare field, since v15 reads a qualified one inside them as a table name). On v15 a field named `if`, `like`, `regexp` or `rlike` cannot be grouped (its query check reads the name as an SQL operator; ffc refuses it before sending). A field above your permission level exits 5 on every version: ffc reads which fields you may read (the DocType's meta and your roles) and refuses it before sending, because v15 would silently leave its aggregate out (or fail with an SQL error on its column alias). When the meta is not readable, ffc still turns v15's answer into the same error. The version comes from the cache `whoami` fills (24 h); when it is unknown or wrong (the site was upgraded), the site refuses the first form (v16: 417 `ValidationError`, v15: 500 `TypeError`), ffc retries once with the other, drops the stale cache, and reports whichever error is about your query.

**7. `bulk-create`, `bulk-update`, `bulk-delete`** (Many documents in one run)

Input is a JSON array, inline with `--data` / `--names` or from a file with `--file` (`-` reads stdin). Each item is reported as created/updated/deleted, `error`, `interrupted` (cut off by Ctrl+C; the server may or may not have applied it) or `skipped`. The command exits non-zero unless every item succeeded.

```bash
ffc bulk-create -d "ToDo" --data '[{"description":"a"},{"description":"b"}]'
ffc bulk-update -d "ToDo" --file updates.json --concurrency 4   # each item needs a "name"
ffc bulk-delete -d "ToDo" --names "TD-001,TD-002" --yes
# Select by filters: the matching names are listed, shown, and asked about (--yes in scripts)
ffc bulk-delete -d "ToDo" --filters '{"status":"Cancelled"}' --dry-run
ffc bulk-update -d "ToDo" --filters '{"status":"Open"}' --set '{"status":"Closed"}' --yes
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

### Collaboration

Comments, assignments, tags and shares go through the methods the desk calls, so Frappe's permission checks, notifications and timeline entries apply.

```bash
ffc comment -d Task -n TASK-0042 "Called the customer, waiting for the PO"   # plain text; "-" reads stdin
ffc assign   -d Task -n TASK-0042 --to jane@example.com,bob@example.com --priority High --date 2026-11-01
ffc unassign -d Task -n TASK-0042 --to bob@example.com
ffc tag      -d Customer -n "Acme Ltd" vip export
ffc untag    -d Customer -n "Acme Ltd" vip
ffc share    -d Project -n PROJ-0001 --user jane@example.com --write --yes   # or --everyone; asks without --yes
ffc unshare  -d Project -n PROJ-0001 --user jane@example.com
```

- `comment` posts as the user ffc signs in as. The text is escaped, so `<b>` shows as typed, and line breaks are kept; `--html` sends HTML, which Frappe sanitises (scripts, forms and event attributes are removed). It needs read permission on the document.
- `assign` creates an open ToDo per user and Frappe notifies them. A user already assigned is reported (`already_assigned`), not assigned twice. An assignee who cannot read the document gets it shared read-only, or the call fails when document sharing is disabled. `unassign` cancels the ToDo; a user not assigned is reported (`not_assigned`).
- `tag` needs write permission and creates missing Tag records; a tag cannot contain a comma. `untag` ignores case, like Frappe.
- `share` grants read plus `--write`, `--submit` (submittable DocTypes only) and `--share`; `--notify` sends a notification. Sharing again with the same user replaces that share's rights. It widens access, so it asks for confirmation unless `--yes` is given. `unshare` removes the share (there is no whitelisted `frappe.share.remove`; it clears the read right with `frappe.share.set_permission`, as the desk does) and reports `removed: false` when there was none.
- Each command reads the current state first and reports what changed; with `--json` the result lists it (`assigned`, `assignees`, `tags`, the share's rights).

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
ffc get-schema -d "Sales Invoice" --refresh   # skip the local cache
```

`get-schema` caches the compact schema per site for **1 hour** and answers from the cache until then: no request and no login, the same output. `--refresh` fetches it again (do that right after a Customize Form change); `--full` always fetches, because the cache keeps only the compact view. **A cache hit does not check credentials** (or that the DocType still exists): a revoked key or a deleted DocType still gets the cached schema, exit 0, until the entry expires; `--refresh` does check. `--debug` says when the answer came from the cache.

### Shell completion and the local cache

```bash
ffc completion bash > /etc/bash_completion.d/ffc        # or: source <(ffc completion bash)
ffc completion zsh > "${fpath[1]}/_ffc"
ffc completion fish > ~/.config/fish/completions/ffc.fish
ffc completion powershell | Out-String | Invoke-Expression
```

Tab completes site names (`--site`, `site use/remove/rename/edit`, `config set --default-site`, `mcp --sites`), DocTypes (`-d/--doctype` on every command, `cache warm --doctypes`, `mcp --allow-doctypes/--deny-doctypes`), fields (`--fields`: the last item of the comma-separated list), report names (`run-report -n`) and fixed values (`--output`, `--number-format`, `--date-format`, `can --perm`, `mcp --toolsets/--confirm/--allow-tools`, `--debug`). Document names (`-n/--name`) are never completed.

Completion reads only the config file and the local cache. It never sends a request, never signs in and never writes the cache; with no fresh cache entry it offers nothing. The cache lives in `<user cache dir>/ffc/<site>-<hash>/<credential>/` (`~/.cache/ffc` on Linux; directories 0700, files 0600, written atomically), bound to the site URL (a changed URL ignores it) and to the login: `<credential>` is a hash of the API key, username or OAuth client id (never a secret), so another login on the same site, `FFC_API_KEY` on a named site included, never sees what one login cached. A site defined only by `FFC_*` variables is keyed by its URL with any password removed. `site remove`, `site rename`, `site edit` and `site add`/`init` over an existing name delete that site's cache:

| Entry | Kept | Filled by |
| --- | --- | --- |
| `server.json` (app versions) | 24 h | `whoami` |
| `doctypes.json` | 24 h | `list-doctypes` returning the whole list (no `--module`; `--all`, `--limit 0` or fewer rows than the limit), `cache warm` |
| `reports.json` (with `ref_doctype`) | 24 h | `list-reports`, same rule, `cache warm` |
| `schema/<doctype>.json` (compact, at most 100; the least recently used is evicted) | 1 h | `get-schema`, `cache warm --doctypes` |

The lists change only when an app or a DocType/report is added, and a stale name costs one failed command, so they last a day. A schema is what writes are built from and Customize Form changes it at once, so it lasts an hour. Documents are never cached.

```bash
ffc cache warm                                   # DocType and report lists (2 requests)
ffc cache warm --doctypes "Sales Invoice,Customer"  # + those schemas (3 requests each)
ffc cache status                                 # entries, age, size, fresh/stale
ffc cache clear                                  # the selected site, every login
ffc cache clear --all-sites
```

A failed item of `cache warm` (the report list, a schema) is listed in `errors` (`[{"item","error"}]`, `item` is `reports` or `schema:<DocType>`) and makes it exit **8**; what did come back is cached. A failed DocType list fails the whole command.

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

### Files and PDF

```bash
ffc upload contract.pdf -d Customer -n "ACME Corp"                 # private, in Home/Attachments
ffc upload logo.png -d Item -n ITEM-001 --field image --public     # also sets Item.image
tar cz notes | ffc upload - --filename notes.tgz -d ToDo -n TD-0001
ffc attachments -d Customer -n "ACME Corp"                         # name, file_name, file_url, is_private, file_size
ffc download /private/files/contract.pdf                           # saved as ./contract.pdf
ffc download /files/logo.png -o - | sha256sum
ffc pdf -d "Sales Invoice" -n SINV-0001 --format "Detailed Invoice" --no-letterhead -o inv.pdf
```

- **`upload FILE`** posts the file to `upload_file` (multipart), as the desk's Attach button does. Files are **private** unless `--public` (Frappe's own default is public). FILE `-` reads stdin and needs `--filename`. The document must exist: `upload_file` would otherwise attach the file to nothing. A file over the site's limit (System Settings > Max File Size, 25 MiB by default) is refused before it is sent (exit 6); the site's allowed file extensions still apply. `--field` also sets that Attach field of the document to the file URL (a second request). Uploads are never retried. `--dry-run` shows the request with the file's name and size, never its content.
- **`download FILE_URL`** takes a File's `file_url` (`/files/…` or `/private/files/…`) or a full URL of the site itself (same scheme, host and port); other hosts are refused, so the credentials never leave the site. The file goes to `--output-file`/`-o` (default: its name in the current directory) through a temporary file, so an interrupted download leaves nothing; an existing file is replaced only with `--force`. `-o -` writes to stdout (binary is refused on a terminal). A private file that is missing and one you may not read both get Frappe's 403 (exit 5). Private files are saved 0600.
- **`attachments`** lists the File documents attached to a document, oldest first (`--limit`, default 100; `--all`).
- **`pdf`** saves `frappe.utils.print_format.download_pdf` (`--format`, `--letterhead` or `--no-letterhead`, `--lang`) to `-o` (default `<name>.pdf`). A response that is not `application/pdf` starting with `%PDF-` is never saved. Frappe v16 renders only a few PDFs at a time and answers 503 with `Retry-After: 10` when no slot frees up within 10 s; like every GET, the request is retried twice. A site in Docker whose wkhtmltopdf cannot reach the site's own URL fails with `OSError` (500).

---

### Search and name resolution

**`search`** (Find a document by text)
```bash
ffc search acme -d Customer            # resolve "acme" to document names (like a Link field)
ffc search "" -d Item --limit 5        # the first 5 items
ffc search "overdue invoice"           # keyword search across DocTypes
ffc search acme -d Customer --json
```

The text is a positional argument (several words are joined; put `--` before a text that starts with a dash). `--limit` / `-l` defaults to 20 and must be at least 1.

- **With `--doctype`** it runs `frappe.desk.search.search_link`, the search a Link field does. It honours the DocType's search fields and title field, its link query (such as ERPNext's item query) and the user's permissions, and returns `value` (the document name), `description` and, for some DocTypes, `label`. Use it to turn "Acme" into `CUST-0042`. An empty text lists the first documents. Frappe marks this answer cacheable for 60 seconds (`Cache-Control: max-age=60`). ffc keeps no HTTP cache, but a caching proxy in front of the site may serve it up to a minute old, so a document created a moment ago may not appear yet.
- **Without `--doctype`** it runs `frappe.utils.global_search.search`, ranked by relevance, and returns `doctype`, `name`, `content` and `rank`. It covers only the DocTypes listed in **Global Search Settings** and, in them, only the fields flagged **In Global Search**; a DocType missing there never returns a hit (use `list-docs --filters` instead). `a & b` searches the phrases `a` and `b` separately (at most 5 phrases) and combines the hits, cut to `--limit`.

---

### Document context

**`doc-info`** (What the form sidebar and timeline show for one document)
```bash
ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001           # versions, comments, files, assignments, shares, tags, workflow log
ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001 --links   # + linked documents per DocType
ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001 --timeline --json
ffc doc-info -d Customer -n "Acme" --onload                      # + party dashboard (billing this year, total unpaid)
```

It reads `frappe.desk.form.load.get_docinfo`, which changes nothing: who changed which fields (the last 10 versions, only for DocTypes with **Track Changes**), comments, emails, attachments (names, not contents), open assignments, shares, tags, the workflow log and your permissions on the document. `--name` defaults to the DocType for Single DocTypes.

- **Versions show only the fields you may read.** Frappe returns the stored version data whole, including fields you cannot read (a permission level above 0 that none of your roles reads) and Password fields. ffc filters it as the desk timeline does: it reads the meta (`frappe.desk.form.load.getdoctype`) and your roles, and keeps a change only when the field is in the meta, is not a Password field and is at a level your roles read; a child-row change also needs the table field. Changes it drops are counted in `hidden_fields`. Desk User is not counted as one of your roles, so a level only Desk User reads is left out. When the meta or your roles cannot be read, the versions are left out entirely and `notes` says why. `--full` prints Frappe's raw answer, unfiltered.
- `--links` adds `frappe.desk.notifications.get_open_count`, the counts of the form's **Connections** panel: the DocTypes come from the DocType's dashboard, each count stops at 100 (`capped`), a count that ran over one second is `timed_out` (when the whole statement times out, Frappe sends no counts and `notes` says "link counts timed out"), and the counts ignore your permissions (the desk shows them the same way). `internal` entries list the documents this one's own fields point to (an invoice's Sales Order). ffc does not use `frappe.desk.form.linked_with.get`: it covers every Link field but loads every linked document without a limit.
- `--timeline` adds `frappe.desk.form.activity.get_activity_timeline` (Frappe v16 and v15 releases from August 2026 on): every change field by field with its label, emails, comments and logs, oldest first, with the fields you may not read left out. On an older release ffc prints a note and the rest.
- `--onload` loads the document through `frappe.desk.form.load.getdoc` (a POST, never retried) and shows its `__onload` values, such as the Customer and Supplier dashboard, which is not reachable over RPC otherwise. **It writes**: a View Log entry on DocTypes that track views, the "seen" mark on DocTypes that track it, and whatever the controller's `onload` does.
- `--json` prints a compact object: `doctype`, `name`, `permissions`, `versions` (`{name, by, at, changed: [{field, from, to}], rows_added, rows_removed, impersonated_by, hidden_fields}`, newest first; a change in a child row is the field `items[2].qty`), `comments` (`{name, by, at, text}`), `communications`, `attachments` (`{name, file_name, file_url, is_private, size}`), `assignments` (`{user, status, description}`), `shares`, `tags`, `workflow_log`, and with the flags `links` (`{doctype, count, open_count, capped, timed_out, internal, names}`), `timeline` (`{at, by, type, field, text}`), `onload`, plus `notes`. Text is stripped of HTML and cut to 500 characters, version values to 200. `--full` prints the raw responses instead.
- Version values are formatted as Frappe stored them (`"د.ج 300.00"`), and a Password field shows only asterisks. Frappe caches per DocType whether any document has tags, and does not always clear a cached "no", so a tag added to the first tagged document of a DocType can be missing from `tags` for a while.

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
ffc mcp status   # show PID, URL, bearer token, site(s), start time, log path
ffc mcp stop     # stop the server and clean up (--force if it is not responding)
```

The HTTP endpoint is `http://127.0.0.1:<port>/mcp` (Streamable HTTP transport, localhost only, bearer token shown by `ffc mcp status`).

**Several sites** — one server for several sites (opt-in):
```bash
ffc mcp --sites prod,staging      # these sites; the default is --site, else default_site, else the first
ffc mcp --all-sites               # every site in the config
```

Every tool then takes a required `site` argument (one of the served sites), so a call never lands on a site by default, and the `list_sites` tool lists them (name, URL, how ffc signs in, read-only; never secrets). Each site's own `mcp:` policy applies to calls on that site; write tools are exposed when any served site allows them, and a write to a read-only site is refused. The policy flags apply to every site. The set of sites is fixed when the server starts; a served site that is later removed or renamed in the config is refused, never matched to another site. Only the default site signs in at start; the others sign in on their first call, so one that is down does not stop the server. `FFC_API_KEY`/`FFC_API_SECRET` cannot be combined with several sites (they would replace every site's credentials).

> **Warning:** one connection that spans several sites lets one mistake by the agent reach every one of them, including other clients' data. Serve only the sites a task needs, make the others `read_only`, and keep `confirm` on.

**Read-only mode** — expose only read tools (no create, update, delete, bulk, lifecycle, workflow or `call_method`):
```bash
ffc mcp --read-only --site prod
```

**Tool sets** — expose only part of the tools:
```bash
ffc mcp --toolsets core        # documents, schema, reports, search, aggregate, get_doc_context, bulk, call_method, whoami, check_permission
ffc mcp --toolsets lifecycle   # submit_doc, cancel_doc, amend_doc, copy_doc, rename_doc, apply_workflow, get_transitions
ffc mcp --toolsets core,lifecycle,collab   # + add_comment, assign_to, remove_assignment, add_tag, remove_tag
ffc mcp --toolsets core,admin  # + share_doc, unshare_doc
ffc mcp --toolsets core,lifecycle,files   # + list_attachments, attach_file, get_print_html
```
The default is `core,lifecycle`: the `collab`, `admin` and `files` sets are exposed only when named. `list_sites` is always there. Like `--allow-tools`, it only narrows what the policy allows; an unknown set name is a usage error.

**Policy.** Each site can limit what MCP tools may do, in its config entry:

```yaml
sites:
  prod:
    url: https://erp.example.com
    mcp:
      read_only: false
      allow_tools: [get_doc, list_docs, update_doc]   # only these tools are exposed
      allow_doctypes: [Sales Order, Customer]         # only these DocTypes
      deny_doctypes: [Salary Slip]
      allow_methods: [erpnext.selling.*]              # call_method; a trailing * is a prefix
      deny_methods: [frappe.client.delete]
      confirm: always                                 # always | if-supported (default) | never
```

- Built in, whatever the config says:
  - MCP may read but not write the sensitive DocTypes: users, roles and permissions (User, Role, Has Role, Role Profile, Module Profile, User Type, User Group, DocType, DocPerm, Custom DocPerm, User Permission, DocShare, Custom Field, Property Setter, Customize Form), settings and credentials (System Settings, OAuth Client, OAuth Provider Settings, OAuth Bearer Token, OAuth Authorization Code, Connected App, Token Cache, Social Login Key, LDAP Settings, Email Account), code and templates (Server Script, Client Script, Report, Print Format, Website Script, Web Page, Web Form, Custom HTML Block, Webhook, Notification, Auto Email Report, Assignment Rule, Energy Point Rule, Scheduled Job Type), Data Import and File. Listing one in the site's `allow_doctypes` allows writes to it.
  - The collaboration tools are also checked against the DocType they write besides the document: `add_comment` Comment, `assign_to` and `remove_assignment` ToDo, `add_tag` Tag Link and Tag, `remove_tag` Tag Link, `share_doc` and `unshare_doc` DocShare. DocShare is sensitive, so `share_doc` and `unshare_doc` are refused unless the site's `allow_doctypes` lists DocShare (and then the DocTypes to share). `call_method` with the same Frappe methods (`frappe.share.add`, `frappe.desk.form.assign_to.add`, …) is checked the same way. Frappe saves data-URI images in a comment's HTML as private File records, so `add_comment` with `html` and an image (or `call_method` of `frappe.desk.form.utils.add_comment` with one) is also checked against File, which is sensitive. `call_method` of `add_comment` is refused when `comment_email` or `comment_by` names anyone but the signed-in user: Frappe stores them as given.
  - `call_method` refuses `system_console.execute_code`, `user.generate_keys` and the Frappe Cloud app installer (`frappe.integrations.frappe_providers.*`) unless the site's `allow_methods` lists them.
- With `allow_doctypes` set, `call_method` may call only the methods in `allow_methods`, since a method can reach any DocType. `run_report` is checked through the report's `ref_doctype`. `search` with a `doctype` is checked like any read of that DocType; a global `search` names none, so its hits are filtered to the DocTypes the rules allow; the tool then answers `{results, hidden_by_policy}`, where `hidden_by_policy` counts the dropped hits. `get_doc_context` is checked against the document's DocType; the parts it reads from other DocTypes (Version, Comment, Communication, File, ToDo, DocShare, Tag Link, the linked DocTypes, and the timeline entries by their source) are emptied or dropped when the rules do not allow reading that DocType, and `hidden_by_policy` names them: `{sections, linked_doctypes, timeline_entries}`. A timeline log needs Comment and the DocType it reports on: an assignment log ToDo (it names the assignee), a share log DocShare, an attachment log File. `aggregate` is checked like any read of its `doctype`. With `allow_doctypes` or `deny_doctypes` set (config or flag), a query may not reach another table: in `list_docs`, `count_docs`, `aggregate` and the query arguments of `call_method` (`filters`, `or_filters`, `fields`, `order_by`, …), a filter field must be a plain fieldname and a field or `order_by` column may not contain `.` or a backtick (`link_field.field`, `child_table.field` and ``` `tabX`.`f` ``` join another DocType), and the DocType of a four-element filter `[doctype, field, op, value]` is checked like `doctype`.
- An `allow_` list that is present but empty is an error, so it never reads as "none" while meaning "no limit". The same goes for a policy flag given with no value.
- The flags `--allow-tools`, `--allow-doctypes`, `--deny-doctypes`, `--allow-methods` and `--deny-methods` only narrow the config, so an MCP client's config cannot widen what the site's owner allowed.
- The policy is read again on every call, so an edit that narrows it applies at once. Widening the tool list needs a restart.
- A refused call sends nothing to the site and returns an error starting with `policy:` that names the setting to change.
- A misspelt key under `mcp:` is an error, so a policy is never silently ignored.

**Confirmation.** Before `delete_doc`, `bulk_delete`, `cancel_doc`, `rename_doc` with `merge`, `apply_workflow` (an action may submit or cancel), `share_doc` (it widens who can open the document), `assign_to` (Frappe shares the document read-only with an assignee who cannot read it) and the `call_method` equivalents, ffc asks the user through the MCP client (elicitation). The client shows what will be deleted, cancelled or merged, and nothing is sent to the site unless the user ticks Confirm. The `call_method` equivalents are:
- `frappe.client.delete`, `frappe.client.cancel` and `frappe.desk.reportview.delete_items`;
- `frappe.desk.form.save.cancel` and `discard`, `savedocs` with action Cancel, and `cancel_all_linked_docs`;
- `submit_cancel_or_update_docs` with action cancel;
- `run_doc_method` with cancel, discard or rename;
- the `frappe.model.workflow` apply methods;
- `rename_doc` or `update_document_title` with `merge`;
- `frappe.share.add`, `frappe.share.set_permission` with a true `value` (Frappe's default), and `frappe.desk.form.assign_to.add` / `add_multiple`, which may give users access to a document. A "no" returns `cancelled by the user; nothing was changed`.

- `confirm: if-supported` (the default) asks when the client supports elicitation and goes ahead without asking when it does not.
- `confirm: always` refuses the call when the client cannot ask, and names the `ffc` command to run in a terminal instead.
- `confirm: never` never asks.
- `--confirm always` or `--confirm if-supported` can only tighten the site's setting; `--confirm never` is refused.
- An answer is bound to the call it was asked for (site, tool and arguments), expires after 10 minutes and works once.
- ffc trusts the MCP client to show the question to a person; it cannot tell a person's answer from the client's.
- The audit line of such a call records `confirm`: `confirmed`, `unsupported` (the client could not ask and the mode is `if-supported`) or `never`.

Some checks are best effort:

- `call_method` refuses a method name with `/` or spaces (Frappe would cut or strip it and run another method). An undotted name is matched both as itself (an API Server Script) and as `frappe.handler.<name>`. It is checked against the DocTypes its arguments name (`doctype`, `dt`, a `doc` given as an object or JSON string, …), and a `frappe.client` or form-save call that names none is refused. A custom method can still change any DocType without naming it, so `deny_doctypes` and the sensitive list do not bind it, and confirmation does not catch it. For a hard limit, set `allow_doctypes` (which requires `allow_methods`), leave `call_method` out of `allow_tools`, or use `read_only`.
- A Query or Script Report can read tables other than its `ref_doctype`.
- `list_doctypes` and `list_reports` list DocType and report names whatever the DocType lists say.

**Audit log.** Every tool call and resource read, allowed or refused, appends one JSON line to `~/.config/ffc/mcp-audit.jsonl` (next to the config file, 0600). It records the time, site, the client's self-reported name, tool, DocTypes, document names (up to 20), status (`ok`, `error`, `denied`, `invalid`, `confirm_pending`, `declined`), error and duration. A resource read is logged under the tool that served it with `"via": "resource"`. The arguments are logged with secrets redacted, and document data and method arguments reduced to their keys and size. The file is rotated to `mcp-audit.jsonl.1` at 10 MiB.

**Instructions, resources and prompts.** On connecting, the client receives instructions for the model: filter syntax, `get_schema` before writing, `docstatus` and the lifecycle tools, `fields` and `limit` on lists, name versus title (`search` resolves one to the other), which sites are read-only and, with several sites, that every call needs `site`. They mention only the tools this server exposes.

Resources (read-only, JSON):
- `ffc://sites`: the served sites (as `list_sites`).
- `ffc://{site}/schema/{doctype}`: the compact schema (as `get_schema`).
- `ffc://{site}/doc/{doctype}/{name}`: a document (as `get_doc`). Percent-encode each segment: `ffc://prod/doc/Sales%20Invoice/SINV%2F0001`.

A resource read runs through the same tool handler: the site must be served, the site's policy applies (a denied DocType is refused, `read_only` does not matter for reads), it is audited, and the 512 KiB cap applies. A template is offered only when its tool is. A failed read is a JSON-RPC error whose message is the tool's (a refusal starts with `policy:`); mcp-go v1.1.1 gives every such error the code -32603, so read the message, not the code. A URI that does not fit a template (an extra segment, a bad `%` escape) is answered as resource not found.

**Completion.** The server answers `completion/complete` for the templates' `{site}` and `{doctype}` and for the prompts' `site`, `doctype` and `report_name`, from the local cache the CLI fills (`ffc cache warm`, see [Shell completion and the local cache](#shell-completion-and-the-local-cache)); without a fresh cache it offers nothing. It sends no request and offers nothing the site's policy would refuse: a site whose policy does not allow the tools, a denied DocType (for `safe-bulk-import`, which writes, a sensitive one too), a report whose `ref_doctype` the DocType rules deny or do not know. Document names are not completed. `get_schema` itself never uses the cache: a long-lived server must not hand a model an hour-old schema to write with.

Prompts (guidance only; they call nothing): `inspect-doctype` (doctype), `safe-bulk-import` (doctype, optional source), `audit-doc-changes` (doctype, name) and `explain-report` (report_name). Each lists which tools to call in which order and what to check, leaving out steps whose tools this server does not expose. With several sites they take a required `site`. A prompt whose essential tools are not exposed is not offered. Arguments are quoted as JSON in the text, and one longer than its limit (140 characters, 500 for `source`) is refused, never cut.

**Progress and structured results.** `bulk_create`, `bulk_update` and `bulk_delete` send `notifications/progress` after each item when the call carries a progress token; cancelling the call stops starting new items. Progress is best effort: a notification can be dropped when the client reads slowly, or arrive after the result. `count_docs` (`{count, doctype}`), `whoami` (the same object as its text) and `list_sites` (`{sites}`; its text stays the bare list) declare an output schema and return `structuredContent`; their text is unchanged. Every tool has a title.

Available MCP tools (38): `list_sites`, `ping`, `whoami`, `check_permission`, `get_doc`, `get_doc_context` (versions, comments, attachments, assignments, links; `ffc doc-info` without `--onload`, at most 50 comments, emails and workflow log entries, 100 attachments, assignments, shares and tags, 50 changes per version and 100 timeline entries, the rest counted in `omitted`), `list_docs`, `count_docs`, `aggregate`, `get_schema`, `list_doctypes`, `list_reports`, `run_report`, `search`, `get_transitions`, and the write tools `create_doc`, `update_doc` (`if_unmodified: <modified>` fails with TimestampMismatchError if the document was saved since it was read), `delete_doc`, `bulk_create`, `bulk_update`, `bulk_delete`, `call_method` (`full_response: true` returns the whole response object), `submit_doc`, `cancel_doc`, `amend_doc`, `copy_doc`, `rename_doc`, `apply_workflow`, and, in the `collab` and `admin` tool sets, `add_comment`, `assign_to`, `remove_assignment`, `add_tag`, `remove_tag`, `share_doc`, `unshare_doc`. The `files` set (only with `--toolsets …,files`) adds `list_attachments`, `get_print_html` (`{html, style}`, or `{text}` with `text_only`; over 512 KiB the style is dropped, then the HTML cut with `truncated: true`) and the write tool `attach_file` (`frappe.client.attach_file`; `data` base64 or `encoding: text`, at most 5 MiB decoded, private unless `is_private: false`). No tool returns file contents or PDFs. `attach_file` writes a File, which is a sensitive DocType: it is refused unless the site's `allow_doctypes` lists `File` (and, being an allowlist, the DocTypes to attach to). `list_attachments` and `attach_file` are also checked against the rules for `File`.

Limits: a tool result over 512 KiB is refused with a hint to narrow it (`limit`, `fields`, `filters`, `keys`), except rows: `list_docs` then returns the rows that fit as `{"data": [...], "truncated": true, "next_start": N, "hint": "..."}` (call again with `start: N` for the rest; a list that fits is still a plain array), and `run_report` drops rows from the end and adds `truncated`, `total_rows` and a `hint` saying how many were dropped. Tools whose result can be large tell the client the cap (`_meta` `anthropic/maxResultSizeChars`), so Claude Code does not cut the JSON. `run_report` returns at most 500 rows unless `limit` is given; bulk tools take at most 200 items per call.

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
│   │   ├── whoami.go, can.go, doctor.go, server_cache.go  # identity, permissions, health; version cache
│   │   ├── meta_cache.go, cache_cmd.go, completion.go  # DocType/report/schema cache, ffc cache, shell completion
│   │   ├── ping.go, get_doc.go, list_docs.go, create_doc.go, update_doc.go, edit_doc.go,
│   │   │   delete_doc.go, count_docs.go, get_schema.go, list_doctypes.go,
│   │   │   list_reports.go, run_report.go, search.go, doc_info.go, aggregate.go, call_method.go   # data commands
│   │   ├── api.go            # api: raw requests to any site path
│   │   ├── bulk.go           # Bulk worker pool and input parsers
│   │   ├── bulk_create.go, bulk_update.go, bulk_delete.go
│   │   ├── submit_doc.go, cancel_doc.go, discard_doc.go, amend_doc.go (amend-doc, copy-doc),
│   │   │   rename_doc.go, restore_doc.go, workflow.go   # document lifecycle
│   │   ├── collab.go, collab_cmds.go   # comment, assign/unassign, tag/untag, share/unshare
│   │   ├── update.go         # update (self-update)
│   │   ├── update_check.go   # background update check + PersistentPreRunE
│   │   ├── mcp.go            # mcp subcommand (stdio/HTTP/detach, --read-only, policy flags)
│   │   ├── mcp_policy.go     # per-site MCP policy (DocTypes, tools, methods)
│   │   ├── mcp_audit.go      # MCP audit log (mcp-audit.jsonl)
│   │   ├── mcp_confirm.go    # confirmation through MCP elicitation
│   │   ├── mcp_sites.go      # multi-site MCP (--sites, --all-sites, list_sites)
│   │   ├── mcp_args.go       # MCP argument parsing and result limits
│   │   ├── mcp_tools.go      # MCP tool definitions (38 with mcp_lifecycle_tools.go, mcp_identity_tools.go, mcp_doc_context.go, mcp_aggregate.go, mcp_collab_tools.go, mcp_files_tools.go)
│   │   ├── files.go / pdf.go # upload, download, attachments, pdf
│   │   ├── mcp_lifecycle_tools.go  # submit/cancel/amend/copy/rename/workflow tools
│   │   ├── mcp_collab_tools.go     # comment/assign/tag (collab) and share (admin) tools
│   │   ├── mcp_completion.go # completion/complete for resource templates and prompts (cache only)
│   │   ├── mcp_daemon.go     # detached server, status/stop, state file
│   │   └── mcp_detach_unix.go / mcp_detach_windows.go  # platform process handling
│   ├── client/
│   │   ├── http.go           # Transport policy: timeout, body cap, redirects, retries
│   │   ├── client.go         # Frappe REST API client (Bearer, token and session auth)
│   │   ├── raw.go            # Raw requests (ffc api): SitePath, streamed bodies
│   │   ├── lifecycle.go      # Submit, cancel, amend, copy, rename, restore, workflow
│   │   ├── collab.go         # Comments, assignments, tags, shares
│   │   ├── server.go         # Versions, logged user, roles, permission checks
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
