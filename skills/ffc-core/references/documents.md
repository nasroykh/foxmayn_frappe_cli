# Document, schema and identity commands

Flags as in `ffc <command> --help`. Every command also takes the global flags (`-s`, `-c`, `-j`, `--output`, `--jq`, `--no-input`, `-q`, `--timeout`, `--debug`).

## Contents

- get-doc, list-docs, count-docs
- create-doc, update-doc, edit-doc, delete-doc
- get-schema, list-doctypes
- whoami, can, ping

## get-doc

| Flag | Notes |
| --- | --- |
| `-d, --doctype` | required |
| `-n, --name` | defaults to the DocType name (Single DocTypes such as `System Settings`) |
| `-f, --fields` | CSV or JSON array; narrows table and JSON |
| `--keys` | top-level keys kept in JSON; wins over `--fields` |

## list-docs

| Flag | Notes |
| --- | --- |
| `-d, --doctype` | required |
| `-f, --fields` | default `name` only |
| `--filters` | JSON object or array, or `@FILE` / `@-` |
| `-l, --limit` | default 20, `0` = no limit |
| `-o, --order-by` | e.g. `"modified desc"` |
| `--start` | offset |
| `--all`, `--page-size` | every page (default page 500), streamed for json/ndjson/csv/tsv |

## count-docs

`-d` (required), `--filters`, `--group-by FIELD`, `--at-least N`. JSON: `{"doctype":"...","count":N}` (`--jq .count` for the number). `--at-least N` stops counting at N and prints `true`/`false`; JSON `{"doctype","at_least","result","count"}` (count exact below N, N otherwise; both `null` with a stderr warning and exit 0 when Frappe v16 on MariaDB hits its 1 s limit). Not with `--group-by`. `--group-by` is the list sidebar count (at most 50 groups); see the ffc-reports-api skill for it and for `ffc aggregate`.

## create-doc

`-d` (required), `--data` (required JSON object, `@FILE`, `@-`), `--keys`, `--dry-run`. Child rows go in as arrays of objects under the table field name.

## update-doc

| Flag | Notes |
| --- | --- |
| `-d, --doctype` | required |
| `-n, --name` | defaults to the DocType name for Single DocTypes |
| `--data` | required; only the fields to change; a `name` key is dropped with a warning (rename with `rename-doc`) |
| `--diff` | reads first, prints changed fields (old → new) on stderr after saving; carries the `modified` it read, so a concurrent save fails with exit 6 |
| `--if-unmodified TS` | the `modified` you read; exit 6 if the document was saved since |
| `--keys` | trim JSON output |
| `--dry-run` | show the PUT; with `--json` adds `changes` per field |

A child table in `--data` replaces the table's rows (Frappe PUT semantics): send the rows you keep, with their `name`, plus new ones without `name`.

## edit-doc (humans only)

Opens the editable fields as YAML in `$VISUAL`/`$EDITOR`, shows the diff, asks (`-y` skips), saves only changed fields with the `modified` it opened (concurrent save: exit 6). Read-only, hidden, computed, system and Password fields are left out, and fields the user's roles may not write. It needs a terminal and fails under `--no-input`, so agents use `update-doc`. Flags: `-d`, `-n`, `-y`, `--keys`, `--dry-run`. A cancelled edit prints `{"cancelled":true,"reason":...}` in machine output.

## delete-doc

`-d`, `-n` (both required; Single DocTypes cannot be deleted), `-y/--yes`, `--dry-run`. Prompts unless `--yes`; without a terminal and without `--yes` it fails with exit 2. Deleted documents can come back with `restore-doc` (System Manager; see ffc-bulk-lifecycle).

## get-schema

| Flag | Notes |
| --- | --- |
| `-d, --doctype` | required |
| `--full` | raw Frappe response (JSON mode only) |
| `--keys` | top-level keys, e.g. `fields` or `name,module,fields` (JSON mode only) |
| `--refresh` | fetch instead of using the local cache |

- Custom Fields are merged in at their `insert_after` position and Property Setters applied. Problems are warnings (`_warnings` in JSON), not failures.
- Cached 1 hour per site and login (compact view). A cache hit does not check credentials or that the DocType still exists; `--refresh` does. `--full` always fetches.
- Compact JSON keeps, for the DocType: `name`, `module`, `autoname`, `naming_rule`, `is_submittable`, `issingle`, `istable`, `is_tree`, `is_virtual`, `read_only`, `custom`; when set: `title_field`, `search_fields`, `sort_field`, `sort_order`, `image_field`, `description`, `allow_rename`, `track_changes`, `actions`, `links`, `states`; and `permissions` (one row per DocPerm: `role`, `permlevel` if above 0, granted `rights`).
- For each field: `fieldname`, `label`, `fieldtype`; when truthy: `reqd`, `read_only`, `hidden`, `unique`, `is_virtual`, `non_negative`, `allow_on_submit`, `in_list_view`, `in_standard_filter`, `set_only_once`, `translatable`, `ignore_user_permissions`, `fetch_if_empty`, `no_copy`, `search_index`, `bold`, `collapsible`, `print_hide`, `report_hide`; when non-empty: `options` (target DocType of a Link or Table, or Select choices), `default`, `description`, `fetch_from`, `depends_on`, `mandatory_depends_on`, `read_only_depends_on`, `precision`, `link_filters`, `insert_after`; when above 0: `length`, `permlevel`.

## list-doctypes

`-m, --module`, `-l, --limit` (default 50, `0` = all), `--all`, `--page-size`.

## whoami

`--refresh` rereads app versions (cached 24 h). JSON: `user`, `full_name`, `roles`, `roles_source`, `site`, `url`, `auth`, `server` (`frappe`, `major`, `apps`, `cached`, `fetched_at`), `notes`. Roles are the user's Has Role rows (automatic roles All, Guest, Desk User are not listed); a non-manager may get `roles_source: unavailable` with a note. Seen as Guest: prints the result, then exits 3.

## can

| Flag | Notes |
| --- | --- |
| `-d, --doctype` | required |
| `-n, --name` | one document: the site judges it (sharing, user permissions, controller rules count) |
| `--perm` | default `read`; `select, read, write, create, delete, submit, cancel, amend, print, email, report, import, export, share`; with `-n` any lower-case type the site defines |
| `--all` | with `-n`: list every right the user holds on the document |

Exit 0 allowed, 5 denied (result still printed), 4 missing DocType or document. Without `-n`, ffc applies the DocType's role rules (basis `doctype`): it ignores sharing, user permissions and controller rules. `owner_only: true` means the right holds only on the user's own documents. Child tables are refused (check the parent). Administrator is allowed everything.

## ping

Measures latency and names the user the credentials belong to. JSON: `response`, `url`, `latency`, `user`. Rejected credentials: exit 3. For a full diagnosis use `ffc doctor` (ffc-setup skill).
