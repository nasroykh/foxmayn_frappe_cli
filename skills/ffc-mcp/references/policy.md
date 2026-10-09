# MCP policy, confirmation, audit and multi-site

## The `mcp:` block

Under `sites.<name>` in `config.yaml`. Read on every tool call, so edits apply without a restart. A misspelt key or an empty list is a config error.

| Key | Effect |
| --- | --- |
| `read_only: true` | register and allow only read tools |
| `allow_tools: [...]` | only these tools are registered |
| `allow_doctypes: [...]` | tools may touch only these DocTypes; also the only way to let MCP **write** a sensitive DocType |
| `deny_doctypes: [...]` | refuse these DocTypes |
| `allow_methods: [...]` | `call_method` may call only these (trailing `*` = prefix); also the only way to call a built-in denied method |
| `deny_methods: [...]` | refuse these methods |
| `confirm: always \| if-supported \| never` | when destructive calls ask the user (default `if-supported`) |

The `ffc mcp` flags (`--read-only`, `--allow-*`, `--deny-*`, `--confirm`) only narrow this block, never widen it. `--confirm never` is not accepted on the command line.

## Built-in rules

- **Sensitive DocTypes**, readable but not writable unless `allow_doctypes` lists them: User, Role, Has Role, Role Profile, Module Profile, User Type, User Group, DocType, DocPerm, Custom DocPerm, User Permission, DocShare, Custom Field, Property Setter, Customize Form, System Settings, OAuth Client, OAuth Provider Settings, OAuth Bearer Token, OAuth Authorization Code, Connected App, Token Cache, Social Login Key, LDAP Settings, Email Account, Server Script, Client Script, Report, Print Format, Website Script, Web Page, Web Form, Custom HTML Block, Webhook, Notification, Auto Email Report, Assignment Rule, Energy Point Rule, Scheduled Job Type, System Console, Data Import, File.
- **Denied methods** unless `allow_methods` lists them: `frappe.desk.doctype.system_console.system_console.execute_code`, `frappe.core.doctype.user.user.generate_keys`, `frappe.integrations.frappe_providers.*` (Frappe Cloud app installer).
- With `allow_doctypes` set, `call_method` needs `allow_methods` in the config too.
- With `allow_doctypes` or `deny_doctypes` set, query arguments may not reach another table: filter fields must be plain fieldnames, `fields` and `order_by` may not contain `.` or a backtick (`link.field`, `items.item_code`, `` `tabX`.`f` ``), and the DocType of a four-element filter is checked too.
- A global `search` and `get_doc_context` filter out hits and sections from DocTypes the policy forbids and report the count in `hidden_by_policy`.
- `whoami` reads the User DocType, so DocType rules on `User` apply to it.

A refused call returns an error starting with `policy:` naming the setting, and sends nothing to the site.

## Confirmation

Confirmed calls: `delete_doc`, `bulk_delete`, `cancel_doc`, `bulk_submit`, `bulk_cancel`, `apply_workflow`, `share_doc`, `assign_to`, `rename_doc` with `merge`, and the matching `call_method` methods (`frappe.client.delete`, `frappe.client.cancel`, workflow apply, sharing and assignment methods, ...).

- `if-supported` (default): ask through the client (MCP elicitation) when it can, otherwise proceed.
- `always`: ask, and refuse when the client cannot ask; the error names the `ffc` command to run in a terminal instead.
- `never`: do not ask (config only).
- A declined call returns "cancelled by the user; nothing was changed".

## Audit log

Every tool call appends one JSON line to `mcp-audit.jsonl` next to the config (`~/.config/ffc/`, mode 0600, rotated at 10 MiB): tool, site, outcome, confirmation, arguments with secrets redacted and document data reduced to keys and size.

## Multi-site

```bash
ffc mcp --sites prod,staging      # or --all-sites
```

- Every tool except `list_sites` then takes a required `site` (an enum of the served sites); the call uses that site's credentials and policy.
- A tool is registered when any served site's policy allows it.
- Only the default site signs in at start; others on their first call.
- `FFC_API_KEY`/`FFC_API_SECRET` cannot be combined with several sites.

## Limits

- Results over 512 KiB are refused with a hint to narrow them (`list_docs` and `run_report` rows are cut instead).
- `run_report` defaults to 500 rows; bulk tools take at most 200 items; `attach_file` at most 5 MiB.
- `--read-only` counts `call_method` as a write.
