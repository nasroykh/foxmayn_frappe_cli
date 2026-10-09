# Bulk operations

Create, update, delete, submit or cancel many documents in one run, with a per-item report: `bulk-create`, `bulk-update`, `bulk-delete`, `bulk-submit`, `bulk-cancel`.

```bash
ffc bulk-create -d ToDo --data '[{"description":"Task 1"},{"description":"Task 2"}]'
ffc bulk-update -d ToDo --file updates.json --concurrency 4        # each item needs "name"
ffc bulk-delete -d ToDo --names "TD-0001,TD-0002" --yes
ffc bulk-submit -d "Sales Invoice" --names "ACC-SINV-2026-00001,ACC-SINV-2026-00002" --yes
ffc bulk-cancel -d "Sales Invoice" --filters '{"customer":"CUST-001"}' --dry-run
```

## Input

| Command | Input |
| --- | --- |
| `bulk-create` | A JSON array of objects (field values), with `--data` or `--file`. |
| `bulk-update` | A JSON array of objects, each with `name` plus the fields to change, with `--data` or `--file`. Or `--filters` plus `--set` (below). |
| `bulk-delete` | Names with `--names` (comma-separated), or a JSON array of names (strings or numbers) with `--file`. Or `--filters`. Use `--file` for names that contain commas. |
| `bulk-submit`, `bulk-cancel` | The same as `bulk-delete`: `--names`, `--file` or `--filters`. |

`--file -` reads stdin. The whole input is validated before anything is sent.

```bash
cat customers.json | ffc bulk-create -d Customer --file - --json
```

## Select documents by filter

`bulk-update`, `bulk-delete`, `bulk-submit` and `bulk-cancel` can pick the documents with `--filters` (same syntax as `list-docs`). ffc lists the matching names first, shows them, and asks before changing anything; pass `--yes` in scripts.

```bash
ffc bulk-delete -d ToDo --filters '{"status":"Cancelled"}' --dry-run      # see what would go
ffc bulk-delete -d ToDo --filters '[["modified","<","2025-01-01"]]' --yes
ffc bulk-update -d ToDo --filters '{"status":"Open","owner":"a@example.com"}' --set '{"status":"Closed"}' --yes
```

`bulk-submit` adds `docstatus = 0` to the filters and `bulk-cancel` adds `docstatus = 1`, so only drafts (or only submitted documents) are listed and shown. A filter object that already has a `docstatus` key is refused.

## Submit and cancel

`bulk-submit` and `bulk-cancel` do for many documents what [`submit-doc` and `cancel-doc`](lifecycle-and-workflow.md) do for one: each document is read and then submitted (`frappe.client.submit`) or cancelled (`frappe.client.cancel`), so its validations and hooks run.

```bash
ffc bulk-submit -d "Journal Entry" --file names.json --yes --json
ffc bulk-cancel -d "Sales Invoice" --names "ACC-SINV-2026-00002,ACC-SINV-2026-00001" --fail-fast
```

- Both ask for confirmation unless `--yes`; a dry run does not ask.
- Documents go one at a time, in the order given (by name with `--filters`): `--concurrency` defaults to `1` because concurrent submits of documents that post ledger or stock entries can deadlock. Order matters for cancel too: submitted documents that link to one block its cancel, so list the linking documents first (`ffc cancel-doc --check` shows them).
- A DocType with an active Workflow is refused once, before anything is sent (exit 6); use [`workflow bulk-apply`](lifecycle-and-workflow.md#workflows-ffc-workflow).
- A document that is not in the right state is a failed item (`error`) with a message, not a skip: a submitted or cancelled one in `bulk-submit`, a draft or cancelled one in `bulk-cancel`. In a dry run it stops the plan with exit 6.

## Flags

| Flag | Commands | Default | Description |
| --- | --- | --- | --- |
| `-d, --doctype` | all | | DocType (required). |
| `--data` | create, update | | JSON array inline. |
| `--file` | all | | JSON file (`-` for stdin). |
| `--names` | delete, submit, cancel | | Comma-separated names. |
| `--filters` | update, delete, submit, cancel | | Select by filters (`@FILE`, `@-` accepted). |
| `--set` | update | | Fields to set on every matching document (with `--filters`). |
| `--concurrency` | all | `1` | Requests in flight, 1 to 10. Keep `1` for submit and cancel. |
| `--fail-fast` | all | off | Stop starting new items after the first failure. |
| `-y, --yes` | update, delete, submit, cancel | off | Skip the confirmation (`bulk-delete`, `bulk-submit` and `bulk-cancel` always ask; `bulk-update` asks only with `--filters`). |
| `--dry-run` | all | off | Show the requests instead of sending them. |

## Results and exit codes

Each item is reported as created, updated, deleted, submitted or cancelled, `error`, `skipped` (not started after `--fail-fast`), or `interrupted` (cut off by Ctrl+C: the server may or may not have applied it, so check before re-running). Processing continues after a failed item unless `--fail-fast` is set.

The command exits 0 only when every item succeeded. When any item failed or was skipped, it exits 8, and the summary says how many. See [Exit codes](exit-codes.md).

With `--dry-run --json` the output is `{"dry_run": true, "requests": [{method, url, body}, ...]}`.

Workflow actions on many documents: [`ffc workflow bulk-apply`](lifecycle-and-workflow.md#workflows-ffc-workflow). Use it instead of `bulk-submit` and `bulk-cancel` on a DocType with a Workflow.

## See also

- [Documents](documents.md)
- [Dry runs and debugging](dry-run-and-debugging.md)
- [Exit codes](exit-codes.md)
