# Customizations

`ffc customize pull` copies a site's customizations into files that you can review and keep in git. It works over the REST API, so it also works where `bench export-fixtures` is not available, such as Frappe Cloud. A matching `push`, which applies a folder to another site, is planned.

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
- A multi-line text field gets a file of its own next to the JSON, so it diffs line by line. This applies to a script, a template or a query, for example. The JSON keeps `{"$file": "<name>.<field>.<ext>"}` in its place. The extension follows the content: `.js`, `.py`, `.html`, `.sql`, `.md`, `.css`, `.json` or `.txt`.
- Child rows (Workflow states and transitions, Webhook headers, Report columns and so on) keep their content and order. Their row identity (`name`, `parent`, `idx`) is removed.
- Password fields are never written. A Webhook's secret is the only one; ffc warns, and you set it on each site yourself.
- Characters that Windows forbids in file names are percent-encoded, and so is `%` itself. So are a leading dot and a trailing dot or space. A name Windows reserves, such as `CON`, gets its first letter encoded. The exact name is always inside the file.
- The folder gets a `.gitattributes` that keeps the files LF in git checkouts on Windows. ffc writes it only when the folder has none.

Pull never deletes a file. If a file belongs to the selection but its document is no longer on the site, ffc keeps the file and lists it as stale (`stale` in `--json`).

## Permissions

Pull reads with your user's permissions. Reading Server Scripts and some Reports needs the System Manager or Script Manager role. A kind you cannot read stops the pull with a permission error (exit 5); `--types` can leave it out.
