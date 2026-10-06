# Frappe API quirks the client handles

Each line is pinned by the fake (`internal/frappetest`) and, where noted in CLAUDE.md, by the contract tests against real v15 and v16 sites. Keep the handling when you refactor.

## Paths and envelopes

- `/api/resource/{DocType}` (list) and `/api/resource/{DocType}/{name}` (one document); `resourcePath(doctype, name...)` escapes them.
- Lists come in `"data"` (v14+) or `"message"` (older): both handled. A 2xx single-document answer without `data` is an error.
- `/api/method/<dotted.path>` answers in `"message"`, except desk methods that set other keys: `getdoctype`/`getdoc` answer in `docs` (plus `docinfo`), `get_docinfo` in `docinfo`. Read those with `do` into a map; the fake returns them with `frappetest.Response`.
- A method missing on an older Frappe is a translated 417 that names it (`missingMethod`, client/docinfo.go); detect by method name, not by message text.

## Errors

- Error bodies carry nested JSON strings with tracebacks (`exc_type`, `_server_messages`, `exception`); `frappeErrorResponse.userMessage()` extracts the user-facing text. Messages are translated by the site, so branch on status and `exc_type`, never on message text.
- A document of a DocType that does not exist: read, create and update give a 500 `ImportError`, not a 404. The client probes the DocType's list (`missingDocType`) and marks `APIError.MissingDocType`, which `classify` maps to exit 4. List and delete give a real 404.
- `TimestampMismatchError` (417) is a concurrent save; `conflictError` rewords it.

## Numbers and bodies

- Responses decode with `UseNumber`: numbers are `json.Number` (exact large integers; `0.0` stays `0.0`). Inspect them with a helper like `numeric` (get_schema.go). Trailing data after the JSON body is an error.
- PUT with a child table replaces the table's rows: send kept rows (with `name`) plus new ones.
- `submit` (`frappe.client.submit`) takes the document as read, so its timestamp check applies.

## Version-dependent behaviour

- Get the major version through `serverInfo(ctx, c, cfg, refresh).FrappeMajor()` (cached 24 h); never hard-code a version check on message text.
- Aggregates in list `fields`: v16 refuses SQL strings (`count(name) as n` → 417) and takes `{"COUNT":"name","as":"n"}`; v15 takes only the string form. Use `client.Aggregate` with `client.SyntaxFor(major)` through `runAggregate`, which retries once on `client.SyntaxRejected`. Only plain identifiers (`client.ValidIdentifier`) reach the query.
- `frappe.desk.form.save.discard` exists only in v16 (`ErrNeedsV16`, exit 6). `get_activity_timeline` exists only in releases from August 2026 (`ErrNoTimeline`, a note).
- `frappe.client.has_permission` requires a document name; DocType-level checks apply the role rows client-side (`DocTypePermission`).

## Permissions that silently drop data

- Frappe drops (does not refuse) writes to fields above the user's write level and v16 masked fields; `edit-doc` offers only writable fields and warns when a value was not kept.
- v15 silently removes an unreadable field from list `fields`; `runAggregate` checks `client.ReadableFields` first and refuses with a 403-class error.
- `get_docinfo` versions contain raw values of every field; they are filtered by `ReadableFields` before output.

Full evidence with Frappe file:line references: CLAUDE.md "Common Pitfalls" (Aggregates, Identity and permissions, Document context, Collaboration, Files and PDF, Lifecycle, OAuth registration and revocation).
