# MCP tools, resources and prompts

50 tools. `*` = required argument. Tools in `collab`, `admin`, `files` and `erp` appear only when `--toolsets` names their set. With several sites served, every tool except `list_sites` also takes a required `site`. "Doc tools" take `doctype*` and `name*`.

## core (default)

| Tool | Kind | Arguments | CLI equivalent |
| --- | --- | --- | --- |
| `list_sites` | read | none | `ffc site list` (served sites only) |
| `ping` | read | none | `ffc ping` |
| `whoami` | read | `refresh` | `ffc whoami` |
| `check_permission` | read | `doctype*`, `name`, `perm_type`, `all` | `ffc can` (denied = `allowed: false`, not an error) |
| `get_doc` | read | `doctype*`, `name` (Single DocTypes may omit), `fields` | `ffc get-doc` |
| `list_docs` | read | `doctype*`, `fields`, `filters`, `limit` (default 20, 0 = all), `start`, `order_by`, `response_format` | `ffc list-docs` |
| `count_docs` | read | `doctype*`, `filters`, `at_least` (stops counting there: `result`, `count`, null with `warning` on a timeout) | `ffc count-docs` |
| `aggregate` | read | `doctype*`, `group_by`, `count`, `sum`, `avg`, `min`, `max`, `filters`, `order_by`, `limit` (1-1000, default 100) | `ffc aggregate` |
| `get_schema` | read | `doctype*`, `full`, `keys` | `ffc get-schema` (always live, never cached) |
| `list_doctypes` | read | `module`, `limit` (default 50) | `ffc list-doctypes` |
| `list_reports` | read | `module`, `limit` (default 50) | `ffc list-reports` |
| `run_report` | read | `report_name*`, `filters`, `limit` (default 500), `response_format`, `prepared`, `fresh`, `prepared_report` | `ffc run-report` |
| `search` | read | `text*`, `doctype`, `limit` (default 20, max 100) | `ffc search` |
| `get_doc_context` | read | `doctype*`, `name`, `links` (default true), `timeline` (default false) | `ffc doc-info --links` (no `--onload`) |
| `create_doc` | write | `doctype*`, `data*` | `ffc create-doc` |
| `update_doc` | write | `doctype*`, `name`, `data*`, `if_unmodified` | `ffc update-doc` |
| `delete_doc` | write, confirmed | `doctype*`, `name*` | `ffc delete-doc` |
| `bulk_create` | write | `doctype*`, `data*` (array, max 200), `concurrency` (1-4, default 1: in order; more only for independent items), `atomic` (bool: one request, all or none, max 200, never with `concurrency`; a failure is one tool error saying "nothing was created", "probably created" or "may or may not have been created" (check the site, do not blindly retry); hooks that commit and DDL break it) | `ffc bulk-create` |
| `bulk_update` | write | `doctype*`, `data*` (array with `name`, max 200), `concurrency` (as above; a name twice is refused above 1) | `ffc bulk-update` |
| `bulk_delete` | write, confirmed | `doctype*`, `names*` (max 200), `concurrency` (as above; a name twice is refused above 1) | `ffc bulk-delete` |
| `call_method` | method | `method*`, `args`, `get`, `full_response` | `ffc call-method` |

## lifecycle (default)

| Tool | Kind | Extra arguments (plus `doctype*`, `name*`) |
| --- | --- | --- |
| `submit_doc` | write | none |
| `cancel_doc` | write, confirmed | none (no `--check` equivalent) |
| `bulk_submit` | write, confirmed | `doctype*`, `names*` instead of `name` (max 200, one at a time in the order given; `ffc bulk-submit`) |
| `bulk_cancel` | write, confirmed | `doctype*`, `names*` instead of `name` (max 200, one at a time in the order given; `ffc bulk-cancel`) |
| `amend_doc` | write | `data` (overrides) |
| `copy_doc` | write | `data` (overrides) |
| `rename_doc` | write, confirmed with `merge` | `new_name*`, `merge` |
| `restore_doc` | write | `deleted_document`, or `doctype` and `name` instead of both (exactly one form; `ffc restore-doc`); the DocType rules apply to the deleted document's own DocType |
| `apply_workflow` | write, confirmed | `action*` |
| `get_transitions` | read | none |

## collab, admin, files, erp (opt-in)

| Tool | Set | Kind | Extra arguments (plus `doctype*`, `name*`) |
| --- | --- | --- | --- |
| `add_comment` | collab | write | `text*`, `html` |
| `assign_to` | collab | write, confirmed | `users` (array or CSV), `description`, `date`, `priority` |
| `remove_assignment` | collab | write | `users` |
| `add_tag` | collab | write | `tags` |
| `remove_tag` | collab | write | `tags` |
| `share_doc` | admin | write, confirmed | `user` or `everyone`, `write`, `submit`, `share`, `notify` |
| `unshare_doc` | admin | write | `user` or `everyone` |
| `site_health` | admin | read (queues one frappe.ping job) | none (no `doctype`/`name`) |
| `list_jobs` | admin | read | `status`, `queue`, `limit` (1-100), `name` (a job_id); no `doctype` |
| `list_errors` | admin | read | `since` (default 24h), `doctype` (reference_doctype), `method`, `limit` (1-100), `name` |
| `scheduler_status` | admin | read | `since` (default 24h); no `doctype`/`name` |
| `list_attachments` | files | read | `limit` (default 100) |
| `attach_file` | files | write | `filename*`, `data*` (base64, or text with `encoding: text`; max 5 MiB), `encoding`, `is_private` (default true), `folder`, `field` |
| `get_print_html` | files | read | `print_format`, `letterhead`, `no_letterhead`, `language`, `text_only` |
| `erp_map` | erp | read | `from_doctype*`, `from_name*`, `to_doctype*` (no `doctype`/`name`); answers `{draft}` |
| `erp_payment` | erp | read | `against_doctype*`, `against_name*`, `amount`, `bank_account`, `reference_date`; answers `{draft, warnings?}` |
| `erp_item` | erp | read | `item_code*`, `company*`, `doctype`, `customer`, `supplier`, `price_list`, `qty`, `warehouse`, `date` |
| `erp_stock` | erp | read | `item_code*`, `warehouse`, `date`, `valuation`; without `warehouse` answers `{item_code, rows, truncated}` |
| `erp_party` | erp | read | `customer` or `supplier`, `company`, `date`, `doctype` |

The four admin read tools need the System Manager role; start with `site_health`, whose `attention` says what is wrong. `list_errors` answers `{errors, hidden_by_policy}` and leaves out entries about DocTypes the policy hides. `share_doc`/`unshare_doc` need `DocShare` in the site's `allow_doctypes`, and `attach_file` needs `File` there (both are sensitive DocTypes). No tool returns file bytes or PDFs: use `ffc download` / `ffc pdf`. The erp tools need ERPNext 15 or 16 and only read: `erp_map` and `erp_payment` return an unsaved draft (ready for `create_doc` `data`), which is saved with `create_doc` and then `submit_doc`; they stay available with `--read-only`. Their DocType scope: erp_map source and target, erp_payment the document and Payment Entry (+ Account with bank_account), erp_item Item/Price List/Bin (+ Customer/Supplier/Warehouse when given), erp_stock Item/Warehouse/Bin, erp_party Customer or Supplier plus Address/Contact. `get_print_html` runs the DocType's `before_print` code even on a read-only server.

## Result shapes worth knowing

- `jq` is accepted by the large read tools only: `list_docs`, `get_doc`, `get_schema`, `list_doctypes`, `list_reports`, `run_report`, `search`, `aggregate`, `get_doc_context`, `list_attachments`, `get_print_html`, the `erp_*` tools. Not by `call_method` or any write. One output as is, several as an array; the filter is stopped after 5 s or 256 MiB.
- `list_docs` too large: `{data, truncated: true, next_start, hint}`; otherwise a plain array. `response_format: detailed` without `fields` returns every field.
- `run_report` returns `columns`, `result`, `report_summary` (if any), plus `total_rows`, `truncated`, `hint` when cut. `detailed` returns the raw result.
- `run_report` `prepared: true` runs a heavy report as a Frappe prepared report (site's `long` queue worker): your finished result for the same filters (`prepared_report: {name, finished}`), else your queued job or a new one, waited for at most a minute with progress notifications. Still running: `{status: "queued", prepared_report: {name}, hint}`; call again with `prepared_report: NAME` and the same filters. `fresh` starts a new one. Read-only servers only reuse a finished or queued one, never start one, refuse `fresh`.
- `search` without `doctype` answers `{results, hidden_by_policy}`; with `doctype` a plain list.
- `aggregate` answers `{doctype, rows, truncated}` (and `warning` when the site ignored the order).
- `get_doc_context` caps comments, emails and workflow log at 50, attachments, assignments, shares and tags at 100, 50 changes per version, 100 timeline entries (`omitted` counts the rest); parts from DocTypes the policy forbids are left out and counted in `hidden_by_policy`.
- `count_docs`, `whoami`, `list_sites` also return `structuredContent`.
- Bulk tools send progress notifications when the call carries a progress token (best effort).

## Resources

- `ffc://sites`
- `ffc://{site}/schema/{doctype}` (served by `get_schema`)
- `ffc://{site}/doc/{doctype}/{name}` (served by `get_doc`)

Percent-encode segments (`Sales%20Invoice`, `/` as `%2F`). A read goes through the tool, so policy and audit apply (`"via":"resource"` in the audit line). A failed read is a JSON-RPC error -32603 carrying the tool's message.

## Prompts

`inspect-doctype`, `safe-bulk-import`, `audit-doc-changes`, `explain-report`: step-by-step plans, no site calls, offered only when the tools they need are registered. The server also sends instructions on connect (filter syntax, schema before writes, docstatus, read-only sites, `site` with several sites). `completion/complete` for `{site}`, `{doctype}` and prompt arguments reads the local cache only: run `ffc cache warm` first.
