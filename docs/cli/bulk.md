# Bulk operations

Create, update or delete many documents in one run, with a per-item report: `bulk-create`, `bulk-update`, `bulk-delete`.

```bash
ffc bulk-create -d ToDo --data '[{"description":"Task 1"},{"description":"Task 2"}]'
ffc bulk-update -d ToDo --file updates.json --concurrency 4        # each item needs "name"
ffc bulk-delete -d ToDo --names "TD-0001,TD-0002" --yes
```

## Input

| Command | Input |
| --- | --- |
| `bulk-create` | A JSON array of objects (field values), with `--data` or `--file`. |
| `bulk-update` | A JSON array of objects, each with `name` plus the fields to change, with `--data` or `--file`. Or `--filters` plus `--set` (below). |
| `bulk-delete` | Names with `--names` (comma-separated), or a JSON array of names (strings or numbers) with `--file`. Or `--filters`. Use `--file` for names that contain commas. |

`--file -` reads stdin. The whole input is validated before anything is sent.

```bash
cat customers.json | ffc bulk-create -d Customer --file - --json
```

## Select documents by filter

`bulk-update` and `bulk-delete` can pick the documents with `--filters` (same syntax as `list-docs`). ffc lists the matching names first, shows them, and asks before changing anything; pass `--yes` in scripts.

```bash
ffc bulk-delete -d ToDo --filters '{"status":"Cancelled"}' --dry-run      # see what would go
ffc bulk-delete -d ToDo --filters '[["modified","<","2025-01-01"]]' --yes
ffc bulk-update -d ToDo --filters '{"status":"Open","owner":"a@example.com"}' --set '{"status":"Closed"}' --yes
```

## Flags

| Flag | Commands | Default | Description |
| --- | --- | --- | --- |
| `-d, --doctype` | all | | DocType (required). |
| `--data` | create, update | | JSON array inline. |
| `--file` | all | | JSON file (`-` for stdin). |
| `--names` | delete | | Comma-separated names. |
| `--filters` | update, delete | | Select by filters (`@FILE`, `@-` accepted). |
| `--set` | update | | Fields to set on every matching document (with `--filters`). |
| `--concurrency` | all | `1` | Requests in flight, 1 to 10. |
| `--fail-fast` | all | off | Stop starting new items after the first failure. |
| `-y, --yes` | update, delete | off | Skip the confirmation (`bulk-delete` always asks; `bulk-update` asks only with `--filters`). |
| `--dry-run` | all | off | Show the requests instead of sending them. |

## Results and exit codes

Each item is reported as created, updated or deleted, `error`, `skipped` (not started after `--fail-fast`), or `interrupted` (cut off by Ctrl+C: the server may or may not have applied it, so check before re-running). Processing continues after a failed item unless `--fail-fast` is set.

The command exits 0 only when every item succeeded. When any item failed or was skipped, it exits 8, and the summary says how many. See [Exit codes](exit-codes.md).

With `--dry-run --json` the output is `{"dry_run": true, "requests": [{method, url, body}, ...]}`.

Workflow actions on many documents: [`ffc workflow bulk-apply`](lifecycle-and-workflow.md#workflows-ffc-workflow).

## See also

- [Documents](documents.md)
- [Dry runs and debugging](dry-run-and-debugging.md)
- [Exit codes](exit-codes.md)
