# Identity and permissions

Find out who you are on a site, whether you may do something before trying it, and what happened to a document: `whoami`, `can`, `doc-info`.

```bash
ffc whoami                                       # user, roles, installed apps and versions
ffc can -d "Sales Invoice" --perm create         # may I create one at all?
ffc can -d ToDo -n TD-0001 --perm write --all    # may I change this one? and what else?
ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001 --links
```

## Who am I: `whoami`

Shows who the configured credentials belong to: the user, their roles, and the installed apps with versions (Frappe, ERPNext, ...).

```bash
ffc whoami --site prod --json
ffc whoami --refresh --jq .server.frappe
```

- Roles come from the user's Has Role rows. A user who is not a System Manager may not be allowed to read them; the roles are then reported as unavailable, with a note. The automatic roles (All, Guest, Desk User) are not listed.
- The app versions are cached for 24 hours per site; `--refresh` reads them again. Other commands (such as `aggregate`) use this cache to know the Frappe version.
- When the site treats the credentials as Guest, the result is printed and the command exits 3.
- Any password in the URL is hidden.

## May I: `can`

Asks whether the configured user holds a permission, without changing anything.

| Flag | Default | Description |
| --- | --- | --- |
| `-d, --doctype` | | DocType (required). |
| `-n, --name` | | One document. Without it, the DocType's role rules are evaluated. |
| `--perm` | `read` | `select`, `read`, `write`, `create`, `delete`, `submit`, `cancel`, `amend`, `print`, `email`, `report`, `import`, `export`, `share`. With `--name`, any permission type the site defines. |
| `--all` | | With `--name`: also list every permission you hold on the document. |

**Exit code:** 0 when allowed, 5 when denied, 4 when the DocType or document does not exist. The answer is printed either way, so `can` works as a guard:

```bash
ffc can -d Customer --perm delete && ffc delete-doc -d Customer -n CUST-0001 --yes
```

**Two kinds of answer** (the `basis` field says which):

- **With `--name`** (`basis: document`): the site judges that document, counting user permissions, sharing and controller rules, as when the action is really attempted. `--all` adds the role-based rights the site reports for it; the two can differ (a user may edit their own User document).
- **Without `--name`** (`basis: doctype`): the site cannot judge a DocType alone, so ffc applies the DocType's permission rows to your roles, as Frappe does. That answers "may I create a Sales Invoice at all". It does not evaluate sharing, user permissions or controller rules. An owner-only right shows as `owner_only`: allowed for `read` and `select` (you see your own documents), denied for other rights because they hold only per document. A child table is refused (exit 2): ask about its parent.

The user Administrator is allowed everything.

## Document history and context: `doc-info`

Shows what the desk's form sidebar and timeline show for one document: who changed which fields (the last 10 versions), comments, emails, attachments, assignments, shares, tags, the workflow log and your permissions on it.

```bash
ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001
ffc doc-info -d "Sales Invoice" -n ACC-SINV-2026-00001 --links --json
ffc doc-info -d ToDo -n TD-0001 --timeline
ffc doc-info -d Customer -n Acme --onload
```

| Flag | Description |
| --- | --- |
| `-d, --doctype` | DocType (required). |
| `-n, --name` | Document. Defaults to the DocType name for Single DocTypes. |
| `--links` | Also count linked documents per DocType, like the form's Connections panel. Each count stops at 100, and the counts ignore your permissions (as in the desk). |
| `--timeline` | Also read the activity timeline: every change field by field, emails, comments and logs, oldest first. Needs a Frappe release from August 2026 or later; on older ones ffc prints a note and the rest. |
| `--onload` | Load the document as the desk form does and show its `__onload` values (such as a Customer's billing this year and total unpaid). **This writes:** a View Log entry and the "seen" mark on DocTypes that track them, plus whatever the DocType's onload code does. |
| `--full` | With `--json`, print Frappe's raw responses instead of the compact view. |

Without `--onload`, `doc-info` changes nothing.

- Versions exist only for DocTypes with **Track Changes** on.
- **Versions show only fields you may read.** Frappe returns stored version data whole, including fields above your permission level and Password fields; ffc filters them out, as the desk timeline does, and counts the dropped changes in `hidden_fields`. If ffc cannot read the DocType's meta or your roles, it leaves versions out and says why in `notes`. `--full` is unfiltered.
- Text is stripped of HTML and cut to 500 characters, version values to 200. A change inside a child row shows as a field like `items[2].qty`.
- Frappe caches whether a DocType has any tags, so the first tag on a DocType can be missing from `tags` for a while.

The `--json` compact view has `doctype`, `name`, `permissions`, `versions`, `comments`, `communications`, `attachments`, `assignments`, `shares`, `tags`, `workflow_log`, and with the flags `links`, `timeline` and `onload`, plus `notes` for parts that could not be read. `ffc doc-info --help` lists every field.

## See also

- [doctor](doctor.md)
- [Collaboration](collaboration.md)
- [Exit codes](exit-codes.md)
