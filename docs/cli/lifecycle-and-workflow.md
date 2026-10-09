# Lifecycle and workflow

Submit, cancel, amend, copy, rename, restore and discard documents, and move them through a Workflow. All of these go through Frappe's own methods, so its validations, permissions and hooks run.

```bash
ffc submit-doc  -d "Sales Invoice" -n ACC-SINV-2026-00001
ffc cancel-doc  -d "Sales Invoice" -n ACC-SINV-2026-00001 --check   # what blocks the cancel?
ffc cancel-doc  -d "Sales Invoice" -n ACC-SINV-2026-00001 --yes
ffc amend-doc   -d "Sales Invoice" -n ACC-SINV-2026-00001           # new draft ACC-SINV-2026-00001-1
```

A document in the wrong state for the command (submitting a submitted document, amending a draft) exits 6. Every command here takes `--dry-run`.

## Submit: `submit-doc`

Submits a draft of a submittable DocType (docstatus 0 → 1), so ledger entries and other hooks run. The document is read first and sent back as read: if someone changes it in between, Frappe rejects the submit (exit 6).

| Flag | Description |
| --- | --- |
| `-d, --doctype`, `-n, --name` | The document (required). |
| `--keys` | Keys to keep in the output, e.g. `name,docstatus`. |

A DocType with an active Workflow is refused: use [`ffc workflow apply`](#workflows-ffc-workflow). To submit many drafts, see [`bulk-submit`](bulk.md#submit-and-cancel).

## Cancel: `cancel-doc`

Cancels a submitted document (docstatus 1 → 2). A cancelled document cannot be edited or submitted again; amend it to make a corrected copy.

| Flag | Description |
| --- | --- |
| `--check` | List the submitted documents that link to it and block the cancel. Cancels nothing. |
| `-y, --yes` | Skip the confirmation. |
| `--keys` | Keys to keep in the output. |

Linked submitted documents block the cancel with `LinkExistsError` (exit 6). A DocType with an active Workflow is refused. To cancel many documents, see [`bulk-cancel`](bulk.md#submit-and-cancel).

## Amend: `amend-doc`

Creates a new draft from a cancelled document, linked by `amended_from`, like the desk's Amend button. Frappe names it `<name>-1`; amending that one gives `<name>-2`. Fields marked "no copy" are kept, as in the desk.

```bash
ffc amend-doc -d "Sales Invoice" -n ACC-SINV-2026-00001 --data '{"due_date":"2026-11-30"}'
```

`--data` overrides fields of the copy (JSON object, `@FILE`, `@-`). Submit the amendment with `submit-doc` when it is ready.

## Duplicate: `copy-doc`

Creates a new document from an existing one, like the desk's Duplicate. Identity fields and "no copy" fields are left out (on child rows too); child rows are copied. Password fields are never copied.

```bash
ffc copy-doc -d Item -n SKU-001 --data '{"item_code":"SKU-002","item_name":"Copy"}'
```

## Rename or merge: `rename-doc`

```bash
ffc rename-doc -d Customer -n "Acme Ltd" --to "Acme Limited"
ffc rename-doc -d Customer -n "Acme Ltd" --to "Acme Limited" --merge --yes
```

Links to the document are updated. The DocType must allow renaming. `--merge` merges the document into an existing one named `--to`: the source disappears and its links point to the target. A merge cannot be undone, so it asks for confirmation unless `--yes`.

## Restore a deleted document: `restore-doc`

The undo of `delete-doc`, from the "Deleted Document" record. Needs the System Manager role.

```bash
ffc restore-doc -d ToDo -n TD-0001        # its latest unrestored deletion
ffc restore-doc --deleted 4f2a1c9e7b      # a specific Deleted Document record
```

A DocType named by hash or naming series may restore the document under a new name; the command prints it.

## Discard a draft: `discard-doc`

Discards a draft (docstatus 0 → 2): the draft is kept as cancelled instead of being deleted. Needs Frappe v16. Asks for confirmation unless `--yes`.

```bash
ffc discard-doc -d "Sales Invoice" -n ACC-SINV-2026-00007 --yes
```

## Workflows: `ffc workflow`

On a DocType with an active Workflow, documents change state through workflow actions (Approve, Reject, ...), not through `submit-doc`, `cancel-doc`, `bulk-submit` or `bulk-cancel`, which refuse such a DocType. An action may submit or cancel the document.

```bash
ffc workflow transitions -d "Leave Application" -n HR-LAP-2026-00001       # actions you can apply now
ffc workflow apply       -d "Leave Application" -n HR-LAP-2026-00001 --action Approve
ffc workflow bulk-apply  -d "Leave Application" --file names.json --action Approve --yes
ffc workflow pending     -d "Leave Application"                            # open Workflow Actions for you
```

| Subcommand | What it does | Main flags |
| --- | --- | --- |
| `transitions` | Lists the actions the current user can apply to the document in its current state. | `-d`, `-n` |
| `apply` | Applies one action. It must be one `transitions` lists. | `-d`, `-n`, `--action`, `--keys`, `--dry-run` |
| `bulk-apply` | Applies one action to many documents, one request each, and reports each result like the bulk commands. Asks unless `--yes`; exits 8 if any document failed. | `-d`, `--names` or `--file`, `--action`, `--concurrency` (1-10), `--fail-fast`, `-y`, `--dry-run` |
| `pending` | Lists the open Workflow Action records the current user can see. | `-d`, `-l/--limit` (default 50), `--all`, `--page-size` |

## See also

- [Documents](documents.md)
- [Bulk operations](bulk.md)
- [Exit codes](exit-codes.md)
