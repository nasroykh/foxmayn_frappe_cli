# Documents

Read, list, count, create, update and delete documents: `get-doc`, `list-docs`, `count-docs`, `create-doc`, `update-doc`, `delete-doc`.

## Get one document: `get-doc`

```bash
ffc get-doc -d Company -n "My Company"
ffc get-doc -d User -n jane@example.com --fields '["name","email","enabled"]'
ffc get-doc -d "Sales Invoice" -n SINV-0001 --json --keys name,status,grand_total
ffc get-doc -d "System Settings" --json       # Single DocType: --name defaults to the DocType
```

| Flag | Description |
| --- | --- |
| `-d, --doctype` | DocType (required). |
| `-n, --name` | Document name. Defaults to the DocType name for Single DocTypes. |
| `-f, --fields` | Fields to show, as a JSON array or comma-separated list. |
| `--keys` | Comma-separated keys to keep in JSON output. |

**`--fields` reads less.** For plain field names on a regular DocType, ffc asks Frappe for just those columns (`frappe.client.get_value`) instead of the whole document with its child tables, which matters for large documents such as an invoice with hundreds of rows. When that answer is incomplete (a Table field, an unknown field, a document the list query does not show) or refused, ffc reads the whole document as before, so output, errors and exit codes do not change. Single DocTypes, DocTypes that Frappe guards with per-document permission hooks (User, File, ToDo, Contact, Address, Communication, ...) and `--keys` outside `--fields` always read the whole document. One difference remains: `get_value` applies list permissions (roles, user permissions, permission query conditions) but not an app's own per-document `has_permission` hook, the same as `list-docs`.

The table view shows Field/Value rows.

## List documents: `list-docs`

```bash
ffc list-docs -d ToDo --filters '{"status":"Open"}' --order-by "modified desc"
ffc list-docs -d User --fields name,email,enabled --limit 10
ffc list-docs -d "Sales Invoice" --all --output csv --fields name,customer,grand_total > invoices.csv
ffc list-docs -d ToDo --filters @filters.json --jq '.[].name'
```

| Flag | Default | Description |
| --- | --- | --- |
| `-d, --doctype` | | DocType (required). |
| `-f, --fields` | | Fields to fetch: JSON array or comma-separated. Without it Frappe returns only `name`. |
| `--filters` | | JSON object or list of conditions (also `@FILE`, `@-`). |
| `-o, --order-by` | | Sort, e.g. `"modified desc"`. |
| `-l, --limit` | `20` | Maximum rows; `0` means no limit. |
| `--start` | `0` | Offset into the result. |
| `--all` | off | Fetch every row, page by page. |
| `--page-size` | `500` | Rows per request with `--all`. |

**`--all`.** The `json`, `ndjson`, `csv` and `tsv` formats are written as each page arrives; the table, `yaml` and `--jq` wait for the whole list. If a page fails or you press Ctrl+C, the rows already written stay on stdout and the exit code reports the failure (a `json` array is then left without its closing bracket). Without `--order-by`, `--all` sorts by `creation asc, name asc`, because the default order (`modified desc`) moves rows between pages while documents change.

**Aggregates in `--fields`.** Frappe v16 refuses SQL functions written as text (`--fields '["count(name) as n"]'` fails with exit 6), and v15 accepts nothing else. Use [`ffc aggregate`](search-and-aggregate.md#totals-per-group-aggregate), which writes the form your site's version expects.

## Count documents: `count-docs`

```bash
ffc count-docs -d ToDo
ffc count-docs -d "Sales Invoice" --filters '{"status":"Paid"}'
ffc count-docs -d ToDo --group-by status
ffc count-docs -d "Error Log" --at-least 1000
```

The count is printed alone on stdout, ready for scripts. `--group-by FIELD` returns one row per value, most frequent first, at most 50 groups. See [Search and aggregate](search-and-aggregate.md#counts-per-value-count-docs---group-by) for the details and for `assigned_to`.

`--at-least N` answers whether N or more documents match, and stops counting at N (Frappe's `frappe.desk.reportview.get_count` with a limit), so it stays cheap on a large table. It prints `true` or `false`. With `--json` the answer is `{"doctype", "at_least", "result", "count"}`, where `count` is exact below N and N otherwise. On MariaDB, Frappe v16 gives that count 1 second; when it runs out, `result` and `count` are `null`, ffc prints `unknown` and a warning on stderr, and exits 0. A plain `count-docs` has no such limit. `--at-least` cannot be combined with `--group-by`.

## Create a document: `create-doc`

```bash
ffc create-doc -d ToDo --data '{"description":"Call Acme","priority":"Medium"}'
ffc create-doc -d Note --data @note.json --json
ffc create-doc -d ToDo --data '{"description":"x"}' --json --keys name
```

| Flag | Description |
| --- | --- |
| `-d, --doctype` | DocType (required). |
| `--data` | JSON object of field values (required; also `@FILE`, `@-`). |
| `--keys` | Keys to keep in JSON output. |
| `--dry-run` | Show the request instead of sending it. |

Child tables are lists of row objects inside `--data`.

## Update a document: `update-doc`

```bash
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}'
ffc update-doc -d "System Settings" --data '{"default_currency":"USD"}'
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --diff
ffc update-doc -d ToDo -n TD-0001 --data '{"status":"Closed"}' --if-unmodified "2026-10-05 16:41:58.083711"
```

| Flag | Description |
| --- | --- |
| `-d, --doctype` | DocType (required). |
| `-n, --name` | Document name. Defaults to the DocType name for Single DocTypes. |
| `--data` | JSON object with only the fields to change (required). A `name` key is ignored with a warning: use [`rename-doc`](lifecycle-and-workflow.md#rename-or-merge-rename-doc) to rename. |
| `--diff` | Print each changed field (old → new) on stderr after the save. |
| `--if-unmodified` | Fail (exit 6) if the document's `modified` is no longer this value. |
| `--keys` | Keys to keep in JSON output. |
| `--dry-run` | Show the request and which fields would change, without saving. |

**Avoid overwriting someone else's change.** `--if-unmodified` sends the `modified` timestamp you read earlier (`ffc get-doc ... --keys modified`); if anyone saved the document since, Frappe refuses the update and nothing is saved (exit 6). `--diff` does the same automatically with the timestamp it reads just before the update.

A child table in `--data` replaces the whole table: Frappe keeps only the rows you send. To change one row safely, use [`edit-doc`](edit-doc.md).

## Delete a document: `delete-doc`

```bash
ffc delete-doc -d ToDo -n TD-0001           # asks for confirmation
ffc delete-doc -d Note -n "Old Note" --yes
```

| Flag | Description |
| --- | --- |
| `-d, --doctype` | DocType (required). |
| `-n, --name` | Document name (required, also for Single DocTypes). |
| `-y, --yes` | Skip the confirmation. Required without a terminal. |
| `--dry-run` | Check that the document exists and show the request. |

Declining the prompt exits non-zero. A deleted document can usually be brought back with [`restore-doc`](lifecycle-and-workflow.md#restore-a-deleted-document-restore-doc).

## Errors you may see

| Situation | Exit code |
| --- | --- |
| The document or DocType does not exist | 4 |
| You lack permission | 5 |
| Validation failed, duplicate name, or the document changed since you read it | 6 |

Full list: [Exit codes](exit-codes.md).

## See also

- [edit-doc](edit-doc.md)
- [Bulk operations](bulk.md)
- [Output formats](output-formats.md)
- [Schema and cache](schema-and-cache.md)
