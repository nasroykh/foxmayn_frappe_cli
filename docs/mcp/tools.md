# MCP tools

The 50 tools the ffc MCP server offers, how tool sets select them, their limits, and the resources, prompts and completion that come with them.

## Tool sets

```bash
ffc mcp --toolsets core                    # documents, schema, reports, search, bulk, call_method, ...
ffc mcp --toolsets core,lifecycle,collab   # + comments, assignments, tags
ffc mcp --toolsets core,admin              # + sharing, site health, jobs, errors, scheduler
ffc mcp --toolsets core,lifecycle,files    # + attachments and print HTML
ffc mcp --toolsets core,erp                # + ERPNext drafts and lookups (reads only)
```

The default is `core,lifecycle`. The `collab`, `admin`, `files` and `erp` sets are exposed only when named. `list_sites` is always there. Like `--allow-tools`, `--toolsets` only narrows what the policy allows; an unknown set name is a usage error.

## Tools

R = read tool (kept by `--read-only`), W = writes, M = `call_method` (treated as a write).

### `core`

| Tool | Kind | What it does | CLI equivalent |
| --- | --- | --- | --- |
| `list_sites` | R | The served sites (name, URL, sign-in method, read-only). | `ffc site list` |
| `ping` | R | Check the connection. | `ffc ping` |
| `whoami` | R | Signed-in user, roles, installed apps. | `ffc whoami` |
| `check_permission` | R | May the user do X on a DocType or document? | `ffc can` |
| `get_doc` | R | One document (with `fields`, only those columns are read when possible). | `ffc get-doc` |
| `get_doc_context` | R | Versions, comments, attachments, assignments, shares, tags, links, timeline. | `ffc doc-info` (without `--onload`) |
| `list_docs` | R | List documents. | `ffc list-docs` |
| `count_docs` | R | Count documents. | `ffc count-docs` |
| `aggregate` | R | Counts and totals per group. | `ffc aggregate` |
| `get_schema` | R | Compact DocType schema (always fetched live, never from the cache). | `ffc get-schema` |
| `list_doctypes` | R | DocType names. | `ffc list-doctypes` |
| `list_reports` | R | Report names. | `ffc list-reports` |
| `run_report` | R | Run a report (at most 500 rows unless `limit` is given). | `ffc run-report` |
| `search` | R | Link-field search in one DocType, or global search. | `ffc search` |
| `create_doc` | W | Create a document. | `ffc create-doc` |
| `update_doc` | W | Update fields; `if_unmodified` fails if the document was saved since it was read. | `ffc update-doc` |
| `delete_doc` | W | Delete a document (asks for confirmation). | `ffc delete-doc` |
| `bulk_create`, `bulk_update`, `bulk_delete` | W | Up to 200 items per call, in order one at a time (optional `concurrency` 1-4); progress notifications; `bulk_delete` asks for confirmation. `bulk_create` also takes `atomic` (all or none, one request). | `ffc bulk-*` |
| `call_method` | M | Call a whitelisted method; `full_response: true` returns the whole response object. | `ffc call-method` |

### `lifecycle`

| Tool | Kind | What it does |
| --- | --- | --- |
| `submit_doc` | W | Submit a draft. |
| `cancel_doc` | W | Cancel a submitted document (asks for confirmation). |
| `bulk_submit`, `bulk_cancel` | W | Submit drafts or cancel submitted documents, up to 200 names of one DocType per call, one at a time in the order given; each document is read first and one in the wrong state fails on its own; refused for a DocType with an active workflow; both ask for confirmation. |
| `amend_doc` | W | Amend a cancelled document. |
| `copy_doc` | W | Duplicate a document. |
| `rename_doc` | W | Rename, or merge into another document (a merge asks for confirmation). |
| `restore_doc` | W | Restore a deleted document, the undo of `delete_doc`: name the Deleted Document (`deleted_document`) or the document (`doctype` and `name`: its latest unrestored deletion), not both. Deleted Document counts as a read and only `deny_doctypes` applies to it (no need to list it in `allow_doctypes`); the DocType rules (write) apply to the DocType being restored, taken from the record after checking that its `data` names the same DocType, so a deleted Server Script, User or Webhook is refused unless `allow_doctypes` lists it. Writing Deleted Document with `create_doc` and the like is refused (sensitive). Does not ask for confirmation. |
| `apply_workflow` | W | Apply a workflow action (asks for confirmation: an action may submit or cancel). |
| `get_transitions` | R | Workflow actions available now. |

### `collab`

| Tool | Kind | What it does |
| --- | --- | --- |
| `add_comment` | W | Comment on a document. |
| `assign_to` | W | Assign users (asks for confirmation: Frappe may share the document with them). |
| `remove_assignment` | W | Remove assignments. |
| `add_tag`, `remove_tag` | W | Add or remove tags. |

### `admin`

| Tool | Kind | What it does |
| --- | --- | --- |
| `share_doc` | W | Share a document (asks for confirmation). Refused unless the site's `allow_doctypes` lists `DocShare`. |
| `unshare_doc` | W | Remove a share. Same `DocShare` rule. |
| `site_health` | R | The System Health Report with `attention`, like `ffc health`. Loading it queues one `frappe.ping` job, as the desk's page does, even on a read-only server. |
| `list_jobs` | R | Background jobs, newest first: `status`, `queue`, `limit` (1-100, default 20), or `name` for one job with its arguments and traceback. Answers `{jobs, warning?}`; each listed job carries `error`, the last line of its traceback. |
| `list_errors` | R | The Error Log, newest first: `since` (default `24h`), `doctype`, `method`, `limit` (1-100), or `name` for one entry with its traceback. Answers `{errors, hidden_by_policy}`; listed entries carry the last line of the traceback as `error`. |
| `scheduler_status` | R | The scheduler's status, enabled and stopped job types, and failed runs per job type within `since` (default `24h`), like `ffc scheduler`, with the last line of each last error. |

The four read tools need the System Manager role. The site's DocType rules apply to what each reads:

- `site_health`: System Health Report, Scheduled Job Type, Error Log, RQ Job, Email Queue and User (the report is built from them);
- `list_jobs`: RQ Job;
- `list_errors`: Error Log and System Settings (the time zone), and each entry's `reference_doctype`: entries about a DocType the server may not read are left out (`hidden_by_policy`), and `name` of such an entry is refused;
- `scheduler_status`: Scheduled Job Type, Scheduled Job Log and System Settings.

A job's arguments and traceback (`list_jobs` with `name`) can hold data of any DocType; only RQ Job is checked. See [Site health and operations](../cli/admin.md).

### `files`

| Tool | Kind | What it does |
| --- | --- | --- |
| `list_attachments` | R | Files attached to a document. |
| `get_print_html` | R | A document's print view as `{html, style}`, or `{text}` with `text_only`. Runs the DocType's `before_print` hook, as the desk's print view does, even on a read-only server. |
| `attach_file` | W | Attach a file: `data` in base64 (or `encoding: text`), at most 5 MiB decoded, private unless `is_private: false`. Refused unless the site's `allow_doctypes` lists `File`. |

No tool returns file contents or PDFs.

### `erp`

ERPNext 15 and 16 only (any other major, or a site without ERPNext, is a tool error; use `call_method`). All five are reads and stay available on a `read_only` server: ERPNext builds a draft or answers a lookup through methods that write nothing (the same ones as [`ffc erp`](../cli/erpnext.md)). A draft is returned as data and is **not saved**; saving and submitting are `create_doc` and `submit_doc`, so confirmation, policy and the audit log stay in one place.

| Tool | Kind | What it does | CLI equivalent |
| --- | --- | --- | --- |
| `erp_map` | R | Map a document into the next one in its chain (`from_doctype*`, `from_name*`, `to_doctype*`). Answers `{draft}`; save it with `create_doc` (`doctype` = `to_doctype`, `data` = the draft). A Quotation made out to a Lead or Prospect with no customer is refused. | `ffc erp map` |
| `erp_payment` | R | A Payment Entry against a Sales Invoice, Sales Order, Purchase Invoice, Purchase Order or Dunning (`against_doctype*`, `against_name*`, `amount`, `bank_account`, `reference_date`). Answers `{draft, warnings?}`: `warnings` says when the draft has no bank or cash account (it cannot be saved until one is set or `bank_account` is passed) or when the document's Mode of Payment overrode `bank_account`. | `ffc erp payment` |
| `erp_item` | R | What ERPNext fills into a document row for an item (`item_code*`, `company*`, `doctype`, `customer`, `supplier`, `price_list`, `qty`, `warehouse`, `date`): rate, UOM, warehouse, accounts, taxes, stock levels. | `ffc erp item` |
| `erp_stock` | R | An item's stock: with `warehouse` the balance there (`date`, `valuation`), without it `{item_code, rows, truncated}` with one row per warehouse (at most 504). | `ffc erp stock` |
| `erp_party` | R | What ERPNext fills into a document for a customer or supplier (`customer` or `supplier`, `company`, `date`, `doctype`). | `ffc erp party` |

The draft is cleaned for `create_doc` (no `__islocal`-style keys, no empty `name`). Arguments are checked like the CLI's flags, with the same messages and the argument names in them; a blank optional argument counts as not given. The site's DocType rules apply to what each tool reads:

- `erp_map`: the source and target DocTypes (and Customer, for a Quotation: the lead check reads it);
- `erp_payment`: the DocType paid against and Payment Entry, plus Account when `bank_account` is given (the party is covered through the document paid against);
- `erp_item`: Item, Price List (given, or the default of the Selling/Buying Settings) and Bin (it answers stock levels), plus Customer, Supplier and Warehouse when given. `doctype` only picks the row's defaults and is not part of the scope;
- `erp_stock`: Item, Warehouse and Bin;
- `erp_party`: Customer or Supplier, plus Address and Contact (it answers the address text and the contact's email, mobile and phone, which ERPNext reads without a permission check of their own).

ERPNext checks the user's own permissions for everything the call touches; `ignore_permissions` is never sent. The DocTypes ERPNext reads inside a mapper (items, taxes, accounts) are not listed in the scope.

## Limits and result shapes

- **512 KiB per result.** A larger result is refused with a hint to narrow it (`limit`, `fields`, `filters`, `keys`), except rows:
  - `list_docs` returns the rows that fit as `{"data": [...], "truncated": true, "next_start": N, "hint": "..."}`; call again with `start: N` for the rest. A list that fits is still a plain array.
  - `run_report` drops rows from the end and adds `truncated`, `total_rows` and a `hint`.
  - `get_print_html` drops the style, then cuts the HTML (`truncated: true`).
- Tools whose results can be large tell the client the cap, so Claude Code does not cut the JSON.
- `get_doc_context` returns at most 50 comments, emails and workflow log entries, 100 attachments, assignments, shares and tags, 50 changes per version and 100 timeline entries; the rest is counted in `omitted`.
- `count_docs`, `whoami` and `list_sites` also return structured content with an output schema.
- `count_docs` with `at_least: N` only answers whether N or more documents match: the site stops counting at N. It returns `result` (true/false) and `count` (exact below N, N otherwise); both are `null`, with a `warning`, when Frappe v16 on MariaDB gave up counting after 1 second.
- `bulk_create`, `bulk_update` and `bulk_delete` run their items in input order, one at a time. The optional `concurrency` argument (1 to 4, default 1) runs that many at once; set it only when the items do not depend on each other (no links between them, no tree parent or shared parent, no delete that must follow another), because the order is then not kept. Results always keep the order of the input. With `concurrency` above 1, `bulk_update` and `bulk_delete` refuse a call that names the same document twice (names compare without regard to case). `bulk_submit` and `bulk_cancel` have no such argument: they always run one at a time, because concurrent submits can deadlock on ERPNext's ledger postings and cancels must follow the order given.
- `bulk_create` with `atomic: true` sends all items in one `frappe.client.insert_many` request, which is one database transaction: every item is created or none is (like `ffc bulk-create --atomic`). At most 200 items; it cannot be combined with `concurrency` (any value is refused), and every item must be of the call's `doctype`: an item naming another one, or carrying `parent` and `parenttype`, is refused before any request. The result has the shape of a call without `atomic` (one `created` result per item, in input order). A failure is a tool error instead of a per-item report, and its text says what it means for the batch: "nothing was created" (the site refused the batch and rolled it back), "probably created" (the site answered success but the reply could not be read), or "may or may not have been created" (no answer, a timeout or a proxy's error: check the site before re-running; the limit is the `--timeout` or `FFC_TIMEOUT` of the `ffc mcp` process, 30 s by default, which 200 inserts can exceed). A controller hook that commits, or DDL such as inserting a Custom Field or DocType, ends the transaction early and breaks all-or-nothing. There is one request, so no progress notifications; cancelling the call cancels it. A value of `atomic` that is not true or false is refused.
- The bulk tools send progress notifications when the call carries a progress token; cancelling the call stops new items.

## `jq` on read tools

Read tools whose results can be large (`get_doc`, `list_docs`, `get_schema`, `list_doctypes`, `list_reports`, `run_report`, `search`, `get_doc_context`, `aggregate`, `list_attachments`, `get_print_html`, and the five `erp_*` tools) take a `jq` argument: a filter the server runs on the result before returning it, for example `"[.[] | {name, status}]"` or `".data | length"`.

The output is JSON (one output as is, none or several as an array) under the same 512 KiB cap. The filter runs in a separate process that sees only the result (no environment, no input, no modules) and is stopped after 5 seconds or 256 MiB.

`list_docs` and `run_report` also take `response_format`: `concise` (default) or `detailed` (every field for `list_docs` without `fields`; the report as Frappe returns it).

### Prepared reports

`run_report` with `prepared: true` uses Frappe's prepared reports, as `ffc run-report --prepared` does: a worker on the site's `long` queue runs the report. It returns your finished result for the same filters (`prepared_report` gives its `name` and when it `finished`; it may be old), else waits for your queued job or starts one. `fresh: true` ignores a finished result; it needs `prepared` and cannot be combined with `prepared_report`. A `prepared_report` made for another report or other filters is refused.

- The server waits at most a minute, sending progress notifications when the client asked for them. A job still running then is answered `{"status": "queued", "prepared_report": {"name": ...}, "hint": ...}`; call again with `prepared_report` set to that name and the same `filters`.
- A read-only server only reuses: a finished result, or a job already queued for those filters (for example by `ffc run-report --prepared`). It never starts one, since that saves a Prepared Report document on the site, and refuses `fresh`. `prepared_report` works there too.
- A failed job is an error. On a report not marked "prepared", `prepared` runs it normally.

## Resources

Read-only JSON resources, served through the same tool handlers (so policy, audit and size cap apply):

| URI | Content |
| --- | --- |
| `ffc://sites` | The served sites (as `list_sites`). |
| `ffc://{site}/schema/{doctype}` | The compact schema (as `get_schema`). |
| `ffc://{site}/doc/{doctype}/{name}` | A document (as `get_doc`). Percent-encode each segment: `ffc://prod/doc/Sales%20Invoice/SINV%2F0001`. |

A template is offered only when its tool is. A failed read returns the tool's error message; read the message, not the JSON-RPC code.

## Prompts

Guidance only; they call nothing. Each lists which tools to call in which order and what to check, leaving out steps whose tools are not exposed.

| Prompt | Arguments |
| --- | --- |
| `inspect-doctype` | `doctype` |
| `safe-bulk-import` | `doctype`, optional `source` |
| `audit-doc-changes` | `doctype`, `name` |
| `explain-report` | `report_name` |

With several sites they take a required `site`. An argument longer than its limit (140 characters, 500 for `source`) is refused, never cut.

## Instructions and completion

On connecting, the client receives short instructions for the model: filter syntax, reading the schema before writing, `docstatus` and the lifecycle tools, `fields` and `limit` on lists, name versus title, which sites are read-only. They mention only the tools this server exposes.

The server answers completion requests for `{site}`, `{doctype}` and the prompts' arguments from the local cache the CLI fills (`ffc cache warm`). Without a fresh cache it offers nothing. It never offers what the site's policy would refuse, and never completes document names.

## See also

- [Safety](safety.md)
- [Running the server](running.md)
- [CLI reference](../cli/README.md)
