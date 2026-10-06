# edit-doc

Open a document's editable fields as YAML in your editor and save what you changed, like `kubectl edit`.

```bash
ffc edit-doc -d ToDo -n TD-0001
EDITOR="code --wait" ffc edit-doc -d "Sales Order" -n SO-0001
ffc edit-doc -d "System Settings"            # Single DocType: --name defaults to the DocType
ffc edit-doc -d ToDo -n TD-0001 --dry-run    # show the request, save nothing
```

| Flag | Description |
| --- | --- |
| `-d, --doctype` | DocType (required). |
| `-n, --name` | Document name. Defaults to the DocType name for Single DocTypes. |
| `-y, --yes` | Save without asking after the editor closes. |
| `--keys` | Keys to keep in JSON output. |
| `--dry-run` | Show the write request instead of sending it. |

## How it works

1. ffc reads the document and its schema and writes the editable fields to a YAML file in a private temporary directory (file mode 0600, removed afterwards).
2. It opens the file in `$VISUAL`, else `$EDITOR`, else `vi` (`notepad` on Windows).
3. When the editor closes, ffc shows the changes and asks before saving (`--yes` skips the question).
4. Only the changed fields are sent, together with the document's `modified` timestamp. If someone saved the document after you opened it, nothing is saved (exit 6).

## What you can edit

- Left out: read-only, hidden, computed, system and Password fields, and fields Frappe would not let you change (a permission level your roles cannot write, or a masked field on v16).
- On a submitted document, only fields allowed on submit are shown, and rows of a table that is not allowed on submit cannot be added, removed or moved.
- **Child tables** are lists of rows keyed by the row `name`. Delete a row to remove it; add a row without `name` to add it. A changed table is sent whole, because Frappe replaces a table with the rows it receives.
- A field you delete from the file is not changed. Set it to `null` to clear it.

## Mistakes and cancelling

- A file with a YAML error opens again with the error at the top. Save it unchanged to give up.
- An unchanged or empty file cancels. With `--json` the result is `{"cancelled": true, "reason": ...}`.
- If the saved document does not hold a value as you sent it (Frappe silently drops some changes), ffc warns instead of reporting success.

## Editor command

`$VISUAL`/`$EDITOR` is split into words like a shell would, without running a shell. Quote a path with spaces:

```bash
EDITOR="'/opt/my editor/ed' --wait" ffc edit-doc -d ToDo -n TD-0001
```

GUI editors must wait for the file to close: `code --wait`, `subl -w`.

`edit-doc` needs a terminal. It fails with `--no-input` or when stdin is not a terminal; use [`update-doc`](documents.md#update-a-document-update-doc) in scripts.

## See also

- [Documents](documents.md)
- [Dry runs and debugging](dry-run-and-debugging.md)
- [Exit codes](exit-codes.md)
