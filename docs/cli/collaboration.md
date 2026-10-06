# Collaboration

Comment on, assign, tag and share documents: `comment`, `assign`, `unassign`, `tag`, `untag`, `share`, `unshare`. These call the same methods as the desk, so Frappe's permission checks, notifications and timeline entries apply.

```bash
ffc comment  -d Task -n TASK-0042 "Called the customer, waiting for the PO"
ffc assign   -d Task -n TASK-0042 --to jane@example.com,bob@example.com --priority High --date 2026-11-01
ffc unassign -d Task -n TASK-0042 --to bob@example.com
ffc tag      -d Customer -n "Acme Ltd" vip export
ffc untag    -d Customer -n "Acme Ltd" vip
ffc share    -d Project -n PROJ-0001 --user jane@example.com --write --yes
ffc unshare  -d Project -n PROJ-0001 --user jane@example.com
```

All of them take `-d/--doctype`, `-n/--name` (both required) and `--dry-run`. Each reads the current state first and reports what changed; with `--json` the result lists it.

## Comment: `comment TEXT`

Adds a comment to the document's timeline, as the user ffc signs in as. Needs read permission.

- TEXT is plain text: it is escaped, so it shows as typed, and line breaks are kept. Several arguments are joined with spaces; `-` reads the text from stdin.
- `--html` sends the text as HTML. Frappe removes scripts, forms and unsafe attributes either way.
- `@mentions` in plain text do not notify anyone.

```bash
git log -1 --format=%B | ffc comment -d Task -n TASK-0042 -
ffc comment -d Issue -n ISS-0007 --html "<b>Fixed</b> in v2.3"
```

## Assign and unassign

`assign` gives each user an open ToDo for the document, and Frappe notifies them. Needs read permission.

| Flag | Description |
| --- | --- |
| `--to` | Users, comma-separated (User IDs, usually emails; required). |
| `--priority` | `Low`, `Medium` or `High` (Frappe's default: Medium). |
| `--date` | Due date, `YYYY-MM-DD`. |
| `--description` | ToDo description (default: Frappe's "Assignment for <DocType> <name>"). |

- A user who already has an open ToDo for the document is reported, not assigned twice.
- When an assignee cannot read the document, Frappe shares it with them read-only, or fails when document sharing is disabled in System Settings.

`unassign --to USERS` cancels their open assignments (the ToDos become Cancelled and Frappe notifies them). A user who is not assigned is reported, not sent.

## Tag and untag: `tag TAG...`, `untag TAG...`

`tag` adds tags, creating each Tag that does not exist yet. Needs write permission. A tag the document already has (same case) is reported, not added again. A tag cannot contain a comma.

`untag` removes tags; the match ignores case, as Frappe's does. The Tag records themselves stay.

## Share and unshare

`share` gives a user, or every user, access to one document on top of what their roles allow. You need share permission on the document and every right you grant.

| Flag | Description |
| --- | --- |
| `--user` | User to share with (User ID, usually an email). |
| `--everyone` | Share with every user instead of one. |
| `--read` | Read (always granted; accepted for clarity). |
| `--write` | Also grant write. |
| `--submit` | Also grant submit (submittable DocTypes only). |
| `--share` | Also let the user share the document further. |
| `--notify` | Send the user a notification. |
| `-y, --yes` | Skip the confirmation. |

- Sharing widens access, so `share` asks for confirmation unless `--yes`.
- Sharing again with the same user replaces that share's rights: `ffc share --user U` after `--write` takes write away.

`unshare --user U` (or `--everyone`) removes the share. Access the user's roles give is not affected. A share that does not exist is reported, not sent.

## See also

- [Identity and permissions](identity-and-permissions.md) (`doc-info` shows comments, assignments, shares and tags)
- [Lifecycle and workflow](lifecycle-and-workflow.md)
- [MCP safety](../mcp/safety.md)
