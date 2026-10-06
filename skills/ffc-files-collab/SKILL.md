---
name: ffc-files-collab
description: Work with what surrounds a Frappe/ERPNext document using ffc - upload and attach files, list attachments, download files, save a print as PDF, add comments, assign users, add or remove tags, share or unshare, and read a document's history (who changed which field, comments, linked documents, timeline) with ffc doc-info. Use it whenever the user wants to attach or fetch a file, print an invoice to PDF, comment on or assign a record, tag or share it, or asks "who changed this and when", even without naming ffc. Read ffc-core first for flags and safety rules.
---

# Files, PDFs, collaboration and document history

Users are User IDs (usually emails), not full names. Every write here takes `--dry-run`; reads (`attachments`, `download`, `pdf`, `doc-info`) do not.

## Files

```bash
ffc upload contract.pdf -d Customer -n "ACME Corp" --json             # private by default
ffc upload logo.png -d Item -n ITEM-001 --field image --public --json  # also sets Item.image
cat notes.txt | ffc upload - --filename notes.txt -d ToDo -n TD-0001
ffc attachments -d Customer -n "ACME Corp" --json
ffc download /private/files/contract.pdf -o contract.pdf              # --force to replace, -o - for stdout
ffc pdf -d "Sales Invoice" -n SINV-0001 --format "Standard" --no-letterhead -o inv.pdf
```

**upload FILE** (`-` = stdin, then `--filename` is required): `-d`, `-n` (required), `--public` (default private: only users who may read the document), `--folder` (default `Home/Attachments`), `--field F` (also sets that Attach field: a second request), `--dry-run` (shows name and size, never content).
- The document must exist. A file over the site's Max File Size (25 MiB unless System Settings says otherwise) is refused before sending (exit 6); the site's allowed extensions apply. Uploads are never retried. Stdin is read into memory, up to 128 MiB.

**attachments**: `-d`, `-n` (required), `-l` (default 100, `0` = all), `--all`, `--page-size`. Rows: `name`, `file_name`, `file_url`, `is_private`, `file_size`, `attached_to_field`, `folder`, `creation`, oldest first.

**download FILE_URL**: `/files/...`, `/private/files/...` or a full URL of the configured site itself (another host is refused, exit 2). `-o/--output-file` (default: the file's name in the current directory; `-` = stdout, binary refused on a terminal), `--force`. Written through a temp file, so an interrupted download leaves nothing. A missing private file and a forbidden one are both 403 (exit 5).

**pdf**: `-d`, `-n` (required), `--format` (Print Format), `--letterhead NAME` or `--no-letterhead`, `--lang` (e.g. `fr`), `-o/--output-file` (default `<name>.pdf`, `-` = stdout except on a terminal), `--force`. Only a real PDF is saved. Frappe v16 limits concurrent PDFs: a 503 is retried twice. A 500 OSError means the site's wkhtmltopdf failed (site-side problem). Raise `--timeout` for long documents.

## Collaboration

```bash
ffc comment -d Task -n TASK-0042 "Waiting for the PO" --json
git log -1 --format=%B | ffc comment -d Task -n TASK-0042 -
ffc assign -d Task -n TASK-0042 --to jane@example.com,bob@example.com --priority High --date 2026-11-01 --json
ffc unassign -d Task -n TASK-0042 --to bob@example.com --json
ffc tag -d Customer -n "Acme Ltd" vip export --json
ffc untag -d Customer -n "Acme Ltd" VIP --json
ffc share -d Project -n PROJ-0001 --user jane@example.com --write --dry-run --json
ffc unshare -d Project -n PROJ-0001 --user jane@example.com --json
```

| Command | Flags | Behaviour |
| --- | --- | --- |
| `comment TEXT` | `-d`, `-n`, `--html` | plain text escaped (line breaks kept); `-` reads stdin; `--html` sends HTML (Frappe sanitises). @mentions in plain text notify no one |
| `assign` | `-d`, `-n`, `--to` (CSV, required), `--priority Low/Medium/High`, `--date YYYY-MM-DD`, `--description` | one open ToDo per user; already assigned is reported, not duplicated; Frappe notifies and shares read-only with an assignee who cannot read the document |
| `unassign` | `-d`, `-n`, `--to` | cancels the users' open ToDos; not assigned is reported |
| `tag TAG...` | `-d`, `-n` | needs write permission; no commas in a tag; existing tag (same case) reported |
| `untag TAG...` | `-d`, `-n` | case-insensitive; missing tag reported; Tag records stay |
| `share` | `-d`, `-n`, `--user` or `--everyone`, `--write`, `--submit`, `--share`, `--notify`, `-y` | read always granted; sharing again **replaces** that share's rights; widens access, so it asks unless `--yes` |
| `unshare` | `-d`, `-n`, `--user` or `--everyone` | removes the share; role-based access unchanged |

Each command reads the current state first and reports it (e.g. `assigned`/`already_assigned`, `added`/`already_tagged`, the share's rights).

## Document history: doc-info

```bash
ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001 --json             # versions, comments, files, assignments, shares, tags, workflow log
ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001 --links --json     # + linked document counts per DocType
ffc doc-info -d ToDo -n TD-0001 --timeline --json                         # + activity timeline (Frappe releases from 2026)
ffc doc-info -d Customer -n "Acme" --onload --json                        # + __onload (e.g. billing this year); WRITES a view log
```

- Reads `get_docinfo`, which changes nothing. `--onload` loads the form like the desk: it adds a View Log / seen mark and runs the controller's onload, so ask first.
- JSON keys: `doctype`, `name`, `permissions`, `versions` (`{name, by, at, changed:[{field, from, to}], rows_added, rows_removed, impersonated_by}`, newest first, last 10, only with Track Changes), `comments` (`{name, by, at, text}`), `communications`, `attachments`, `assignments`, `shares`, `tags`, `workflow_log`, `links` (`{doctype, count, open_count, capped, timed_out, internal, names}`), `timeline`, `onload`, `notes`.
- A child-row change shows as `items[2].qty`. Version values are formatted strings, not numbers. Fields the user may not read are left out of versions and counted in `hidden_fields`.
- Link counts come from the DocType's dashboard, stop at 100 (`capped`) and ignore permissions. A DocType without a dashboard has no links: use `list-docs --filters` on the linking DocType. "link counts timed out" in `notes` means `links` is absent, not empty.
- `--full` prints the raw responses (JSON only). For Single DocTypes `-n` can be omitted.
