---
name: ffc-bulk-lifecycle
description: Change many Frappe/ERPNext documents at once or move documents through their lifecycle with ffc - bulk-create, bulk-update, bulk-delete, bulk-submit, bulk-cancel (by list or by filters), submit, cancel, amend, copy, rename or merge, restore deleted, discard drafts, and Workflow actions (transitions, apply, bulk-apply, pending). Use it whenever the user wants to import or fix records in bulk, submit or cancel invoices and orders, approve or reject through a workflow, or undo a delete, even if they only say "approve these" or "cancel that invoice". Read ffc-core first for flags and safety rules.
---

# Bulk changes and document lifecycle

These commands write. Follow ffc-core's rules: machine output (`--json`), `--dry-run` first, user approval before `--yes`, `-s SITE` when several sites exist.

## Bulk create, update, delete

```bash
ffc bulk-create -d ToDo --data '[{"description":"a"},{"description":"b"}]' --json
ffc bulk-create -d Customer --file customers.json --concurrency 4 --json   # --file - reads stdin
ffc bulk-create -d ToDo --file todos.json --atomic --timeout 2m --json     # all or none, one request
ffc bulk-update -d ToDo --file updates.json --json                         # each object needs "name"
ffc bulk-update -d ToDo --filters '{"status":"Open"}' --set '{"status":"Closed"}' --dry-run --json
ffc bulk-delete -d ToDo --names "TD-0001,TD-0002" --dry-run --json
ffc bulk-delete -d Note --file names.json --yes --json                     # names with commas: use --file
ffc bulk-delete -d ToDo --filters '[["modified","<","2025-01-01"]]' --dry-run --json
ffc bulk-submit -d "Sales Invoice" --names "ACC-SINV-2026-00001,ACC-SINV-2026-00002" --dry-run --json
ffc bulk-cancel -d "Sales Invoice" --filters '{"customer":"CUST-001"}' --dry-run --json
```

| Flag | Applies to | Notes |
| --- | --- | --- |
| `-d, --doctype` | all | required |
| `--data` | create, update | JSON array (also `@FILE`) |
| `--file` | all | JSON file; `-` = stdin; delete takes an array of names |
| `--names` | delete, submit, cancel | comma-separated |
| `--filters` | update (with `--set`), delete, submit, cancel | names are listed first (one request), up to 10 shown, then confirmed; submit adds `docstatus` 0, cancel 1 |
| `--set` | update | JSON object applied to every matching document |
| `--concurrency` | all | 1-10, default 1 |
| `--fail-fast` | all | stop starting items after the first failure (not with `--atomic`) |
| `--atomic` | create | one `insert_many` request: all or none, at most 200 items |
| `-y, --yes` | update `--filters`, delete, submit, cancel | skip the confirmation |
| `--dry-run` | all | show every request; nothing written |

- The whole input is validated before anything is sent.
- Empty filters (`{}`, `[]`) are refused because they match every document. To act on all of them, say so: `'[["name","is","set"]]'`.
- JSON result: `{"created"|"updated"|"deleted"|"submitted"|"cancelled": N, "failed": N, "skipped": N, "results": [{"index","name","status","error"}]}`. Status is the verb, `error`, `interrupted` or `skipped`.
- `bulk-submit` / `bulk-cancel`: one document at a time in the order given (keep `--concurrency 1`; cancel linking documents first, see `cancel-doc --check`); a DocType with an active Workflow is refused once (exit 6, use `workflow bulk-apply`); a document already submitted/cancelled (or a draft, for cancel) is an `error` item, not a skip. Status is `submitted` / `cancelled`.
- Exit 8 unless every item succeeded. An `interrupted` item (Ctrl+C) may or may not have been applied: check it before re-running.
- `bulk-create --atomic`: one request, one transaction. Success is the usual report; any failure is a single error (exit by class, e.g. 6) with no per-item report, and nothing was created. Over 200 items, a foreign `doctype` in an item, or `--concurrency`/`--fail-fast` is a usage error (exit 2) before anything is sent. A timeout (exit 7) means the batch may or may not exist: check the site, raise `--timeout` (default 30s is tight for 200). Hooks that `db.commit()` and DDL (Custom Field, DocType) break all-or-nothing, so not for those.
- Without `--atomic`, each item is a separate request with Frappe's validations, so one bad row does not stop the others unless `--fail-fast`.

## Lifecycle (docstatus 0 draft, 1 submitted, 2 cancelled)

```bash
ffc submit-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --json --keys name,docstatus
ffc cancel-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --check --json   # what blocks the cancel; cancels nothing
ffc cancel-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --yes --json
ffc amend-doc  -d "Sales Invoice" -n ACC-SINV-2026-00001 --data '{"due_date":"2026-11-30"}' --json
ffc copy-doc   -d Item -n SKU-001 --data '{"item_code":"SKU-002"}' --json
ffc rename-doc -d Customer -n "Acme Ltd" --to "Acme Limited" --json
ffc rename-doc -d Customer -n "Acme Ltd" --to "Acme Limited" --merge --yes --json
ffc restore-doc -d ToDo -n TD-0001 --json                  # or --deleted <Deleted Document name>
ffc discard-doc -d "Sales Invoice" -n ACC-SINV-2026-00007 --yes   # Frappe v16+
```

| Command | What it does | Asks? | Notes |
| --- | --- | --- | --- |
| `submit-doc` | 0 → 1 via `frappe.client.submit` | no | reads the doc and sends it back, so a concurrent change fails (exit 6) |
| `cancel-doc` | 1 → 2 | yes (`-y`) | `--check` lists submitted documents linking to it; those block the cancel (exit 6) |
| `amend-doc` | new draft from a cancelled doc | no | named `<name>-1`, then `-2`...; keeps "no copy" fields like the desk's Amend; `--data` overrides |
| `copy-doc` | duplicate any doc | no | drops identity and "no copy" fields like the desk's Duplicate; child rows copied |
| `rename-doc` | rename, links updated | only `--merge` | the DocType must allow renaming; `--merge` folds it into an existing `--to` and cannot be undone |
| `restore-doc` | undo a delete | no | System Manager only; may get a new name (printed) |
| `discard-doc` | draft 0 → 2, kept as cancelled | yes (`-y`) | needs Frappe v16 (exit 6 on older) |

All take `-d`, `-n` (restore: or `--deleted`), `--dry-run`; submit, cancel, amend, copy also `--keys`. A wrong state (submitting a submitted doc, amending a draft) is exit 6. Check the version with `ffc whoami --jq .server.major`.

## Workflows

`submit-doc` and `cancel-doc` refuse a DocType with an active Workflow (exit 6). Move those documents with actions:

```bash
ffc workflow transitions -d "Leave Application" -n HR-LAP-2026-00001 --json   # actions you may apply now
ffc workflow apply -d "Leave Application" -n HR-LAP-2026-00001 --action Approve --json
ffc workflow bulk-apply -d "Leave Application" --names "HR-LAP-2026-00001,HR-LAP-2026-00002" --action Approve --dry-run --json
ffc workflow bulk-apply -d "Leave Application" --file names.json --action Approve --yes --json
ffc workflow pending -d "Leave Application" --json                            # open Workflow Actions for my roles
```

- `apply`: `-d`, `-n`, `--action` (required, one of the listed transitions), `--keys`, `--dry-run`. An action may itself submit or cancel the document.
- `bulk-apply`: `--names` or `--file`, `--action`, `--concurrency 1-10`, `--fail-fast`, `-y`, `--dry-run`. One request per document; asks unless `--yes`; exit 8 if any failed.
- `pending`: `-d` (optional), `-l` (default 50), `--all`, `--page-size`.

## Safe sequence for a destructive or bulk change

1. Count what matches: `ffc count-docs -d DT --filters '...' --json`.
2. Check the right: `ffc can -d DT --perm delete` (or `write`, `submit`, `cancel`).
3. Preview: same command with `--dry-run --json`; show the user the names or count.
4. Run with `--yes` only after the user agrees; read the per-item results and the exit code.
