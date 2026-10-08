# Customizations

`ffc customize pull` copies a site's customizations into files that you can review and keep in git. `ffc customize push` applies such a folder to a site, for example from staging to production. Both work over the REST API, so they also work where `bench export-fixtures` and `bench migrate` are not available, such as Frappe Cloud.

```bash
ffc customize pull -d "Sales Invoice" --out customizations
ffc customize pull -d "Sales Invoice,Customer" --types custom_field,property_setter --out customizations
ffc customize pull --module Selling --out customizations
```

| Flag | Meaning |
| --- | --- |
| `-d, --doctype` | The DocTypes whose customizations to pull (comma-separated). |
| `-m, --module` | Pull a module instead (see below). |
| `--out` | The folder to write to. It is created if it does not exist. |
| `--types` | Pull only these kinds: `custom_field`, `property_setter`, `client_script`, `server_script`, `print_format`, `report`, `notification`, `workflow`, `webhook`. |
| `--include-system` | Also pull the Custom Fields and Property Setters made by apps and patches. |

## What is pulled

| Kind | Selected by | Left out |
| --- | --- | --- |
| Custom Field | `dt` | Fields made by apps and patches (`is_system_generated`), unless you pass `--include-system`. |
| Property Setter | `doc_type` | Same as Custom Field. |
| Client Script | `dt` | |
| Server Script | `reference_doctype` | |
| Print Format | `doc_type` | Standard print formats. |
| Report | `ref_doctype` | Standard reports. |
| Notification | `document_type` | Standard notifications. |
| Workflow | `document_type` | |
| Webhook | `webhook_doctype` | |

- Each pulled Workflow also brings the Workflow States and Workflow Action Masters it uses.
- Standard Reports, Print Formats and Notifications are left out because they ship in app code and can only be changed in developer mode.
- Customize Form saves your changes as non-system Custom Fields and Property Setters, so those are pulled.
- "Not standard" does not always mean "made by you": a few apps ship non-standard records too, for example some regional print formats.

### Pulling a module

With `--module`, a document is pulled in either of two cases:

- its own module is that module, or
- the DocType it customizes belongs to that module.

The second case matters because Custom Fields and Property Setters rarely have a module of their own, and Workflows and Webhooks have none. A Server Script that names no DocType, such as an API or Scheduler script, is pulled only through its own module.

## Files

Each document is written to one file, `<out>/<kind>/<name>.json`.

- The files are stable. Keys are sorted, and the timestamps, owner, `docstatus`, `idx`, tags and comments are removed. Null values are removed too. Lines end in LF and every file ends with a newline. Pulling again changes only what changed on the site, so `git diff` shows the real changes.
- A multi-line text field gets a file of its own next to the JSON, so it diffs line by line. This applies to a script, a template or a query, for example. The JSON keeps `{"$file": "<name>.<field>.<ext>"}` in its place. The extension follows the content: `.js`, `.py`, `.html`, `.sql`, `.md`, `.css`, `.jinja` (a Webhook's JSON template) or `.txt`. A sidecar never ends in `.json`, so it cannot take another document's file name.
- Child rows (Workflow states and transitions, Webhook headers, Report columns and so on) keep their content and order. Their row identity (`name`, `parent`, `idx`) is removed.
- Password fields are never written, including those in child rows. A Webhook's secret is the only one in Frappe; ffc warns, and you set it on each site yourself.
- Webhook headers are plain fields, so they are written. When a header's name looks like a credential (`Authorization`, a token, a key and so on), ffc warns. Keep such files out of shared repositories, or set the header on each site.
- Characters that Windows forbids in file names are percent-encoded, and so is `%` itself. So are a leading dot and a trailing dot or space. A name Windows reserves, such as `CON`, gets its first letter encoded. The exact name is always inside the file.
- The folder gets a `.gitattributes` that keeps the files LF in git checkouts on Windows. ffc writes it only when the folder has none.

Pull never deletes a document's file. If a file belongs to the selection but its document is no longer on the site, ffc keeps the file and lists it as stale (`stale` in `--json`). Pull does delete a document's old sidecar when the field it held is no longer multi-line. It only deletes names that could be that document's sidecars, so an edited file cannot point it at another file.

If two documents would get file names that differ only in case, pull stops before writing anything. Windows and macOS file names ignore case.

## Permissions

Pull reads with your user's permissions. Reading Server Scripts and some Reports needs the System Manager or Script Manager role. A kind you cannot read stops the pull with a permission error (exit 5); `--types` can leave it out.

## Push

```bash
ffc --site prod customize push customizations --dry-run
ffc --site prod customize push customizations -d "Sales Invoice"
ffc --site prod customize push customizations --types custom_field,property_setter --yes
```

| Flag | Meaning |
| --- | --- |
| `-d, --doctype` | Push only the customizations of these DocTypes (comma-separated). |
| `--types` | Push only these kinds (same names as for pull). |
| `--dry-run` | Print the plan and stop. Reads still run; exits 0. |
| `-y, --yes` | Apply without asking. Without a terminal, push needs it. |
| `--fail-fast` | Stop after the first document that fails. |

Push reads every file first. A file it cannot trust stops it before any request (exit 2):

- a file whose name does not match the document's `name`;
- a document whose name does not match the fields Frappe names it from (a Custom Field is named `dt-fieldname`, a Workflow by its `workflow_name`, and so on): Frappe would create it under the other name, and every push would try to create it again;
- a file that is not JSON;
- a `$file` reference that is not one of the document's own sidecars in the same folder;
- a link, device or pipe instead of a regular file or folder (a cloned repository could hold a link to a secret elsewhere on the disk), or a file over 16 MiB.

It then reads each document from the site and prints a plan: what it would create, what it would update with the fields that change, and how many are unchanged. It asks before writing.

- **Matching.** A file matches the site's document with the same name. Names are the same on every site (a Custom Field is `DocType-fieldname`, a Property Setter `DocType-field-property`, a Workflow its workflow name, and so on).
- **Only what is in the file.** Push compares and sends only the fields in the file. Pull leaves out empty (null) values, so a field that was cleared on the source site keeps its value on the target. Clear it there by hand.
- **Child tables.** A table that differs is sent whole and replaces the site's rows, like Frappe's own save. A table counts as unchanged when every field in the file's rows matches; columns that only the site has (a newer Frappe version adds some) do not count. When a table is replaced, those columns go back to their defaults, and push warns.
- **Fields the site does not have** are left out with a warning, unless the site's copy already has the same value. This happens when pushing from a newer Frappe version to an older one.
- **Passwords** are never sent: an empty password field would delete the stored secret. A Webhook created with security on needs its secret set on the site.
- **Order.** Workflow States and Actions come before Workflows, Custom Fields before Property Setters, and a Custom Field after the field it is inserted after. Print Formats come before Notifications.
- **Concurrent changes.** Each update carries the modification time push read for the plan. A document that changed on the site after that is refused ("was changed on the server since the plan was made"), never overwritten. Run push again to see the new plan.
- **Workflows.** Saving an active Workflow deactivates the other Workflows of its DocType (Frappe allows one active Workflow per DocType). Push warns when that would happen.
- **Server Scripts.** Saving one needs only the Script Manager role, but it runs only when the site allows Server Scripts. Push warns when the site says they are disabled.

Push never deletes. With `-d`, the plan lists the documents of those DocTypes that are on the site but not in the folder ("extras"), selected the same way pull selects them. Pass the `--types` you pulled with, or kinds you did not pull show up there too.

Documents are written one at a time. When some fail, the others are still applied (unless `--fail-fast`), the report lists each result, and the exit code is 8. With `--json` the answer is `{plan, create, update, unchanged, extras, warnings}`, plus `applied`, `failed`, `skipped` and `results` when push wrote. If push asks first, the plan goes to stderr before the question.

Push writes with your user's permissions: Custom Fields and Property Setters need System Manager, Server Scripts Script Manager.
