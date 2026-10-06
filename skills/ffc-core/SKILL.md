---
name: ffc-core
description: Read and change Frappe or ERPNext data from the terminal with ffc (Foxmayn Frappe CLI) - get, list, count, create, update and delete documents, filters and fields, DocType schemas, output formats and --jq, exit codes, dry runs and safe-write habits. Use this whenever the user mentions ffc, a Frappe or ERPNext site, a DocType or a document (Sales Invoice, Customer, ToDo, Item...), or wants Frappe data in a script, even if they never name ffc. Start here; it routes to the other ffc-* skills.
---

# ffc core: query and change Frappe data

ffc is a Go command-line client for Frappe and ERPNext sites over the REST API. It is a community project (Foxmayn), **not an official Frappe project**.

## Before the first command

- `ffc --version` fails, or `ffc site list` shows no site: use the **ffc-setup** skill.
- `ffc site list --json` lists the configured sites. With more than one, pass `-s NAME` on every command so a write never lands on the default site by accident.
- `ffc whoami --json` names the user, their roles and the Frappe major version (`.server.major`; some commands need v16).

## Rules that keep an agent safe

1. **Use machine output.** Add `--json` (`-j`) or `--output json|ndjson|csv|tsv|yaml`. The table view is for humans: formatted numbers, cut columns. Machine output also turns errors into JSON on stderr.
2. **Never wait on a prompt.** Without a terminal ffc refuses to prompt (`--no-input` forces this) and fails with "pass --yes". Add `--yes` only after the user approved that exact write.
3. **Read, preview, then write.** `get-schema` for field names and types, `get-doc` for current values, `--dry-run` to see the request (it sends no write and exits 0), `ffc can` to check the right. Ask the user before writing to any site that is not clearly a test site.
4. **Never show secrets.** Do not print `config.yaml` or any `api_secret`, `password`, token or the bearer token from `ffc mcp status`. `--debug` redacts secrets; still keep `--debug=body` output out of shared logs.
5. **Read the exit code.** 0 ok, 2 usage, 3 auth, 4 not found, 5 permission, 6 validation or conflict, 7 network or server, 8 partial bulk failure. Details: [references/exit-codes.md](references/exit-codes.md).

## Global flags

| Flag | Meaning |
| --- | --- |
| `-s, --site NAME` | Site from the config (default: `default_site`) |
| `-c, --config PATH` | Config file (default `~/.config/ffc/config.yaml`) |
| `-j, --json` | JSON output (same as `--output json`) |
| `--output FMT` | `table`, `json`, `ndjson`, `csv`, `tsv`, `yaml` |
| `--jq EXPR` | Filter the JSON result with jq (strings print raw) |
| `--no-input` | Never prompt; fail instead |
| `-q, --quiet` | No spinner |
| `--timeout DUR` | Per-request timeout, default `30s` (raise for heavy reports) |
| `--debug[=body]` | Trace HTTP on stderr, secrets redacted |

Env vars `FFC_SITE`, `FFC_CONFIG`, `FFC_TIMEOUT`, `FFC_OUTPUT`, `FFC_DEBUG` set the same things; a flag wins.

## Documents

```bash
ffc get-doc -d "Sales Invoice" -n SINV-0001 --json --keys name,status,grand_total
ffc get-doc -d "System Settings" --json                     # Single DocType: --name optional
ffc list-docs -d ToDo -f name,status,modified --filters '{"status":"Open"}' -o "modified desc" -l 50 --json
ffc list-docs -d "Sales Invoice" --all --output csv -f name,customer,grand_total > invoices.csv
ffc count-docs -d "Sales Invoice" --filters '[["outstanding_amount",">",0]]' --json
ffc create-doc -d ToDo --data '{"description":"Call Acme"}' --json --keys name
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --dry-run --json
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --if-unmodified "2026-10-05 16:41:58.083711" --json
ffc delete-doc -d ToDo -n TD-0001 --dry-run --json           # then --yes, once approved
```

- `list-docs` returns only `name` unless you pass `-f`. Default limit 20; `-l 0` = no limit; `--start` offsets; `--all` pages through everything.
- JSON flags (`--filters`, `--data`, `--set`, `--args`) also take `@file.json`, or `@-` for stdin.
- `update-doc` sends only the fields in `--data`. A child table you send replaces the whole table, so send every row you want to keep.
- Avoid lost updates: read `modified` (`get-doc --keys modified`) and pass it to `--if-unmodified`; exit 6 means someone saved in between, so read again and redo.
- `edit-doc` opens `$EDITOR` and needs a human at a terminal. Agents use `update-doc`.

Every flag, plus Single DocTypes, schema keys, `whoami`, `can` and `ping`: [references/documents.md](references/documents.md).

## Schema, names and permissions

```bash
ffc list-doctypes --module Accounts --json
ffc get-schema -d "Sales Invoice" --json --keys fields       # compact field list
ffc get-schema -d "Sales Invoice" --json --refresh           # skip the 1 h cache after Customize Form
ffc search acme -d Customer --json                           # title -> document name
ffc can -d "Sales Invoice" --perm create                     # exit 5 when denied
ffc can -d ToDo -n TD-0001 --perm write --json
```

## Reference files

- [references/filters-and-fields.md](references/filters-and-fields.md): filter syntax, operators, child-table filters, fields, ordering, paging.
- [references/output-formats.md](references/output-formats.md): formats, `--jq`, `--keys`, `--all`, numbers, error JSON.
- [references/exit-codes.md](references/exit-codes.md): every exit code and when it happens.
- [references/documents.md](references/documents.md): flags of the document, schema and identity commands.

## Other ffc skills

| Task | Skill |
| --- | --- |
| Bulk create/update/delete, submit, cancel, amend, rename, restore, workflows | ffc-bulk-lifecycle |
| Reports, search, aggregates, group counts, server methods, raw `ffc api` | ffc-reports-api |
| Upload/download files, PDFs, comments, assignments, tags, shares, document history | ffc-files-collab |
| Install, sites, login methods, config, doctor, update, cache, desktop app | ffc-setup |
| Let an AI client use Frappe through `ffc mcp` | ffc-mcp |
